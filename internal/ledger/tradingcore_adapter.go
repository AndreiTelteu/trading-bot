package ledger

import (
	"context"
	"fmt"
	"gorm.io/gorm"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"
	"trading-go/internal/tradingcore"
)

type ContractAdapter struct{ service *Service }

func NewContractAdapter(db *gorm.DB) *ContractAdapter { return &ContractAdapter{service: New(db)} }

var _ tradingcore.Ledger = (*ContractAdapter)(nil)

func (adapter *ContractAdapter) AppendAtomic(ctx context.Context, batch tradingcore.LedgerBatch) (tradingcore.LedgerAppendOutcome, error) {
	events := batch.Events()
	ids := make([]tradingcore.EventID, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	if len(events) != 1 {
		return tradingcore.NewLedgerAppendOutcome(tradingcore.LedgerRejected, batch.IdempotencyKey(), nil, "unsupported_batch_shape", "runtime adapter accepts one projection-safe cash event")
	}
	event := events[0]
	postings := event.Postings()
	if len(postings) != 1 || postings[0].Dimension != tradingcore.PostingCash {
		return tradingcore.NewLedgerAppendOutcome(tradingcore.LedgerRejected, batch.IdempotencyKey(), nil, "projection_data_required", "asset/trade events must use typed fill or correction commands")
	}
	amount, err := accounting.Parse(postings[0].Amount.Decimal().String())
	if err != nil {
		return tradingcore.LedgerAppendOutcome{}, err
	}
	kind := string(event.Type)
	commandAmount := amount
	if kind == EventCapitalWithdrawal {
		commandAmount = amount.Neg()
	}
	result, err := adapter.service.ApplyAdjustment(ctx, AdjustmentCommand{EventID: event.ID.String(), IdempotencyKey: batch.IdempotencyKey().String(), AccountID: event.AccountID.String(), Type: kind, Amount: commandAmount, Currency: postings[0].AssetID.String(), Actor: event.Provenance.Actor, Reason: event.Provenance.Reason, OccurredAt: event.OccurredAt})
	if err != nil {
		errorKind, code := ErrorDetails(err)
		status := tradingcore.LedgerRejected
		if errorKind == KindIndeterminate {
			status = tradingcore.LedgerAppendIndeterminate
		}
		return tradingcore.NewLedgerAppendOutcome(status, batch.IdempotencyKey(), nil, code, err.Error())
	}
	status := tradingcore.LedgerAppended
	if result.AlreadyApplied {
		status = tradingcore.LedgerAlreadyApplied
	}
	return tradingcore.NewLedgerAppendOutcome(status, batch.IdempotencyKey(), ids, "", "")
}

func (adapter *ContractAdapter) Events(ctx context.Context, account tradingcore.AccountID, until time.Time) ([]tradingcore.LedgerEvent, error) {
	if err := requirePrimaryAccount(account.String()); err != nil {
		return nil, err
	}
	var rows []database.LedgerEvent
	query := adapter.service.DB.WithContext(ctx).Where("account_id = ?", account.String())
	if !until.IsZero() {
		query = query.Where("occurred_at <= ?", until)
	}
	if err := query.Order("occurred_at,recorded_at,id").Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]tradingcore.LedgerEvent, 0, len(rows))
	for _, row := range rows {
		eventID, err := tradingcore.NewEventID(row.ID)
		if err != nil {
			return nil, err
		}
		key, err := tradingcore.NewIdempotencyKey(row.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		accountID, err := tradingcore.NewAccountID(row.AccountID)
		if err != nil {
			return nil, err
		}
		venue, err := tradingcore.NewVenueID(row.VenueID)
		if err != nil {
			return nil, err
		}
		event := tradingcore.LedgerEvent{ID: eventID, IdempotencyKey: key, Type: tradingcore.LedgerEventType(row.EventType), AccountID: accountID, VenueID: venue, OccurredAt: row.OccurredAt, RecordedAt: row.RecordedAt, Versions: tradingcore.VersionContext{Strategy: row.StrategyVersion, Policy: row.PolicyVersion}, Provenance: tradingcore.Provenance{Actor: row.Actor, Reason: row.Reason, Source: "postgres"}}
		if row.OrderID != nil {
			event.OrderID, err = tradingcore.NewOrderID(fmt.Sprint(*row.OrderID))
			if err != nil {
				return nil, err
			}
		}
		if row.FillID != nil {
			event.FillID, err = tradingcore.NewFillID(*row.FillID)
			if err != nil {
				return nil, err
			}
		}
		if row.PositionID != nil {
			event.PositionID, err = tradingcore.NewPositionID(fmt.Sprint(*row.PositionID))
			if err != nil {
				return nil, err
			}
		}
		if row.ReversesEventID != nil {
			event.ReversesEventID, err = tradingcore.NewEventID(*row.ReversesEventID)
			if err != nil {
				return nil, err
			}
		}
		postings := []tradingcore.LedgerPosting{}
		if row.CashDelta.Sign() != 0 {
			asset, _ := tradingcore.NewAssetID(row.Currency)
			amount, _ := tradingcore.NewSignedAmount(mustCoreDecimal(row.CashDelta))
			postings = append(postings, tradingcore.LedgerPosting{Dimension: tradingcore.PostingCash, AssetID: asset, Amount: amount})
		}
		if row.AssetDelta.Sign() != 0 {
			asset, _ := tradingcore.NewAssetID(row.Symbol)
			instrument, _ := tradingcore.NewInstrumentID(row.Symbol)
			amount, _ := tradingcore.NewSignedAmount(mustCoreDecimal(row.AssetDelta))
			postings = append(postings, tradingcore.LedgerPosting{Dimension: tradingcore.PostingAsset, AssetID: asset, InstrumentID: instrument, Amount: amount})
		}
		if len(postings) == 0 {
			asset, _ := tradingcore.NewAssetID(row.Currency)
			amount, _ := tradingcore.NewSignedAmount(mustCoreDecimal(accounting.Zero()))
			postings = append(postings, tradingcore.LedgerPosting{Dimension: tradingcore.PostingCash, AssetID: asset, Amount: amount})
		}
		converted, err := tradingcore.NewLedgerEvent(event, postings)
		if err != nil {
			return nil, err
		}
		result = append(result, converted)
	}
	return result, nil
}

