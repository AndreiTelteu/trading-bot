package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/websocket"

	"gorm.io/gorm"
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
	Symbol            string             `json:"symbol"`
	Price             float64            `json:"price"`
	Change24h         float64            `json:"change_24h"`
	RankScore         float64            `json:"rank_score,omitempty"`
	RankComponents    map[string]float64 `json:"rank_components,omitempty"`
	Signal            string             `json:"signal"`
	Rating            float64            `json:"rating"`
	Timeframe         string             `json:"timeframe"`
	CreatedAt         time.Time          `json:"created_at"`
	ModelVersion      string             `json:"model_version,omitempty"`
	PolicyVersion     string             `json:"policy_version,omitempty"`
	UniverseMode      string             `json:"universe_mode,omitempty"`
	RolloutState      string             `json:"rollout_state,omitempty"`
	ExperimentID      string             `json:"experiment_id,omitempty"`
	ModelScore        *float64           `json:"model_score,omitempty"`
	ModelRank         *int               `json:"model_rank,omitempty"`
	PolicySelected    *bool              `json:"policy_selected,omitempty"`
	ProbUp            *float64           `json:"prob_up,omitempty"`
	ExpectedValue     *float64           `json:"expected_value,omitempty"`
	Indicators        []IndicatorResult  `json:"indicators"`
	TradeExecuted     *bool              `json:"trade_executed,omitempty"`
	Error             string             `json:"error,omitempty"`
	Decision          string             `json:"decision,omitempty"`
	DecisionReason    string             `json:"decision_reason,omitempty"`
	FeatureSnapshotID *uint              `json:"-"`
	PredictionLogID   *uint              `json:"-"`
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
	Timestamp    string                   `json:"timestamp"`
	Trending     TrendingData             `json:"trending"`
	Universe     *UniverseSelectionResult `json:"universe,omitempty"`
	Analyzed     []AnalyzedCoin           `json:"analyzed"`
	TradesOpened int                      `json:"trades_opened"`
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
		CreatedAt:  time.Now().UTC(),
		Signal:     finalSignal,
		Rating:     finalRating,
		Indicators: indicators,
	}
}

func AnalyzeCandles(candles []Candle) (float64, string) {
	if len(candles) == 0 {
		return 0, "NEUTRAL"
	}
	config := GetIndicatorSettings()
	weights := GetIndicatorWeights()
	return AnalyzeCandlesWithConfig(candles, config, weights)
}

