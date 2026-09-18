package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
	"trading-go/internal/websocket"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ExchangeOrderExecutor remains for the fenced exchange adapter contract. No
// close path is allowed to call it until durable broker recovery exists.
type ExchangeOrderExecutor interface {
	ExecuteSell(symbol string, quantity float64, price float64) (*OrderResponse, error)
}

type CloseRequest struct {
	PositionID     uint
	Symbol, Reason string
	RequestedPrice float64
	TriggeredAt    time.Time
	Source         string
}
type CloseResult struct {
	Closed, Duplicate bool
	Position          database.Position
	Order             database.Order
	Wallet            database.Wallet
	Reason            string
	Price             float64
}
type ExecutionCoordinator struct{ exchange ExchangeOrderExecutor }

var executionCoordinator *ExecutionCoordinator

func InitExecutionCoordinator(ex ExchangeOrderExecutor) {
	executionCoordinator = NewExecutionCoordinator(ex)
}
func GetExecutionCoordinator() *ExecutionCoordinator {
	if executionCoordinator == nil {
		executionCoordinator = NewExecutionCoordinator(GetExchange())
	}
	return executionCoordinator
}
func NewExecutionCoordinator(ex ExchangeOrderExecutor) *ExecutionCoordinator {
	return &ExecutionCoordinator{exchange: ex}
}

const (
	closeRequestPending   = "pending"
	closeRequestRetryable = "retryable"
	closeRequestApplied   = "applied"
	closeRequestRejected  = "rejected"
)

// RequestClose first persists a runtime-owned reservation, then the isolated
// ledger writer atomically creates the order/fill/events and marks it applied.
// Retries retain the exact same request ID, price, and cost policy.
func (c *ExecutionCoordinator) RequestClose(req CloseRequest) (*CloseResult, error) {
	if req.TriggeredAt.IsZero() {
		req.TriggeredAt = time.Now().UTC()
	}
	if err := ledgerpkg.New(database.LedgerWriter()).CheckReady(context.Background(), ""); err != nil {
		return nil, err
	}
	reservation, duplicate, err := c.reserveClose(req)
	if err != nil {
		return nil, err
	}
	if duplicate && reservation.ID == "" {
		var position database.Position
		if err := database.DB.First(&position, reservation.PositionID).Error; err != nil {
			return nil, err
		}
		return &CloseResult{Duplicate: true, Closed: position.Status == "closed", Position: position}, nil
	}
	if duplicate && reservation.Status == closeRequestApplied {
		result, err := c.loadAppliedClose(reservation)
		if err != nil {
			return nil, err
		}
		result.Duplicate = true
		return result, nil
	}
	result, err := c.applyClose(reservation.ID)
	if err != nil {
		return nil, err
	}
	result.Duplicate = duplicate
	if !result.Duplicate {
		websocket.BroadcastTradeExecuted("sell", result.Position.Symbol, result.Order.AmountCrypto, result.Price, result.Wallet.Balance)
		broadcastTradeUpdates()
		NotifyPositionChanged()
	}
	return result, nil
}

func closeCycleID(position database.Position) string {
	return fmt.Sprintf("%d", position.OpenedAt.UTC().UnixNano())
}
func closeRequestID(position database.Position) string {
	return fmt.Sprintf("close:%d:%s", position.ID, closeCycleID(position))
}

