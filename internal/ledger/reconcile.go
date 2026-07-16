package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"
)

type Difference struct {
	Dimension  string  `json:"dimension"`
	Key        string  `json:"key"`
	Ledger     string  `json:"ledger"`
	Projection *string `json:"projection,omitempty"`
	Difference *string `json:"difference,omitempty"`
}

type ReconciliationReport struct {
	AsOf                  time.Time         `json:"as_of"`
	AccountID             string            `json:"account_id"`
	Balanced              bool              `json:"balanced"`
	CashByCurrency        map[string]string `json:"cash_by_currency"`
	AssetsBySymbol        map[string]string `json:"assets_by_symbol"`
	FeesByCurrency        map[string]string `json:"fees_by_currency"`
	RealizedPnLByCurrency map[string]string `json:"realized_pnl_by_currency"`
	Differences           []Difference      `json:"differences"`
	OrphanOrderIDs        []uint            `json:"orphan_order_ids"`
	OrphanFillIDs         []string          `json:"orphan_fill_ids"`
	OrphanEventIDs        []string          `json:"orphan_event_ids"`
	RecordIssues          []string          `json:"record_issues"`
	Unresolved            []string          `json:"unresolved"`
}

func (s *Service) Reconcile(ctx context.Context, account string, asOf time.Time) (ReconciliationReport, error) {
	if account == "" {
		account = DefaultAccountID
	}
	if asOf.IsZero() {
		asOf = s.now()
	}
	report := ReconciliationReport{AsOf: asOf, AccountID: account, Balanced: true, CashByCurrency: map[string]string{}, AssetsBySymbol: map[string]string{}, FeesByCurrency: map[string]string{}, RealizedPnLByCurrency: map[string]string{}, Differences: []Difference{}, OrphanOrderIDs: []uint{}, OrphanFillIDs: []string{}, OrphanEventIDs: []string{}, RecordIssues: []string{}, Unresolved: []string{}}
	var events []database.LedgerEvent
	if err := s.DB.WithContext(ctx).Where("account_id = ? AND occurred_at <= ?", account, asOf).Order("occurred_at, recorded_at, id").Find(&events).Error; err != nil {
		return report, err
	}
	cash, assets, fees, pnl := map[string]accounting.Decimal{}, map[string]accounting.Decimal{}, map[string]accounting.Decimal{}, map[string]accounting.Decimal{}
	for _, event := range events {
		cash[event.Currency] = cash[event.Currency].Add(event.CashDelta)
		if event.Symbol != "" {
			assets[event.Symbol] = assets[event.Symbol].Add(event.AssetDelta)
		}
		if event.EventType == EventTradingFee || event.EventType == EventExchangeFee {
			fees[event.Currency] = fees[event.Currency].Add(event.CashDelta.Neg())
		}
		pnl[event.Currency] = pnl[event.Currency].Add(event.RealizedPnL)
	}
	for key, value := range cash {
		report.CashByCurrency[key] = value.String()
	}
	for key, value := range assets {
		report.AssetsBySymbol[key] = value.String()
	}
	for key, value := range fees {
		report.FeesByCurrency[key] = value.String()
	}
	for key, value := range pnl {
		report.RealizedPnLByCurrency[key] = value.String()
	}

	var wallet database.Wallet
	if err := s.DB.WithContext(ctx).First(&wallet).Error; err != nil {
		return report, err
	}
	ledgerCash := cash[wallet.Currency]
	if wallet.BalanceExact == nil {
		report.Differences = append(report.Differences, Difference{Dimension: "cash", Key: wallet.Currency, Ledger: ledgerCash.String()})
		report.Unresolved = append(report.Unresolved, "wallet exact projection is absent")
	} else if wallet.BalanceExact.Cmp(ledgerCash) != 0 {
		projection, difference := wallet.BalanceExact.String(), wallet.BalanceExact.Sub(ledgerCash).String()
		report.Differences = append(report.Differences, Difference{Dimension: "cash", Key: wallet.Currency, Ledger: ledgerCash.String(), Projection: &projection, Difference: &difference})
	}
	var positions []database.Position
	if err := s.DB.WithContext(ctx).Where("status = ?", "open").Find(&positions).Error; err != nil {
		return report, err
	}
	seen := map[string]bool{}
	for _, position := range positions {
		seen[position.Symbol] = true
		ledgerAmount := assets[position.Symbol]
		if position.AmountExact == nil {
			report.Differences = append(report.Differences, Difference{Dimension: "asset", Key: position.Symbol, Ledger: ledgerAmount.String()})
			report.Unresolved = append(report.Unresolved, "position "+position.Symbol+" has no exact ledger projection")
		} else if position.AmountExact.Cmp(ledgerAmount) != 0 {
			projection, difference := position.AmountExact.String(), position.AmountExact.Sub(ledgerAmount).String()
			report.Differences = append(report.Differences, Difference{Dimension: "asset", Key: position.Symbol, Ledger: ledgerAmount.String(), Projection: &projection, Difference: &difference})
		}
	}
	for symbol, amount := range assets {
		if !seen[symbol] && amount.Sign() != 0 {
			report.Differences = append(report.Differences, Difference{Dimension: "asset", Key: symbol, Ledger: amount.String()})
		}
	}

	if err := s.DB.WithContext(ctx).Raw(`SELECT o.id FROM orders o LEFT JOIN fills f ON f.order_id=o.id WHERE f.id IS NULL ORDER BY o.id`).Scan(&report.OrphanOrderIDs).Error; err != nil {
		return report, err
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT f.id FROM fills f LEFT JOIN ledger_events e ON e.fill_id=f.id AND e.event_type IN ('buy_fill','sell_fill') WHERE e.id IS NULL ORDER BY f.id`).Scan(&report.OrphanFillIDs).Error; err != nil {
		return report, err
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT e.id FROM ledger_events e LEFT JOIN fills f ON f.id=e.fill_id WHERE e.fill_id IS NOT NULL AND f.id IS NULL ORDER BY e.id`).Scan(&report.OrphanEventIDs).Error; err != nil {
		return report, err
	}
	var fills []database.Fill
	if err := s.DB.WithContext(ctx).Where("account_id = ? AND occurred_at <= ?", account, asOf).Find(&fills).Error; err != nil {
		return report, err
	}
	eventsByFill := map[string][]database.LedgerEvent{}
	for _, event := range events {
		if event.FillID != nil {
			eventsByFill[*event.FillID] = append(eventsByFill[*event.FillID], event)
		}
	}
	type orderTotals struct{ quantity, gross, fee accounting.Decimal }
	totalsByOrder := map[uint]orderTotals{}
	for _, fill := range fills {
		fillEvents := eventsByFill[fill.ID]
		fillCount := 0
		feeCash := accounting.Zero()
		for _, event := range fillEvents {
			if event.EventType == EventBuyFill || event.EventType == EventSellFill {
				fillCount++
				expectedCash := fill.GrossAmount
				if fill.Side == "buy" {
					expectedCash = expectedCash.Neg()
				}
				expectedAsset := fill.Quantity
				if fill.Side == "sell" {
					expectedAsset = expectedAsset.Neg()
				}
				if event.CashDelta.Cmp(expectedCash) != 0 || event.AssetDelta.Cmp(expectedAsset) != 0 {
					report.RecordIssues = append(report.RecordIssues, "fill "+fill.ID+" postings do not match immutable fill")
				}
			} else if event.EventType == EventTradingFee || event.EventType == EventExchangeFee {
				feeCash = feeCash.Add(event.CashDelta.Neg())
			}
		}
		if fillCount != 1 {
			report.RecordIssues = append(report.RecordIssues, fmt.Sprintf("fill %s has %d fill events", fill.ID, fillCount))
		}
		if feeCash.Cmp(fill.FeeAmount) != 0 {
			report.RecordIssues = append(report.RecordIssues, "fill "+fill.ID+" fee events do not match fill fee")
		}
		total := totalsByOrder[fill.OrderID]
		total.quantity = total.quantity.Add(fill.Quantity)
		total.gross = total.gross.Add(fill.GrossAmount)
		total.fee = total.fee.Add(fill.FeeAmount)
		totalsByOrder[fill.OrderID] = total
	}
	for orderID, total := range totalsByOrder {
		var order database.Order
		if err := s.DB.WithContext(ctx).First(&order, orderID).Error; err != nil {
			report.RecordIssues = append(report.RecordIssues, fmt.Sprintf("fills reference missing order %d", orderID))
			continue
		}
		if order.AmountCryptoExact == nil || order.AmountUsdtExact == nil || order.FeeExact == nil {
			report.RecordIssues = append(report.RecordIssues, fmt.Sprintf("order %d has no exact fill projection", order.ID))
		} else if order.AmountCryptoExact.Cmp(total.quantity) != 0 || order.AmountUsdtExact.Cmp(total.gross) != 0 || order.FeeExact.Cmp(total.fee) != 0 {
			report.RecordIssues = append(report.RecordIssues, fmt.Sprintf("order %d projection does not match its fills", order.ID))
		}
	}
	var state database.LedgerMigrationState
	if err := s.DB.WithContext(ctx).First(&state, "account_id = ?", account).Error; err == nil && state.UnresolvedJSON != "" {
		var unresolved []string
		if json.Unmarshal([]byte(state.UnresolvedJSON), &unresolved) == nil {
			report.Unresolved = append(report.Unresolved, unresolved...)
		}
	} else if err != nil {
		report.Unresolved = append(report.Unresolved, "ledger migration state is absent")
	}
	sort.Slice(report.Differences, func(i, j int) bool {
		return report.Differences[i].Dimension+report.Differences[i].Key < report.Differences[j].Dimension+report.Differences[j].Key
	})
	sort.Strings(report.Unresolved)
	sort.Strings(report.RecordIssues)
	report.Balanced = len(report.Differences) == 0 && len(report.OrphanOrderIDs) == 0 && len(report.OrphanFillIDs) == 0 && len(report.OrphanEventIDs) == 0 && len(report.RecordIssues) == 0 && len(report.Unresolved) == 0
	return report, nil
}

func (r ReconciliationReport) String() string {
	status := "BALANCED"
	if !r.Balanced {
		status = "UNRECONCILED"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s account=%s as_of=%s\n", status, r.AccountID, r.AsOf.UTC().Format(time.RFC3339Nano))
	for _, difference := range r.Differences {
		fmt.Fprintf(&b, "difference %s %s ledger=%s", difference.Dimension, difference.Key, difference.Ledger)
		if difference.Projection != nil {
			fmt.Fprintf(&b, " projection=%s delta=%s", *difference.Projection, *difference.Difference)
		}
		b.WriteByte('\n')
	}
	for _, issue := range r.Unresolved {
		fmt.Fprintf(&b, "unresolved %s\n", issue)
	}
	for _, id := range r.OrphanOrderIDs {
		fmt.Fprintf(&b, "orphan_order %d\n", id)
	}
	for _, id := range r.OrphanFillIDs {
		fmt.Fprintf(&b, "orphan_fill %s\n", id)
	}
	for _, id := range r.OrphanEventIDs {
		fmt.Fprintf(&b, "orphan_event %s\n", id)
	}
	for _, issue := range r.RecordIssues {
		fmt.Fprintf(&b, "record_issue %s\n", issue)
	}
	return b.String()
}
