package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/cutover"
	"trading-go/internal/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const DefaultAccountID = "primary"

const (
	EventCapitalDeposit    = "capital_deposit"
	EventCapitalWithdrawal = "capital_withdrawal"
	EventBuyFill           = "buy_fill"
	EventSellFill          = "sell_fill"
	EventTradingFee        = "trading_fee"
	EventExchangeFee       = "exchange_fee"
	EventFundingInterest   = "funding_interest"
	EventAdminCorrection   = "administrative_correction"
	EventReversal          = "reversal"
)

var (
	ErrUnreconciledLegacyState             = unavailable("ledger_not_ready", "ledger migration or exposure resolution required")
	ErrInsufficientCash                    = conflict("insufficient_cash", "insufficient cash", nil)
	ErrInsufficientAsset                   = conflict("insufficient_asset", "insufficient position quantity", nil)
	ErrIdempotencyConflict                 = conflict("idempotency_conflict", "idempotency key reused with different payload", nil)
	ErrProjectionUnavailable               = unavailable("projection_unavailable", "exact ledger projection unavailable")
	ErrExchangeExecutionFenced             = unavailable("exchange_execution_fenced", "exchange execution is disabled until durable Stage 02 broker recovery is available")
	ErrHistoricalReconciliationUnsupported = validation("historical_reconciliation_unsupported", "historical as_of reconciliation is not supported by current projections")
)

type Service struct {
	DB         *gorm.DB
	Now        func() time.Time
	AfterWrite func(stage string) error // deterministic rollback seam used by transaction tests
}

func New(db *gorm.DB) *Service {
	// Ledger writes must retain the top-level transaction ID because deferred
	// database guards couple projections and immutable evidence by xmin. When a
	// caller already owns a transaction, reuse it instead of introducing a
	// savepoint subtransaction with a different xmin.
	if db == nil {
		return &Service{Now: time.Now}
	}
	return &Service{DB: db.Session(&gorm.Session{DisableNestedTransaction: true}), Now: time.Now}
}

func (s *Service) CheckReady(ctx context.Context, account string) error {
	if err := requirePrimaryAccount(account); err != nil {
		return err
	}
	if account == "" {
		account = DefaultAccountID
	}
	var state database.LedgerMigrationState
	if err := s.DB.WithContext(ctx).First(&state, "account_id = ?", account).Error; err != nil || state.Status != "ready" {
		return ErrUnreconciledLegacyState
	}
	var wallet database.Wallet
	if err := s.DB.WithContext(ctx).Where("account_id = ?", account).First(&wallet).Error; err != nil {
		return err
	}
	if wallet.BalanceExact == nil {
		return ErrProjectionUnavailable
	}
	return nil
}

type FillCommand struct {
	IdempotencyKey, AccountID, Symbol, Side                     string
	Quantity, RequestedPrice, FillPrice, Fee                    accounting.Decimal
	FeeType, Currency, ExecutionMode                            string
	ProviderFillID                                              string
	ProviderOrderID                                             string
	VenueID                                                     string
	FeeCurrency                                                 string
	OrderStatus                                                 string
	ExistingOrderID                                             uint
	OccurredAt                                                  time.Time
	Actor, Reason                                               string
	StrategyVersion, PolicyVersion                              string
	CostModelVersion                                            string
	Metadata                                                    map[string]interface{}
	EntrySource, DecisionTimeframe                              string
	ModelVersion, UniverseMode, RolloutState                    string
	ExperimentID                                                *string
	PredictionLogID                                             *uint
	DecisionContextJSON                                         string
	StopPrice, TakeProfitPrice, TrailingStopPrice, LastAtrValue *float64
	MaxBarsHeld                                                 *int
}

type FillResult struct {
	AlreadyApplied bool              `json:"already_applied"`
	Wallet         database.Wallet   `json:"wallet"`
	Position       database.Position `json:"position"`
	Order          database.Order    `json:"order"`
	Fill           database.Fill     `json:"fill"`
}

