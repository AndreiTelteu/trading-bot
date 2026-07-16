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
	ErrUnreconciledLegacyState = errors.New("ledger migration approval required")
	ErrInsufficientCash        = errors.New("insufficient cash")
	ErrInsufficientAsset       = errors.New("insufficient position quantity")
	ErrIdempotencyConflict     = errors.New("idempotency key reused with different payload")
	ErrProjectionUnavailable   = errors.New("exact ledger projection unavailable")
)

type Service struct {
	DB         *gorm.DB
	Now        func() time.Time
	AfterWrite func(stage string) error // deterministic rollback seam used by transaction tests
}

func New(db *gorm.DB) *Service { return &Service{DB: db, Now: time.Now} }

func (s *Service) CheckReady(ctx context.Context, account string) error {
	if account == "" {
		account = DefaultAccountID
	}
	var state database.LedgerMigrationState
	if err := s.DB.WithContext(ctx).First(&state, "account_id = ?", account).Error; err != nil || state.Status != "ready" {
		return ErrUnreconciledLegacyState
	}
	var wallet database.Wallet
	if err := s.DB.WithContext(ctx).First(&wallet).Error; err != nil {
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
	OrderStatus                                                 string
	ExistingOrderID                                             uint
	OccurredAt                                                  time.Time
	Actor, Reason                                               string
	StrategyVersion, PolicyVersion                              string
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
	if err := validateFill(command); err != nil {
		return FillResult{}, err
	}
	if command.AccountID == "" {
		command.AccountID = DefaultAccountID
	}
	if command.OccurredAt.IsZero() {
		command.OccurredAt = s.now()
	}
	payloadHash, metadataJSON, err := hashPayload(command)
	if err != nil {
		return FillResult{}, err
	}
	var result FillResult
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
			return fmt.Errorf("fill currency %s does not match wallet currency %s", command.Currency, wallet.Currency)
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

		position, realized, err := applyPosition(tx, command, gross)
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
			ID: fillID, LedgerBatchID: command.IdempotencyKey, AccountID: command.AccountID,
			OrderID: order.ID, ProviderFillID: providerFillID, PositionID: position.ID,
			Symbol: command.Symbol, Side: command.Side, Quantity: command.Quantity,
			RequestedPrice: command.RequestedPrice, FillPrice: command.FillPrice, GrossAmount: gross,
			FeeAmount: command.Fee, FeeType: command.FeeType, FeeCurrency: command.Currency,
			ExecutionMode: command.ExecutionMode, StrategyVersion: command.StrategyVersion,
			PolicyVersion: command.PolicyVersion, OccurredAt: command.OccurredAt, CreatedAt: s.now(),
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
			AccountID: command.AccountID, Currency: command.Currency, Symbol: command.Symbol,
			CashDelta: cashDelta, AssetDelta: assetDelta, OrderID: &orderID, FillID: &fillID,
			PositionID: &positionID, ExecutionMode: command.ExecutionMode,
			StrategyVersion: command.StrategyVersion, PolicyVersion: command.PolicyVersion,
			Actor: nonempty(command.Actor, "execution_adapter"), Reason: command.Reason,
			RealizedPnL: realized, MetadataJSON: metadataJSON,
			OccurredAt: command.OccurredAt, RecordedAt: s.now(),
		}}
		if command.Fee.Sign() > 0 {
			feeType := EventTradingFee
			if command.FeeType == EventExchangeFee || command.ExecutionMode == "exchange" {
				feeType = EventExchangeFee
			}
			events = append(events, database.LedgerEvent{
				ID: stableID("event-fee", command.IdempotencyKey), LedgerBatchID: command.IdempotencyKey,
				Sequence: 2, IdempotencyKey: command.IdempotencyKey + ":fee", EventType: feeType,
				AccountID: command.AccountID, Currency: command.Currency, Symbol: command.Symbol,
				CashDelta: command.Fee.Neg(), AssetDelta: accounting.Zero(), OrderID: &orderID,
				FillID: &fillID, PositionID: &positionID, ExecutionMode: command.ExecutionMode,
				StrategyVersion: command.StrategyVersion, PolicyVersion: command.PolicyVersion,
				Actor: nonempty(command.Actor, "execution_adapter"), Reason: command.Reason,
				RealizedPnL: command.Fee.Neg(), MetadataJSON: metadataJSON,
				OccurredAt: command.OccurredAt, RecordedAt: s.now(),
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
	return result, err
}

func validateFill(command FillCommand) error {
	if strings.TrimSpace(command.IdempotencyKey) == "" || strings.TrimSpace(command.Symbol) == "" {
		return fmt.Errorf("idempotency key and symbol are required")
	}
	if command.Side != "buy" && command.Side != "sell" {
		return fmt.Errorf("side must be buy or sell")
	}
	if command.Quantity.Sign() <= 0 || command.RequestedPrice.Sign() <= 0 || command.FillPrice.Sign() <= 0 || command.Fee.Sign() < 0 {
		return fmt.Errorf("quantity/prices must be positive and fee non-negative")
	}
	if command.Currency == "" || command.ExecutionMode == "" || command.FeeType == "" {
		return fmt.Errorf("currency, execution mode, and fee type are required")
	}
	return nil
}

func applyPosition(tx *gorm.DB, command FillCommand, gross accounting.Decimal) (database.Position, accounting.Decimal, error) {
	var position database.Position
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("symbol = ?", command.Symbol).First(&position).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return position, accounting.Decimal{}, err
	}
	now := command.OccurredAt
	if command.Side == "buy" {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			position = database.Position{Symbol: command.Symbol, Status: "open", OpenedAt: now}
		} else if position.Status != "open" {
			if position.AmountExact == nil || position.CostBasisExact == nil {
				return position, accounting.Decimal{}, ErrProjectionUnavailable
			}
			position.Status, position.OpenedAt, position.ClosedAt, position.CloseReason = "open", now, nil, nil
			position.AmountExact, position.CostBasisExact = nil, nil
			position.RealizedPnLExact, position.FeesExact = decimalPtr(accounting.Zero()), decimalPtr(accounting.Zero())
			position.Pnl, position.PnlPercent = 0, 0
		} else if position.AmountExact == nil || position.CostBasisExact == nil {
			return position, accounting.Decimal{}, ErrProjectionUnavailable
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
		return position, accounting.Zero(), err
	}

	if errors.Is(err, gorm.ErrRecordNotFound) || position.Status != "open" || position.AmountExact == nil || position.CostBasisExact == nil {
		return position, accounting.Decimal{}, ErrProjectionUnavailable
	}
	oldQuantity, oldBasis := *position.AmountExact, *position.CostBasisExact
	if command.Quantity.Cmp(oldQuantity) > 0 {
		return position, accounting.Decimal{}, ErrInsufficientAsset
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
	return position, realized, err
}

func upsertFilledOrder(tx *gorm.DB, command FillCommand, position database.Position, gross accounting.Decimal) (database.Order, error) {
	var order database.Order
	if command.ExistingOrderID > 0 {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, command.ExistingOrderID).Error; err != nil {
			return order, err
		}
	} else {
		batch := command.IdempotencyKey
		order = database.Order{LedgerBatchID: &batch, OrderType: command.Side, Symbol: command.Symbol, ExecutedAt: command.OccurredAt}
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
	order.AmountCrypto, order.AmountUsdt, order.Price, order.Fee = qty, gross.Float64(), fill, fee
	order.AmountCryptoExact, order.AmountUsdtExact, order.FeeExact = decimalPtr(totalQuantity), decimalPtr(totalGross), decimalPtr(totalFee)
	order.RequestedPrice, order.FillPrice, order.ExecutedQty, order.ExchangeFee = &requested, &fill, &qty, &fee
	order.ModelVersion, order.PolicyVersion = command.ModelVersion, command.PolicyVersion
	order.UniverseMode, order.RolloutState = command.UniverseMode, command.RolloutState
	order.ExperimentID, order.PredictionLogID, order.DecisionContextJSON = command.ExperimentID, command.PredictionLogID, command.DecisionContextJSON
	order.TriggerReason, order.SubmittedAt, order.FilledAt = stringPtrOrNil(command.Reason), &command.OccurredAt, &command.OccurredAt
	if command.ProviderOrderID != "" {
		order.ExchangeOrderID = &command.ProviderOrderID
	}
	if order.ID == 0 {
		return order, tx.Create(&order).Error
	}
	return order, tx.Save(&order).Error
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
		return accounting.Decimal{}, accounting.Decimal{}, fmt.Errorf("basis points must be non-negative")
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
