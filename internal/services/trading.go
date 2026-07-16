package services

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/accounting"
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
	if err := ledgerpkg.New(database.DB).CheckReady(context.Background(), ""); err != nil {
		return nil, fiber.NewError(409, err.Error())
	}
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
	approvedQuantity, quantityErr := accounting.FromFloat(amount)
	approvedPrice, approvedPriceErr := accounting.FromFloat(price)
	if quantityErr != nil || approvedPriceErr != nil || wallet.BalanceExact == nil {
		return nil, fiber.NewError(409, "Exact ledger cash projection is unavailable")
	}
	if approvedQuantity.Mul(approvedPrice).Cmp(*wallet.BalanceExact) > 0 {
		return nil, fiber.NewError(400, "Insufficient balance")
	}

	orderResp, err := exchange.ExecuteBuy(symbol, amount, 0)
	if err != nil {
		return nil, fiber.NewError(500, "Failed to place buy order")
	}

	executed := orderResp.ExecutedQty
	if executed <= 0 && strings.EqualFold(orderResp.Status, "filled") {
		executed = amount
	}
	if executed <= 0 {
		return fiber.Map{"success": true, "order_id": orderResp.OrderID, "status": normalizeOrderStatus(orderResp.Status), "message": "order accepted without a reported fill; no economic projection changed"}, nil
	}
	fillPrice := orderResp.Price
	if fillPrice <= 0 {
		fillPrice = price
	}
	quantityExact, qErr := accounting.FromFloat(executed)
	requestedExact, pErr := accounting.FromFloat(price)
	fillExact, fErr := accounting.FromFloat(fillPrice)
	if qErr != nil || pErr != nil || fErr != nil {
		return nil, fiber.NewError(500, "Exchange fill precision is invalid")
	}
	key := req.IdempotencyKey
	if key == "" {
		key = fmt.Sprintf("exchange-fill-%d-%s", orderResp.OrderID, quantityExact.String())
	}
	now := time.Now().UTC()
	providerID := ""
	providerOrderID := ""
	if orderResp.OrderID > 0 {
		providerOrderID = strconv.FormatInt(orderResp.OrderID, 10)
		providerID = providerOrderID + ":" + quantityExact.String()
	}
	fillResult, err := ledgerpkg.New(database.DB).ApplyFill(context.Background(), ledgerpkg.FillCommand{IdempotencyKey: key, Symbol: symbol, Side: "buy", Quantity: quantityExact, RequestedPrice: requestedExact, FillPrice: fillExact, Fee: accounting.Zero(), FeeType: ledgerpkg.EventExchangeFee, Currency: wallet.Currency, ExecutionMode: ExecutionModeExchange, ProviderFillID: providerID, ProviderOrderID: providerOrderID, OrderStatus: normalizeOrderStatus(orderResp.Status), OccurredAt: now, Actor: "binance_adapter", Reason: "manual exchange buy", EntrySource: EntrySourceManual, DecisionTimeframe: DecisionTimeframeDefault, Metadata: map[string]interface{}{"exchange_fee_status": "unavailable_from_order_response"}})
	if err != nil {
		if errors.Is(err, ledgerpkg.ErrInsufficientCash) {
			return nil, fiber.NewError(400, err.Error())
		}
		return nil, fiber.NewError(409, err.Error())
	}
	wallet, position := fillResult.Wallet, fillResult.Position

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
	if err := ledgerpkg.New(database.DB).CheckReady(context.Background(), ""); err != nil {
		return nil, fiber.NewError(409, err.Error())
	}
	var position database.Position
	if err := database.DB.Where("symbol = ? AND status = ?", req.Symbol, "open").First(&position).Error; err != nil {
		return nil, fiber.NewError(404, "Position not found")
	}

	if req.Amount > position.Amount {
		return nil, fiber.NewError(400, "Insufficient position amount")
	}
	if position.AmountExact == nil {
		return nil, fiber.NewError(409, ledgerpkg.ErrProjectionUnavailable.Error())
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

	orderResp, err := exchange.ExecuteSell(symbol, amount, 0)
	if err != nil {
		return nil, fiber.NewError(500, "Failed to place sell order")
	}

	executed := orderResp.ExecutedQty
	if executed <= 0 && strings.EqualFold(orderResp.Status, "filled") {
		executed = amount
	}
	if executed <= 0 {
		return fiber.Map{"success": true, "order_id": orderResp.OrderID, "status": normalizeOrderStatus(orderResp.Status), "message": "order accepted without a reported fill; no economic projection changed"}, nil
	}
	fillPrice := orderResp.Price
	if fillPrice <= 0 {
		fillPrice = price
	}
	quantityExact, qErr := accounting.FromFloat(executed)
	requestedExact, pErr := accounting.FromFloat(price)
	fillExact, fErr := accounting.FromFloat(fillPrice)
	if qErr != nil || pErr != nil || fErr != nil {
		return nil, fiber.NewError(500, "Exchange fill precision is invalid")
	}
	key := req.IdempotencyKey
	if key == "" {
		key = fmt.Sprintf("exchange-fill-%d-%s", orderResp.OrderID, quantityExact.String())
	}
	now := time.Now().UTC()
	providerID := ""
	providerOrderID := ""
	if orderResp.OrderID > 0 {
		providerOrderID = strconv.FormatInt(orderResp.OrderID, 10)
		providerID = providerOrderID + ":" + quantityExact.String()
	}
	var wallet database.Wallet
	fillResult, err := ledgerpkg.New(database.DB).ApplyFill(context.Background(), ledgerpkg.FillCommand{IdempotencyKey: key, Symbol: symbol, Side: "sell", Quantity: quantityExact, RequestedPrice: requestedExact, FillPrice: fillExact, Fee: accounting.Zero(), FeeType: ledgerpkg.EventExchangeFee, Currency: "USDT", ExecutionMode: ExecutionModeExchange, ProviderFillID: providerID, ProviderOrderID: providerOrderID, OrderStatus: normalizeOrderStatus(orderResp.Status), OccurredAt: now, Actor: "binance_adapter", Reason: "manual_sell", Metadata: map[string]interface{}{"exchange_fee_status": "unavailable_from_order_response"}})
	if err != nil {
		return nil, fiber.NewError(409, err.Error())
	}
	wallet, position = fillResult.Wallet, fillResult.Position

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
