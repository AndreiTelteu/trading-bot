package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
	"trading-go/internal/services"
	ws "trading-go/internal/websocket"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

func GetPositions(c *fiber.Ctx) error {
	positions, err := database.ListPositionsForDisplay()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch positions"})
	}
	if positions == nil {
		positions = []database.Position{}
	}
	return c.JSON(positions)
}

func CreatePosition(c *fiber.Ctx) error {
	return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Direct position creation is disabled; submit a paper or exchange execution request"})
}

func ClosePosition(c *fiber.Ctx) error {
	id := c.Params("id")

	type ClosePositionRequest struct {
		CloseReason *string `json:"close_reason"`
	}

	var req ClosePositionRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	positionID, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid position id"})
	}
	reason := "manual"
	if req.CloseReason != nil && strings.TrimSpace(*req.CloseReason) != "" {
		reason = *req.CloseReason
	}
	result, err := services.GetExecutionCoordinator().RequestClose(services.CloseRequest{PositionID: uint(positionID), Reason: reason, TriggeredAt: time.Now(), Source: "positions_close_api"})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.Status(404).JSON(fiber.Map{"error": "Position not found"})
		}
		if _, code := ledgerpkg.ErrorDetails(err); code != "internal_error" {
			return writeLedgerError(c, err)
		}
		return c.Status(500).JSON(fiber.Map{"error": "close request failed"})
	}
	if result.Duplicate {
		return c.Status(409).JSON(fiber.Map{"error": "Position is already closed or closing"})
	}
	return c.JSON(result.Position)
}

func applyDirectCloseProjection(position *database.Position, reason *string, closedAt time.Time) error {
	if position.Status != "open" {
		return fiber.NewError(fiber.StatusBadRequest, "Position is already closed")
	}
	position.Status = "closed"
	position.ExitPending = false
	position.ClosedAt = &closedAt
	if reason != nil {
		position.CloseReason = reason
	}
	return nil
}

func DeletePosition(c *fiber.Ctx) error {
	return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "Position deletion is disabled; immutable economic history must be retained"})
}

var errDirectPositionDelete = errors.New("direct position delete failed")

type directPositionStore interface {
	FindBySymbol(string) (database.Position, error)
	Delete(database.Position) error
}

type gormDirectPositionStore struct{}

func (gormDirectPositionStore) FindBySymbol(symbol string) (database.Position, error) {
	var position database.Position
	err := database.DB.Where("symbol = ?", symbol).First(&position).Error
	return position, err
}
func (gormDirectPositionStore) Delete(position database.Position) error {
	return database.DB.Delete(&position).Error
}

func performDirectPositionDelete(store directPositionStore, symbol string) (database.Position, error) {
	position, err := store.FindBySymbol(symbol)
	if err != nil {
		return database.Position{}, err
	}
	if err := store.Delete(position); err != nil {
		return database.Position{}, fmt.Errorf("%w: %v", errDirectPositionDelete, err)
	}
	return position, nil
}

