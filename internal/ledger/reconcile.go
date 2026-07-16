package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"

	"gorm.io/gorm"
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
	PendingOrderIDs       []uint            `json:"pending_order_ids"`
	OrphanFillIDs         []string          `json:"orphan_fill_ids"`
	OrphanEventIDs        []string          `json:"orphan_event_ids"`
	RecordIssues          []string          `json:"record_issues"`
	Unresolved            []string          `json:"unresolved"`
	ExpectedIssues        []string          `json:"expected_issues"`
	ActionableIssues      []string          `json:"actionable_issues"`
	UnrealizedPnLBySymbol map[string]string `json:"unrealized_pnl_by_symbol"`
}

func (s *Service) Reconcile(ctx context.Context, account string, asOf time.Time) (ReconciliationReport, error) {
	if err := requirePrimaryAccount(account); err != nil {
		return ReconciliationReport{}, err
	}
	if !asOf.IsZero() {
		return ReconciliationReport{}, ErrHistoricalReconciliationUnsupported
	}
	if account == "" {
		account = DefaultAccountID
	}
	var report ReconciliationReport
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		snapshot := *s
		snapshot.DB = tx
		var reconcileErr error
		report, reconcileErr = snapshot.reconcileCurrent(ctx, account)
		return reconcileErr
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return report, err
}

