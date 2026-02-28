package services

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"

	"github.com/gofiber/fiber/v2"
)

var exchange *ExchangeService

func InitTradingService(apiKey, apiSecret string) {
	exchange = NewExchangeService(apiKey, apiSecret)
}

func GetExchange() *ExchangeService {
	if exchange == nil {
		exchange = NewExchangeService("", "")
	}
	return exchange
}

type BuyRequest struct {
	Symbol string  `json:"symbol"`
	Amount float64 `json:"amount"`
	Price  float64 `json:"price"`
	UserID uint    `json:"user_id"`
}

type SellRequest struct {
	Symbol string  `json:"symbol"`
	Amount float64 `json:"amount"`
	Price  float64 `json:"price"`
	UserID uint    `json:"user_id"`
}

type UpdatePricesRequest struct {
	Prices map[string]float64 `json:"prices"`
}

func ExecuteBuy(req BuyRequest) (interface{}, error) {
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return nil, fiber.NewError(500, "Failed to fetch wallet")
	}

	symbol := req.Symbol
	amount := req.Amount
	price := req.Price

	if price <= 0 {
		ticker, err := exchange.FetchTickerPrice(symbol)
		if err != nil {
			return nil, fiber.NewError(500, "Failed to fetch price")
		}
		p, _ := strconv.ParseFloat(ticker.LastPrice, 64)
		price = p
	}

	totalCost := amount * price
	if totalCost > wallet.Balance {
		return nil, fiber.NewError(400, "Insufficient balance")
	}

	orderResp, err := exchange.ExecuteBuy(symbol, amount, 0)
	if err != nil {
		wallet.Balance -= totalCost
		database.DB.Save(&wallet)
		return nil, fiber.NewError(500, "Failed to place buy order")
	}

	wallet.Balance -= totalCost
	database.DB.Save(&wallet)

	position := database.Position{
		Symbol:     symbol,
		Amount:     amount,
		AvgPrice:   price,
		EntryPrice: &price,
		Status:     "open",
		OpenedAt:   time.Now(),
	}
	database.DB.Create(&position)

	order := database.Order{
		OrderType:    "buy",
		Symbol:       symbol,
		AmountCrypto: amount,
		AmountUsdt:   totalCost,
		Price:        price,
		ExecutedAt:   time.Now(),
	}
	database.DB.Create(&order)

	return fiber.Map{
		"success":     true,
		"order_id":    orderResp.OrderID,
		"position":    position,
		"new_balance": wallet.Balance,
	}, nil
}

func ExecuteSell(req SellRequest) (interface{}, error) {
	var position database.Position
	if err := database.DB.Where("symbol = ? AND status = ?", req.Symbol, "open").First(&position).Error; err != nil {
		return nil, fiber.NewError(404, "Position not found")
	}

	if req.Amount > position.Amount {
		return nil, fiber.NewError(400, "Insufficient position amount")
	}

	symbol := req.Symbol
	amount := req.Amount
	price := req.Price

	if price <= 0 {
		ticker, err := exchange.FetchTickerPrice(symbol)
		if err != nil {
			return nil, fiber.NewError(500, "Failed to fetch price")
		}
		p, _ := strconv.ParseFloat(ticker.LastPrice, 64)
		price = p
	}

	totalValue := amount * price

	orderResp, err := exchange.ExecuteSell(symbol, amount, 0)
	if err != nil {
		return nil, fiber.NewError(500, "Failed to place sell order")
	}

	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return nil, fiber.NewError(500, "Failed to fetch wallet")
	}

	wallet.Balance += totalValue
	database.DB.Save(&wallet)

	position.Amount -= amount
	if position.Amount <= 0 {
		position.Status = "closed"
		now := time.Now()
		position.ClosedAt = &now
		reason := "sold"
		position.CloseReason = &reason
	}
	database.DB.Save(&position)

	order := database.Order{
		OrderType:    "sell",
		Symbol:       symbol,
		AmountCrypto: amount,
		AmountUsdt:   totalValue,
		Price:        price,
		ExecutedAt:   time.Now(),
	}
	database.DB.Create(&order)

	return fiber.Map{
		"success":     true,
		"order_id":    orderResp.OrderID,
		"position":    position,
		"new_balance": wallet.Balance,
	}, nil
}