func (s *Service) ApplyFill(ctx context.Context, command FillCommand) (FillResult, error) {
	if err := requirePrimaryAccount(command.AccountID); err != nil {
		return FillResult{}, err
	}
	if command.CostModelVersion == "" {
		command.CostModelVersion = "legacy-cost-v1"
	}
	if err := validateFill(command); err != nil {
		return FillResult{}, err
	}
	if command.AccountID == "" {
		command.AccountID = DefaultAccountID
	}
	if command.VenueID == "" {
		command.VenueID = "internal"
	}
	if command.FeeCurrency == "" {
		command.FeeCurrency = command.Currency
	}
	if command.FeeCurrency != command.Currency {
		return FillResult{}, validation("unsupported_fee_asset", "fees must use the settlement currency")
	}
	if command.ExecutionMode == "exchange" && (command.VenueID == "internal" || command.ProviderFillID == "" || command.ProviderOrderID == "") {
		return FillResult{}, validation("provider_identity_required", "exchange fills require venue, provider order id, and genuine provider fill id")
	}
	if command.OccurredAt.IsZero() {
		command.OccurredAt = s.now()
	}
	recordedAt := s.now()
	if command.OccurredAt.After(recordedAt) {
		return FillResult{}, validation("future_occurred_at", "occurred_at must be no later than recorded_at")
	}
	payloadHash, metadataJSON, err := hashPayload(command)
	if err != nil {
		return FillResult{}, err
	}
	stage08JSON := metadataJSONForCommand(command)
	var result FillResult
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := allowLedgerProjectionWrites(tx); err != nil {
			return err
		}
		if err := lockAccount(tx, command.AccountID); err != nil {
			return err
		}
		already, err := beginBatch(tx, command.IdempotencyKey, command.AccountID, payloadHash, command.OccurredAt)
		if err != nil {
			return err
		}
		if already {
			loaded, err := loadFillResult(tx, command.IdempotencyKey)
			if err != nil {
				return err
			}
			loaded.AlreadyApplied = true
			result = loaded
			return nil
		}
		if err := requireReady(tx, command.AccountID); err != nil {
			return err
		}

		var wallet database.Wallet
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&wallet).Error; err != nil {
			return err
		}
		if wallet.BalanceExact == nil {
			return ErrProjectionUnavailable
		}
		if !strings.EqualFold(wallet.Currency, command.Currency) {
			return validation("unsupported_currency", fmt.Sprintf("fill currency %s does not match wallet currency %s", command.Currency, wallet.Currency))
		}

		gross := command.Quantity.Mul(command.FillPrice)
		cashDelta := gross
		assetDelta := command.Quantity
		if command.Side == "buy" {
			cashDelta = gross.Neg()
		} else {
			assetDelta = command.Quantity.Neg()
		}
		newCash := wallet.BalanceExact.Add(cashDelta).Sub(command.Fee)
		if newCash.Sign() < 0 {
			return ErrInsufficientCash
		}

		position, realized, basisDelta, err := applyPosition(tx, command, gross)
		if err != nil {
			return err
		}
		wallet.BalanceExact = decimalPtr(newCash)
		wallet.Balance = newCash.Float64()
		if err := tx.Save(&wallet).Error; err != nil {
			return err
		}
		if err := s.fail("projection"); err != nil {
			return err
		}

		order, err := upsertFilledOrder(tx, command, position, gross)
		if err != nil {
			return err
		}
		if err := s.fail("order"); err != nil {
			return err
		}
		fillID := stableID("fill", command.IdempotencyKey)
		providerFillID := stringPtrOrNil(command.ProviderFillID)
		fill := database.Fill{
			ID: fillID, LedgerBatchID: command.IdempotencyKey, AccountID: command.AccountID, VenueID: command.VenueID,
			OrderID: order.ID, ProviderFillID: providerFillID, PositionID: position.ID,
			Symbol: command.Symbol, Side: command.Side, Quantity: command.Quantity,
			RequestedPrice: command.RequestedPrice, FillPrice: command.FillPrice, GrossAmount: gross,
			FeeAmount: command.Fee, FeeType: command.FeeType, FeeCurrency: command.FeeCurrency,
			ExecutionMode: command.ExecutionMode, StrategyVersion: command.StrategyVersion,
			PolicyVersion: command.PolicyVersion, OccurredAt: command.OccurredAt, CreatedAt: recordedAt,
			CostModelVersion:   command.CostModelVersion,
			Stage08ContextJSON: stage08JSON,
		}
		if err := tx.Create(&fill).Error; err != nil {
			return err
		}
		if err := s.fail("fill"); err != nil {
			return err
		}

		fillEventType := EventBuyFill
		if command.Side == "sell" {
			fillEventType = EventSellFill
		}
		orderID, positionID := order.ID, position.ID
		events := []database.LedgerEvent{{
			ID: stableID("event-fill", command.IdempotencyKey), LedgerBatchID: command.IdempotencyKey,
			Sequence: 1, IdempotencyKey: command.IdempotencyKey + ":fill", EventType: fillEventType,
			AccountID: command.AccountID, VenueID: command.VenueID, Currency: command.Currency, Symbol: command.Symbol,
			CashDelta: cashDelta, AssetDelta: assetDelta, OrderID: &orderID, FillID: &fillID,
			PositionID: &positionID, ExecutionMode: command.ExecutionMode,
			StrategyVersion: command.StrategyVersion, PolicyVersion: command.PolicyVersion,
			Actor: nonempty(command.Actor, "execution_adapter"), Reason: command.Reason,
			RealizedPnL: realized, CostBasisDelta: basisDelta, FeeDelta: accounting.Zero(), MetadataJSON: metadataJSON,
			OccurredAt: command.OccurredAt, RecordedAt: recordedAt,
			Stage08ContextJSON: stage08JSON,
		}}
		if command.Fee.Sign() > 0 {
			feeType := EventTradingFee
			if command.FeeType == EventExchangeFee || command.ExecutionMode == "exchange" {
				feeType = EventExchangeFee
			}
			events = append(events, database.LedgerEvent{
				ID: stableID("event-fee", command.IdempotencyKey), LedgerBatchID: command.IdempotencyKey,
				Sequence: 2, IdempotencyKey: command.IdempotencyKey + ":fee", EventType: feeType,
				AccountID: command.AccountID, VenueID: command.VenueID, Currency: command.Currency, Symbol: command.Symbol,
				CashDelta: command.Fee.Neg(), AssetDelta: accounting.Zero(), OrderID: &orderID,
				FillID: &fillID, PositionID: &positionID, ExecutionMode: command.ExecutionMode,
				StrategyVersion: command.StrategyVersion, PolicyVersion: command.PolicyVersion,
				Actor: nonempty(command.Actor, "execution_adapter"), Reason: command.Reason,
				RealizedPnL: command.Fee.Neg(), CostBasisDelta: accounting.Zero(), FeeDelta: command.Fee, MetadataJSON: metadataJSON,
				OccurredAt: command.OccurredAt, RecordedAt: recordedAt,
				Stage08ContextJSON: stage08JSON,
			})
		}
		if err := tx.Create(&events).Error; err != nil {
			return err
		}
		if err := s.fail("ledger"); err != nil {
			return err
		}
		result = FillResult{Wallet: wallet, Position: position, Order: order, Fill: fill}
		return nil
	})
	return result, normalizePersistenceError(err)
}

