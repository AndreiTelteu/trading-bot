package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"
	"trading-go/internal/tradingcore"
)

type ContractAdapter struct{ service *Service }

func NewContractAdapter(db *gorm.DB) *ContractAdapter { return &ContractAdapter{service: New(db)} }
func (adapter *ContractAdapter) SetAfterWriteHook(hook func(string) error) {
	adapter.service.AfterWrite = hook
}

var _ tradingcore.Ledger = (*ContractAdapter)(nil)
var _ tradingcore.FillLedger = (*ContractAdapter)(nil)

// RecordBrokerOutcome is the only economic mutation port used by the shared
// orchestrator. Brokers themselves never update projections.
func (adapter *ContractAdapter) RecordBrokerOutcome(ctx context.Context, approved tradingcore.DecisionBatch, outcome tradingcore.BrokerBatchOutcome) error {
	plan, identity, payloadHash, err := validateBrokerOutcome(approved, outcome)
	if err != nil {
		return err
	}
	err = adapter.service.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing database.BrokerOutcomeIngestion
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&existing, "id = ?", identity).Error
		if err == nil {
			if existing.PayloadHash != payloadHash {
				return ErrIdempotencyConflict
			}
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		account := "primary"
		if len(plan) > 0 {
			account = plan[0].intent.AccountID.String()
		}
		if err := tx.Create(&database.BrokerOutcomeIngestion{ID: identity, AccountID: account, PayloadHash: payloadHash, CreatedAt: adapter.service.now()}).Error; err != nil {
			return err
		}
		for _, item := range plan {
			zero := accounting.Zero()
			requestedPrice := 0.0
			if reference, ok := item.intent.ReferencePrice.Get(); ok {
				requestedPrice = reference.Decimal().Float64()
			} else if limit, ok := item.intent.LimitPrice.Get(); ok {
				requestedPrice = limit.Decimal().Float64()
			}
			clientID := item.intent.IdempotencyKey.String()
			providerID := item.accepted.ProviderOrderID
			observation, _ := json.Marshal(map[string]string{"active_path": string(item.intent.ExecutionMode), "flag_schema_version": item.intent.Versions.FlagSchema, "engine_version": item.intent.Versions.Engine, "strategy_version": item.intent.Versions.Strategy, "policy_version": item.intent.Versions.Policy, "model_version": item.intent.Versions.Model, "dataset_version": item.intent.Versions.Dataset, "universe_version": item.intent.Versions.Universe})
			order := database.Order{AccountID: item.intent.AccountID.String(), OrderType: string(item.intent.Side), Symbol: item.symbol, Status: string(tradingcore.BrokerAccepted), ExecutionMode: string(item.intent.ExecutionMode), RequestedQuantityExact: &item.requested, ExecutedQuantityExact: &zero, RemainingQuantityExact: &item.requested, AmountCryptoExact: &zero, AmountUsdtExact: &zero, FeeExact: &zero, AmountCrypto: 0, AmountUsdt: 0, RequestedPrice: &requestedPrice, ClientOrderID: &clientID, ExchangeOrderID: &providerID, SubmittedAt: &item.accepted.AcceptedAt, ExecutedAt: item.accepted.AcceptedAt, PolicyVersion: item.intent.Versions.Policy, ModelVersion: item.intent.Versions.Model, Stage08ContextJSON: string(observation)}
			if err := tx.Create(&order).Error; err != nil {
				return err
			}
			child := *adapter.service
			child.DB = tx
			for _, command := range item.commands {
				command.ExistingOrderID = order.ID
				if _, err := child.ApplyFill(ctx, command); err != nil {
					return err
				}
			}
			executed := item.requested.Sub(item.remaining)
			updates := map[string]interface{}{"status": string(item.accepted.Status), "requested_quantity_exact": item.requested, "executed_quantity_exact": executed, "remaining_quantity_exact": item.remaining}
			if item.accepted.Status == tradingcore.BrokerFilled {
				updates["filled_at"] = item.accepted.AcceptedAt
			}
			if err := tx.Model(&database.Order{}).Where("id = ?", order.ID).Updates(updates).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		// A concurrent retry may have raced the insert. It is successful only when
		// the complete durable outcome with the identical payload is now present.
		var existing database.BrokerOutcomeIngestion
		if lookupErr := adapter.service.DB.WithContext(ctx).First(&existing, "id = ?", identity).Error; lookupErr == nil && existing.PayloadHash == payloadHash {
			return nil
		}
	}
	return normalizePersistenceError(err)
}