func (c *ExecutionCoordinator) reserveClose(req CloseRequest) (database.CloseRequest, bool, error) {
	var reservation database.CloseRequest
	duplicate := false
	err := database.DB.Transaction(func(tx *gorm.DB) error {
		var position database.Position
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"})
		switch {
		case req.PositionID > 0:
			query = query.First(&position, req.PositionID)
		case strings.TrimSpace(req.Symbol) != "":
			query = query.Where("symbol = ?", req.Symbol).First(&position)
		default:
			return fmt.Errorf("missing position identifier")
		}
		if query.Error != nil {
			return query.Error
		}
		if position.Status != "open" {
			duplicate = true
			if err := tx.First(&reservation, "id = ?", closeRequestID(position)).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if reservation.ID == "" {
				reservation.PositionID = position.ID
			}
			return nil
		}
		id := closeRequestID(position)
		err := tx.First(&reservation, "id = ?", id).Error
		if err == nil {
			duplicate = true
			if reservation.Status == closeRequestRejected {
				return ledgerpkg.ErrExchangeExecutionFenced
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		} else {
			var wallet database.Wallet
			if err := tx.Where("account_id = ?", position.AccountID).First(&wallet).Error; err != nil {
				return err
			}
			if strings.TrimSpace(wallet.Currency) == "" {
				return ledgerpkg.ErrProjectionUnavailable
			}
			price := req.RequestedPrice
			if price <= 0 {
				price = positionPriceForExecution(position)
			}
			if price <= 0 {
				return fmt.Errorf("close request requires a positive executable mark")
			}
			settings := GetAllSettings()
			reservation = database.CloseRequest{ID: id, PositionID: position.ID, CycleID: closeCycleID(position), AccountID: position.AccountID, Currency: wallet.Currency, Symbol: position.Symbol, Reason: nonemptySource(req.Reason), Source: nonemptySource(req.Source), Price: price, FeeBPS: int64(getSettingInt(settings, "paper_fee_bps", 10)), SlippageBPS: int64(getSettingInt(settings, "paper_slippage_bps", 5)), Status: closeRequestPending, TriggeredAt: req.TriggeredAt.UTC()}
			if err := tx.Create(&reservation).Error; err != nil {
				return err
			}
		}
		return tx.Model(&database.Position{}).Where("id = ? AND status = 'open'", position.ID).Updates(map[string]interface{}{"exit_pending": true, "last_mark_at": req.TriggeredAt.UTC()}).Error
	})
	return reservation, duplicate, err
}

func (c *ExecutionCoordinator) applyClose(id string) (*CloseResult, error) {
	var result *CloseResult
	err := database.LedgerWriter().Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_ledger_writer").Error; err != nil {
			return err
		}
		var request database.CloseRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&request, "id = ?", id).Error; err != nil {
			return err
		}
		if request.Status == closeRequestApplied {
			loaded, err := c.loadAppliedCloseWith(tx, request)
			result = loaded
			return err
		}
		if request.Status == closeRequestRejected {
			return ledgerpkg.ErrExchangeExecutionFenced
		}
		var position database.Position
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&position, request.PositionID).Error; err != nil {
			return err
		}
		if position.Status != "open" || closeCycleID(position) != request.CycleID {
			return fmt.Errorf("close request %s no longer matches an open position lifecycle", request.ID)
		}
		if normalizeExecutionMode(position.ExecutionMode) == ExecutionModeExchange {
			return ledgerpkg.ErrExchangeExecutionFenced
		}
		command, err := closeFillCommand(position, request)
		if err != nil {
			return err
		}
		fill, err := ledgerpkg.New(tx).ApplyFill(context.Background(), command)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		if err := tx.Model(&database.CloseRequest{}).Where("id = ?", request.ID).Updates(map[string]interface{}{"status": closeRequestApplied, "attempts": request.Attempts + 1, "last_error": "", "applied_at": now}).Error; err != nil {
			return err
		}
		result = &CloseResult{Closed: fill.Position.Status == "closed", Position: fill.Position, Order: fill.Order, Wallet: fill.Wallet, Reason: request.Reason, Price: command.FillPrice.Float64()}
		return nil
	})
	if err == nil {
		// Monitoring labels are non-economic runtime observations. They must not
		// widen the ledger role or turn a fully committed close into a retry.
		if result != nil && result.Closed {
			_ = database.DB.Transaction(func(tx *gorm.DB) error { return RecordTradeOutcome(tx, result.Position) })
		}
		return result, nil
	}
	status := closeRequestRetryable
	if errors.Is(err, ledgerpkg.ErrExchangeExecutionFenced) {
		status = closeRequestRejected
	}
	if recordErr := c.recordCloseFailure(id, status, err); recordErr != nil {
		return nil, fmt.Errorf("close failed: %w (could not persist recovery state: %v)", err, recordErr)
	}
	return nil, err
}

func closeFillCommand(position database.Position, request database.CloseRequest) (ledgerpkg.FillCommand, error) {
	if position.AmountExact == nil || position.AmountExact.Sign() <= 0 {
		return ledgerpkg.FillCommand{}, ledgerpkg.ErrProjectionUnavailable
	}
	price, err := accounting.FromFloat(request.Price)
	if err != nil {
		return ledgerpkg.FillCommand{}, err
	}
	fillPrice, fee, err := ledgerpkg.CostedPaperFill("sell", *position.AmountExact, price, request.FeeBPS, request.SlippageBPS)
	if err != nil {
		return ledgerpkg.FillCommand{}, err
	}
	metadata := map[string]interface{}{"fee_bps": request.FeeBPS, "slippage_bps": request.SlippageBPS, "close_request_id": request.ID, "close_source": request.Source}
	return ledgerpkg.FillCommand{IdempotencyKey: request.ID, ClientOrderID: request.ID, AccountID: position.AccountID, Symbol: position.Symbol, Side: "sell", Quantity: *position.AmountExact, RequestedPrice: price, FillPrice: fillPrice, Fee: fee, FeeType: ledgerpkg.EventTradingFee, Currency: request.Currency, ExecutionMode: normalizeExecutionMode(position.ExecutionMode), OrderStatus: OrderStatusFilled, OccurredAt: request.TriggeredAt, Actor: request.Source, Reason: request.Reason, StrategyVersion: position.StrategyVersion, PolicyVersion: position.PolicyVersion, CostModelVersion: "paper-cost-v1", Metadata: metadata}, nil
}

