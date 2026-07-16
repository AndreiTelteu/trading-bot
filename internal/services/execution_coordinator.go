package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
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
	if err := ledgerpkg.New(database.DB).CheckReady(context.Background(), ""); err != nil {
		return nil, err
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
		return nil, c.failClose(position.ID, order.ID, err)
	}

	filledQuantity := position.Amount
	if executedQty != nil && *executedQty > 0 {
		filledQuantity = *executedQty
	}
	websocket.BroadcastTradeExecuted("sell", result.Position.Symbol, filledQuantity, result.Price, result.Wallet.Balance)
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
	quantity := position.Amount
	if executedQty != nil && *executedQty > 0 {
		quantity = *executedQty
	}
	quantityExact, qErr := accounting.FromFloat(quantity)
	requestedExact, pErr := accounting.FromFloat(order.Price)
	fillExact, fErr := accounting.FromFloat(fillPrice)
	if qErr != nil || pErr != nil || fErr != nil {
		return nil, fmt.Errorf("invalid close fill precision")
	}
	providerID := ""
	if exchangeOrderID != nil {
		providerID = *exchangeOrderID
	}
	key := fmt.Sprintf("coordinated-close-order-%d", order.ID)
	if order.ClientOrderID != nil {
		key = *order.ClientOrderID
	}
	mode := normalizeExecutionMode(position.ExecutionMode)
	if mode == ExecutionModeShadow {
		return nil, fmt.Errorf("shadow position close requires a separate shadow account ledger adapter")
	}
	fee := accounting.Zero()
	feeType := ledgerpkg.EventExchangeFee
	metadata := map[string]interface{}{"exchange_fee_status": "unavailable_from_order_response", "broker_status": status}
	if mode == ExecutionModePaper {
		settings := GetAllSettings()
		feeBPS := int64(getSettingInt(settings, "paper_fee_bps", 10))
		slippageBPS := int64(getSettingInt(settings, "paper_slippage_bps", 5))
		costedFill, costedFee, costErr := ledgerpkg.CostedPaperFill("sell", quantityExact, requestedExact, feeBPS, slippageBPS)
		if costErr != nil {
			return nil, costErr
		}
		fillExact, fee, feeType = costedFill, costedFee, ledgerpkg.EventTradingFee
		metadata = map[string]interface{}{"fee_bps": feeBPS, "slippage_bps": slippageBPS, "broker_status": status}
	}
	fillResult, err := ledgerpkg.New(database.DB).ApplyFill(context.Background(), ledgerpkg.FillCommand{IdempotencyKey: key, Symbol: position.Symbol, Side: "sell", Quantity: quantityExact, RequestedPrice: requestedExact, FillPrice: fillExact, Fee: fee, FeeType: feeType, Currency: "USDT", ExecutionMode: mode, ProviderFillID: providerID, ProviderOrderID: providerID, OrderStatus: status, ExistingOrderID: order.ID, OccurredAt: req.TriggeredAt, Actor: nonemptySource(req.Source), Reason: req.Reason, StrategyVersion: position.ModelVersion, PolicyVersion: position.PolicyVersion, Metadata: metadata})
	if err != nil {
		return nil, err
	}
	if fillResult.Position.Status == "closed" {
		_ = database.DB.Transaction(func(tx *gorm.DB) error { return RecordTradeOutcome(tx, fillResult.Position) })
	}
	return &CloseResult{Closed: fillResult.Position.Status == "closed", Position: fillResult.Position, Order: fillResult.Order, Wallet: fillResult.Wallet, Reason: req.Reason, Price: fillExact.Float64()}, nil
}

func nonemptySource(source string) string {
	if strings.TrimSpace(source) == "" {
		return "execution_coordinator"
	}
	return source
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
