package handlers

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/services"
	ws "trading-go/internal/websocket"

	"github.com/gofiber/fiber/v2"
)

func GetPositions(c *fiber.Ctx) error {
	var positions []database.Position
	if err := database.DB.Find(&positions).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch positions"})
	}
	if positions == nil {
		positions = []database.Position{}
	}
	return c.JSON(positions)
}

func CreatePosition(c *fiber.Ctx) error {
	type CreatePositionRequest struct {
		Symbol     string   `json:"symbol"`
		Amount     float64  `json:"amount"`
		AvgPrice   float64  `json:"avg_price"`
		EntryPrice *float64 `json:"entry_price"`
	}

	var req CreatePositionRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.Symbol == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Symbol is required"})
	}

	existing := database.Position{}
	if err := database.DB.Where("symbol = ? AND status = ?", req.Symbol, "open").First(&existing).Error; err == nil {
		return c.Status(400).JSON(fiber.Map{"error": "Position already exists for this symbol"})
	}

	position := database.Position{
		Symbol:     req.Symbol,
		Amount:     req.Amount,
		AvgPrice:   req.AvgPrice,
		EntryPrice: req.EntryPrice,
		Status:     "open",
		OpenedAt:   time.Now(),
	}

	if err := database.DB.Create(&position).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to create position"})
	}

	if wsHub != nil {
		wsHub.BroadcastMsg(&ws.Message{
			Type:    "position_update",
			Payload: position,
		})
	}

	return c.Status(201).JSON(position)
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

	var position database.Position
	if err := database.DB.First(&position, id).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Position not found"})
	}

	now := time.Now()
	position.Status = "closed"
	position.ClosedAt = &now
	if req.CloseReason != nil {
		position.CloseReason = req.CloseReason
	}

	if err := database.DB.Save(&position).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to close position"})
	}

	if wsHub != nil {
		wsHub.BroadcastMsg(&ws.Message{
			Type:    "position_update",
			Payload: position,
		})
	}

	return c.JSON(position)
}

func DeletePosition(c *fiber.Ctx) error {
	symbol := c.Params("symbol")

	var position database.Position
	if err := database.DB.Where("symbol = ?", symbol).First(&position).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "Position not found"})
	}

	if err := database.DB.Delete(&position).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to delete position"})
	}

	if wsHub != nil {
		wsHub.BroadcastMsg(&ws.Message{
			Type:    "position_deleted",
			Payload: symbol,
		})
	}

	return c.JSON(fiber.Map{"message": "Position deleted successfully"})
}