func validateFill(command FillCommand) error {
	if strings.TrimSpace(command.IdempotencyKey) == "" || strings.TrimSpace(command.Symbol) == "" {
		return validation("invalid_fill", "idempotency key and symbol are required")
	}
	if command.Side != "buy" && command.Side != "sell" {
		return validation("invalid_side", "side must be buy or sell")
	}
	if command.Quantity.Sign() <= 0 || command.RequestedPrice.Sign() <= 0 || command.FillPrice.Sign() <= 0 || command.Fee.Sign() < 0 {
		return validation("invalid_amount", "quantity/prices must be positive and fee non-negative")
	}
	if command.Currency == "" || command.ExecutionMode == "" || command.FeeType == "" {
		return validation("invalid_dimensions", "currency, execution mode, and fee type are required")
	}
	if command.CostModelVersion == "" {
		return validation("invalid_cost_model_version", "cost model version is required")
	}
	return nil
}

func applyPosition(tx *gorm.DB, command FillCommand, gross accounting.Decimal) (database.Position, accounting.Decimal, accounting.Decimal, error) {
	var position database.Position
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND symbol = ?", command.AccountID, command.Symbol).First(&position).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return position, accounting.Decimal{}, accounting.Decimal{}, err
	}
	now := command.OccurredAt
	if command.Side == "buy" {
		openingCycle := errors.Is(err, gorm.ErrRecordNotFound) || position.Status != "open"
		if errors.Is(err, gorm.ErrRecordNotFound) {
			position = database.Position{AccountID: command.AccountID, Symbol: command.Symbol, Status: "open", OpenedAt: now}
		} else if position.Status != "open" {
			if position.AmountExact == nil || position.CostBasisExact == nil {
				return position, accounting.Decimal{}, accounting.Decimal{}, ErrProjectionUnavailable
			}
			position.Status, position.OpenedAt, position.ClosedAt, position.CloseReason = "open", now, nil, nil
			position.AmountExact, position.CostBasisExact = nil, nil
		} else if position.AmountExact == nil || position.CostBasisExact == nil {
			return position, accounting.Decimal{}, accounting.Decimal{}, ErrProjectionUnavailable
		}
		oldQuantity, oldBasis := accounting.Zero(), accounting.Zero()
		if position.AmountExact != nil {
			oldQuantity = *position.AmountExact
		}
		if position.CostBasisExact != nil {
			oldBasis = *position.CostBasisExact
		}
		newQuantity := oldQuantity.Add(command.Quantity)
		newBasis := oldBasis.Add(gross)
		average, _ := newBasis.Div(newQuantity)
		position.AmountExact, position.CostBasisExact = decimalPtr(newQuantity), decimalPtr(newBasis)
		position.Amount, position.AvgPrice = newQuantity.Float64(), average.Float64()
		position.EntryPrice = floatPtr(average.Float64())
		position.ExecutionMode, position.EntrySource = command.ExecutionMode, command.EntrySource
		position.DecisionTimeframe = command.DecisionTimeframe
		position.ModelVersion, position.PolicyVersion = command.ModelVersion, command.PolicyVersion
		if openingCycle {
			position.StrategyVersion = nonempty(command.StrategyVersion, nonempty(command.ModelVersion, "manual-execution"))
		}
		position.UniverseMode, position.RolloutState = command.UniverseMode, command.RolloutState
		position.ExperimentID, position.PredictionLogID = command.ExperimentID, command.PredictionLogID
		position.DecisionContextJSON = command.DecisionContextJSON
		position.StopPrice, position.TakeProfitPrice = command.StopPrice, command.TakeProfitPrice
		position.TrailingStopPrice, position.LastAtrValue, position.MaxBarsHeld = command.TrailingStopPrice, command.LastAtrValue, command.MaxBarsHeld
		position.ExitPending = false
		mark := command.FillPrice.Float64()
		position.CurrentPrice, position.LastMarkPrice, position.LastMarkAt = &mark, &mark, &now
		priorRealized := accounting.Zero()
		if position.RealizedPnLExact != nil {
			priorRealized = *position.RealizedPnLExact
		}
		position.RealizedPnLExact = decimalPtr(priorRealized.Sub(command.Fee))
		fees := accounting.Zero()
		if position.FeesExact != nil {
			fees = *position.FeesExact
		}
		fees = fees.Add(command.Fee)
		position.FeesExact = decimalPtr(fees)
		if position.ID == 0 {
			err = tx.Create(&position).Error
		} else {
			err = tx.Save(&position).Error
		}
		return position, accounting.Zero(), gross, err
	}

	if errors.Is(err, gorm.ErrRecordNotFound) || position.Status != "open" || position.AmountExact == nil || position.CostBasisExact == nil {
		return position, accounting.Decimal{}, accounting.Decimal{}, ErrProjectionUnavailable
	}
	oldQuantity, oldBasis := *position.AmountExact, *position.CostBasisExact
	if command.Quantity.Cmp(oldQuantity) > 0 {
		return position, accounting.Decimal{}, accounting.Decimal{}, ErrInsufficientAsset
	}
	releasedBasis, _ := oldBasis.MulDiv(command.Quantity, oldQuantity)
	realized := gross.Sub(releasedBasis)
	newQuantity, newBasis := oldQuantity.Sub(command.Quantity), oldBasis.Sub(releasedBasis)
	position.AmountExact, position.CostBasisExact = decimalPtr(newQuantity), decimalPtr(newBasis)
	position.Amount = newQuantity.Float64()
	priorRealized := accounting.Zero()
	if position.RealizedPnLExact != nil {
		priorRealized = *position.RealizedPnLExact
	}
	priorFees := accounting.Zero()
	if position.FeesExact != nil {
		priorFees = *position.FeesExact
	}
	netRealized := realized.Sub(command.Fee)
	position.RealizedPnLExact, position.FeesExact = decimalPtr(priorRealized.Add(netRealized)), decimalPtr(priorFees.Add(command.Fee))
	position.Pnl = priorRealized.Add(netRealized).Float64()
	if releasedBasis.Sign() > 0 {
		percent, _ := netRealized.Div(releasedBasis)
		position.PnlPercent = percent.Mul(accounting.MustParse("100")).Float64()
	}
	mark := command.FillPrice.Float64()
	position.CurrentPrice, position.LastMarkPrice, position.LastMarkAt = &mark, &mark, &now
	position.ExitPending = false
	if newQuantity.Sign() == 0 {
		position.Status, position.ClosedAt = "closed", &now
		position.CloseReason = stringPtrOrNil(command.Reason)
	} else {
		average, _ := newBasis.Div(newQuantity)
		position.AvgPrice = average.Float64()
	}
	err = tx.Save(&position).Error
	return position, realized, releasedBasis.Neg(), err
}