func (adapter *ContractAdapter) Reconcile(ctx context.Context, snapshot tradingcore.PortfolioSnapshot) (tradingcore.ReconciliationReport, error) {
	events, err := adapter.Events(ctx, snapshot.AccountID(), snapshot.AsOf())
	if err != nil {
		return tradingcore.ReconciliationReport{}, err
	}
	cash := map[tradingcore.AssetID]accounting.Decimal{}
	assets := map[tradingcore.InstrumentID]accounting.Decimal{}
	for _, event := range events {
		for _, posting := range event.Postings() {
			value, _ := accounting.Parse(posting.Amount.Decimal().String())
			if posting.Dimension == tradingcore.PostingCash {
				cash[posting.AssetID] = cash[posting.AssetID].Add(value)
			} else {
				assets[posting.InstrumentID] = assets[posting.InstrumentID].Add(value)
			}
		}
	}
	cashDiff := map[tradingcore.AssetID]tradingcore.SignedAmount{}
	for asset, projected := range snapshot.Cash() {
		p, _ := accounting.Parse(projected.Decimal().String())
		difference := p.Sub(cash[asset])
		if difference.Sign() != 0 {
			cashDiff[asset], _ = tradingcore.NewSignedAmount(mustCoreDecimal(difference))
		}
	}
	for asset, ledgerValue := range cash {
		if _, present := snapshot.Cash()[asset]; present || ledgerValue.Sign() == 0 {
			continue
		}
		cashDiff[asset], _ = tradingcore.NewSignedAmount(mustCoreDecimal(ledgerValue.Neg()))
	}
	positionDiff := map[tradingcore.InstrumentID]tradingcore.SignedAmount{}
	seenPositions := map[tradingcore.InstrumentID]bool{}
	for _, position := range snapshot.Positions() {
		seenPositions[position.Instrument.ID] = true
		p, _ := accounting.Parse(position.Quantity.Decimal().String())
		difference := p.Sub(assets[position.Instrument.ID])
		if difference.Sign() != 0 {
			positionDiff[position.Instrument.ID], _ = tradingcore.NewSignedAmount(mustCoreDecimal(difference))
		}
	}
	for instrument, ledgerValue := range assets {
		if seenPositions[instrument] || ledgerValue.Sign() == 0 {
			continue
		}
		positionDiff[instrument], _ = tradingcore.NewSignedAmount(mustCoreDecimal(ledgerValue.Neg()))
	}
	balanced := len(cashDiff) == 0 && len(positionDiff) == 0
	return tradingcore.NewReconciliationReport(snapshot.AsOf(), snapshot.AccountID(), balanced, cashDiff, positionDiff, nil), nil
}

func mustCoreDecimal(value accounting.Decimal) tradingcore.Decimal {
	result, err := tradingcore.ParseDecimal(value.String())
	if err != nil {
		panic(err)
	}
	return result
}
