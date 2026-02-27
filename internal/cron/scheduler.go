package cron

import (
	"encoding/json"
	"log"
	"strconv"
	"strings"

	"trading-go/internal/database"
	"trading-go/internal/services"

	"github.com/robfig/cron/v3"
)

var (
	scheduler    *cron.Cron
	priceJobID    cron.EntryID
	trendingJobID cron.EntryID
	proposalJobID cron.EntryID
)

func Start() {
	scheduler = cron.New()

	// Price update every 30 seconds
	scheduler.AddFunc("@every 30s", func() {
		if err := runPriceUpdate(); err != nil {
			log.Printf("Price update job failed: %v", err)
		}
	})

	// Trending analysis every 15 minutes
	scheduler.AddFunc("0 */15 * * * *", func() {
		if err := runTrendingAnalysis(); err != nil {
			log.Printf("Trending analysis job failed: %v", err)
		}
	})

	// AI proposals - configurable, default disabled
	proposalInterval := getProposalInterval()
	if proposalInterval != "" && proposalInterval != "disabled" {
		scheduler.AddFunc(proposalInterval, func() {
			if err := runGenerateProposals(); err != nil {
				log.Printf("AI proposal generation job failed: %v", err)
			}
		})
	}

	scheduler.Start()
	log.Println("Cron scheduler started")
}

func Stop() {
	if scheduler != nil {
		scheduler.Stop()
		log.Println("Cron scheduler stopped")
	}
}

func runPriceUpdate() error {
	result, err := services.UpdatePositionsPrices()
	if err != nil {
		return err
	}
	log.Printf("Price update completed: %v", result)
	return nil
}

func runTrendingAnalysis() error {
	symbols := getTrendingSymbols()
	if len(symbols) == 0 {
		symbols = []string{"BTCUSDT", "ETHUSDT", "BNBUSDT"}
	}

	exchange := services.GetExchange()
	for _, symbol := range symbols {
		ohlcv, err := exchange.FetchOHLCV(symbol, "15m", 100)
		if err != nil {
			log.Printf("Failed to fetch candles for %s: %v", symbol, err)
			continue
		}

		if len(ohlcv) == 0 {
			continue
		}

		// Convert OHLCV to Candle
		candles := make([]services.Candle, len(ohlcv))
		for i, o := range ohlcv {
			candles[i] = services.Candle{
				Close:  o.Close,
				High:   o.High,
				Low:    o.Low,
				Volume: o.Volume,
			}
		}

		currentPrice := candles[len(candles)-1].Close

		analysis := services.AnalyzeSymbol(candles, symbol, currentPrice)

		change24h := calculateChange24h(ohlcv)

		indicatorsJSON, _ := json.Marshal(analysis.Indicators)

		rating := analysis.Rating

		history := database.TrendAnalysisHistory{
			Symbol:         symbol,
			Timeframe:      "15m",
			CurrentPrice:   &currentPrice,
			Change24h:      &change24h,
			FinalSignal:    &analysis.Signal,
			FinalRating:    &rating,
			IndicatorsJSON: string(indicatorsJSON),
		}
		database.DB.Create(&history)
	}
	log.Printf("Trending analysis completed for %d symbols", len(symbols))
	return nil
}

func runGenerateProposals() error {
	result, err := services.GenerateProposals()
	if err != nil {
		return err
	}
	log.Printf("AI proposal generation completed: %v", result)
	return nil
}

func getTrendingSymbols() []string {
	var history []database.TrendAnalysisHistory
	database.DB.Select("DISTINCT symbol").Order("analyzed_at DESC").Limit(10).Find(&history)

	symbols := make([]string, len(history))
	for i, h := range history {
		symbols[i] = h.Symbol
	}
	return symbols
}

func getProposalInterval() string {
	var setting database.Setting
	if err := database.DB.First(&setting, "key = ?", "ai_proposal_interval").Error; err != nil {
		return "disabled"
	}

	interval := setting.Value
	interval = strings.TrimSpace(interval)

	if interval == "" || interval == "0" || interval == "disabled" {
		return "disabled"
	}

	if strings.HasPrefix(interval, "@every ") {
		return interval
	}

	if strings.HasSuffix(interval, "m") {
		minutes, err := strconv.Atoi(strings.TrimSuffix(interval, "m"))
		if err != nil || minutes <= 0 {
			return "disabled"
		}
		return "0 */" + strconv.Itoa(minutes) + " * * * *"
	}

	return "disabled"
}

func calculateChange24h(candles []services.OHLCV) float64 {
	if len(candles) < 2 {
		return 0
	}
	oldPrice := candles[0].Close
	newPrice := candles[len(candles)-1].Close
	if oldPrice == 0 {
		return 0
	}
	return ((newPrice - oldPrice) / oldPrice) * 100
}
