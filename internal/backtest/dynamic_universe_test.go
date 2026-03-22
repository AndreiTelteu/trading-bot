package backtest

import (
	"testing"
	"time"
	"trading-go/internal/services"
)

func TestRunBacktestDynamicUniverseTradesOnlyFromShortlist(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	series := map[string][]services.OHLCV{
		"BTCUSDT": buildBacktestSeries(start, 800, 100, 0.01, 20),
		"AAAUSDT": buildBacktestSeries(start, 800, 50, 0.08, 30),
		"BBBUSDT": buildBacktestSeries(start, 800, 40, -0.01, 15),
	}

	config := BacktestConfig{
		Symbols:      []string{"BTCUSDT", "AAAUSDT", "BBBUSDT"},
		UniverseMode: UniverseDynamicRecompute,
		UniversePolicy: services.UniversePolicy{
			RebalanceInterval:      time.Hour,
			RebalanceIntervalLabel: "1h",
			MinListingDays:         1,
			MinDailyQuoteVolume:    1,
			MinIntradayQuoteVolume: 1,
			MaxGapRatio:            1,
			VolRatioMin:            0,
			VolRatioMax:            1,
			Max24hMove:             500,
			TopK:                   2,
			AnalyzeTopN:            1,
		},
		Start:              start,
		End:                start.Add(800 * 15 * time.Minute),
		IndicatorConfig:    services.DefaultIndicatorConfig(),
		IndicatorWeights:   map[string]float64{"rsi": 1, "macd": 1, "bollinger": 1, "volume": 0.5, "momentum": 1},
		Timeframe:          "15m",
		TimeframeMinutes:   15,
		InitialBalance:     1000,
		MaxPositions:       1,
		StrategyMode:       StrategyBaseline,
		EntryPercent:       20,
		BuyOnlyStrong:      false,
		MinConfidenceToBuy: 3,
		TimeStopBars:       4,
	}

	result, err := RunBacktest(config, series)
	if err != nil {
		t.Fatalf("RunBacktest() error = %v", err)
	}
	if len(result.Trades) == 0 {
		t.Fatal("expected at least one closed trade in dynamic universe mode")
	}
	for _, trade := range result.Trades {
		if trade.Symbol != "AAAUSDT" {
			t.Fatalf("expected only AAAUSDT trades from shortlist, got %s", trade.Symbol)
		}
	}
	if result.Metrics.TradeCount == 0 {
		t.Fatal("expected trade count to be recorded")
	}
}

func buildBacktestSeries(start time.Time, bars int, base float64, slope float64, volume float64) []services.OHLCV {
	series := make([]services.OHLCV, 0, bars)
	for i := 0; i < bars; i++ {
		openTime := start.Add(time.Duration(i) * 15 * time.Minute)
		close := base + slope*float64(i)
		if close < 1 {
			close = 1
		}
		open := close - slope*0.5
		if open < 1 {
			open = close
		}
		series = append(series, services.OHLCV{
			OpenTime:  openTime.UnixMilli(),
			Open:      open,
			High:      close * 1.01,
			Low:       close * 0.99,
			Close:     close,
			Volume:    volume,
			CloseTime: openTime.Add(15 * time.Minute).UnixMilli(),
		})
	}
	return series
}