type outcomePlan struct {
	intent               tradingcore.OrderIntent
	accepted             tradingcore.AcceptedOrder
	requested, remaining accounting.Decimal
	symbol               string
	commands             []FillCommand
}

func validateBrokerOutcome(approved tradingcore.DecisionBatch, outcome tradingcore.BrokerBatchOutcome) ([]outcomePlan, string, string, error) {
	if outcome.Completeness() != tradingcore.OutcomeComplete {
		return nil, "", "", newError(KindIndeterminate, "broker_outcome_indeterminate", "broker outcome must be recovered before ledger ingestion", nil)
	}
	intents := map[tradingcore.OrderID]tradingcore.OrderIntent{}
	seen := map[tradingcore.OrderID]bool{}
	providerFills := map[string]bool{}
	identityValues := []string{}
	for _, intent := range approved.Intents() {
		if err := requirePrimaryAccount(intent.AccountID.String()); err != nil {
			return nil, "", "", err
		}
		if intent.ExecutionMode != tradingcore.ExecutionPaper && intent.ExecutionMode != tradingcore.ExecutionLimitedLive && intent.ExecutionMode != tradingcore.ExecutionFullLive {
			return nil, "", "", fmt.Errorf("execution mode %s cannot produce an economic broker outcome", intent.ExecutionMode)
		}
		intents[intent.ID] = intent
		identityValues = append(identityValues, intent.IdempotencyKey.String())
	}
	plans := []outcomePlan{}
	payload := []map[string]interface{}{}
	for _, accepted := range outcome.Accepted() {
		intent, ok := intents[accepted.OrderID]
		if !ok || seen[accepted.OrderID] {
			return nil, "", "", fmt.Errorf("accepted order %s is not a unique approved intent", accepted.OrderID.String())
		}
		seen[accepted.OrderID] = true
		if intent.ExecutionMode == tradingcore.ExecutionLimitedLive || intent.ExecutionMode == tradingcore.ExecutionFullLive {
			return nil, "", "", ErrExchangeExecutionFenced
		}
		requested, _ := accounting.Parse(intent.Quantity.Decimal().String())
		executed := accounting.Zero()
		commands := []FillCommand{}
		metadata := intent.Metadata()
		if metadata["cost_policy_version"] == "" {
			return nil, "", "", fmt.Errorf("approved intent %s lacks execution cost reservation version", intent.ID.String())
		}
		symbol := intent.Instrument.BaseAsset.String()
		for _, fill := range accepted.Fills() {
			if fill.Instrument.ID != intent.Instrument.ID || fill.Side != intent.Side || fill.FeeAsset != intent.Instrument.QuoteAsset {
				return nil, "", "", fmt.Errorf("fill %s dimensions do not match approved intent", fill.ID.String())
			}
			if providerFills[fill.ProviderFillID] {
				return nil, "", "", fmt.Errorf("duplicate provider fill id %s", fill.ProviderFillID)
			}
			providerFills[fill.ProviderFillID] = true
			if fill.CostModelVersion != metadata["cost_policy_version"] {
				return nil, "", "", fmt.Errorf("fill cost model version does not match approved reservation")
			}
			quantity, _ := accounting.Parse(fill.Quantity.Decimal().String())
			executed = executed.Add(quantity)
			fillPrice, _ := accounting.Parse(fill.Price.Decimal().String())
			requestedPrice := fillPrice
			if reference, ok := intent.ReferencePrice.Get(); ok {
				requestedPrice, _ = accounting.Parse(reference.Decimal().String())
			}
			fee, _ := accounting.Parse(fill.Fee.Decimal().String())
			stop, _ := metadataFloat(metadata, "stop_price")
			target, _ := metadataFloat(metadata, "take_profit_price")
			atr, hasATR := metadataFloat(metadata, "atr_value")
			mult, hasMult := metadataFloat(metadata, "atr_trailing_mult")
			var trailing *float64
			if hasATR && hasMult {
				value := fillPrice.Float64() - *atr**mult
				if value > 0 {
					trailing = &value
				}
			}
			maxBars, _ := metadataInt(metadata, "max_bars_held")
			eventMetadata := map[string]interface{}{"horizon": intent.Horizon, "active_path": string(intent.ExecutionMode), "flag_schema_version": intent.Versions.FlagSchema, "engine_version": intent.Versions.Engine, "strategy_version": intent.Versions.Strategy, "risk_policy_version": intent.Versions.Policy, "model_version": intent.Versions.Model, "dataset_version": intent.Versions.Dataset, "universe_version": intent.Versions.Universe, "cost_model_version": fill.CostModelVersion}
			commands = append(commands, FillCommand{IdempotencyKey: intent.IdempotencyKey.String() + ":" + fill.ProviderFillID, AccountID: intent.AccountID.String(), Symbol: symbol, Side: string(intent.Side), Quantity: quantity, RequestedPrice: requestedPrice, FillPrice: fillPrice, Fee: fee, FeeType: EventTradingFee, Currency: intent.Instrument.QuoteAsset.String(), ExecutionMode: string(intent.ExecutionMode), VenueID: intent.Instrument.Venue.String(), ProviderFillID: fill.ProviderFillID, ProviderOrderID: accepted.ProviderOrderID, OrderStatus: string(accepted.Status), OccurredAt: fill.FilledAt, Actor: fill.Provenance.Source, Reason: intent.Reason, StrategyVersion: intent.Versions.Strategy, PolicyVersion: intent.Versions.Policy, CostModelVersion: fill.CostModelVersion, Metadata: eventMetadata, StopPrice: stop, TakeProfitPrice: target, TrailingStopPrice: trailing, LastAtrValue: atr, MaxBarsHeld: maxBars, EntrySource: "auto_trend", DecisionTimeframe: intent.Horizon, ModelVersion: intent.Versions.Model, RolloutState: metadata["rollout_state"]})
		}
		remaining := requested.Sub(executed)
		if value, ok := accepted.Remaining.Get(); ok {
			remaining, _ = accounting.Parse(value.Decimal().String())
		}
		if executed.Add(remaining).Cmp(requested) != 0 || remaining.Sign() < 0 {
			return nil, "", "", fmt.Errorf("accepted order %s quantities do not reconcile", accepted.OrderID.String())
		}
		if accepted.Status == tradingcore.BrokerFilled && remaining.Sign() != 0 {
			return nil, "", "", fmt.Errorf("filled order has remaining quantity")
		}
		plans = append(plans, outcomePlan{intent: intent, accepted: accepted, requested: requested, remaining: remaining, symbol: symbol, commands: commands})
		payload = append(payload, map[string]interface{}{"intent": intent.IdempotencyKey.String(), "provider_order": accepted.ProviderOrderID, "status": accepted.Status, "requested": requested.String(), "executed": executed.String(), "remaining": remaining.String(), "fills": commands})
	}
	for _, rejected := range outcome.Rejected() {
		if _, ok := intents[rejected.OrderID]; !ok || seen[rejected.OrderID] {
			return nil, "", "", fmt.Errorf("rejected order %s is not a unique approved intent", rejected.OrderID.String())
		}
		seen[rejected.OrderID] = true
		payload = append(payload, map[string]interface{}{"intent": intents[rejected.OrderID].IdempotencyKey.String(), "rejection": rejected.Code})
	}
	if len(seen) != len(intents) {
		return nil, "", "", fmt.Errorf("complete broker outcome does not resolve every approved intent")
	}
	identityJSON, _ := json.Marshal(identityValues)
	identitySum := sha256.Sum256(identityJSON)
	identity := "outcome-" + hex.EncodeToString(identitySum[:])
	payloadJSON, _ := json.Marshal(payload)
	payloadSum := sha256.Sum256(payloadJSON)
	return plans, identity, hex.EncodeToString(payloadSum[:]), nil
}

func metadataFloat(values map[string]string, key string) (*float64, bool) {
	raw, ok := values[key]
	if !ok {
		return nil, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, false
	}
	return &value, true
}
func metadataInt(values map[string]string, key string) (*int, bool) {
	raw, ok := values[key]
	if !ok {
		return nil, false
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, false
	}
	return &value, true
}

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
