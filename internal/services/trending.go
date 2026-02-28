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
	Symbol        string            `json:"symbol"`
	Price         float64           `json:"price"`
	Change24h     float64           `json:"change_24h"`
	Signal        string            `json:"signal"`
	Rating        float64           `json:"rating"`
	Timeframe     string            `json:"timeframe"`
	Indicators    []IndicatorResult `json:"indicators"`
	TradeExecuted *bool             `json:"trade_executed,omitempty"`
	Error         string            `json:"error,omitempty"`
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
	ex := GetExchange()

	// Binance symbols don't have "/" so ensure format is e.g. "ETHUSDT"
	cleanSymbol := strings.ReplaceAll(symbol, "/", "")

	ohlcv, err := ex.FetchOHLCV(cleanSymbol, timeframe, 200)
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

	currentPrice := ohlcv[len(ohlcv)-1].Close

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
	}, nil
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

	// Calculate amount to buy
	entryPercent := getSettingFloat(settings, "entry_percent", 5.0)
	amountUsdt := wallet.Balance * (entryPercent / 100)

	if amountUsdt > wallet.Balance {
		return false, fmt.Errorf("insufficient balance")
	}

	if amountUsdt <= 0 {
		return false, fmt.Errorf("calculated amount is 0")
	}

	cryptoAmount := amountUsdt / currentPrice

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
		database.DB.Save(&existingPosition)
	} else {
		position := database.Position{
			Symbol:       cleanSymbol,
			Amount:       cryptoAmount,
			AvgPrice:     currentPrice,
			EntryPrice:   &currentPrice,
			CurrentPrice: &currentPrice,
			Status:       "open",
			OpenedAt:     time.Now(),
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

	logActivity("system", "Starting trending coins analysis",
		fmt.Sprintf("Auto-trade: %v, buy_only_strong: %v, min_confidence: %.1f", autoTradeEnabled, buyOnlyStrong, minConfidenceToBuy))

	// Get trending data from Binance
	trendingData, err := GetBinanceTrending(20, 10, 10)
	if err != nil {
		logActivity("error", "Failed to fetch trending data", err.Error())
		return nil, fmt.Errorf("failed to fetch trending data: %w", err)
	}

	// Analyze top gainers
	coinsToAnalyze := trendingData.TopGainers
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

		analysis, err := analyzeSymbolForTrending(symbol, "15m")
		if err != nil {
			logActivity("error", fmt.Sprintf("Error analyzing %s", symbol), err.Error())
			results = append(results, AnalyzedCoin{
				Symbol: symbol,
				Error:  err.Error(),
			})
			continue
		}

		analysis.Change24h = coin.Change24h

		// Save to trend analysis history
		indicatorsJSON, _ := json.Marshal(analysis.Indicators)
		history := database.TrendAnalysisHistory{
			Symbol:         symbol,
			Timeframe:      "15m",
			CurrentPrice:   &analysis.Price,
			Change24h:      &coin.Change24h,
			FinalSignal:    &analysis.Signal,
			FinalRating:    &analysis.Rating,
			IndicatorsJSON: string(indicatorsJSON),
			AnalyzedAt:     time.Now(),
		}
		database.DB.Create(&history)

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

		if autoTradeEnabled && signalQualifies && confidenceQualifies &&
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
					logActivity("trade", fmt.Sprintf("Bought %s", symbol),
						fmt.Sprintf("At $%.2f", analysis.Price))
				} else {
					falseVal := false
					analysis.TradeExecuted = &falseVal
					errMsg := "Unknown error"
					if buyErr != nil {
						errMsg = buyErr.Error()
					}
					logActivity("trade", fmt.Sprintf("Failed to buy %s", symbol), errMsg)
				}
			} else {
				logActivity("trade", fmt.Sprintf("Skipped %s - position already exists", symbol), "")
			}
		}

		results = append(results, *analysis)
	}

	logActivity("system", "Trending analysis complete",
		fmt.Sprintf("Analyzed %d coins, opened %d trades", len(results), tradesOpened))

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

		latestBySymbol[row.Symbol] = AnalyzedCoin{
			Symbol:     row.Symbol,
			Price:      price,
			Change24h:  change,
			Signal:     signal,
			Rating:     rating,
			Timeframe:  row.Timeframe,
			Indicators: indicators,
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
