package services

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/websocket"
)

// TrendingCoin represents a coin from the trending list
type TrendingCoin struct {
	Symbol    string  `json:"symbol"`
	Price     float64 `json:"price"`
	Change24h float64 `json:"change_24h"`
	Volume24h float64 `json:"volume_24h"`
}

// TrendingData holds the categorized trending results
type TrendingData struct {
	Source     string         `json:"source"`
	Timestamp  string         `json:"timestamp"`
	TopVolume  []TrendingCoin `json:"top_volume"`
	TopGainers []TrendingCoin `json:"top_gainers"`
	TopLosers  []TrendingCoin `json:"top_losers"`
}

// AnalyzedCoin represents the result of analyzing a single coin
type AnalyzedCoin struct {
	Symbol         string            `json:"symbol"`
	Price          float64           `json:"price"`
	Change24h      float64           `json:"change_24h"`
	Signal         string            `json:"signal"`
	Rating         float64           `json:"rating"`
	Timeframe      string            `json:"timeframe"`
	ProbUp         *float64          `json:"prob_up,omitempty"`
	ExpectedValue  *float64          `json:"expected_value,omitempty"`
	Indicators     []IndicatorResult `json:"indicators"`
	TradeExecuted  *bool             `json:"trade_executed,omitempty"`
	Error          string            `json:"error,omitempty"`
	Decision       string            `json:"decision,omitempty"`
	DecisionReason string            `json:"decision_reason,omitempty"`
}

// IndicatorResult stores a single indicator's analysis
type IndicatorResult struct {
	Name        string      `json:"name"`
	Value       interface{} `json:"value"`
	Signal      string      `json:"signal"`
	Rating      int         `json:"rating"`
	Description string      `json:"description"`
}

// TrendingAnalysisResult is the final return from AnalyzeTrendingCoins
type TrendingAnalysisResult struct {
	Timestamp    string         `json:"timestamp"`
	Trending     TrendingData   `json:"trending"`
	Analyzed     []AnalyzedCoin `json:"analyzed"`
	TradesOpened int            `json:"trades_opened"`
}

// GetAllSettings retrieves all settings from the database as a map
func GetAllSettings() map[string]string {
	var settings []database.Setting
	database.DB.Find(&settings)

	result := make(map[string]string)
	for _, s := range settings {
		result[s.Key] = s.Value
	}
	return result
}

// getSettingBool returns a boolean setting value with a default
func getSettingBool(settings map[string]string, key string, defaultVal bool) bool {
	val, ok := settings[key]
	if !ok {
		return defaultVal
	}
	return strings.ToLower(val) == "true"
}

// getSettingInt returns an integer setting value with a default
func getSettingInt(settings map[string]string, key string, defaultVal int) int {
	val, ok := settings[key]
	if !ok {
		return defaultVal
	}
	v, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	return v
}

// getSettingFloat returns a float setting value with a default
func getSettingFloat(settings map[string]string, key string, defaultVal float64) float64 {
	val, ok := settings[key]
	if !ok {
		return defaultVal
	}
	v, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return defaultVal
	}
	return v
}

func getSettingString(settings map[string]string, key string, defaultVal string) string {
	val, ok := settings[key]
	if !ok {
		return defaultVal
	}
	if val == "" {
		return defaultVal
	}
	return val
}

// logActivity creates an activity log entry in the database
func logActivity(logType, message string, details string) {
	log := database.ActivityLog{
		LogType:   logType,
		Message:   message,
		Timestamp: time.Now(),
	}
	if details != "" {
		log.Details = &details
	}
	database.DB.Create(&log)

	// Broadcast via WebSocket
	websocket.BroadcastActivityLogNew(log)
}

// FetchAllTickers fetches all 24hr tickers from Binance
func (s *ExchangeService) FetchAllTickers() ([]TickerPrice, error) {
	data, err := s.makeRequest("GET", "/api/v3/ticker/24hr", nil)
	if err != nil {
		return nil, err
	}

	var tickers []TickerPrice
	if err := json.Unmarshal(data, &tickers); err != nil {
		return nil, fmt.Errorf("failed to parse tickers response: %w", err)
	}

	return tickers, nil
}