// ExecuteOpenTrade simulates a buy order and creates a position (paper trading)
func ExecuteOpenTrade(c *fiber.Ctx) error {
	type OpenTradeRequest struct {
		Symbol         string          `json:"symbol"`
		Amount         json.RawMessage `json:"amount"`
		Price          json.RawMessage `json:"price"`      // 0 for market order
		OrderType      string          `json:"order_type"` // "market" or "limit"
		IdempotencyKey string          `json:"idempotency_key"`
	}

	var req OpenTradeRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.Symbol == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Symbol is required"})
	}

	quantityExact, quantityErr := parseExactJSON(req.Amount, "amount", false)
	if quantityErr != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Amount must be greater than 0"})
	}

	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch wallet"})
	}

	// Format symbol for price lookup in the configured settlement currency.
	symbol := services.PositionPairSymbol(req.Symbol, wallet.Currency)

	// Fetch current price (for paper trading, we still need current market price)
	exchange := services.GetExchange()
	requestedExact, _ := parseExactJSON(req.Price, "price", true)
	if req.OrderType == "market" || requestedExact.Sign() <= 0 {
		ticker, err := exchange.FetchTickerPrice(symbol)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to fetch price: %v", err)})
		}
		requestedExact, quantityErr = accounting.Parse(ticker.LastPrice)
		if quantityErr != nil {
			return c.Status(502).JSON(fiber.Map{"error": "Provider returned an invalid exact price"})
		}
	}
	if requestedExact.Sign() <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Amount and price must be exact finite decimals"})
	}
	feeBPS, slippageBPS := paperCostBPS()
	fillExact, feeExact, costErr := ledgerpkg.CostedPaperFill("buy", quantityExact, requestedExact, feeBPS, slippageBPS)
	if costErr != nil {
		return c.Status(500).JSON(fiber.Map{"error": costErr.Error()})
	}
	totalCostExact := quantityExact.Mul(fillExact).Add(feeExact)
	totalCost := totalCostExact.Float64()

	// Check wallet balance
	if wallet.BalanceExact == nil || totalCostExact.Cmp(*wallet.BalanceExact) > 0 {
		return c.Status(400).JSON(fiber.Map{"error": fmt.Sprintf("Insufficient balance. Required: %.2f %s, Available: %.2f %s", totalCost, wallet.Currency, wallet.Balance, wallet.Currency)})
	}

	now := time.Now().UTC()
	key := req.IdempotencyKey
	if key == "" {
		key = fmt.Sprintf("paper-open-%s-%d", req.Symbol, now.UnixNano())
	}
	fillResult, err := ledgerpkg.New(database.LedgerWriter()).ApplyFill(c.UserContext(), ledgerpkg.FillCommand{IdempotencyKey: key, Symbol: req.Symbol, Side: "buy", Quantity: quantityExact, RequestedPrice: requestedExact, FillPrice: fillExact, Fee: feeExact, FeeType: ledgerpkg.EventTradingFee, Currency: wallet.Currency, ExecutionMode: services.ExecutionModePaper, OccurredAt: now, Actor: "paper_trade_api", Reason: "manual paper buy", EntrySource: services.EntrySourcePaperTest, DecisionTimeframe: services.DecisionTimeframeDefault, Metadata: map[string]interface{}{"fee_bps": feeBPS, "slippage_bps": slippageBPS}})
	if err != nil {
		return writeLedgerError(c, err)
	}
	wallet, position := fillResult.Wallet, fillResult.Position
	price := fillExact.Float64()
	amount := quantityExact.Float64()

	// Broadcast updates
	if wsHub != nil {
		wsHub.BroadcastMsg(&ws.Message{
			Type: "trade_executed",
			Payload: fiber.Map{
				"type":        "buy",
				"symbol":      req.Symbol,
				"amount":      amount,
				"price":       price,
				"total_cost":  totalCost,
				"new_balance": wallet.Balance,
			},
		})
		wsHub.BroadcastMsg(&ws.Message{
			Type:    "position_update",
			Payload: position,
		})
		wsHub.BroadcastMsg(&ws.Message{
			Type:    "wallet_update",
			Payload: fiber.Map{"balance": wallet.Balance, "currency": wallet.Currency},
		})
	}
	services.NotifyPositionChanged()

	return c.JSON(fiber.Map{
		"success":     true,
		"order_id":    fillResult.Order.ID,
		"symbol":      req.Symbol,
		"amount":      amount,
		"price":       price,
		"total_cost":  totalCost,
		"new_balance": wallet.Balance,
		"position":    position,
	})
}

