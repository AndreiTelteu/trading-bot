package services

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/websocket"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ExchangeOrderExecutor interface {
	ExecuteSell(symbol string, quantity float64, price float64) (*OrderResponse, error)
}

type CloseRequest struct {
	PositionID     uint
	Symbol         string
	Reason         string
	RequestedPrice float64
	TriggeredAt    time.Time
	Source         string
}

type CloseResult struct {
	Closed    bool
	Duplicate bool
	Position  database.Position
	Order     database.Order
	Wallet    database.Wallet
	Reason    string
	Price     float64
}

type ExecutionCoordinator struct {
	exchange ExchangeOrderExecutor
}

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

func (c *ExecutionCoordinator) RequestClose(req CloseRequest) (*CloseResult, error) {
	if req.TriggeredAt.IsZero() {
		req.TriggeredAt = time.Now()
	}

	position, order, err := c.markExitPending(req)
	if err != nil {
		return nil, err
	}
	if order == nil {
		return &CloseResult{Duplicate: true, Position: position}, nil
	}

	fillPrice := req.RequestedPrice
	status := OrderStatusFilled
	var exchangeOrderID *string
	var executedQty *float64

	if normalizeExecutionMode(position.ExecutionMode) == ExecutionModeExchange {
		if c.exchange == nil {
			return nil, c.failClose(position.ID, order.ID, fmt.Errorf("exchange executor unavailable"))
		}
		resp, execErr := c.exchange.ExecuteSell(positionPairSymbol(position.Symbol), position.Amount, 0)
		if execErr != nil {
			return nil, c.failClose(position.ID, order.ID, execErr)
		}

		status = normalizeOrderStatus(resp.Status)
		if fillPrice <= 0 {
			fillPrice = resp.Price
		}
		if fillPrice <= 0 {
			fillPrice = positionPriceForExecution(position)
		}
		if resp.OrderID > 0 {
			value := strconv.FormatInt(resp.OrderID, 10)
			exchangeOrderID = &value
		}
		if resp.ExecutedQty > 0 {
			qty := resp.ExecutedQty
			executedQty = &qty
		}
	}

	if fillPrice <= 0 {
		fillPrice = positionPriceForExecution(position)
	}
	if executedQty == nil {
		qty := position.Amount
		executedQty = &qty
	}

	result, err := c.completeClose(position, *order, fillPrice, status, exchangeOrderID, executedQty, req)
	if err != nil {
		return nil, err
	}

	websocket.BroadcastTradeExecuted("sell", result.Position.Symbol, result.Position.Amount, fillPrice, result.Wallet.Balance)
	broadcastTradeUpdates()
	NotifyPositionChanged()

	return result, nil
}

func (c *ExecutionCoordinator) markExitPending(req CloseRequest) (database.Position, *database.Order, error) {
	var position database.Position
	var order database.Order
	err := database.DB.Transaction(func(tx *gorm.DB) error {
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"})
		switch {
		case req.PositionID > 0:
			query = query.First(&position, req.PositionID)
		case req.Symbol != "":
			query = query.Where("symbol = ?", req.Symbol).First(&position)
		default:
			return fmt.Errorf("missing position identifier")
		}
		if query.Error != nil {
			return query.Error
		}

		if position.Status != "open" || position.ExitPending {
			return nil
		}

		price := req.RequestedPrice
		if price <= 0 {
			price = positionPriceForExecution(position)
		}
		position.ExitPending = true
		if price > 0 {
			position.CurrentPrice = floatPtr(price)
			position.LastMarkPrice = floatPtr(price)
		}
		triggeredAt := req.TriggeredAt
		position.LastMarkAt = &triggeredAt
		if err := tx.Save(&position).Error; err != nil {
			return err
		}

		requestedPrice := price
		clientOrderID := clientOrderID(position.Symbol, req.TriggeredAt)
		order = database.Order{
			OrderType:      "sell",
			Symbol:         position.Symbol,
			AmountCrypto:   position.Amount,
			AmountUsdt:     position.Amount * price,
			Price:          price,
			Status:         OrderStatusPending,
			ExecutionMode:  normalizeExecutionMode(position.ExecutionMode),
			TriggerReason:  stringPtr(req.Reason),
			RequestedPrice: &requestedPrice,
			ClientOrderID:  &clientOrderID,
			SubmittedAt:    &triggeredAt,
			ExecutedAt:     triggeredAt,
		}
		return tx.Create(&order).Error
	})
	if err != nil {
		return database.Position{}, nil, err
	}

	if order.ID == 0 {
		return position, nil, nil
	}
	return position, &order, nil
}

