package handlers

import (
	"fmt"
	"strconv"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/services"

	"github.com/gofiber/fiber/v2"
)

type AnalyzeRequest struct {
	Symbol  string            `json:"symbol"`
	Candles []services.Candle `json:"candles"`
	Price   float64           `json:"price"`
}

func GetAnalysis(c *fiber.Ctx) error {
	symbol := c.Params("symbol")
	if symbol == "" {
		symbol = "BTCUSDT"
	}

	result, err := performAnalysis(symbol)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(result)
}

func GetAnalysisDefault(c *fiber.Ctx) error {
	symbol := "BTCUSDT"

	result, err := performAnalysis(symbol)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}

	return c.JSON(result)
}

func GetTrendingRecent(c *fiber.Ctx) error {
	// Get distinct symbols from recent trend analysis history
	var history []database.TrendAnalysisHistory
	if err := database.DB.
		Select("symbol, MAX(analyzed_at) as analyzed_at, current_price, change_24h, final_signal, final_rating").
		Group("symbol").
		Order("analyzed_at DESC").
		Limit(20).
		Find(&history).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "Failed to fetch recent coins"})
	}

	type RecentCoin struct {
		Symbol      string  `json:"symbol"`
		Price       float64 `json:"price"`
		Change24h   float64 `json:"change_24h"`
		Signal      string  `json:"signal"`
		Rating      float64 `json:"rating"`
		AnalyzedAt  string  `json:"analyzed_at"`
	}

	coins := make([]RecentCoin, len(history))
	for i, h := range history {
		price := 0.0
		if h.CurrentPrice != nil {
			price = *h.CurrentPrice
		}
		change := 0.0
		if h.Change24h != nil {
			change = *h.Change24h
		}
		signal := ""
		if h.FinalSignal != nil {
			signal = *h.FinalSignal
		}
		rating := 0.0
		if h.FinalRating != nil {
			rating = *h.FinalRating
		}
		coins[i] = RecentCoin{
			Symbol:     h.Symbol,
			Price:      price,
			Change24h:  change,
			Signal:     signal,
			Rating:     rating,
			AnalyzedAt: h.AnalyzedAt.Format("2006-01-02T15:04:05Z"),
		}
	}

	return c.JSON(coins)
}

func AnalyzeSymbol(c *fiber.Ctx) error {
	var req AnalyzeRequest
	// Try to parse body, but allow empty body (use defaults)
	if err := c.BodyParser(&req); err != nil {
		// If body is empty or invalid, use defaults
		req = AnalyzeRequest{}
	}

	if req.Symbol == "" {
		req.Symbol = "BTCUSDT"
	}

	// If no candles provided, fetch them from exchange
	if len(req.Candles) == 0 {
		fmt.Printf("DEBUG: performAnalysis for %s\n", req.Symbol)
		result, err := performAnalysis(req.Symbol)
		if err != nil {
			fmt.Printf("DEBUG: performAnalysis error: %v\n", err)
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(result)
	}

	if req.Price <= 0 {
		req.Price = req.Candles[len(req.Candles)-1].Close
	}

	fmt.Printf("DEBUG: AnalyzeSymbol with candles for %s\n", req.Symbol)
	result := services.AnalyzeSymbol(req.Candles, req.Symbol, req.Price)
	result.Timestamp = time.Now().Format(time.RFC3339)

	saveAnalysisHistory(req.Symbol, result)

	return c.JSON(result)
}

func performAnalysis(symbol string) (services.AnalysisResult, error) {
	exchange := services.GetExchange()

	fmt.Printf("DEBUG: FetchTickerPrice for %s\n", symbol)
	ticker, err := exchange.FetchTickerPrice(symbol)
	if err != nil {
		fmt.Printf("DEBUG: FetchTickerPrice error: %v\n", err)
		return services.AnalysisResult{}, err
	}

	fmt.Printf("DEBUG: Ticker result: %+v\n", ticker)
	currentPrice, _ := strconv.ParseFloat(ticker.LastPrice, 64)

	fmt.Printf("DEBUG: FetchOHLCV for %s\n", symbol)
	ohlcv, err := exchange.FetchOHLCV(symbol, "15m", 100)
	if err != nil {
		fmt.Printf("DEBUG: FetchOHLCV error: %v\n", err)
		return services.AnalysisResult{}, err
	}

	candles := make([]services.Candle, len(ohlcv))
	for i, kline := range ohlcv {
		candles[i] = services.Candle{
			Close:  kline.Close,
			High:   kline.High,
			Low:    kline.Low,
			Volume: kline.Volume,
		}
	}

	result := services.AnalyzeSymbol(candles, symbol, currentPrice)
	result.Timestamp = time.Now().Format(time.RFC3339)

	saveAnalysisHistory(symbol, result)

	return result, nil
}

func saveAnalysisHistory(symbol string, result services.AnalysisResult) {
	history := database.TrendAnalysisHistory{
		Symbol:       symbol,
		Timeframe:    "15m",
		CurrentPrice: &result.CurrentPrice,
		FinalSignal:  &result.Signal,
		FinalRating:  &result.Rating,
		AnalyzedAt:   time.Now(),
	}
	database.DB.Create(&history)
}