func (s *Service) reconcileCurrent(ctx context.Context, account string) (ReconciliationReport, error) {
	asOf := s.now()
	report := ReconciliationReport{AsOf: asOf, AccountID: account, Balanced: true, CashByCurrency: map[string]string{}, AssetsBySymbol: map[string]string{}, FeesByCurrency: map[string]string{}, RealizedPnLByCurrency: map[string]string{}, Differences: []Difference{}, OrphanOrderIDs: []uint{}, PendingOrderIDs: []uint{}, OrphanFillIDs: []string{}, OrphanEventIDs: []string{}, RecordIssues: []string{}, Unresolved: []string{}, ExpectedIssues: []string{}, ActionableIssues: []string{}, UnrealizedPnLBySymbol: map[string]string{}}
	var events []database.LedgerEvent
	if err := s.DB.WithContext(ctx).Where("account_id = ? AND occurred_at <= ?", account, asOf).Order("occurred_at, recorded_at, id").Find(&events).Error; err != nil {
		return report, err
	}
	cash, assets, feesCurrency, pnlCurrency, basis, feesSymbol, pnlSymbol := map[string]accounting.Decimal{}, map[string]accounting.Decimal{}, map[string]accounting.Decimal{}, map[string]accounting.Decimal{}, map[string]accounting.Decimal{}, map[string]accounting.Decimal{}, map[string]accounting.Decimal{}
	for _, event := range events {
		cash[event.Currency] = cash[event.Currency].Add(event.CashDelta)
		if event.Symbol != "" {
			assets[event.Symbol] = assets[event.Symbol].Add(event.AssetDelta)
		}
		if event.EventType == EventTradingFee || event.EventType == EventExchangeFee {
			feesCurrency[event.Currency] = feesCurrency[event.Currency].Add(event.CashDelta.Neg())
		}
		pnlCurrency[event.Currency] = pnlCurrency[event.Currency].Add(event.RealizedPnL)
		if event.Symbol != "" {
			feesSymbol[event.Symbol] = feesSymbol[event.Symbol].Add(event.FeeDelta)
			pnlSymbol[event.Symbol] = pnlSymbol[event.Symbol].Add(event.RealizedPnL)
			basis[event.Symbol] = basis[event.Symbol].Add(event.CostBasisDelta)
		}
	}
	for key, value := range cash {
		report.CashByCurrency[key] = value.String()
	}
	for key, value := range assets {
		report.AssetsBySymbol[key] = value.String()
	}
	for key, value := range feesCurrency {
		report.FeesByCurrency[key] = value.String()
	}
	for key, value := range pnlCurrency {
		report.RealizedPnLByCurrency[key] = value.String()
	}

	var wallet database.Wallet
	if err := s.DB.WithContext(ctx).Where("account_id = ?", account).First(&wallet).Error; err != nil {
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
	for currency, amount := range cash {
		if currency != wallet.Currency && amount.Sign() != 0 {
			report.Differences = append(report.Differences, Difference{Dimension: "unsupported_currency", Key: currency, Ledger: amount.String()})
		}
	}
	var positions []database.Position
	if err := s.DB.WithContext(ctx).Where("account_id = ?", account).Find(&positions).Error; err != nil {
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
		compareExact := func(dimension string, projection *accounting.Decimal, ledgerValue accounting.Decimal) {
			if projection == nil {
				report.Differences = append(report.Differences, Difference{Dimension: dimension, Key: position.Symbol, Ledger: ledgerValue.String()})
				return
			}
			if projection.Cmp(ledgerValue) != 0 {
				p, delta := projection.String(), projection.Sub(ledgerValue).String()
				report.Differences = append(report.Differences, Difference{Dimension: dimension, Key: position.Symbol, Ledger: ledgerValue.String(), Projection: &p, Difference: &delta})
			}
		}
		compareExact("cost_basis", position.CostBasisExact, basis[position.Symbol])
		compareExact("fees", position.FeesExact, feesSymbol[position.Symbol])
		compareExact("realized_pnl", position.RealizedPnLExact, pnlSymbol[position.Symbol])
		if position.AmountExact != nil && position.CostBasisExact != nil && position.LastMarkPrice != nil {
			mark, _ := accounting.FromFloat(*position.LastMarkPrice)
			report.UnrealizedPnLBySymbol[position.Symbol] = position.AmountExact.Mul(mark).Sub(*position.CostBasisExact).String()
		}
	}
	for symbol, amount := range assets {
		if !seen[symbol] && amount.Sign() != 0 {
			report.Differences = append(report.Differences, Difference{Dimension: "asset", Key: symbol, Ledger: amount.String()})
		}
	}

	if err := s.DB.WithContext(ctx).Raw(`SELECT o.id FROM orders o LEFT JOIN fills f ON f.order_id=o.id WHERE o.account_id=? AND f.id IS NULL AND o.status='filled' ORDER BY o.id`, account).Scan(&report.OrphanOrderIDs).Error; err != nil {
		return report, err
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT o.id FROM orders o LEFT JOIN fills f ON f.order_id=o.id WHERE o.account_id=? AND f.id IS NULL AND o.status IN ('pending','failed') ORDER BY o.id`, account).Scan(&report.PendingOrderIDs).Error; err != nil {
		return report, err
	}
	for _, id := range report.PendingOrderIDs {
		report.ExpectedIssues = append(report.ExpectedIssues, fmt.Sprintf("non-filled audit order %d has no fill", id))
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT f.id FROM fills f LEFT JOIN ledger_events e ON e.fill_id=f.id AND e.event_type IN ('buy_fill','sell_fill') WHERE f.account_id=? AND e.id IS NULL ORDER BY f.id`, account).Scan(&report.OrphanFillIDs).Error; err != nil {
		return report, err
	}
	if err := s.DB.WithContext(ctx).Raw(`SELECT e.id FROM ledger_events e LEFT JOIN fills f ON f.id=e.fill_id WHERE e.account_id=? AND e.fill_id IS NOT NULL AND f.id IS NULL ORDER BY e.id`, account).Scan(&report.OrphanEventIDs).Error; err != nil {
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
			if state.Status == "ready" {
				report.ExpectedIssues = append(report.ExpectedIssues, unresolved...)
			} else {
				report.ActionableIssues = append(report.ActionableIssues, unresolved...)
			}
		}
	} else if err != nil {
		report.Unresolved = append(report.Unresolved, "ledger migration state is absent")
		report.ActionableIssues = append(report.ActionableIssues, "ledger migration state is absent")
	}
	sort.Slice(report.Differences, func(i, j int) bool {
		return report.Differences[i].Dimension+report.Differences[i].Key < report.Differences[j].Dimension+report.Differences[j].Key
	})
	sort.Strings(report.Unresolved)
	sort.Strings(report.RecordIssues)
	for _, d := range report.Differences {
		report.ActionableIssues = append(report.ActionableIssues, "exact "+d.Dimension+" difference for "+d.Key)
	}
	for _, id := range report.OrphanOrderIDs {
		report.ActionableIssues = append(report.ActionableIssues, fmt.Sprintf("filled order %d has no fill", id))
	}
	for _, id := range report.OrphanFillIDs {
		report.ActionableIssues = append(report.ActionableIssues, "fill "+id+" has no economic fill event")
	}
	for _, id := range report.OrphanEventIDs {
		report.ActionableIssues = append(report.ActionableIssues, "event "+id+" references a missing fill")
	}
	report.ActionableIssues = append(report.ActionableIssues, report.RecordIssues...)
	sort.Strings(report.ExpectedIssues)
	sort.Strings(report.ActionableIssues)
	report.Balanced = len(report.ActionableIssues) == 0
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