// ExecuteOpenTrade simulates a buy order and creates a position (paper trading)
func ExecuteOpenTrade(c *fiber.Ctx) error {
	type OpenTradeRequest struct {
		Symbol    string  `json:"symbol"`
		Amount    float64 `json:"amount"`
		Price     float64 `json:"price"`      // 0 for market order
		OrderType string  `json:"order_type"` // "market" or "limit"
	}

	var req OpenTradeRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.Symbol == "" {
		return c.Status(400).JSON(fiber.Map{"error": "Symbol is required"})
	}

	if req.Amount <= 0 {
		return c.Status(400).JSON(fiber.Map{"error": "Amount must be greater than 0"})
	}

	// Format symbol for price lookup
	symbol := strings.ToUpper(req.Symbol)
	if !strings.HasSuffix(symbol, "USDT") {
		symbol = symbol + "USDT"
	}

	// Fetch current price (for paper trading, we still need current market price)
	exchange := services.GetExchange()
	var price float64 = req.Price
	if req.OrderType == "market" || req.Price <= 0 {
		ticker, err := exchange.FetchTickerPrice(symbol)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to fetch price: %v", err)})
		}
		price, _ = strconv.ParseFloat(ticker.LastPrice, 64)
	}

	totalCost := req.Amount * price

	// Check wallet balance
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch wallet"})
	}

	if totalCost > wallet.Balance {
		return c.Status(400).JSON(fiber.Map{"error": fmt.Sprintf("Insufficient balance. Required: %.2f USDT, Available: %.2f USDT", totalCost, wallet.Balance)})
	}

	// Simulate buy order - update wallet locally (paper trading)
	wallet.Balance -= totalCost
	database.DB.Save(&wallet)

	// Create position record
	position := database.Position{
		Symbol:     req.Symbol,
		Amount:     req.Amount,
		AvgPrice:   price,
		EntryPrice: &price,
		Status:     "open",
		OpenedAt:   time.Now(),
	}

	if err := database.DB.Create(&position).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to create position record"})
	}

	// Create order record
	order := database.Order{
		OrderType:    "buy",
		Symbol:       req.Symbol,
		AmountCrypto: req.Amount,
		AmountUsdt:   totalCost,
		Price:        price,
		ExecutedAt:   time.Now(),
	}
	database.DB.Create(&order)

	// Broadcast updates
	if wsHub != nil {
		wsHub.BroadcastMsg(&ws.Message{
			Type: "trade_executed",
			Payload: fiber.Map{
				"type":        "buy",
				"symbol":      req.Symbol,
				"amount":      req.Amount,
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

	return c.JSON(fiber.Map{
		"success":     true,
		"order_id":    fmt.Sprintf("paper_%d", time.Now().Unix()),
		"symbol":      req.Symbol,
		"amount":      req.Amount,
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
		CloseReason string  `json:"close_reason"` // manual, take_profit, stop_loss
		Price       float64 `json:"price"`        // 0 for market order
		OrderType   string  `json:"order_type"`   // "market" or "limit"
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

	// Format symbol for price lookup
	symbol := strings.ToUpper(position.Symbol)
	if !strings.HasSuffix(symbol, "USDT") {
		symbol = symbol + "USDT"
	}

	// Fetch current price (for paper trading, we still need current market price)
	exchange := services.GetExchange()
	var price float64 = req.Price
	if req.OrderType == "market" || req.Price <= 0 {
		ticker, err := exchange.FetchTickerPrice(symbol)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": fmt.Sprintf("Failed to fetch price: %v", err)})
		}
		price, _ = strconv.ParseFloat(ticker.LastPrice, 64)
	}

	totalValue := position.Amount * price

	// Calculate PnL
	pnl := totalValue - (position.Amount * position.AvgPrice)
	pnlPercent := 0.0
	if position.AvgPrice > 0 {
		pnlPercent = (pnl / (position.Amount * position.AvgPrice)) * 100
	}

	// Simulate sell order - update wallet locally (paper trading)
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch wallet"})
	}
	wallet.Balance += totalValue
	database.DB.Save(&wallet)

	// Update position status
	now := time.Now()
	position.Status = "closed"
	position.ClosedAt = &now
	position.CloseReason = &req.CloseReason
	position.CurrentPrice = &price
	position.Pnl = pnl
	position.PnlPercent = pnlPercent
	database.DB.Save(&position)

	// Create order record
	order := database.Order{
		OrderType:    "sell",
		Symbol:       position.Symbol,
		AmountCrypto: position.Amount,
		AmountUsdt:   totalValue,
		Price:        price,
		ExecutedAt:   time.Now(),
	}
	database.DB.Create(&order)

	// Broadcast updates
	if wsHub != nil {
		wsHub.BroadcastMsg(&ws.Message{
			Type: "trade_executed",
			Payload: fiber.Map{
				"type":        "sell",
				"symbol":      position.Symbol,
				"amount":      position.Amount,
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
		wsHub.BroadcastMsg(&ws.Message{
			Type:    "wallet_update",
			Payload: fiber.Map{"balance": wallet.Balance, "currency": wallet.Currency},
		})
	}

	return c.JSON(fiber.Map{
		"success":     true,
		"order_id":    fmt.Sprintf("paper_%d", time.Now().Unix()),
		"symbol":      position.Symbol,
		"amount":      position.Amount,
		"price":       price,
		"total_value": totalValue,
		"pnl":         pnl,
		"pnl_percent": pnlPercent,
		"new_balance": wallet.Balance,
		"position":    position,
	})
}