// GetBinanceTrending fetches trending coins from Binance (by volume, gainers, losers)
func GetBinanceTrending(limitVolume, limitGainers, limitLosers int) (*TrendingData, error) {
	ex := GetExchange()
	tickers, err := ex.FetchAllTickers()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch tickers: %w", err)
	}

	var coins []TrendingCoin
	for _, t := range tickers {
		// Only include /USDT pairs, exclude BTC pairs
		if !strings.HasSuffix(t.Symbol, "USDT") {
			continue
		}
		if strings.Contains(t.Symbol, "BTC") {
			continue
		}

		price, _ := strconv.ParseFloat(t.LastPrice, 64)
		changePct, _ := strconv.ParseFloat(t.PriceChangePercent, 64)
		quoteVolume, _ := strconv.ParseFloat(t.QuoteVolume, 64)

		coins = append(coins, TrendingCoin{
			Symbol:    t.Symbol,
			Price:     price,
			Change24h: changePct,
			Volume24h: quoteVolume,
		})
	}

	// Sort by volume (descending)
	byVolume := make([]TrendingCoin, len(coins))
	copy(byVolume, coins)
	sort.Slice(byVolume, func(i, j int) bool {
		return byVolume[i].Volume24h > byVolume[j].Volume24h
	})
	if len(byVolume) > limitVolume {
		byVolume = byVolume[:limitVolume]
	}

	// Sort by change (descending) for gainers
	gainers := make([]TrendingCoin, len(coins))
	copy(gainers, coins)
	sort.Slice(gainers, func(i, j int) bool {
		return gainers[i].Change24h > gainers[j].Change24h
	})
	if len(gainers) > limitGainers {
		gainers = gainers[:limitGainers]
	}

	// Sort by change (ascending) for losers
	losers := make([]TrendingCoin, len(coins))
	copy(losers, coins)
	sort.Slice(losers, func(i, j int) bool {
		return losers[i].Change24h < losers[j].Change24h
	})
	if len(losers) > limitLosers {
		losers = losers[:limitLosers]
	}

	return &TrendingData{
		Source:     "Binance",
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		TopVolume:  byVolume,
		TopGainers: gainers,
		TopLosers:  losers,
	}, nil
}

// calculateIndicatorResults computes all indicators and returns them as IndicatorResult slices
// (matching the old Python format with name, value, signal, rating, description)
func calculateIndicatorResults(candles []Candle, config IndicatorConfig) []IndicatorResult {
	closes := make([]float64, len(candles))
	volumes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
		volumes[i] = c.Volume
	}

	var results []IndicatorResult

	// RSI
	rsi := CalculateRSI(closes, config.RSIPeriod)
	rsiSignal, rsiRating := convertRSIToRating(rsi.RSI, config.RSIOversold, config.RSIOverbought)
	results = append(results, IndicatorResult{
		Name:        "RSI",
		Value:       math.Round(rsi.RSI*100) / 100,
		Signal:      rsiSignal,
		Rating:      rsiRating,
		Description: fmt.Sprintf("Period: %d, Oversold: %.0f, Overbought: %.0f", config.RSIPeriod, config.RSIOversold, config.RSIOverbought),
	})

	// MACD
	macd := CalculateMACD(closes, config.MACDFastPeriod, config.MACDSlowPeriod, config.MACDSignalPeriod)
	macdSignal, macdRating := convertMACDToRating(macd)
	results = append(results, IndicatorResult{
		Name:        "MACD",
		Value:       fmt.Sprintf("MACD: %.2f, Signal: %.2f, Hist: %.2f", macd.MACD, macd.SignalLine, macd.Histogram),
		Signal:      macdSignal,
		Rating:      macdRating,
		Description: fmt.Sprintf("Fast: %d, Slow: %d, Signal: %d", config.MACDFastPeriod, config.MACDSlowPeriod, config.MACDSignalPeriod),
	})

	// Bollinger Bands
	bb := CalculateBollingerBands(closes, int(config.BBPeriod), config.BBStd)
	bbSignal, bbRating := convertBollingerToRating(bb, closes[len(closes)-1])
	results = append(results, IndicatorResult{
		Name:        "Bollinger",
		Value:       fmt.Sprintf("Upper: %.2f, Middle: %.2f, Lower: %.2f", bb.Upper, bb.Middle, bb.Lower),
		Signal:      bbSignal,
		Rating:      bbRating,
		Description: fmt.Sprintf("Period: %.0f, Std: %.1f", config.BBPeriod, config.BBStd),
	})

	// Momentum
	mom := CalculateMomentum(closes, config.MomentumPeriod)
	momSignal, momRating := convertMomentumToRating(mom)
	results = append(results, IndicatorResult{
		Name:        "Momentum",
		Value:       math.Round(mom.Momentum*100) / 100,
		Signal:      momSignal,
		Rating:      momRating,
		Description: fmt.Sprintf("Period: %d", config.MomentumPeriod),
	})

	// Volume
	vol := CalculateVolumeMA(volumes, config.VolumeMAPeriod)
	volSignal, volRating := convertVolumeToRating(vol)
	results = append(results, IndicatorResult{
		Name:        "Volume",
		Value:       fmt.Sprintf("Current: %.0f, MA%d: %.0f", volumes[len(volumes)-1], config.VolumeMAPeriod, vol.VolumeMA),
		Signal:      volSignal,
		Rating:      volRating,
		Description: fmt.Sprintf("MA Period: %d", config.VolumeMAPeriod),
	})

	return results
}

// Rating conversion functions matching the old Python logic

