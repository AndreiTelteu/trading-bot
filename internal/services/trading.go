package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
	"trading-go/internal/tradingcore"
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
	_, _ = buildApplicationFencedLiveRequest("buy", req.Symbol, req.Amount, req.Price, req.IdempotencyKey)
	return nil, ledgerpkg.ErrExchangeExecutionFenced
}
func ExecuteSell(req SellRequest) (interface{}, error) {
	_, _ = buildApplicationFencedLiveRequest("sell", req.Symbol, req.Amount, req.Price, req.IdempotencyKey)
	return nil, ledgerpkg.ErrExchangeExecutionFenced
}

// BuildFencedLiveRequest exposes deterministic Stage 02 request construction
// without making exchange submission reachable.
type FencedLiveRequestConfig struct {
	AccountID, SettlementCurrency, VenueID, VenueSymbol, BaseAsset string
	Portfolio                                                      tradingcore.PortfolioSnapshot
	Policy                                                         tradingcore.RiskPolicy
	At                                                             time.Time
}
type manualDecisionSource struct {
	snapshot tradingcore.DecisionContext
	policy   tradingcore.RiskPolicy
}

func (source manualDecisionSource) DecisionContext(context.Context) (tradingcore.DecisionContext, tradingcore.RiskPolicy, error) {
	return source.snapshot, source.policy, nil
}

func BuildApprovedFencedLiveRequest(ctx context.Context, side string, amount, price float64, idempotency string, config FencedLiveRequestConfig) (tradingcore.LiveOrderRequest, error) {
	normalized := strings.ToUpper(config.VenueSymbol)
	baseName := strings.ToUpper(config.BaseAsset)
	settlement := strings.ToUpper(config.SettlementCurrency)
	instrumentID, err := tradingcore.NewInstrumentID(strings.ToLower(baseName + "-" + settlement))
	if err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	base, _ := tradingcore.NewAssetID(baseName)
	quote, _ := tradingcore.NewAssetID(settlement)
	venue, _ := tradingcore.NewVenueID(config.VenueID)
	instrument, err := tradingcore.NewInstrument(instrumentID, base, quote, venue, normalized)
	if err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	quantityDecimal, err := tradingcore.ParseDecimal(strconv.FormatFloat(amount, 'f', -1, 64))
	if err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	quantity, err := tradingcore.NewQuantity(quantityDecimal)
	if err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	if strings.TrimSpace(idempotency) == "" {
		idempotency = "fenced-live-" + strings.ToLower(side) + "-" + strings.ToLower(normalized)
	}
	key, err := tradingcore.NewIdempotencyKey(idempotency)
	if err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	orderID, _ := tradingcore.NewOrderID("request-" + idempotency)
	account, _ := tradingcore.NewAccountID(config.AccountID)
	orderSide := tradingcore.Buy
	if strings.EqualFold(side, "sell") {
		orderSide = tradingcore.Sell
	}
	now := config.At.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	intent := tradingcore.OrderIntent{ID: orderID, IdempotencyKey: key, AccountID: account, Instrument: instrument, Side: orderSide, Type: tradingcore.MarketOrder, Quantity: quantity, SignalAt: now, DecisionAt: now, CreatedAt: now, ExecutionMode: tradingcore.ExecutionFullLive, QuantitySemantics: tradingcore.QuantityExact, Priority: 1, Reason: "manual_exchange_request", Versions: tradingcore.VersionContext{Strategy: "manual", Policy: config.Policy.Version}}
	if price > 0 {
		priceDecimal, parseErr := tradingcore.ParseDecimal(strconv.FormatFloat(price, 'f', -1, 64))
		if parseErr != nil {
			return tradingcore.LiveOrderRequest{}, parseErr
		}
		reference, priceErr := tradingcore.NewPrice(priceDecimal)
		if priceErr != nil {
			return tradingcore.LiveOrderRequest{}, priceErr
		}
		intent.ReferencePrice = tradingcore.SomePrice(reference)
	}
	validated, err := tradingcore.NewOrderIntent(intent, nil)
	if err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	batch, err := tradingcore.NewDecisionBatch([]tradingcore.OrderIntent{validated})
	if err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	strategyResult := tradingcore.NewStrategyResult(batch, nil)
	universe, _ := tradingcore.NewUniverseSnapshot(now, "manual-live-universe-v1", "manual", nil)
	snapshot, err := tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{MarketObservedAt: now, SignalAt: now, DecisionAt: now, Universe: universe, Portfolio: config.Portfolio, Settings: map[string]string{}, Versions: intent.Versions})
	if err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	runner := tradingcore.Orchestrator{Source: manualDecisionSource{snapshot, config.Policy}, Strategy: tradingcore.FixedStrategy{Result: strategyResult}, Risk: tradingcore.PortfolioRiskEngine{}, Broker: tradingcore.LiveBroker{}, Ledger: discardOutcomeLedger{}}
	result, err := runner.Run(ctx)
	if err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	approved := result.Risk.Approved()
	if len(approved.Intents()) != 1 {
		return tradingcore.LiveOrderRequest{}, fmt.Errorf("live request was not approved: %+v", result.Risk.Rejected())
	}
	requests, err := (tradingcore.LiveBroker{}).BuildRequests(approved)
	if err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	return requests[0], nil
}

