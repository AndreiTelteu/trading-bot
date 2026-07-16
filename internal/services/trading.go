package services

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
	"trading-go/internal/websocket"

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
	Symbol         string  `json:"symbol"`
	Amount         float64 `json:"amount"`
	Price          float64 `json:"price"`
	UserID         uint    `json:"user_id"`
	IdempotencyKey string  `json:"idempotency_key"`
}

type SellRequest struct {
	Symbol         string  `json:"symbol"`
	Amount         float64 `json:"amount"`
	Price          float64 `json:"price"`
	UserID         uint    `json:"user_id"`
	IdempotencyKey string  `json:"idempotency_key"`
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
			symbol := PositionPairSymbol(pos.Symbol, wallet.Currency)
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
	return nil, ledgerpkg.ErrExchangeExecutionFenced
}
func ExecuteSell(req SellRequest) (interface{}, error) {
	return nil, ledgerpkg.ErrExchangeExecutionFenced
}
func UpdatePositionsPrices() (interface{}, error) {
	settings := GetAllSettings()
	policy := BuildExitPolicy(settings)

	var positions []database.Position
	if err := database.DB.Where("status = ?", "open").Find(&positions).Error; err != nil {
		return nil, fiber.NewError(500, "Failed to fetch positions")
	}
	var settlement database.Wallet
	if err := database.DB.First(&settlement).Error; err != nil {
		return nil, fiber.NewError(500, "Failed to fetch wallet")
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
		symbols[i] = PositionPairSymbol(pos.Symbol, settlement.Currency)
	}

	tickers, err := GetExchange().FetchMultipleTickerPrices(symbols)
	if err != nil {
		return nil, fiber.NewError(500, "Failed to fetch prices")
	}

	updatedCount := 0
	coordinator := GetExecutionCoordinator()
	supervisor := GetStreamSupervisor()
	for i := range positions {
		tickerKey := PositionPairSymbol(positions[i].Symbol, settlement.Currency)

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

			updated, err := updatePositionOperational(positions[i].ID, now, map[string]interface{}{"current_price": currentPrice, "last_mark_price": currentPrice, "trailing_stop_price": positions[i].TrailingStopPrice})
			if err != nil {
				return nil, fiber.NewError(500, "Failed to update position price")
			}
			if !updated {
				continue
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