func (c *ExecutionCoordinator) recordCloseFailure(id, status string, closeErr error) error {
	message := closeErr.Error()
	if len(message) > 2000 {
		message = message[:2000]
	}
	return database.DB.Transaction(func(tx *gorm.DB) error {
		var request database.CloseRequest
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&request, "id = ?", id).Error; err != nil {
			return err
		}
		// A connection can fail after PostgreSQL committed the coupled fill and
		// reservation completion. Never overwrite that durable applied state with
		// a stale client-side error classification.
		if request.Status == closeRequestApplied {
			return nil
		}
		if err := tx.Model(&database.CloseRequest{}).Where("id = ?", id).Updates(map[string]interface{}{"status": status, "last_error": message, "attempts": gorm.Expr("attempts + 1")}).Error; err != nil {
			return err
		}
		if status == closeRequestRejected {
			return tx.Model(&database.Position{}).Where("id = (SELECT position_id FROM close_requests WHERE id = ?)", id).Update("exit_pending", false).Error
		}
		return nil
	})
}

func (c *ExecutionCoordinator) loadAppliedClose(request database.CloseRequest) (*CloseResult, error) {
	return c.loadAppliedCloseWith(database.LedgerWriter(), request)
}
func (c *ExecutionCoordinator) loadAppliedCloseWith(db *gorm.DB, request database.CloseRequest) (*CloseResult, error) {
	var order database.Order
	if err := db.Where("client_order_id = ?", request.ID).First(&order).Error; err != nil {
		return nil, err
	}
	var position database.Position
	if err := db.First(&position, request.PositionID).Error; err != nil {
		return nil, err
	}
	var wallet database.Wallet
	if err := db.Where("account_id = ?", request.AccountID).First(&wallet).Error; err != nil {
		return nil, err
	}
	price := request.Price
	if order.FillPrice != nil {
		price = *order.FillPrice
	}
	return &CloseResult{Closed: position.Status == "closed", Position: position, Order: order, Wallet: wallet, Reason: request.Reason, Price: price}, nil
}

// RecoverPendingCloses runs before the stream supervisor begins: persisted
// non-terminal reservations are retried with their original payload.
func (c *ExecutionCoordinator) RecoverPendingCloses(ctx context.Context) (int, error) {
	if err := ledgerpkg.New(database.LedgerWriter()).CheckReady(ctx, ""); err != nil {
		return 0, err
	}
	var requests []database.CloseRequest
	if err := database.DB.Where("status IN ?", []string{closeRequestPending, closeRequestRetryable}).Order("created_at,id").Find(&requests).Error; err != nil {
		return 0, err
	}
	for index := range requests {
		if _, err := c.applyClose(requests[index].ID); err != nil {
			return index, err
		}
	}
	return len(requests), nil
}

func normalizeExecutionMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ExecutionModeExchange:
		return ExecutionModeExchange
	case ExecutionModeShadow:
		return ExecutionModeShadow
	default:
		return ExecutionModePaper
	}
}
func nonemptySource(source string) string {
	if strings.TrimSpace(source) == "" {
		return "execution_coordinator"
	}
	return source
}
func PositionPairSymbol(symbol, settlementCurrency string) string {
	pair := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(symbol), "/", ""))
	quote := strings.ToUpper(strings.TrimSpace(settlementCurrency))
	if quote != "" && !strings.HasSuffix(pair, quote) {
		pair += quote
	}
	return pair
}
func positionPriceForExecution(position database.Position) float64 {
	if position.LastMarkPrice != nil && *position.LastMarkPrice > 0 {
		return *position.LastMarkPrice
	}
	if position.CurrentPrice != nil && *position.CurrentPrice > 0 {
		return *position.CurrentPrice
	}
	if position.EntryPrice != nil && *position.EntryPrice > 0 {
		return *position.EntryPrice
	}
	return position.AvgPrice
}
func stringPtr(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}