func buildApplicationFencedLiveRequest(side, symbol string, amount, price float64, idempotency string) (tradingcore.LiveOrderRequest, error) {
	if database.DB == nil {
		return tradingcore.LiveOrderRequest{}, fmt.Errorf("database unavailable")
	}
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	settlement := strings.ToUpper(wallet.Currency)
	venueSymbol := strings.ToUpper(strings.ReplaceAll(symbol, "/", ""))
	if !strings.HasSuffix(venueSymbol, settlement) {
		venueSymbol += settlement
	}
	base := strings.TrimSuffix(venueSymbol, settlement)
	instrumentID, _ := tradingcore.NewInstrumentID(strings.ToLower(base + "-" + settlement))
	baseAsset, _ := tradingcore.NewAssetID(base)
	quote, _ := tradingcore.NewAssetID(settlement)
	venueName := getSettingString(GetAllSettings(), "exchange_venue_id", "binance")
	venue, _ := tradingcore.NewVenueID(venueName)
	instrument, _ := tradingcore.NewInstrument(instrumentID, baseAsset, quote, venue, venueSymbol)
	at := time.Now().UTC()
	if wallet.BalanceExact == nil {
		return tradingcore.LiveOrderRequest{}, ledgerpkg.ErrProjectionUnavailable
	}
	cash := mustCoreAmount(wallet.BalanceExact.String())
	positions := []tradingcore.Position{}
	var rows []database.Position
	if err := database.DB.Where("status = ?", "open").Find(&rows).Error; err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	for _, row := range rows {
		rowSymbol := PositionPairSymbol(row.Symbol, settlement)
		if rowSymbol != venueSymbol || row.AmountExact == nil {
			continue
		}
		mark := price
		if mark <= 0 {
			mark = positionPriceForExecution(row)
		}
		positions = append(positions, tradingcore.Position{ID: mustCorePositionID(row.ID), Instrument: instrument, Quantity: mustCoreQuantity(row.AmountExact.String()), AveragePrice: mustCorePrice(mark), MarkPrice: mustCorePrice(mark), OpenedAt: row.OpenedAt, RealizedPnL: mustCoreAmount("0")})
	}
	account, _ := tradingcore.NewAccountID(wallet.AccountID)
	portfolio, err := tradingcore.NewPortfolioSnapshot(at, account, tradingcore.ExecutionFullLive, map[tradingcore.AssetID]tradingcore.SignedAmount{quote: cash}, positions, nil, tradingcore.RiskState{})
	if err != nil {
		return tradingcore.LiveOrderRequest{}, err
	}
	limit := mustCoreAmount("999999999999")
	policy := tradingcore.RiskPolicy{Version: "live-fenced-risk-v1", MaxPositions: 100, MaxGrossExposure: limit, MaxPositionValue: limit, MaxTurnover: limit, CashReserve: mustCoreAmount("0"), MaxConcurrentOrders: 100, LotSize: mustCoreQuantity("0.00000001"), ExecutionCosts: tradingcore.ExecutionCostPolicy{Version: "live-request-cost-v1"}}
	return BuildApprovedFencedLiveRequest(context.Background(), side, amount, price, idempotency, FencedLiveRequestConfig{AccountID: wallet.AccountID, SettlementCurrency: settlement, VenueID: venueName, VenueSymbol: venueSymbol, BaseAsset: base, Portfolio: portfolio, Policy: policy, At: at})
}
func mustCorePositionID(id uint) tradingcore.PositionID {
	value, _ := tradingcore.NewPositionID(fmt.Sprint(id))
	return value
}
func mustCorePrice(value float64) tradingcore.Price {
	result, err := corePrice(value)
	if err != nil {
		panic(err)
	}
	return result
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