func (c *ExecutionCoordinator) completeClose(position database.Position, order database.Order, fillPrice float64, status string, exchangeOrderID *string, executedQty *float64, req CloseRequest) (*CloseResult, error) {
	var result CloseResult
	err := database.DB.Transaction(func(tx *gorm.DB) error {
		var lockedPosition database.Position
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedPosition, position.ID).Error; err != nil {
			return err
		}

		var lockedOrder database.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedOrder, order.ID).Error; err != nil {
			return err
		}

		var wallet database.Wallet
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&wallet).Error; err != nil {
			return err
		}

		now := req.TriggeredAt
		if now.IsZero() {
			now = time.Now()
		}
		proceeds := lockedPosition.Amount * fillPrice
		pnl := proceeds - (lockedPosition.Amount * lockedPosition.AvgPrice)
		pnlPercent := 0.0
		if lockedPosition.AvgPrice > 0 {
			pnlPercent = ((fillPrice - lockedPosition.AvgPrice) / lockedPosition.AvgPrice) * 100
		}

		if normalizeExecutionMode(lockedPosition.ExecutionMode) != ExecutionModeShadow {
			wallet.Balance += proceeds
			if err := tx.Save(&wallet).Error; err != nil {
				return err
			}
		}

		lockedPosition.Status = "closed"
		lockedPosition.ExitPending = false
		lockedPosition.CurrentPrice = floatPtr(fillPrice)
		lockedPosition.LastMarkPrice = floatPtr(fillPrice)
		lockedPosition.LastMarkAt = &now
		lockedPosition.ClosedAt = &now
		lockedPosition.CloseReason = stringPtr(req.Reason)
		lockedPosition.Pnl = pnl
		lockedPosition.PnlPercent = pnlPercent
		if err := tx.Save(&lockedPosition).Error; err != nil {
			return err
		}

		lockedOrder.Status = status
		lockedOrder.Price = fillPrice
		lockedOrder.AmountUsdt = proceeds
		lockedOrder.ExchangeOrderID = exchangeOrderID
		lockedOrder.FillPrice = floatPtr(fillPrice)
		lockedOrder.ExecutedQty = executedQty
		lockedOrder.FilledAt = &now
		lockedOrder.ExecutedAt = now
		if err := tx.Save(&lockedOrder).Error; err != nil {
			return err
		}
		if err := RecordTradeOutcome(tx, lockedPosition); err != nil {
			return err
		}

		result = CloseResult{
			Closed:   true,
			Position: lockedPosition,
			Order:    lockedOrder,
			Wallet:   wallet,
			Reason:   req.Reason,
			Price:    fillPrice,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *ExecutionCoordinator) failClose(positionID uint, orderID uint, closeErr error) error {
	rollbackErr := database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&database.Position{}).
			Where("id = ?", positionID).
			Updates(map[string]interface{}{"exit_pending": false}).Error; err != nil {
			return err
		}
		return tx.Model(&database.Order{}).
			Where("id = ?", orderID).
			Updates(map[string]interface{}{"status": OrderStatusFailed}).Error
	})
	if rollbackErr != nil {
		return fmt.Errorf("close failed: %w (rollback error: %v)", closeErr, rollbackErr)
	}
	return closeErr
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

func normalizeOrderStatus(status string) string {
	if strings.TrimSpace(status) == "" {
		return OrderStatusFilled
	}
	return strings.ToLower(status)
}

func clientOrderID(symbol string, when time.Time) string {
	clean := strings.ToLower(strings.ReplaceAll(symbol, " ", ""))
	return fmt.Sprintf("%s-%d", clean, when.UnixMilli())
}

func positionPairSymbol(symbol string) string {
	pair := strings.ToUpper(strings.TrimSpace(symbol))
	if !strings.HasSuffix(pair, "USDT") {
		pair += "USDT"
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
