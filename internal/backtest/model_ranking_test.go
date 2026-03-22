package backtest

import (
	"testing"
	"time"
	"trading-go/internal/services"
)

func TestRunBacktestUsesModelRanking(t *testing.T) {
	artifact, err := services.LoadModelArtifact(services.DefaultActiveModelVersion)
	if err != nil {
		t.Fatalf("LoadModelArtifact() error = %v", err)
	}

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	series := map[string][]services.OHLCV{
		"BTCUSDT": buildBacktestSeries(start, 800, 40_000, 6, 5_000),
		"AAAUSDT": buildBacktestSeries(start, 800, 50, 0.18, 45),
		"BBBUSDT": buildBacktestSeries(start, 800, 50, -0.03, 20),
	}

	config := BacktestConfig{
		Symbols:             []string{"BTCUSDT", "AAAUSDT", "BBBUSDT"},
		UniverseMode:        UniverseStatic,
		UniversePolicy:      services.UniversePolicy{TopK: 3, AnalyzeTopN: 3},
		Start:               start,
		End:                 start.Add(800 * 15 * time.Minute),
		IndicatorConfig:     services.DefaultIndicatorConfig(),
		IndicatorWeights:    map[string]float64{"rsi": 1, "macd": 1, "bollinger": 1, "volume": 0.5, "momentum": 1},
		Timeframe:           "15m",
		TimeframeMinutes:    15,
		InitialBalance:      1_000,
		FeeBps:              10,
		SlippageBps:         0,
		ModelArtifact:       artifact,
		ModelPolicy:         services.ModelSelectionPolicy{ActiveModelVersion: artifact.Version, TopK: 1, MinProbability: 0, MinExpectedValue: -1},
		MaxPositions:        1,
		StrategyMode:        StrategyBaseline,
		EntryPercent:        25,
		StopLossPercent:     5,
		TakeProfitPercent:   8,
		SellOnSignal:        false,
		AllowSellAtLoss:     true,
		MinConfidenceToSell: 3.5,
	}

	result, err := RunBacktest(config, series)
	if err != nil {
		t.Fatalf("RunBacktest() error = %v", err)
	}
	if len(result.Trades) == 0 {
		t.Fatal("expected model-ranked backtest to produce trades")
	}
	for _, trade := range result.Trades {
		if trade.Symbol != "AAAUSDT" {
			t.Fatalf("expected only AAAUSDT trades from top-ranked model policy, got %s", trade.Symbol)
		}
		if trade.EntryRank != 1 {
			t.Fatalf("expected rank-1 entries, got rank %d", trade.EntryRank)
		}
		if trade.PredictedProbability == nil || trade.PredictedEV == nil {
			t.Fatalf("expected stored model predictions on trade %+v", trade)
		}
	}
	if result.RankingMetrics == nil || result.RankingMetrics.Selected == 0 {
		t.Fatalf("expected ranking metrics to be populated, got %+v", result.RankingMetrics)
	}
}