func convertRSIToRating(rsi, oversold, overbought float64) (string, int) {
	midBuy := (oversold + 50) / 2
	midSell := (overbought + 50) / 2

	if rsi >= overbought {
		return "sell", 5
	} else if rsi > midSell {
		return "sell", 4
	} else if rsi >= midBuy && rsi <= midSell {
		return "neutral", 3
	} else if rsi < oversold {
		return "buy", 5
	} else {
		return "buy", 4
	}
}

func convertMACDToRating(macd MACDResult) (string, int) {
	if macd.Signal == "bullish" {
		if macd.Histogram > 0 {
			return "buy", 4
		}
		return "buy", 4
	} else if macd.Signal == "bearish" {
		if macd.Histogram < 0 {
			return "sell", 4
		}
		return "sell", 4
	}
	return "neutral", 3
}

func convertBollingerToRating(bb BollingerBandsResult, currentPrice float64) (string, int) {
	if currentPrice > bb.Upper {
		return "sell", 4
	} else if currentPrice < bb.Lower {
		return "buy", 4
	} else if currentPrice > bb.Middle {
		return "buy", 3
	} else if currentPrice < bb.Middle {
		return "sell", 3
	}
	return "neutral", 3
}

func convertMomentumToRating(mom MomentumResult) (string, int) {
	if mom.Momentum > 0 {
		return "buy", 4
	} else if mom.Momentum < 0 {
		return "sell", 4
	}
	return "neutral", 3
}

func convertVolumeToRating(vol VolumeMAResult) (string, int) {
	if vol.Signal == "high" {
		return "buy", 4
	} else if vol.Signal == "low" {
		return "sell", 3
	}
	return "neutral", 3
}

func fetchCandles(symbol string, timeframe string, limit int) ([]Candle, error) {
	ex := GetExchange()
	cleanSymbol := strings.ReplaceAll(symbol, "/", "")

	ohlcv, err := ex.FetchOHLCV(cleanSymbol, timeframe, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OHLCV for %s: %w", cleanSymbol, err)
	}

	if len(ohlcv) == 0 {
		return nil, fmt.Errorf("no OHLCV data for %s", cleanSymbol)
	}

	candles := make([]Candle, len(ohlcv))
	for i, kline := range ohlcv {
		candles[i] = Candle{
			Close:  kline.Close,
			High:   kline.High,
			Low:    kline.Low,
			Volume: kline.Volume,
		}
	}

	return candles, nil
}

func analyzeSymbolFromCandles(symbol string, timeframe string, candles []Candle) *AnalyzedCoin {
	currentPrice := candles[len(candles)-1].Close

	config := GetIndicatorSettings()
	weights := GetIndicatorWeights()

	indicators := calculateIndicatorResults(candles, config)
	finalRating, finalSignal := CalculateFinalScore(indicators, weights)

	return &AnalyzedCoin{
		Symbol:     symbol,
		Price:      currentPrice,
		Timeframe:  timeframe,
		Signal:     finalSignal,
		Rating:     finalRating,
		Indicators: indicators,
	}
}

func computeRegimeGate(candles []Candle, emaFast int, emaSlow int) bool {
	if len(candles) < emaSlow {
		return false
	}

	closes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
	}

	fast := CalculateEMA(closes, emaFast)
	slow := CalculateEMA(closes, emaSlow)

	return fast > slow
}

func computeVolGate(candles []Candle, price float64, atrPeriod int, minRatio float64, maxRatio float64) bool {
	if len(candles) < atrPeriod+1 {
		return false
	}

	atr := CalculateATR(candles, atrPeriod)
	if atr <= 0 || price <= 0 {
		return false
	}

	ratio := atr / price
	return ratio >= minRatio && ratio <= maxRatio
}

func computePortfolioValue(wallet database.Wallet) float64 {
	total := wallet.Balance
	var positions []database.Position
	database.DB.Where("status = ?", "open").Find(&positions)
	for _, pos := range positions {
		price := pos.AvgPrice
		if pos.CurrentPrice != nil {
			price = *pos.CurrentPrice
		}
		total += pos.Amount * price
	}
	return total
}

