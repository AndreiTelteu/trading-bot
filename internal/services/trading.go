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

type TradeDecisionContext struct {
	ModelVersion        string
	PolicyVersion       string
	UniverseMode        string
	RolloutState        string
	ExperimentID        string
	PredictionLogID     *uint
	DecisionContextJSON string
}

func newClientPositionID(symbol string, now time.Time) *string {
	value := fmt.Sprintf("%s-%d", strings.ToLower(strings.TrimSpace(symbol)), now.UnixMilli())
	return &value
}

func markPositionPrice(position *database.Position, price float64, at time.Time) {
	position.CurrentPrice = floatPtr(price)
	position.LastMarkPrice = floatPtr(price)
	position.LastMarkAt = &at
	position.Pnl = (price - position.AvgPrice) * position.Amount
	if position.AvgPrice > 0 {
		position.PnlPercent = ((price - position.AvgPrice) / position.AvgPrice) * 100
	}
}

func resolveCloseReason(currentPrice float64, pnlPercent float64, stopPrice *float64, takeProfitPrice *float64, atrTrailingEnabled bool, trailingStopPrice *float64, trailingStopEnabled bool, entryPrice *float64, trailingStopPercent float64, stopLossPercent float64, takeProfitPercent float64) string {
	entry := 0.0
	if entryPrice != nil {
		entry = *entryPrice
	}
	legacyTrailingStop := trailingStopPrice
	if legacyTrailingStop == nil && trailingStopEnabled && entry > 0 {
		legacyTrailingStop = floatPtr(entry * (1 - (trailingStopPercent / 100)))
	}
	decision := EvaluateProtectiveExit(ExitEvaluationInput{
		CurrentPrice:      currentPrice,
		HighPrice:         currentPrice,
		LowPrice:          currentPrice,
		EntryPrice:        entry,
		StopPrice:         stopPrice,
		TakeProfitPrice:   takeProfitPrice,
		TrailingStopPrice: legacyTrailingStop,
	}, ExitPolicy{
		StopLossPercent:     stopLossPercent,
		TakeProfitPercent:   takeProfitPercent,
		TrailingStopEnabled: trailingStopEnabled,
		TrailingStopPercent: trailingStopPercent,
		ATRTrailingEnabled:  atrTrailingEnabled,
	})
	return decision.Reason
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
			position.ExecutionMode = ExecutionModeExchange
			position.EntrySource = EntrySourceManual
			position.ExitPending = false
			position.CurrentPrice = &price
			position.LastMarkPrice = &price
			position.LastMarkAt = &now
			position.ClientPositionID = newClientPositionID(symbol, now)
			position.DecisionTimeframe = DecisionTimeframeDefault
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
				Symbol:            symbol,
				Amount:            amount,
				AvgPrice:          price,
				EntryPrice:        &price,
				CurrentPrice:      &price,
				ExecutionMode:     ExecutionModeExchange,
				EntrySource:       EntrySourceManual,
				LastMarkPrice:     &price,
				LastMarkAt:        &now,
				ClientPositionID:  newClientPositionID(symbol, now),
				DecisionTimeframe: DecisionTimeframeDefault,
				Status:            "open",
				OpenedAt:          now,
			}
			if err := tx.Create(&position).Error; err != nil {
				return err
			}
		default:
			return lookupErr
		}

		order := database.Order{
			OrderType:      "buy",
			Symbol:         symbol,
			AmountCrypto:   amount,
			AmountUsdt:     totalCost,
			Price:          price,
			Status:         normalizeOrderStatus(orderResp.Status),
			ExecutionMode:  ExecutionModeExchange,
			RequestedPrice: &price,
			FillPrice:      &price,
			ExecutedQty:    floatPtr(amount),
			SubmittedAt:    &now,
			FilledAt:       &now,
			ExecutedAt:     now,
		}
		if orderResp.OrderID > 0 {
			value := strconv.FormatInt(orderResp.OrderID, 10)
			order.ExchangeOrderID = &value
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
	NotifyPositionChanged()

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
		now := time.Now()
		if err := tx.First(&wallet).Error; err != nil {
			return err
		}

		wallet.Balance += totalValue
		if err := tx.Save(&wallet).Error; err != nil {
			return err
		}

		position.Amount -= amount
		markPositionPrice(&position, price, time.Now())
		position.ExitPending = false
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
		if position.Status == "closed" {
			if err := RecordTradeOutcome(tx, position); err != nil {
				return err
			}
		}

		order := database.Order{
			OrderType:      "sell",
			Symbol:         symbol,
			AmountCrypto:   amount,
			AmountUsdt:     totalValue,
			Price:          price,
			Status:         normalizeOrderStatus(orderResp.Status),
			ExecutionMode:  ExecutionModeExchange,
			RequestedPrice: &price,
			FillPrice:      &price,
			ExecutedQty:    floatPtr(amount),
			TriggerReason:  stringPtr("manual_sell"),
			SubmittedAt:    &now,
			FilledAt:       &now,
			ExecutedAt:     now,
		}
		if orderResp.OrderID > 0 {
			value := strconv.FormatInt(orderResp.OrderID, 10)
			order.ExchangeOrderID = &value
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
	NotifyPositionChanged()

	return fiber.Map{
		"success":     true,
		"order_id":    orderResp.OrderID,
		"position":    position,
		"new_balance": wallet.Balance,
	}, nil
}

func UpdatePositionsPrices() (interface{}, error) {
	settings := GetAllSettings()
	policy := BuildExitPolicy(settings)

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
	coordinator := GetExecutionCoordinator()
	supervisor := GetStreamSupervisor()
	for i := range positions {
		tickerKey := positions[i].Symbol
		if !strings.HasSuffix(tickerKey, "USDT") {
			tickerKey += "USDT"
		}

		if ticker, ok := tickers[tickerKey]; ok {
			currentPrice, _ := strconv.ParseFloat(ticker.LastPrice, 64)
			now := time.Now()
			markPositionPrice(&positions[i], currentPrice, now)

			entryPrice := positions[i].AvgPrice
			if positions[i].EntryPrice != nil && *positions[i].EntryPrice > 0 {
				entryPrice = *positions[i].EntryPrice
			}

			if policy.ATRTrailingEnabled && positions[i].LastAtrValue != nil {
				positions[i].TrailingStopPrice = RatchetATRTrailingStop(positions[i].TrailingStopPrice, currentPrice, entryPrice, *positions[i].LastAtrValue, policy.ATRTrailingMult)
			} else if policy.TrailingStopEnabled {
				positions[i].TrailingStopPrice = RatchetPercentTrailingStop(positions[i].TrailingStopPrice, currentPrice, entryPrice, policy.TrailingStopPercent)
			}

			if err := database.DB.Save(&positions[i]).Error; err != nil {
				return nil, fiber.NewError(500, "Failed to update position price")
			}
			updatedCount++

			if positions[i].ExitPending {
				continue
			}
			if supervisor != nil && !supervisor.ShouldFallback(positions[i].Symbol) {
				continue
			}

			decision := EvaluateProtectiveExit(ExitEvaluationInput{
				CurrentPrice:      currentPrice,
				HighPrice:         currentPrice,
				LowPrice:          currentPrice,
				EntryPrice:        entryPrice,
				StopPrice:         positions[i].StopPrice,
				TakeProfitPrice:   positions[i].TakeProfitPrice,
				TrailingStopPrice: positions[i].TrailingStopPrice,
				ObservedAt:        now,
				ExecutionMode:     positions[i].ExecutionMode,
			}, policy)
			if decision.Reason == "" {
				continue
			}

			if _, err := coordinator.RequestClose(CloseRequest{
				PositionID:     positions[i].ID,
				Reason:         decision.Reason,
				RequestedPrice: decision.TriggerPrice,
				TriggeredAt:    now,
				Source:         "cron_reconcile",
			}); err != nil {
				return nil, fiber.NewError(500, "Failed to reconcile protective exit")
			}
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