func upsertFilledOrder(tx *gorm.DB, command FillCommand, position database.Position, gross accounting.Decimal) (database.Order, error) {
	var order database.Order
	if command.ExistingOrderID > 0 {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, command.ExistingOrderID).Error; err != nil {
			return order, err
		}
		if order.AccountID != command.AccountID || order.Symbol != command.Symbol || order.OrderType != command.Side {
			return order, conflict("order_fill_mismatch", "fill does not match reserved order dimensions", nil)
		}
	} else {
		batch := command.IdempotencyKey
		order = database.Order{AccountID: command.AccountID, LedgerBatchID: &batch, OrderType: command.Side, Symbol: command.Symbol, ExecutedAt: command.OccurredAt}
	}
	totalQuantity, totalGross, totalFee := command.Quantity, gross, command.Fee
	if order.AmountCryptoExact != nil && order.AmountUsdtExact != nil && order.FeeExact != nil {
		totalQuantity = order.AmountCryptoExact.Add(command.Quantity)
		totalGross = order.AmountUsdtExact.Add(gross)
		totalFee = order.FeeExact.Add(command.Fee)
	}
	averageFill, _ := totalGross.Div(totalQuantity)
	requested, fill, qty, fee := command.RequestedPrice.Float64(), averageFill.Float64(), totalQuantity.Float64(), totalFee.Float64()
	orderStatus := command.OrderStatus
	if orderStatus == "" {
		orderStatus = "filled"
	}
	order.OrderType, order.Symbol, order.Status, order.ExecutionMode = command.Side, command.Symbol, orderStatus, command.ExecutionMode
	order.AmountCrypto, order.AmountUsdt, order.Price, order.Fee = qty, totalGross.Float64(), fill, fee
	order.AmountCryptoExact, order.AmountUsdtExact, order.FeeExact = decimalPtr(totalQuantity), decimalPtr(totalGross), decimalPtr(totalFee)
	order.ExecutedQuantityExact = decimalPtr(totalQuantity)
	if order.RequestedQuantityExact == nil && command.OrderStatus == "filled" {
		order.RequestedQuantityExact = decimalPtr(totalQuantity)
	}
	if order.RequestedQuantityExact != nil {
		remaining := order.RequestedQuantityExact.Sub(totalQuantity)
		if remaining.Sign() < 0 {
			return order, conflict("order_overfill", "executed quantity exceeds requested quantity", nil)
		}
		order.RemainingQuantityExact = decimalPtr(remaining)
	}
	order.RequestedPrice, order.FillPrice, order.ExecutedQty, order.ExchangeFee = &requested, &fill, &qty, &fee
	order.ModelVersion, order.PolicyVersion = command.ModelVersion, command.PolicyVersion
	order.Stage08ContextJSON = metadataJSONForCommand(command)
	order.UniverseMode, order.RolloutState = command.UniverseMode, command.RolloutState
	order.ExperimentID, order.PredictionLogID, order.DecisionContextJSON = command.ExperimentID, command.PredictionLogID, command.DecisionContextJSON
	order.TriggerReason, order.SubmittedAt = stringPtrOrNil(command.Reason), &command.OccurredAt
	if orderStatus == "filled" {
		order.FilledAt = &command.OccurredAt
	} else {
		order.FilledAt = nil
	}
	if command.ProviderOrderID != "" {
		order.ExchangeOrderID = &command.ProviderOrderID
	}
	if order.ID == 0 {
		return order, tx.Create(&order).Error
	}
	return order, tx.Save(&order).Error
}

