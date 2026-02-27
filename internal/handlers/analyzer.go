package handlers

import (
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

func AnalyzeSymbol(c *fiber.Ctx) error {
	var req AnalyzeRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "Invalid request body"})
	}

	if req.Symbol == "" {
		req.Symbol = "BTCUSDT"
	}

	if len(req.Candles) == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "No candles provided"})
	}

	if req.Price <= 0 {
		req.Price = req.Candles[len(req.Candles)-1].Close
	}

	result := services.AnalyzeSymbol(req.Candles, req.Symbol, req.Price)
	result.Timestamp = time.Now().Format(time.RFC3339)

	saveAnalysisHistory(req.Symbol, result)

	return c.JSON(result)
}

func performAnalysis(symbol string) (services.AnalysisResult, error) {
	exchange := services.GetExchange()

	ticker, err := exchange.FetchTickerPrice(symbol)
	if err != nil {
		return services.AnalysisResult{}, err
	}

	currentPrice, _ := strconv.ParseFloat(ticker.Price, 64)

	ohlcv, err := exchange.FetchOHLCV(symbol, "15m", 100)
	if err != nil {
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