func UpdatePositionsPrices() (interface{}, error) {
	settings := GetAllSettings()
	stopLossPercent := getSettingFloat(settings, "stop_loss_percent", 5.0)
	takeProfitPercent := getSettingFloat(settings, "take_profit_percent", 30.0)
	trailingStopEnabled := getSettingBool(settings, "trailing_stop_enabled", false)
	trailingStopPercent := getSettingFloat(settings, "trailing_stop_percent", 10.0)
	allowSellAtLoss := getSettingBool(settings, "allow_sell_at_loss", false)
	sellOnSignal := getSettingBool(settings, "sell_on_signal", true)
	minConfidenceToSell := getSettingFloat(settings, "min_confidence_to_sell", 3.5)

	var positions []database.Position
	if err := database.DB.Where("status = ?", "open").Find(&positions).Error; err != nil {
		return nil, fiber.NewError(500, "Failed to fetch positions")
	}

	if len(positions) == 0 {
		return fiber.Map{"success": true, "updated": 0}, nil
	}

	var wallet database.Wallet
	database.DB.First(&wallet)

	symbols := make([]string, len(positions))
	for i, pos := range positions {
		symbol := pos.Symbol
		if !strings.HasSuffix(symbol, "USDT") {
			symbol += "USDT"
		}
		symbols[i] = symbol
	}

	tickers, err := GetExchange().FetchMultipleTickerPrices(symbols)
	if err != nil {
		return nil, fiber.NewError(500, "Failed to fetch prices")
	}

	updatedCount := 0
	for i := range positions {
		tickerKey := positions[i].Symbol
		if !strings.HasSuffix(tickerKey, "USDT") {
			tickerKey += "USDT"
		}

		if ticker, ok := tickers[tickerKey]; ok {
			currentPrice, _ := strconv.ParseFloat(ticker.LastPrice, 64)
			positions[i].CurrentPrice = &currentPrice

			pnl := (currentPrice - positions[i].AvgPrice) * positions[i].Amount
			pnlPercent := 0.0
			if positions[i].AvgPrice > 0 {
				pnlPercent = ((currentPrice - positions[i].AvgPrice) / positions[i].AvgPrice) * 100
			}

			positions[i].Pnl = pnl
			positions[i].PnlPercent = pnlPercent

			// Update trailing peak
			if trailingStopEnabled {
				if positions[i].EntryPrice == nil {
					positions[i].EntryPrice = &positions[i].AvgPrice
				}
				if currentPrice > *positions[i].EntryPrice {
					*positions[i].EntryPrice = currentPrice
				}
			}

			closeReason := ""

			if pnlPercent <= -stopLossPercent {
				closeReason = "stop_loss"
			} else if pnlPercent >= takeProfitPercent {
				closeReason = "take_profit"
			} else if trailingStopEnabled && positions[i].EntryPrice != nil {
				dropFromHigh := ((currentPrice - *positions[i].EntryPrice) / *positions[i].EntryPrice) * 100
				if dropFromHigh <= -trailingStopPercent {
					closeReason = "trailing_stop"
				}
			}

			if closeReason == "" && sellOnSignal {
				analysis, aimErr := analyzeSymbolForTrending(tickerKey, "15m")
				if aimErr == nil {
					if analysis.Signal == "SELL" || analysis.Signal == "STRONG_SELL" {
						if analysis.Rating <= minConfidenceToSell {
							closeReason = "sell_signal"
						}
					}
				}
			}

			if closeReason != "" {
				if closeReason == "stop_loss" && !allowSellAtLoss {
					// Skip loss
				} else {
					positions[i].Status = "closed"
					now := time.Now()
					positions[i].ClosedAt = &now
					positions[i].CloseReason = &closeReason

					wallet.Balance += positions[i].Amount * currentPrice
					database.DB.Save(&wallet)

					logActivity("trade", fmt.Sprintf("Auto-closed %s", positions[i].Symbol), fmt.Sprintf("Reason: %s, PnL: %.2f%%", closeReason, pnlPercent))
				}
			}

			database.DB.Save(&positions[i])
			updatedCount++
		}
	}

	return fiber.Map{"success": true, "updated": updatedCount}, nil
}

func CheckStopLoss(symbol string, stopLossPercent float64) (bool, string, error) {
	var position database.Position
	if err := database.DB.Where("symbol = ? AND status = ?", symbol, "open").First(&position).Error; err != nil {
		return false, "", nil
	}

	if position.CurrentPrice == nil {
		return false, "", nil
	}

	currentPrice := *position.CurrentPrice
	entryPrice := position.AvgPrice
	lossPercent := ((currentPrice - entryPrice) / entryPrice) * 100

	if lossPercent <= -stopLossPercent {
		return true, "stop_loss", nil
	}

	return false, "", nil
}

func CheckTakeProfit(symbol string, takeProfitPercent float64) (bool, string, error) {
	var position database.Position
	if err := database.DB.Where("symbol = ? AND status = ?", symbol, "open").First(&position).Error; err != nil {
		return false, "", nil
	}

	if position.CurrentPrice == nil {
		return false, "", nil
	}

	currentPrice := *position.CurrentPrice
	entryPrice := position.AvgPrice
	profitPercent := ((currentPrice - entryPrice) / entryPrice) * 100

	if profitPercent >= takeProfitPercent {
		return true, "take_profit", nil
	}

	return false, "", nil
}

func CheckTrailingStop(symbol string, trailingStopPercent float64, highestPrice *float64) (bool, string, float64, error) {
	var position database.Position
	if err := database.DB.Where("symbol = ? AND status = ?", symbol, "open").First(&position).Error; err != nil {
		return false, "", 0, nil
	}

	if position.CurrentPrice == nil {
		return false, "", 0, nil
	}

	currentPrice := *position.CurrentPrice

	if highestPrice == nil || currentPrice > *highestPrice {
		newHighest := currentPrice
		highestPrice = &newHighest
	}

	entryPrice := position.AvgPrice
	highestSinceEntry := *highestPrice

	if highestSinceEntry > entryPrice {
		dropFromHigh := ((highestSinceEntry - currentPrice) / highestSinceEntry) * 100
		if dropFromHigh >= trailingStopPercent {
			return true, "trailing_stop", *highestPrice, nil
		}
	}

	return false, "", *highestPrice, nil
}

func ExecuteStopLoss(symbol string, amount float64) (interface{}, error) {
	req := SellRequest{
		Symbol: symbol,
		Amount: amount,
		Price:  0,
	}
	return ExecuteSell(req)
}

func ExecuteTakeProfit(symbol string, amount float64) (interface{}, error) {
	req := SellRequest{
		Symbol: symbol,
		Amount: amount,
		Price:  0,
	}
	return ExecuteSell(req)
}