func computePositionSize(atr float64, price float64, balance float64, portfolioValue float64, settings map[string]string) (float64, float64, float64, float64, *int, error) {
	if atr <= 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("invalid ATR for sizing")
	}
	if price <= 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("invalid price for sizing")
	}
	if balance <= 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("insufficient balance")
	}
	if portfolioValue <= 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("invalid portfolio value")
	}

	riskPerTrade := getSettingFloat(settings, "risk_per_trade", 0.5)
	stopMult := getSettingFloat(settings, "stop_mult", 1.5)
	tpMult := getSettingFloat(settings, "tp_mult", 3.0)
	maxPositionValue := getSettingFloat(settings, "max_position_value", 0)
	timeStopBars := getSettingInt(settings, "time_stop_bars", 0)

	if riskPerTrade <= 0 || riskPerTrade > 100 {
		return 0, 0, 0, 0, nil, fmt.Errorf("risk_per_trade must be between 0 and 100")
	}
	if stopMult <= 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("stop_mult must be greater than 0")
	}
	if tpMult <= 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("tp_mult must be greater than 0")
	}
	if maxPositionValue < 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("max_position_value must be >= 0")
	}
	if timeStopBars < 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("time_stop_bars must be >= 0")
	}

	volStop := atr * stopMult
	if volStop <= 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("invalid stop distance from ATR")
	}

	riskBudget := portfolioValue * (riskPerTrade / 100)
	if riskBudget <= 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("risk budget is too small")
	}

	positionSize := riskBudget / volStop
	if positionSize <= 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("position size is too small")
	}

	amountUsdt := positionSize * price
	if maxPositionValue > 0 && amountUsdt > maxPositionValue {
		amountUsdt = maxPositionValue
	}
	if amountUsdt > balance {
		amountUsdt = balance
	}
	if amountUsdt <= 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("calculated order value is too small")
	}

	cryptoAmount := amountUsdt / price
	if cryptoAmount <= 0 || math.IsNaN(cryptoAmount) || math.IsInf(cryptoAmount, 0) {
		return 0, 0, 0, 0, nil, fmt.Errorf("invalid crypto amount")
	}

	stopPrice := price - volStop
	takeProfitPrice := price + (atr * tpMult)
	if stopPrice <= 0 || takeProfitPrice <= 0 {
		return 0, 0, 0, 0, nil, fmt.Errorf("invalid stop or take-profit price")
	}

	var maxBarsHeld *int
	if timeStopBars > 0 {
		maxBars := timeStopBars
		maxBarsHeld = &maxBars
	}

	return amountUsdt, cryptoAmount, stopPrice, takeProfitPrice, maxBarsHeld, nil
}

func computeProbUp(features FeatureVector, beta0 float64, beta1 float64, beta2 float64, beta3 float64, beta4 float64, beta5 float64, beta6 float64) float64 {
	z := beta0 +
		beta1*features.RSI +
		beta2*features.MACDHistogram +
		beta3*features.BBPercentB +
		beta4*features.MomentumPercent +
		beta5*features.VolumeRatio +
		beta6*features.VolatilityRatio

	if z >= 0 {
		return 1 / (1 + math.Exp(-z))
	}
	expZ := math.Exp(z)
	return expZ / (1 + expZ)
}

func computeEV(pUp float64, avgGain float64, avgLoss float64) float64 {
	if avgGain < 0 {
		avgGain = 0
	}
	if avgLoss < 0 {
		avgLoss = 0
	}
	return pUp*avgGain - (1-pUp)*avgLoss
}

func computeProbGate(features FeatureVector, settings map[string]string) (float64, float64, bool) {
	if !features.Valid {
		return 0, 0, false
	}

	beta0 := getSettingFloat(settings, "prob_model_beta0", 0)
	beta1 := getSettingFloat(settings, "prob_model_beta1", 0)
	beta2 := getSettingFloat(settings, "prob_model_beta2", 0)
	beta3 := getSettingFloat(settings, "prob_model_beta3", 0)
	beta4 := getSettingFloat(settings, "prob_model_beta4", 0)
	beta5 := getSettingFloat(settings, "prob_model_beta5", 0)
	beta6 := getSettingFloat(settings, "prob_model_beta6", 0)

	pUp := computeProbUp(features, beta0, beta1, beta2, beta3, beta4, beta5, beta6)
	if math.IsNaN(pUp) || math.IsInf(pUp, 0) {
		return 0, 0, false
	}

	avgGain := getSettingFloat(settings, "prob_avg_gain", 0)
	avgLoss := getSettingFloat(settings, "prob_avg_loss", 0)
	ev := computeEV(pUp, avgGain, avgLoss)

	pMin := getSettingFloat(settings, "prob_p_min", 0)
	evMin := getSettingFloat(settings, "prob_ev_min", 0)

	ok := ev > evMin && pUp > pMin
	return pUp, ev, ok
}

// CalculateFinalScore computes weighted final score from indicators
// matching the old Python calculate_final_score function
func CalculateFinalScore(indicators []IndicatorResult, weights map[string]float64) (float64, string) {
	var totalWeight float64
	var weightedSum float64

	for _, ind := range indicators {
		name := strings.ToLower(ind.Name)
		weight, ok := weights[name]
		if !ok {
			weight = 1.0
		}

		var score float64
		switch ind.Signal {
		case "buy":
			score = float64(ind.Rating)
		case "sell":
			score = -float64(ind.Rating)
		default:
			score = 0
		}

		weightedSum += score * weight
		totalWeight += weight
	}

	if totalWeight == 0 {
		return 3.0, "NEUTRAL"
	}

	// Normalize: weighted average score ranges from -5 to +5.
	// Map that linearly to a 1–5 rating scale (0 → 3, +5 → 5, -5 → 1).
	avgScore := weightedSum / totalWeight
	finalRating := avgScore + 3
	finalRating = math.Max(1.0, math.Min(5.0, finalRating))

	var finalSignal string
	if finalRating >= 4.5 {
		finalSignal = "STRONG_BUY"
	} else if finalRating >= 4.0 {
		finalSignal = "BUY"
	} else if finalRating <= 1.5 {
		finalSignal = "STRONG_SELL"
	} else if finalRating <= 2.0 {
		finalSignal = "SELL"
	} else {
		finalSignal = "NEUTRAL"
	}

	return math.Round(finalRating*100) / 100, finalSignal
}