func metadataJSONForCommand(command FillCommand) string {
	metadata := map[string]interface{}{}
	for key, value := range command.Metadata {
		metadata[key] = value
	}
	metadata["active_path"] = command.ExecutionMode
	metadata["strategy_version"] = command.StrategyVersion
	metadata["policy_version"] = command.PolicyVersion
	metadata["model_version"] = command.ModelVersion
	metadata["dataset_version"] = command.Metadata["dataset_version"]
	metadata["universe_version"] = command.UniverseMode
	metadata["schema_version"] = "stage08-observation-context-v2"
	if flags, active := cutover.Active(); active {
		metadata["flag_schema_version"] = flags.SchemaVersion
		_, flagID, _ := flags.Canonical()
		metadata["flag_snapshot_id"] = flagID
		metadata["ledger_authority"] = flags.LedgerAuthority
		metadata["engine_mode"] = flags.SharedEngine
		metadata["candidate_strategy_mode"] = flags.CandidateStrategy
		_, verifiedID, authority, verified := cutover.ActiveEvidence()
		if verified && verifiedID != "" {
			metadata["flag_snapshot_id"] = verifiedID
			metadata["effective_authority"] = authority
		}
	}
	base, err := json.Marshal(metadata)
	if err == nil {
		sum := sha256.Sum256(base)
		metadata["content_digest"] = hex.EncodeToString(sum[:])
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func loadFillResult(tx *gorm.DB, batchID string) (FillResult, error) {
	var fill database.Fill
	if err := tx.Where("ledger_batch_id = ?", batchID).First(&fill).Error; err != nil {
		return FillResult{}, err
	}
	var result FillResult
	result.Fill = fill
	if err := tx.First(&result.Order, fill.OrderID).Error; err != nil {
		return FillResult{}, err
	}
	if err := tx.First(&result.Position, fill.PositionID).Error; err != nil {
		return FillResult{}, err
	}
	if err := tx.First(&result.Wallet).Error; err != nil {
		return FillResult{}, err
	}
	return result, nil
}

func beginBatch(tx *gorm.DB, id, account, payloadHash string, at time.Time) (bool, error) {
	var existing database.LedgerBatch
	err := tx.First(&existing, "id = ?", id).Error
	if err == nil {
		if existing.PayloadHash != payloadHash {
			return false, ErrIdempotencyConflict
		}
		return true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	err = tx.Create(&database.LedgerBatch{ID: id, AccountID: account, PayloadHash: payloadHash, CreatedAt: at}).Error
	return false, err
}

func requireReady(tx *gorm.DB, account string) error {
	var state database.LedgerMigrationState
	if err := tx.First(&state, "account_id = ?", account).Error; err != nil || state.Status != "ready" {
		return ErrUnreconciledLegacyState
	}
	return nil
}

func lockAccount(tx *gorm.DB, account string) error {
	return tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "ledger:"+account).Error
}

func allowLedgerProjectionWrites(tx *gorm.DB) error {
	// The writer role is deliberately not granted to the runtime role. The
	// ledger service must run on the separately configured writer pool (or an
	// administrative migration/test connection). Unlike a custom GUC, callers
	// cannot mint this authority with SET CONFIG.
	return tx.Exec("SET LOCAL ROLE trading_bot_ledger_writer").Error
}

func hashPayload(value interface{}) (string, string, error) {
	switch typed := value.(type) {
	case FillCommand:
		typed.OccurredAt = time.Time{}
		value = typed
	case AdjustmentCommand:
		typed.OccurredAt = time.Time{}
		value = typed
	case ReversalCommand:
		typed.OccurredAt = time.Time{}
		value = typed
	case AssetCorrectionCommand:
		typed.OccurredAt = time.Time{}
		value = typed
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(payload)
	metadata := "{}"
	if command, ok := value.(FillCommand); ok && command.Metadata != nil {
		encoded, encodeErr := json.Marshal(command.Metadata)
		if encodeErr != nil {
			return "", "", encodeErr
		}
		metadata = string(encoded)
	}
	return hex.EncodeToString(sum[:]), metadata, nil
}

func stableID(kind, key string) string {
	sum := sha256.Sum256([]byte(kind + ":" + key))
	return kind + "_" + hex.EncodeToString(sum[:16])
}
func decimalPtr(value accounting.Decimal) *accounting.Decimal { return &value }
func floatPtr(value float64) *float64                         { return &value }
func stringPtrOrNil(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
func nonempty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *Service) fail(stage string) error {
	if s.AfterWrite != nil {
		return s.AfterWrite(stage)
	}
	return nil
}

// CostedPaperFill applies configured basis points deterministically. Buy
// slippage raises fill price; sell slippage lowers it. Fee is charged on the
// slipped gross amount.
func CostedPaperFill(side string, quantity, requested accounting.Decimal, feeBPS, slippageBPS int64) (accounting.Decimal, accounting.Decimal, error) {
	if feeBPS < 0 || slippageBPS < 0 {
		return accounting.Decimal{}, accounting.Decimal{}, validation("invalid_fee_bps", "basis points must be non-negative")
	}
	bps := accounting.MustParse("10000")
	slip, _ := accounting.MustParse(fmt.Sprint(slippageBPS)).Div(bps)
	multiplier := accounting.MustParse("1").Add(slip)
	if side == "sell" {
		multiplier = accounting.MustParse("1").Sub(slip)
	}
	fill := requested.Mul(multiplier)
	feeRate, _ := accounting.MustParse(fmt.Sprint(feeBPS)).Div(bps)
	fee := quantity.Mul(fill).Mul(feeRate)
	return fill, fee, nil
}