func AnalyzeCandlesWithConfig(candles []Candle, config IndicatorConfig, weights map[string]float64) (float64, string) {
	if len(candles) == 0 {
		return 0, "NEUTRAL"
	}
	indicators := calculateIndicatorResults(candles, config)
	finalRating, finalSignal := CalculateFinalScore(indicators, weights)
	return finalRating, finalSignal
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

func getAtrValue(candles []Candle, period int, annualizeEnabled bool, timeframeMinutes int, annualizationDays int) float64 {
	if annualizeEnabled {
		return CalculateAnnualizedATR(candles, period, timeframeMinutes, annualizationDays)
	}
	return CalculateATR(candles, period)
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

func AnalyzeShortlist(selection *UniverseSelectionResult, settings map[string]string) ([]AnalyzedCoin, error) {
	if selection == nil {
		return nil, nil
	}

	results := make([]AnalyzedCoin, 0, len(selection.Shortlist))
	modelPolicy := GetModelSelectionPolicy(settings)
	governance, governanceErr := ResolveGovernanceContext(settings, getSettingString(settings, "universe_mode", "dynamic"))
	if governanceErr != nil {
		logActivity("error", "Failed to resolve governance context", governanceErr.Error())
	}
	modelPolicy.PolicyVersion = governance.PolicyVersions.ModelSelectionPolicyVersion
	modelPolicy.ExperimentID = governance.ExperimentID
	modelArtifact, modelErr := LoadConfiguredModel(settings)
	if modelErr != nil {
		logActivity("error", "Failed to load learned model artifact", modelErr.Error())
		modelArtifact = nil
	}

	btcCandles, err := fetchCandles("BTCUSDT", "15m", 200)
	if err != nil {
		btcCandles = nil
	}

	portfolioExposure := 0.0
	openPositionCount := 0
	openSymbols := make(map[string]struct{})
	if database.DB != nil {
		var wallet database.Wallet
		if err := database.DB.First(&wallet).Error; err == nil {
			portfolioValue := computePortfolioValue(wallet)
			if portfolioValue > 0 {
				portfolioExposure = math.Max(0, (portfolioValue-wallet.Balance)/portfolioValue)
			}
		}
		var positions []database.Position
		database.DB.Where("status = ?", "open").Find(&positions)
		openPositionCount = len(positions)
		for _, position := range positions {
			openSymbols[strings.ToUpper(position.Symbol)+"USDT"] = struct{}{}
		}
	}

	rankingInputs := make([]ModelRankedCandidate, 0, len(selection.Shortlist))
	observations := make([]modelObservation, 0, len(selection.Shortlist))
	observationIndexBySymbol := make(map[string]int, len(selection.Shortlist))
	resultIndexBySymbol := make(map[string]int, len(selection.Shortlist))
	activeUniverse := selection.ActiveUniverse
	if len(activeUniverse) == 0 {
		activeUniverse = selection.Shortlist
	}

	for _, candidate := range selection.Shortlist {
		candles15m, err := fetchCandles(candidate.Symbol, "15m", 200)
		if err != nil {
			logActivity("error", fmt.Sprintf("Error analyzing %s", candidate.Symbol), err.Error())
			results = append(results, AnalyzedCoin{
				Symbol:         candidate.Symbol,
				Change24h:      candidate.Change24h,
				RankScore:      candidate.RankScore,
				RankComponents: candidate.RankComponents,
				Error:          err.Error(),
			})
			continue
		}

		analysis := analyzeSymbolFromCandles(candidate.Symbol, "15m", candles15m)
		analysis.CreatedAt = time.Now().UTC()
		analysis.Change24h = candidate.Change24h
		analysis.RankScore = candidate.RankScore
		analysis.RankComponents = candidate.RankComponents
		analysis.PolicyVersion = governance.PolicyVersions.CompositeVersion
		analysis.UniverseMode = governance.UniverseMode
		analysis.RolloutState = governance.RolloutState
		analysis.ExperimentID = governance.ExperimentID

		if modelArtifact != nil {
			_, alreadyOpen := openSymbols[strings.ToUpper(candidate.Symbol)]
			featureRow := BuildModelFeatureRow(ModelFeatureInput{
				Timestamp:         analysis.CreatedAt,
				Symbol:            candidate.Symbol,
				Candles15m:        candles15m,
				Candidate:         candidate,
				ActiveUniverse:    activeUniverse,
				RegimeState:       selection.RegimeState,
				BreadthRatio:      selection.BreadthRatio,
				BTCCandles15m:     btcCandles,
				OpenPositionCount: openPositionCount,
				ExposureRatio:     portfolioExposure,
				AlreadyOpen:       alreadyOpen,
			})

			if featureRow.Valid {
				prediction, predictionErr := modelArtifact.PredictRow(featureRow)
				if predictionErr == nil {
					analysis.ModelVersion = prediction.ModelVersion
					analysis.ProbUp = float64Ptr(prediction.Probability)
					analysis.ExpectedValue = float64Ptr(prediction.ExpectedValue)
					analysis.ModelScore = float64Ptr(prediction.RawScore)
					if snapshotID, snapshotErr := persistFeatureSnapshot(featureRow, candidate, prediction.ModelVersion, selection, governance); snapshotErr == nil {
						analysis.FeatureSnapshotID = snapshotID
					}
					rankingInputs = append(rankingInputs, ModelRankedCandidate{
						Symbol:        candidate.Symbol,
						Probability:   prediction.Probability,
						ExpectedValue: prediction.ExpectedValue,
						RawScore:      prediction.RawScore,
					})
					observations = append(observations, modelObservation{
						Symbol:            candidate.Symbol,
						FeatureSnapshotID: analysis.FeatureSnapshotID,
						Prediction:        prediction,
					})
					observationIndexBySymbol[candidate.Symbol] = len(observations) - 1
				} else {
					analysis.DecisionReason = predictionErr.Error()
				}
			} else if len(featureRow.QualityFlags) > 0 {
				analysis.DecisionReason = strings.Join(featureRow.QualityFlags, ",")
			}
		}

		logActivity("analysis", fmt.Sprintf("Analyzed %s", candidate.Symbol),
			fmt.Sprintf("Signal: %s, Rating: %.2f, Rank: %.2f", analysis.Signal, analysis.Rating, analysis.RankScore))

		results = append(results, *analysis)
		resultIndexBySymbol[candidate.Symbol] = len(results) - 1
	}

	if len(rankingInputs) > 0 {
		ranked := RankModelPredictions(rankingInputs, modelPolicy)
		for _, rankedCandidate := range ranked {
			resultIndex, ok := resultIndexBySymbol[rankedCandidate.Symbol]
			if !ok {
				continue
			}
			analysis := &results[resultIndex]
			analysis.ModelRank = intValuePtr(rankedCandidate.Rank)
			analysis.PolicySelected = boolValuePtr(rankedCandidate.Selected)
			analysis.DecisionReason = rankedCandidate.SelectionReason

			if observationIndex, ok := observationIndexBySymbol[rankedCandidate.Symbol]; ok {
				decisionResult := "shadow_only"
				if modelPolicy.UseForLiveEntries() {
					if rankedCandidate.Selected {
						decisionResult = "selected"
					} else {
						decisionResult = "rejected"
					}
				}
				observations[observationIndex].Rank = rankedCandidate.Rank
				observations[observationIndex].Selected = rankedCandidate.Selected
				observations[observationIndex].DecisionResult = decisionResult
			}
		}

		sort.SliceStable(results, func(i, j int) bool {
			leftRank := modelRankValue(results[i].ModelRank)
			rightRank := modelRankValue(results[j].ModelRank)
			if leftRank == rightRank {
				if results[i].RankScore == results[j].RankScore {
					return results[i].Symbol < results[j].Symbol
				}
				return results[i].RankScore > results[j].RankScore
			}
			return leftRank < rightRank
		})

		if idsBySymbol, err := persistPredictionLogs(observations, selection, governance); err != nil {
			logActivity("error", "Failed to persist model prediction logs", err.Error())
		} else {
			for symbol, id := range idsBySymbol {
				if resultIndex, ok := resultIndexBySymbol[symbol]; ok {
					logID := id
					results[resultIndex].PredictionLogID = &logID
				}
			}
		}
	}

	return results, nil
}

func ExecuteShortlistTrades(analyses []AnalyzedCoin, universe *UniverseSelectionResult, settings map[string]string) ([]AnalyzedCoin, int) {
	autoTradeEnabled := getSettingBool(settings, "auto_trade_enabled", false)
	maxPositions := getSettingInt(settings, "max_positions", 5)
	buyOnlyStrong := getSettingBool(settings, "buy_only_strong", true)
	minConfidenceToBuy := getSettingFloat(settings, "min_confidence_to_buy", 4.0)
	modelPolicy := GetModelSelectionPolicy(settings)
	useModelEntries := modelPolicy.UseForLiveEntries() && hasModelRankings(analyses)
	regimeGateEnabled := getSettingBool(settings, "regime_gate_enabled", true)
	regimeTimeframe := getSettingString(settings, "regime_timeframe", "1h")
	regimeEmaFast := getSettingInt(settings, "regime_ema_fast", 50)
	regimeEmaSlow := getSettingInt(settings, "regime_ema_slow", 200)
	volAtrPeriod := getSettingInt(settings, "vol_atr_period", 14)
	volRatioMin := getSettingFloat(settings, "vol_ratio_min", 0.002)
	volRatioMax := getSettingFloat(settings, "vol_ratio_max", 0.02)

	var currentOpenCount int64
	database.DB.Model(&database.Position{}).Where("status = ?", "open").Count(&currentOpenCount)
	tradesOpened := 0

	for i := range analyses {
		analysis := &analyses[i]
		legacySignalQualifies := false
		if buyOnlyStrong {
			legacySignalQualifies = analysis.Signal == "STRONG_BUY"
		} else {
			legacySignalQualifies = analysis.Signal == "BUY" || analysis.Signal == "STRONG_BUY"
		}

		probOk := analysis.ProbUp != nil && analysis.ExpectedValue != nil && *analysis.ProbUp >= modelPolicy.MinProbability && *analysis.ExpectedValue >= modelPolicy.MinExpectedValue
		modelSelected := analysis.PolicySelected != nil && *analysis.PolicySelected
		signalQualifies := legacySignalQualifies
		confidenceQualifies := analysis.Rating >= minConfidenceToBuy
		if useModelEntries {
			signalQualifies = modelSelected
			confidenceQualifies = probOk
		}

		regimeOk := universe == nil || universe.RegimeState != UniverseRegimeRiskOff
		volOk := true
		if regimeGateEnabled && analysis.Error == "" {
			candles15m, candleErr := fetchCandles(analysis.Symbol, "15m", 200)
			if candleErr != nil {
				regimeOk = false
				volOk = false
			} else {
				candlesHigher, regimeErr := fetchCandles(analysis.Symbol, regimeTimeframe, 200)
				if regimeErr != nil {
					regimeOk = false
				} else {
					regimeOk = regimeOk && computeRegimeGate(candlesHigher, regimeEmaFast, regimeEmaSlow)
				}
				volOk = computeVolGate(candles15m, analysis.Price, volAtrPeriod, volRatioMin, volRatioMax)
			}
		}

		decision := "skip"
		decisionReason := ""
		if analysis.Error != "" {
			decisionReason = "analysis_error"
		} else if !autoTradeEnabled {
			decisionReason = "auto_trade_disabled"
		} else if universe != nil && universe.RegimeState == UniverseRegimeRiskOff {
			decisionReason = "universe_regime_risk_off"
		} else if useModelEntries && !modelSelected {
			decisionReason = defaultString(analysis.DecisionReason, "model_policy_not_selected")
		} else if !signalQualifies {
			decisionReason = "signal_not_qualified"
		} else if !confidenceQualifies {
			if useModelEntries {
				decisionReason = "model_policy_floor_failed"
			} else {
				decisionReason = "confidence_not_qualified"
			}
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

		if autoTradeEnabled && analysis.Error == "" && signalQualifies && confidenceQualifies && regimeOk && volOk &&
			(universe == nil || universe.RegimeState != UniverseRegimeRiskOff) &&
			(int(currentOpenCount)+tradesOpened) < maxPositions {

			cleanSymbol := strings.ReplaceAll(analysis.Symbol, "USDT", "")
			var existingPosition database.Position
			hasExisting := database.DB.Where("symbol = ? AND status = ?", cleanSymbol, "open").First(&existingPosition).Error == nil

			if !hasExisting {
				success, buyErr := executeBuyFromTrendingWithContext(analysis.Symbol, buildTradeDecisionContext(*analysis))
				if success {
					tradesOpened++
					trueVal := true
					analysis.TradeExecuted = &trueVal
					decision = "buy"
					decisionReason = "order_executed"
					logActivity("trade", fmt.Sprintf("Bought %s", analysis.Symbol), fmt.Sprintf("At $%.2f", analysis.Price))
					broadcastTradeUpdates()
				} else {
					falseVal := false
					analysis.TradeExecuted = &falseVal
					if buyErr != nil {
						decisionReason = buyErr.Error()
					} else {
						decisionReason = "buy_failed"
					}
					decision = "buy_failed"
					logActivity("trade", fmt.Sprintf("Failed to buy %s", analysis.Symbol), decisionReason)
				}
			} else {
				decision = "skip"
				decisionReason = "position_exists"
				logActivity("trade", fmt.Sprintf("Skipped %s - position already exists", analysis.Symbol), "")
			}
		}

		analysis.Decision = decision
		analysis.DecisionReason = decisionReason

		indicatorsJSON, _ := json.Marshal(analysis.Indicators)
		decisionContext := buildTradeDecisionContext(*analysis)
		history := database.TrendAnalysisHistory{
			Symbol:              analysis.Symbol,
			Timeframe:           "15m",
			ModelVersion:        analysis.ModelVersion,
			PolicyVersion:       analysis.PolicyVersion,
			UniverseMode:        analysis.UniverseMode,
			RolloutState:        analysis.RolloutState,
			ExperimentID:        stringPtr(analysis.ExperimentID),
			PredictionLogID:     analysis.PredictionLogID,
			CurrentPrice:        &analysis.Price,
			Change24h:           &analysis.Change24h,
			FinalSignal:         &analysis.Signal,
			FinalRating:         &analysis.Rating,
			ProbUp:              analysis.ProbUp,
			ExpectedValue:       analysis.ExpectedValue,
			AutoTrade:           &autoTradeEnabled,
			SignalQualifies:     &signalQualifies,
			ConfidenceQualifies: &confidenceQualifies,
			RegimeOk:            &regimeOk,
			VolOk:               &volOk,
			ProbOk:              &probOk,
			Decision:            &decision,
			DecisionReason:      &decisionReason,
			IndicatorsJSON:      string(indicatorsJSON),
			DecisionContextJSON: defaultDecisionContextJSON(decisionContext),
			AnalyzedAt:          time.Now(),
		}
		database.DB.Create(&history)
	}

	return analyses, tradesOpened
}

// executeBuyFromTrending performs a buy based on trending analysis (matching Python execute_buy logic)
func executeBuyFromTrending(symbol string) (bool, error) {
	return executeBuyFromTrendingWithContext(symbol, TradeDecisionContext{})
}

func executeBuyFromTrendingWithContext(symbol string, decisionContext TradeDecisionContext) (bool, error) {
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
	atrTrailingEnabled := getSettingBool(settings, "atr_trailing_enabled", false)
	atrTrailingMult := getSettingFloat(settings, "atr_trailing_mult", 1.0)
	atrTrailingPeriod := getSettingInt(settings, "atr_trailing_period", 14)
	atrAnnualizationEnabled := getSettingBool(settings, "atr_annualization_enabled", false)
	atrAnnualizationDays := getSettingInt(settings, "atr_annualization_days", 365)
	amountUsdt := 0.0
	cryptoAmount := 0.0
	var stopPrice *float64
	var takeProfitPrice *float64
	var maxBarsHeld *int
	var atr float64

	if volSizingEnabled || atrTrailingEnabled {
		candles, err := fetchCandles(pairSymbol, "15m", 200)
		if err != nil {
			return false, fmt.Errorf("failed to fetch candles for %s: %w", pairSymbol, err)
		}
		atr = getAtrValue(candles, atrTrailingPeriod, atrAnnualizationEnabled, 15, atrAnnualizationDays)
	}

	if volSizingEnabled {
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

	if err := database.DB.Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.First(&wallet).Error; err != nil {
			return err
		}
		if amountUsdt > wallet.Balance {
			return fmt.Errorf("insufficient balance")
		}

		var existingPosition database.Position
		lookupErr := tx.Where("symbol = ?", cleanSymbol).First(&existingPosition).Error
		hasExisting := lookupErr == nil
		if lookupErr != nil && !errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return lookupErr
		}

		if hasExisting && existingPosition.Status == "open" {
			oldAmount := existingPosition.Amount
			oldAvg := existingPosition.AvgPrice
			newAvg := ((oldAmount * oldAvg) + (cryptoAmount * currentPrice)) / (oldAmount + cryptoAmount)
			existingPosition.Amount += cryptoAmount
			existingPosition.AvgPrice = newAvg
			existingPosition.ExecutionMode = ExecutionModePaper
			existingPosition.EntrySource = EntrySourceAutoTrend
			existingPosition.ExitPending = false
			existingPosition.CurrentPrice = &currentPrice
			existingPosition.LastMarkPrice = &currentPrice
			existingPosition.LastMarkAt = &now
			existingPosition.DecisionTimeframe = DecisionTimeframeDefault
			existingPosition.ModelVersion = decisionContext.ModelVersion
			existingPosition.PolicyVersion = decisionContext.PolicyVersion
			existingPosition.UniverseMode = decisionContext.UniverseMode
			existingPosition.RolloutState = decisionContext.RolloutState
			existingPosition.ExperimentID = stringPtr(decisionContext.ExperimentID)
			existingPosition.PredictionLogID = decisionContext.PredictionLogID
			existingPosition.DecisionContextJSON = defaultDecisionContextJSON(decisionContext)
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
			if atrTrailingEnabled && atr > 0 && atrTrailingMult > 0 {
				candidateStop := currentPrice - (atr * atrTrailingMult)
				if candidateStop > 0 {
					if existingPosition.TrailingStopPrice == nil || candidateStop > *existingPosition.TrailingStopPrice {
						existingPosition.TrailingStopPrice = &candidateStop
					}
				}
				existingPosition.LastAtrValue = &atr
			}
			if err := tx.Save(&existingPosition).Error; err != nil {
				return err
			}
		} else {
			var trailingStopPrice *float64
			var lastAtrValue *float64
			if atrTrailingEnabled && atr > 0 && atrTrailingMult > 0 {
				entryStop := currentPrice - (atr * atrTrailingMult)
				if entryStop > 0 {
					trailingStopPrice = &entryStop
				}
				lastAtrValue = &atr
			}
			if hasExisting {
				existingPosition.Amount = cryptoAmount
				existingPosition.AvgPrice = currentPrice
				existingPosition.EntryPrice = &currentPrice
				existingPosition.CurrentPrice = &currentPrice
				existingPosition.ExecutionMode = ExecutionModePaper
				existingPosition.EntrySource = EntrySourceAutoTrend
				existingPosition.ExitPending = false
				existingPosition.LastMarkPrice = &currentPrice
				existingPosition.LastMarkAt = &now
				existingPosition.ClientPositionID = newClientPositionID(cleanSymbol, now)
				existingPosition.DecisionTimeframe = DecisionTimeframeDefault
				existingPosition.ModelVersion = decisionContext.ModelVersion
				existingPosition.PolicyVersion = decisionContext.PolicyVersion
				existingPosition.UniverseMode = decisionContext.UniverseMode
				existingPosition.RolloutState = decisionContext.RolloutState
				existingPosition.ExperimentID = stringPtr(decisionContext.ExperimentID)
				existingPosition.PredictionLogID = decisionContext.PredictionLogID
				existingPosition.DecisionContextJSON = defaultDecisionContextJSON(decisionContext)
				existingPosition.StopPrice = stopPrice
				existingPosition.TakeProfitPrice = takeProfitPrice
				existingPosition.TrailingStopPrice = trailingStopPrice
				existingPosition.LastAtrValue = lastAtrValue
				existingPosition.MaxBarsHeld = maxBarsHeld
				existingPosition.Pnl = 0
				existingPosition.PnlPercent = 0
				existingPosition.Status = "open"
				existingPosition.OpenedAt = now
				existingPosition.ClosedAt = nil
				existingPosition.CloseReason = nil
				if err := tx.Save(&existingPosition).Error; err != nil {
					return fmt.Errorf("failed to reopen position for %s: %w", cleanSymbol, err)
				}
			} else {
				position := database.Position{
					Symbol:              cleanSymbol,
					Amount:              cryptoAmount,
					AvgPrice:            currentPrice,
					EntryPrice:          &currentPrice,
					CurrentPrice:        &currentPrice,
					ExecutionMode:       ExecutionModePaper,
					EntrySource:         EntrySourceAutoTrend,
					LastMarkPrice:       &currentPrice,
					LastMarkAt:          &now,
					ClientPositionID:    newClientPositionID(cleanSymbol, now),
					DecisionTimeframe:   DecisionTimeframeDefault,
					ModelVersion:        decisionContext.ModelVersion,
					PolicyVersion:       decisionContext.PolicyVersion,
					UniverseMode:        decisionContext.UniverseMode,
					RolloutState:        decisionContext.RolloutState,
					ExperimentID:        stringPtr(decisionContext.ExperimentID),
					PredictionLogID:     decisionContext.PredictionLogID,
					DecisionContextJSON: defaultDecisionContextJSON(decisionContext),
					StopPrice:           stopPrice,
					TakeProfitPrice:     takeProfitPrice,
					TrailingStopPrice:   trailingStopPrice,
					LastAtrValue:        lastAtrValue,
					MaxBarsHeld:         maxBarsHeld,
					Status:              "open",
					OpenedAt:            now,
				}
				if err := tx.Create(&position).Error; err != nil {
					return fmt.Errorf("failed to create position for %s: %w", cleanSymbol, err)
				}
			}
		}

		order := database.Order{
			OrderType:           "buy",
			Symbol:              cleanSymbol,
			AmountCrypto:        cryptoAmount,
			AmountUsdt:          amountUsdt,
			Price:               currentPrice,
			Status:              OrderStatusFilled,
			ExecutionMode:       ExecutionModePaper,
			ModelVersion:        decisionContext.ModelVersion,
			PolicyVersion:       decisionContext.PolicyVersion,
			UniverseMode:        decisionContext.UniverseMode,
			RolloutState:        decisionContext.RolloutState,
			ExperimentID:        stringPtr(decisionContext.ExperimentID),
			PredictionLogID:     decisionContext.PredictionLogID,
			DecisionContextJSON: defaultDecisionContextJSON(decisionContext),
			RequestedPrice:      &currentPrice,
			FillPrice:           &currentPrice,
			ExecutedQty:         &cryptoAmount,
			SubmittedAt:         &now,
			FilledAt:            &now,
			ExecutedAt:          now,
		}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}

		wallet.Balance -= amountUsdt
		return tx.Save(&wallet).Error
	}); err != nil {
		return false, err
	}

	NotifyPositionChanged()

	return true, nil
}