// analyzeSymbolForTrending performs full analysis for a symbol (used by trending)
func analyzeSymbolForTrending(symbol string, timeframe string) (*AnalyzedCoin, error) {
	candles, err := fetchCandles(symbol, timeframe, 200)
	if err != nil {
		return nil, err
	}

	return analyzeSymbolFromCandles(symbol, timeframe, candles), nil
}

// executeBuyFromTrending performs a buy based on trending analysis (matching Python execute_buy logic)
func executeBuyFromTrending(symbol string) (bool, error) {
	settings := GetAllSettings()

	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return false, fmt.Errorf("no wallet found")
	}

	// Clean symbol for position/order storage (e.g., "ETHUSDT" not "ETH/USDT")
	cleanSymbol := strings.ReplaceAll(symbol, "/", "")
	cleanSymbol = strings.ReplaceAll(cleanSymbol, "USDT", "")
	// For Binance API we need the full pair symbol
	pairSymbol := cleanSymbol + "USDT"

	// Get current price
	ex := GetExchange()
	ticker, err := ex.FetchTickerPrice(pairSymbol)
	if err != nil {
		return false, fmt.Errorf("failed to fetch price for %s: %w", pairSymbol, err)
	}
	currentPrice, _ := strconv.ParseFloat(ticker.LastPrice, 64)
	if currentPrice <= 0 {
		return false, fmt.Errorf("invalid price for %s", pairSymbol)
	}

	volSizingEnabled := getSettingBool(settings, "vol_sizing_enabled", false)
	amountUsdt := 0.0
	cryptoAmount := 0.0
	var stopPrice *float64
	var takeProfitPrice *float64
	var maxBarsHeld *int
	var atr float64

	if volSizingEnabled {
		candles, err := fetchCandles(pairSymbol, "15m", 200)
		if err != nil {
			return false, fmt.Errorf("failed to fetch candles for %s: %w", pairSymbol, err)
		}
		atr = CalculateATR(candles, 14)
		portfolioValue := computePortfolioValue(wallet)
		stopVal := 0.0
		takeProfitVal := 0.0
		var maxBars *int
		amountUsdt, cryptoAmount, stopVal, takeProfitVal, maxBars, err = computePositionSize(atr, currentPrice, wallet.Balance, portfolioValue, settings)
		if err != nil {
			return false, err
		}
		stopPrice = &stopVal
		takeProfitPrice = &takeProfitVal
		maxBarsHeld = maxBars
	} else {
		entryPercent := getSettingFloat(settings, "entry_percent", 5.0)
		amountUsdt = wallet.Balance * (entryPercent / 100)

		if amountUsdt > wallet.Balance {
			return false, fmt.Errorf("insufficient balance")
		}

		if amountUsdt <= 0 {
			return false, fmt.Errorf("calculated amount is 0")
		}

		cryptoAmount = amountUsdt / currentPrice
	}

	// Check for existing position - if exists, update (DCA/average), else create new
	var existingPosition database.Position
	hasExisting := database.DB.Where("symbol = ? AND status = ?", cleanSymbol, "open").First(&existingPosition).Error == nil

	if hasExisting {
		// Average into position
		oldAmount := existingPosition.Amount
		oldAvg := existingPosition.AvgPrice
		newAvg := ((oldAmount * oldAvg) + (cryptoAmount * currentPrice)) / (oldAmount + cryptoAmount)
		existingPosition.Amount += cryptoAmount
		existingPosition.AvgPrice = newAvg
		if volSizingEnabled {
			stopMult := getSettingFloat(settings, "stop_mult", 1.5)
			tpMult := getSettingFloat(settings, "tp_mult", 3.0)
			volStop := atr * stopMult
			volTp := atr * tpMult
			if volStop > 0 && volTp > 0 {
				newStop := newAvg - volStop
				newTakeProfit := newAvg + volTp
				if newStop > 0 && newTakeProfit > 0 {
					existingPosition.StopPrice = &newStop
					existingPosition.TakeProfitPrice = &newTakeProfit
				}
			}
			existingPosition.MaxBarsHeld = maxBarsHeld
		}
		database.DB.Save(&existingPosition)
	} else {
		position := database.Position{
			Symbol:          cleanSymbol,
			Amount:          cryptoAmount,
			AvgPrice:        currentPrice,
			EntryPrice:      &currentPrice,
			CurrentPrice:    &currentPrice,
			StopPrice:       stopPrice,
			TakeProfitPrice: takeProfitPrice,
			MaxBarsHeld:     maxBarsHeld,
			Status:          "open",
			OpenedAt:        time.Now(),
		}
		database.DB.Create(&position)
	}

	// Create order record
	order := database.Order{
		OrderType:    "buy",
		Symbol:       cleanSymbol,
		AmountCrypto: cryptoAmount,
		AmountUsdt:   amountUsdt,
		Price:        currentPrice,
		ExecutedAt:   time.Now(),
	}
	database.DB.Create(&order)

	// Update wallet balance
	wallet.Balance -= amountUsdt
	database.DB.Save(&wallet)

	return true, nil
}