// ExecuteCloseTrade simulates a sell order and closes the position (paper trading)
func ExecuteCloseTrade(c *fiber.Ctx) error {
	id := c.Params("id")

	type CloseTradeRequest struct {
		CloseReason    string          `json:"close_reason"` // manual, take_profit, stop_loss
		Price          json.RawMessage `json:"price"`        // 0 for market order
		OrderType      string          `json:"order_type"`   // "market" or "limit"
		IdempotencyKey string          `json:"idempotency_key"`
	}

	var req CloseTradeRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	// Set default close reason
	if req.CloseReason == "" {
		req.CloseReason = "manual"
	}

	var position database.Position
	if err := database.DB.First(&position, id).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Position not found"})
	}

	if position.Status != "open" {
		return c.Status(400).JSON(fiber.Map{"error": "Position is already closed"})
	}

	if position.Symbol == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Position has no symbol"})
	}

	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch wallet"})
	}

	// Format symbol for price lookup in the configured settlement currency.
	symbol := services.PositionPairSymbol(position.Symbol, wallet.Currency)

	// Fetch current price (for paper trading, we still need current market price)
	exchange := services.GetExchange()
	requestedExact, _ := parseExactJSON(req.Price, "price", true)
	if req.OrderType == "market" || requestedExact.Sign() <= 0 {
		ticker, err := exchange.FetchTickerPrice(symbol)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to fetch price: %v", err)})
		}
		requestedExact, err = accounting.Parse(ticker.LastPrice)
		if err != nil {
			return c.Status(502).JSON(fiber.Map{"error": "Provider returned an invalid exact price"})
		}
	}

	if position.AmountExact == nil {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": ledgerpkg.ErrProjectionUnavailable.Error()})
	}
	if requestedExact.Sign() <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid price"})
	}
	feeBPS, slippageBPS := paperCostBPS()
	fillExact, feeExact, costErr := ledgerpkg.CostedPaperFill("sell", *position.AmountExact, requestedExact, feeBPS, slippageBPS)
	if costErr != nil {
		return c.Status(500).JSON(fiber.Map{"error": costErr.Error()})
	}
	now := time.Now().UTC()
	key := req.IdempotencyKey
	if key == "" {
		key = fmt.Sprintf("paper-close-%d-%d", position.ID, now.UnixNano())
	}
	fillResult, err := ledgerpkg.New(database.LedgerWriter()).ApplyFill(c.UserContext(), ledgerpkg.FillCommand{IdempotencyKey: key, Symbol: position.Symbol, Side: "sell", Quantity: *position.AmountExact, RequestedPrice: requestedExact, FillPrice: fillExact, Fee: feeExact, FeeType: ledgerpkg.EventTradingFee, Currency: wallet.Currency, ExecutionMode: services.ExecutionModePaper, OccurredAt: now, Actor: "paper_trade_api", Reason: req.CloseReason, Metadata: map[string]interface{}{"fee_bps": feeBPS, "slippage_bps": slippageBPS}})
	if err != nil {
		return writeLedgerError(c, err)
	}
	wallet, position = fillResult.Wallet, fillResult.Position
	price := fillExact.Float64()
	closedQuantity := fillResult.Fill.Quantity.Float64()
	totalValue := position.Amount * price
	// Amount is zero after a full close; use the immutable fill gross instead.
	totalValue = fillResult.Fill.GrossAmount.Float64()
	pnl := position.Pnl
	pnlPercent := position.PnlPercent

	// Calculate total portfolio value for wallet_update (wallet + remaining open positions)
	totalPortfolioValue := wallet.Balance
	var openPositions []database.Position
	database.DB.Where("status = ?", "open").Find(&openPositions)
	for _, p := range openPositions {
		if p.CurrentPrice != nil {
			totalPortfolioValue += p.Amount * (*p.CurrentPrice)
		} else {
			totalPortfolioValue += p.Amount * p.AvgPrice
		}
	}

	// Broadcast updates
	if wsHub != nil {
		wsHub.BroadcastMsg(&ws.Message{
			Type: "trade_executed",
			Payload: fiber.Map{
				"type":        "sell",
				"symbol":      position.Symbol,
				"amount":      closedQuantity,
				"price":       price,
				"total_value": totalValue,
				"pnl":         pnl,
				"pnl_percent": pnlPercent,
				"new_balance": wallet.Balance,
			},
		})
		wsHub.BroadcastMsg(&ws.Message{
			Type:    "position_update",
			Payload: position,
		})

		// Broadcast updated positions list
		allPositions, _ := database.ListPositionsForDisplay()
		wsHub.BroadcastMsg(&ws.Message{
			Type:    "positions_update",
			Payload: allPositions,
		})

		wsHub.BroadcastMsg(&ws.Message{
			Type:    "wallet_update",
			Payload: fiber.Map{"balance": wallet.Balance, "currency": wallet.Currency, "total_value": totalPortfolioValue},
		})
	}
	services.NotifyPositionChanged()

	return c.JSON(fiber.Map{
		"success":     true,
		"order_id":    fillResult.Order.ID,
		"symbol":      position.Symbol,
		"amount":      closedQuantity,
		"price":       price,
		"total_value": totalValue,
		"pnl":         pnl,
		"pnl_percent": pnlPercent,
		"new_balance": wallet.Balance,
		"position":    position,
	})
}

func paperCostBPS() (int64, int64) {
	values := map[string]int64{"paper_fee_bps": 10, "paper_slippage_bps": 5}
	var settings []database.Setting
	if err := database.DB.Where("key IN ?", []string{"paper_fee_bps", "paper_slippage_bps"}).Find(&settings).Error; err == nil {
		for _, setting := range settings {
			if value, err := strconv.ParseInt(strings.TrimSpace(setting.Value), 10, 64); err == nil && value >= 0 {
				values[setting.Key] = value
			}
		}
	}
	return values["paper_fee_bps"], values["paper_slippage_bps"]
}

func parseExactJSON(raw json.RawMessage, field string, allowZero bool) (accounting.Decimal, error) {
	if len(raw) == 0 {
		return accounting.Zero(), nil
	}
	text := strings.TrimSpace(string(raw))
	if strings.HasPrefix(text, "\"") {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return accounting.Decimal{}, err
		}
		text = value
	}
	value, err := accounting.Parse(text)
	if err != nil {
		return accounting.Decimal{}, fmt.Errorf("invalid %s: %w", field, err)
	}
	if value.Sign() < 0 || (!allowZero && value.Sign() == 0) {
		return accounting.Decimal{}, fmt.Errorf("%s must be positive", field)
	}
	return value, nil
}