func buildTradeDecisionContext(analysis AnalyzedCoin) TradeDecisionContext {
	payload, _ := json.Marshal(map[string]interface{}{
		"model_version":     analysis.ModelVersion,
		"policy_version":    analysis.PolicyVersion,
		"universe_mode":     analysis.UniverseMode,
		"rollout_state":     analysis.RolloutState,
		"experiment_id":     analysis.ExperimentID,
		"prediction_log_id": analysis.PredictionLogID,
		"model_rank":        analysis.ModelRank,
		"policy_selected":   analysis.PolicySelected,
		"decision_reason":   analysis.DecisionReason,
	})
	return TradeDecisionContext{
		ModelVersion:        analysis.ModelVersion,
		PolicyVersion:       analysis.PolicyVersion,
		UniverseMode:        analysis.UniverseMode,
		RolloutState:        analysis.RolloutState,
		ExperimentID:        analysis.ExperimentID,
		PredictionLogID:     analysis.PredictionLogID,
		DecisionContextJSON: string(payload),
	}
}

func defaultDecisionContextJSON(context TradeDecisionContext) string {
	if strings.TrimSpace(context.DecisionContextJSON) != "" {
		return context.DecisionContextJSON
	}
	payload, _ := json.Marshal(context)
	return string(payload)
}