// AnalyzeTrendingCoins is the main function that mirrors the Python analyze_trending_coins
func AnalyzeTrendingCoins() (*TrendingAnalysisResult, error) {
	settings := GetAllSettings()

	autoTradeEnabled := getSettingBool(settings, "auto_trade_enabled", false)
	maxPositions := getSettingInt(settings, "max_positions", 5)
	topNToAnalyze := getSettingInt(settings, "trending_coins_to_analyze", 5)
	buyOnlyStrong := getSettingBool(settings, "buy_only_strong", true)
	minConfidenceToBuy := getSettingFloat(settings, "min_confidence_to_buy", 4.0)
	probModelEnabled := getSettingBool(settings, "prob_model_enabled", false)
	regimeGateEnabled := getSettingBool(settings, "regime_gate_enabled", true)
	regimeTimeframe := getSettingString(settings, "regime_timeframe", "1h")
	regimeEmaFast := getSettingInt(settings, "regime_ema_fast", 50)
	regimeEmaSlow := getSettingInt(settings, "regime_ema_slow", 200)
	volAtrPeriod := getSettingInt(settings, "vol_atr_period", 14)
	volRatioMin := getSettingFloat(settings, "vol_ratio_min", 0.002)
	volRatioMax := getSettingFloat(settings, "vol_ratio_max", 0.02)

	logActivity("system", "Starting trending coins analysis",
		fmt.Sprintf("Auto-trade: %v, buy_only_strong: %v, min_confidence: %.1f", autoTradeEnabled, buyOnlyStrong, minConfidenceToBuy))

	// Get trending data from Binance
	trendingData, err := GetBinanceTrending(20, 20, 20)
	if err != nil {
		logActivity("error", "Failed to fetch trending data", err.Error())
		return nil, fmt.Errorf("failed to fetch trending data: %w", err)
	}

	// Combine all trending categories (TopVolume + TopGainers + TopLosers) and deduplicate
	coinsMap := make(map[string]TrendingCoin)
	for _, coin := range trendingData.TopVolume {
		coinsMap[coin.Symbol] = coin
	}
	for _, coin := range trendingData.TopGainers {
		coinsMap[coin.Symbol] = coin
	}
	for _, coin := range trendingData.TopLosers {
		coinsMap[coin.Symbol] = coin
	}

	// Convert map to slice
	var allCoins []TrendingCoin
	for _, coin := range coinsMap {
		allCoins = append(allCoins, coin)
	}

	// Limit to topNToAnalyze (prioritize by volume)
	sort.Slice(allCoins, func(i, j int) bool {
		return allCoins[i].Volume24h > allCoins[j].Volume24h
	})
	coinsToAnalyze := allCoins
	if len(coinsToAnalyze) > topNToAnalyze {
		coinsToAnalyze = coinsToAnalyze[:topNToAnalyze]
	}

	var results []AnalyzedCoin
	tradesOpened := 0

	// Count current open positions
	var currentOpenCount int64
	database.DB.Model(&database.Position{}).Where("status = ?", "open").Count(&currentOpenCount)

	for _, coin := range coinsToAnalyze {
		// Convert symbol format for analysis (Binance uses ETHUSDT, keep that)
		symbol := coin.Symbol

		candles15m, err := fetchCandles(symbol, "15m", 200)
		if err != nil {
			logActivity("error", fmt.Sprintf("Error analyzing %s", symbol), err.Error())
			results = append(results, AnalyzedCoin{
				Symbol: symbol,
				Error:  err.Error(),
			})
			continue
		}

		analysis := analyzeSymbolFromCandles(symbol, "15m", candles15m)
		analysis.Change24h = coin.Change24h

		var probUp *float64
		var expectedValue *float64
		probOk := true
		if probModelEnabled {
			features := CalculateFeatureVector(candles15m, GetIndicatorSettings())
			pUp, ev, ok := computeProbGate(features, settings)
			probOk = ok
			if features.Valid {
				probUp = &pUp
				expectedValue = &ev
				analysis.ProbUp = probUp
				analysis.ExpectedValue = expectedValue
			}
		}

		logActivity("analysis", fmt.Sprintf("Analyzed %s", symbol),
			fmt.Sprintf("Signal: %s, Rating: %.2f", analysis.Signal, analysis.Rating))

		// Determine buy signal qualification (matching Python logic)
		signalQualifies := false
		if buyOnlyStrong {
			signalQualifies = analysis.Signal == "STRONG_BUY"
		} else {
			signalQualifies = analysis.Signal == "BUY" || analysis.Signal == "STRONG_BUY"
		}
		confidenceQualifies := analysis.Rating >= minConfidenceToBuy
		if probModelEnabled {
			confidenceQualifies = probOk
		}

		regimeOk := true
		volOk := true
		if regimeGateEnabled {
			candlesHigher, regimeErr := fetchCandles(symbol, regimeTimeframe, 200)
			if regimeErr != nil {
				regimeOk = false
			} else {
				regimeOk = computeRegimeGate(candlesHigher, regimeEmaFast, regimeEmaSlow)
			}
			volOk = computeVolGate(candles15m, analysis.Price, volAtrPeriod, volRatioMin, volRatioMax)
		}

		decision := "skip"
		decisionReason := ""

		if !autoTradeEnabled {
			decisionReason = "auto_trade_disabled"
		} else if !signalQualifies {
			decisionReason = "signal_not_qualified"
		} else if !confidenceQualifies {
			decisionReason = "confidence_not_qualified"
		} else if !regimeOk {
			decisionReason = "regime_gate_failed"
		} else if !volOk {
			decisionReason = "vol_gate_failed"
		} else if (int(currentOpenCount) + tradesOpened) >= maxPositions {
			decisionReason = "max_positions_reached"
		} else {
			decision = "buy_candidate"
			decisionReason = "passed_gates"
		}

		if autoTradeEnabled && signalQualifies && confidenceQualifies && regimeOk && volOk &&
			(int(currentOpenCount)+tradesOpened) < maxPositions {

			// Check if position already exists
			cleanSymbol := strings.ReplaceAll(symbol, "USDT", "")
			var existingPosition database.Position
			hasExisting := database.DB.Where("symbol = ?", cleanSymbol).First(&existingPosition).Error == nil

			if !hasExisting {
				success, buyErr := executeBuyFromTrending(symbol)
				if success {
					tradesOpened++
					trueVal := true
					analysis.TradeExecuted = &trueVal
					decision = "buy"
					decisionReason = "order_executed"
					logActivity("trade", fmt.Sprintf("Bought %s", symbol),
						fmt.Sprintf("At $%.2f", analysis.Price))

					// Broadcast wallet and positions updates after successful trade
					broadcastTradeUpdates()
				} else {
					falseVal := false
					analysis.TradeExecuted = &falseVal
					errMsg := "Unknown error"
					if buyErr != nil {
						errMsg = buyErr.Error()
					}
					decision = "buy_failed"
					decisionReason = errMsg
					logActivity("trade", fmt.Sprintf("Failed to buy %s", symbol), errMsg)
				}
			} else {
				decision = "skip"
				decisionReason = "position_exists"
				logActivity("trade", fmt.Sprintf("Skipped %s - position already exists", symbol), "")
			}
		}

		indicatorsJSON, _ := json.Marshal(analysis.Indicators)
		history := database.TrendAnalysisHistory{
			Symbol:              symbol,
			Timeframe:           "15m",
			CurrentPrice:        &analysis.Price,
			Change24h:           &coin.Change24h,
			FinalSignal:         &analysis.Signal,
			FinalRating:         &analysis.Rating,
			ProbUp:              probUp,
			ExpectedValue:       expectedValue,
			AutoTrade:           &autoTradeEnabled,
			SignalQualifies:     &signalQualifies,
			ConfidenceQualifies: &confidenceQualifies,
			RegimeOk:            &regimeOk,
			VolOk:               &volOk,
			ProbOk:              &probOk,
			Decision:            &decision,
			DecisionReason:      &decisionReason,
			IndicatorsJSON:      string(indicatorsJSON),
			AnalyzedAt:          time.Now(),
		}
		database.DB.Create(&history)

		results = append(results, *analysis)
	}

	logActivity("system", "Trending analysis complete",
		fmt.Sprintf("Analyzed %d coins, opened %d trades", len(results), tradesOpened))

	// Broadcast trending updates and analysis completion
	websocket.BroadcastTrendingUpdate(results)
	websocket.BroadcastAnalysisComplete(
		time.Now().UTC().Format(time.RFC3339),
		len(results),
		tradesOpened,
	)

	return &TrendingAnalysisResult{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Trending:     *trendingData,
		Analyzed:     results,
		TradesOpened: tradesOpened,
	}, nil
}

