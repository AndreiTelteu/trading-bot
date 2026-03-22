package services

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/websocket"

	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
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

func createPortfolioSnapshot() (database.PortfolioSnapshot, database.Wallet, []database.Position, error) {
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return database.PortfolioSnapshot{}, database.Wallet{}, nil, fiber.NewError(500, "Failed to fetch wallet")
	}

	var openPositions []database.Position
	if err := database.DB.Where("status = ?", "open").Find(&openPositions).Error; err != nil {
		return database.PortfolioSnapshot{}, database.Wallet{}, nil, fiber.NewError(500, "Failed to fetch positions")
	}

	settings := GetAllSettings()
	atrAnnualizationEnabled := getSettingBool(settings, "atr_annualization_enabled", false)
	atrAnnualizationDays := getSettingInt(settings, "atr_annualization_days", 365)
	atrTrailingPeriod := getSettingInt(settings, "atr_trailing_period", 14)

	totalSnapshotValue := wallet.Balance
	var snapshotVolatility *float64
	if atrAnnualizationEnabled {
		var sum float64
		var count int
		for _, pos := range openPositions {
			if pos.LastAtrValue != nil {
				sum += *pos.LastAtrValue
				count++
				continue
			}
			symbol := pos.Symbol
			if !strings.HasSuffix(symbol, "USDT") {
				symbol += "USDT"
			}
			candles, err := fetchCandles(symbol, "15m", 200)
			if err != nil {
				continue
			}
			atr := getAtrValue(candles, atrTrailingPeriod, atrAnnualizationEnabled, 15, atrAnnualizationDays)
			if atr > 0 {
				sum += atr
				count++
			}
		}
		if count > 0 {
			avg := sum / float64(count)
			snapshotVolatility = &avg
		}
	}

	for _, pos := range openPositions {
		if pos.CurrentPrice != nil {
			totalSnapshotValue += pos.Amount * (*pos.CurrentPrice)
		} else {
			totalSnapshotValue += pos.Amount * pos.AvgPrice
		}
	}

	snapshot := database.PortfolioSnapshot{
		TotalValue:           totalSnapshotValue,
		VolatilityAnnualized: snapshotVolatility,
		Timestamp:            time.Now(),
	}
	if err := database.DB.Create(&snapshot).Error; err != nil {
		return database.PortfolioSnapshot{}, database.Wallet{}, nil, fiber.NewError(500, "Failed to create portfolio snapshot")
	}

	return snapshot, wallet, openPositions, nil
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
		return nil, fiber.NewError(500, "Failed to place buy order")
	}

	now := time.Now()
	position := database.Position{}
	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&wallet).Error; err != nil {
			return err
		}

		if totalCost > wallet.Balance {
			return fiber.NewError(400, "Insufficient balance")
		}

		wallet.Balance -= totalCost
		if err := tx.Save(&wallet).Error; err != nil {
			return err
		}

		lookupErr := tx.Where("symbol = ?", symbol).First(&position).Error
		switch {
		case lookupErr == nil:
			if position.Status == "open" {
				return fiber.NewError(400, "Position already exists for this symbol")
			}

			position.Amount = amount
			position.AvgPrice = price
			position.EntryPrice = &price
			position.CurrentPrice = &price
			position.StopPrice = nil
			position.TakeProfitPrice = nil
			position.TrailingStopPrice = nil
			position.LastAtrValue = nil
			position.MaxBarsHeld = nil
			position.Pnl = 0
			position.PnlPercent = 0
			position.Status = "open"
			position.OpenedAt = now
			position.ClosedAt = nil
			position.CloseReason = nil

			if err := tx.Save(&position).Error; err != nil {
				return err
			}
		case errors.Is(lookupErr, gorm.ErrRecordNotFound):
			position = database.Position{
				Symbol:       symbol,
				Amount:       amount,
				AvgPrice:     price,
				EntryPrice:   &price,
				CurrentPrice: &price,
				Status:       "open",
				OpenedAt:     now,
			}
			if err := tx.Create(&position).Error; err != nil {
				return err
			}
		default:
			return lookupErr
		}

		order := database.Order{
			OrderType:    "buy",
			Symbol:       symbol,
			AmountCrypto: amount,
			AmountUsdt:   totalCost,
			Price:        price,
			ExecutedAt:   time.Now(),
		}
		return tx.Create(&order).Error
	}); err != nil {
		if fiberErr, ok := err.(*fiber.Error); ok {
			return nil, fiberErr
		}
		return nil, fiber.NewError(500, "Failed to persist buy order")
	}

	// Calculate total value for broadcast (wallet + open positions)
	totalValue := wallet.Balance
	var currentPositions []database.Position
	database.DB.Where("status = ?", "open").Find(&currentPositions)
	for _, p := range currentPositions {
		if p.CurrentPrice != nil {
			totalValue += p.Amount * (*p.CurrentPrice)
		} else {
			totalValue += p.Amount * p.AvgPrice
		}
	}

	// Broadcast updates via WebSocket
	websocket.BroadcastTradeExecuted("buy", symbol, amount, price, wallet.Balance)
	websocket.BroadcastWalletUpdate(wallet.Balance, wallet.Currency, totalValue)
	websocket.BroadcastPositionUpdate(position)

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
	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&wallet).Error; err != nil {
			return err
		}

		wallet.Balance += totalValue
		if err := tx.Save(&wallet).Error; err != nil {
			return err
		}

		position.Amount -= amount
		if position.Amount <= 0 {
			position.Status = "closed"
			now := time.Now()
			position.ClosedAt = &now
			reason := "sold"
			position.CloseReason = &reason
		}
		if err := tx.Save(&position).Error; err != nil {
			return err
		}

		order := database.Order{
			OrderType:    "sell",
			Symbol:       symbol,
			AmountCrypto: amount,
			AmountUsdt:   totalValue,
			Price:        price,
			ExecutedAt:   time.Now(),
		}
		return tx.Create(&order).Error
	}); err != nil {
		return nil, fiber.NewError(500, "Failed to persist sell order")
	}

	// Calculate total value for broadcast (wallet + open positions)
	portfolioTotalValue := wallet.Balance
	var currentPositions []database.Position
	database.DB.Where("status = ?", "open").Find(&currentPositions)
	for _, p := range currentPositions {
		if p.CurrentPrice != nil {
			portfolioTotalValue += p.Amount * (*p.CurrentPrice)
		} else {
			portfolioTotalValue += p.Amount * p.AvgPrice
		}
	}

	// Broadcast updates via WebSocket
	websocket.BroadcastTradeExecuted("sell", symbol, amount, price, wallet.Balance)
	websocket.BroadcastWalletUpdate(wallet.Balance, wallet.Currency, portfolioTotalValue)
	websocket.BroadcastPositionUpdate(position)

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
	atrTrailingEnabled := getSettingBool(settings, "atr_trailing_enabled", false)
	atrTrailingMult := getSettingFloat(settings, "atr_trailing_mult", 1.0)
	atrTrailingPeriod := getSettingInt(settings, "atr_trailing_period", 14)
	atrAnnualizationEnabled := getSettingBool(settings, "atr_annualization_enabled", false)
	atrAnnualizationDays := getSettingInt(settings, "atr_annualization_days", 365)
	allowSellAtLoss := getSettingBool(settings, "allow_sell_at_loss", false)
	sellOnSignal := getSettingBool(settings, "sell_on_signal", true)
	minConfidenceToSell := getSettingFloat(settings, "min_confidence_to_sell", 3.5)
	timeStopBars := getSettingInt(settings, "time_stop_bars", 0)

	var positions []database.Position
	if err := database.DB.Where("status = ?", "open").Find(&positions).Error; err != nil {
		return nil, fiber.NewError(500, "Failed to fetch positions")
	}

	if len(positions) == 0 {
		snapshot, wallet, _, err := createPortfolioSnapshot()
		if err != nil {
			return nil, err
		}
		websocket.BroadcastSnapshotUpdate(snapshot.Timestamp, snapshot.TotalValue)
		websocket.BroadcastPositionsUpdate([]database.Position{})
		websocket.BroadcastWalletUpdate(wallet.Balance, wallet.Currency, snapshot.TotalValue)
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

			if atrTrailingEnabled && atrTrailingMult > 0 {
				candles, err := fetchCandles(tickerKey, "15m", 200)
				if err == nil {
					atr := getAtrValue(candles, atrTrailingPeriod, atrAnnualizationEnabled, 15, atrAnnualizationDays)
					if atr > 0 {
						positions[i].LastAtrValue = &atr
						if positions[i].TrailingStopPrice == nil {
							entryBase := positions[i].AvgPrice
							if positions[i].EntryPrice != nil {
								entryBase = *positions[i].EntryPrice
							}
							entryStop := entryBase - (atr * atrTrailingMult)
							if entryStop > 0 {
								positions[i].TrailingStopPrice = &entryStop
							}
						}
						candidateStop := currentPrice - (atr * atrTrailingMult)
						if candidateStop > 0 {
							if positions[i].TrailingStopPrice == nil || candidateStop > *positions[i].TrailingStopPrice {
								positions[i].TrailingStopPrice = &candidateStop
							}
						}
					}
				}
			}

			// Update trailing peak
			if trailingStopEnabled {
				if positions[i].EntryPrice == nil {
					positions[i].EntryPrice = &positions[i].AvgPrice
				}
				if currentPrice > *positions[i].EntryPrice {
					*positions[i].EntryPrice = currentPrice
				}
			}

			closeReason := resolveCloseReason(currentPrice, pnlPercent, positions[i].StopPrice, positions[i].TakeProfitPrice, atrTrailingEnabled, positions[i].TrailingStopPrice, trailingStopEnabled, positions[i].EntryPrice, trailingStopPercent, stopLossPercent, takeProfitPercent)

			if closeReason == "" {
				maxBars := timeStopBars
				if positions[i].MaxBarsHeld != nil {
					maxBars = *positions[i].MaxBarsHeld
				}
				if maxBars > 0 {
					barsHeld := int(time.Since(positions[i].OpenedAt) / (15 * time.Minute))
					if barsHeld >= maxBars && pnlPercent <= 0 {
						closeReason = "time_stop"
					}
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

	snapshot, latestWallet, _, err := createPortfolioSnapshot()
	if err != nil {
		return nil, err
	}

	allPositions, err := database.ListPositionsForDisplay()
	if err != nil {
		return nil, err
	}

	// Broadcast updates via WebSocket
	websocket.BroadcastSnapshotUpdate(snapshot.Timestamp, snapshot.TotalValue)
	websocket.BroadcastPositionsUpdate(allPositions)

	websocket.BroadcastWalletUpdate(latestWallet.Balance, latestWallet.Currency, snapshot.TotalValue)

	return fiber.Map{"success": true, "updated": updatedCount}, nil
}

func resolveCloseReason(currentPrice float64, pnlPercent float64, stopPrice *float64, takeProfitPrice *float64, atrTrailingEnabled bool, trailingStopPrice *float64, trailingStopEnabled bool, entryPrice *float64, trailingStopPercent float64, stopLossPercent float64, takeProfitPercent float64) string {
	if stopPrice != nil && currentPrice <= *stopPrice {
		return "stop_loss"
	}
	if takeProfitPrice != nil && currentPrice >= *takeProfitPrice {
		return "take_profit"
	}
	if atrTrailingEnabled && trailingStopPrice != nil && currentPrice <= *trailingStopPrice {
		return "atr_trailing_stop"
	}
	if trailingStopEnabled && entryPrice != nil {
		dropFromHigh := ((currentPrice - *entryPrice) / *entryPrice) * 100
		if dropFromHigh <= -trailingStopPercent {
			return "trailing_stop"
		}
	}
	if stopPrice == nil && takeProfitPrice == nil {
		if pnlPercent <= -stopLossPercent {
			return "stop_loss"
		}
		if pnlPercent >= takeProfitPercent {
			return "take_profit"
		}
	}
	return ""
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