// AnalyzeTrendingCoins is the main function that mirrors the Python analyze_trending_coins
func AnalyzeTrendingCoins() (*TrendingAnalysisResult, error) {
	settings := GetAllSettings()
	autoTradeEnabled := getSettingBool(settings, "auto_trade_enabled", false)
	policy := GetUniversePolicy(settings)

	logActivity("system", "Starting trending coins analysis",
		fmt.Sprintf("Auto-trade: %v, universe_mode: %s, analyze_top_n: %d", autoTradeEnabled, policy.Mode, policy.AnalyzeTopN))

	selection, err := BuildUniverseSnapshot(policy)
	if err != nil {
		logActivity("error", "Failed to build universe snapshot", err.Error())
		return nil, fmt.Errorf("failed to build universe snapshot: %w", err)
	}

	results, err := AnalyzeShortlist(selection, settings)
	if err != nil {
		logActivity("error", "Failed to analyze shortlist", err.Error())
		return nil, err
	}
	results, tradesOpened := ExecuteShortlistTrades(results, selection, settings)
	if err := RefreshMonitoringSnapshot(settings); err != nil {
		logActivity("error", "Failed to refresh monitoring snapshot", err.Error())
	}

	logActivity("system", "Trending analysis complete",
		fmt.Sprintf("Analyzed %d coins, opened %d trades, regime %s", len(results), tradesOpened, selection.RegimeState))

	// Broadcast trending updates and analysis completion
	websocket.BroadcastTrendingUpdate(results)
	websocket.BroadcastAnalysisComplete(
		time.Now().UTC().Format(time.RFC3339),
		len(results),
		tradesOpened,
	)

	return &TrendingAnalysisResult{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Trending:     selection.Trending,
		Universe:     selection,
		Analyzed:     results,
		TradesOpened: tradesOpened,
	}, nil
}