// GetRecentAnalyzedCoins returns the most recently analyzed coins from the database
func GetRecentAnalyzedCoins() ([]AnalyzedCoin, error) {
	var history []database.TrendAnalysisHistory
	if err := database.DB.
		Order("analyzed_at DESC").
		Limit(200).
		Find(&history).Error; err != nil {
		return nil, err
	}

	latestBySymbol := make(map[string]AnalyzedCoin)
	var orderedSymbols []string

	for _, row := range history {
		if _, exists := latestBySymbol[row.Symbol]; exists {
			continue
		}

		var indicators []IndicatorResult
		if row.IndicatorsJSON != "" {
			json.Unmarshal([]byte(row.IndicatorsJSON), &indicators)
		}

		price := 0.0
		if row.CurrentPrice != nil {
			price = *row.CurrentPrice
		}
		change := 0.0
		if row.Change24h != nil {
			change = *row.Change24h
		}
		signal := ""
		if row.FinalSignal != nil {
			signal = *row.FinalSignal
		}
		rating := 0.0
		if row.FinalRating != nil {
			rating = *row.FinalRating
		}
		var probUp *float64
		if row.ProbUp != nil {
			val := *row.ProbUp
			probUp = &val
		}
		var expectedValue *float64
		if row.ExpectedValue != nil {
			val := *row.ExpectedValue
			expectedValue = &val
		}

		latestBySymbol[row.Symbol] = AnalyzedCoin{
			Symbol:        row.Symbol,
			Price:         price,
			Change24h:     change,
			Signal:        signal,
			Rating:        rating,
			Timeframe:     row.Timeframe,
			ProbUp:        probUp,
			ExpectedValue: expectedValue,
			Indicators:    indicators,
		}
		orderedSymbols = append(orderedSymbols, row.Symbol)

		if len(latestBySymbol) >= 20 {
			break
		}
	}

	var coins []AnalyzedCoin
	for _, sym := range orderedSymbols {
		coins = append(coins, latestBySymbol[sym])
	}

	return coins, nil
}

// GetLatestAnalysisForSymbol returns the most recent analysis for a specific symbol
func GetLatestAnalysisForSymbol(symbol string) (*AnalyzedCoin, error) {
	var history database.TrendAnalysisHistory
	if err := database.DB.
		Where("symbol = ?", symbol).
		Order("analyzed_at DESC").
		First(&history).Error; err != nil {
		return nil, err
	}

	var indicators []IndicatorResult
	if history.IndicatorsJSON != "" {
		json.Unmarshal([]byte(history.IndicatorsJSON), &indicators)
	}

	price := 0.0
	if history.CurrentPrice != nil {
		price = *history.CurrentPrice
	}
	change := 0.0
	if history.Change24h != nil {
		change = *history.Change24h
	}
	signal := ""
	if history.FinalSignal != nil {
		signal = *history.FinalSignal
	}
	rating := 0.0
	if history.FinalRating != nil {
		rating = *history.FinalRating
	}
	var probUp *float64
	if history.ProbUp != nil {
		val := *history.ProbUp
		probUp = &val
	}
	var expectedValue *float64
	if history.ExpectedValue != nil {
		val := *history.ExpectedValue
		expectedValue = &val
	}

	decision := ""
	if history.Decision != nil {
		decision = *history.Decision
	}
	decisionReason := ""
	if history.DecisionReason != nil {
		decisionReason = *history.DecisionReason
	}

	return &AnalyzedCoin{
		Symbol:         history.Symbol,
		Price:          price,
		Change24h:      change,
		Signal:         signal,
		Rating:         rating,
		Timeframe:      history.Timeframe,
		ProbUp:         probUp,
		ExpectedValue:  expectedValue,
		Indicators:     indicators,
		Decision:       decision,
		DecisionReason: decisionReason,
	}, nil
}

// broadcastTradeUpdates broadcasts wallet, positions, and orders updates via WebSocket
// after a successful trade execution
func broadcastTradeUpdates() {
	// Get wallet for balance info
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return
	}

	// Calculate total value including positions
	var totalValue float64 = wallet.Balance
	var positions []database.Position
	database.DB.Where("status = ?", "open").Find(&positions)
	for _, pos := range positions {
		if pos.CurrentPrice != nil {
			totalValue += pos.Amount * (*pos.CurrentPrice)
		}
	}

	// Broadcast wallet update
	websocket.BroadcastWalletUpdate(wallet.Balance, wallet.Currency, totalValue)

	// Broadcast all positions
	var allPositions []database.Position
	database.DB.Order("opened_at DESC").Find(&allPositions)
	websocket.BroadcastPositionsUpdate(allPositions)

	// Broadcast recent orders
	var orders []database.Order
	database.DB.Order("executed_at DESC").Limit(10).Find(&orders)
	websocket.BroadcastOrdersUpdate(orders)
}