// GetRecentAnalyzedCoins returns the most recently analyzed coins from the database
func GetRecentAnalyzedCoins() ([]AnalyzedCoin, error) {
	var history []database.TrendAnalysisHistory
	if err := database.DB.
		Order("analyzed_at DESC").
		Find(&history).Error; err != nil {
		return nil, err
	}

	coins := make([]AnalyzedCoin, 0, len(history))

	for _, row := range history {
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

		coins = append(coins, AnalyzedCoin{
			Symbol:        row.Symbol,
			Price:         price,
			Change24h:     change,
			Signal:        signal,
			Rating:        rating,
			Timeframe:     row.Timeframe,
			CreatedAt:     row.AnalyzedAt,
			ModelVersion:  row.ModelVersion,
			PolicyVersion: row.PolicyVersion,
			UniverseMode:  row.UniverseMode,
			RolloutState:  row.RolloutState,
			ExperimentID:  valueFromStringPtr(row.ExperimentID),
			ProbUp:        probUp,
			ExpectedValue: expectedValue,
			Indicators:    indicators,
		})
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
		CreatedAt:      history.AnalyzedAt,
		ModelVersion:   history.ModelVersion,
		PolicyVersion:  history.PolicyVersion,
		UniverseMode:   history.UniverseMode,
		RolloutState:   history.RolloutState,
		ExperimentID:   valueFromStringPtr(history.ExperimentID),
		ProbUp:         probUp,
		ExpectedValue:  expectedValue,
		Indicators:     indicators,
		Decision:       decision,
		DecisionReason: decisionReason,
	}, nil
}

func float64Ptr(value float64) *float64 {
	return &value
}

func intValuePtr(value int) *int {
	return &value
}

func boolValuePtr(value bool) *bool {
	return &value
}

func modelRankValue(rank *int) int {
	if rank == nil || *rank <= 0 {
		return int(^uint(0) >> 1)
	}
	return *rank
}

func hasModelRankings(analyses []AnalyzedCoin) bool {
	for _, analysis := range analyses {
		if analysis.ModelRank != nil {
			return true
		}
	}
	return false
}

func defaultString(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func valueFromStringPtr(value *string) string {
	if value == nil {
		return ""
	}
	return *value
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
	if allPositions, err := database.ListPositionsForDisplay(); err == nil {
		websocket.BroadcastPositionsUpdate(allPositions)
	}

	// Broadcast recent orders
	var orders []database.Order
	database.DB.Order("executed_at DESC").Limit(10).Find(&orders)
	websocket.BroadcastOrdersUpdate(orders)
}
