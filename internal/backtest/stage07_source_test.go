package backtest

import (
	"testing"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/validation"
)

func TestStage07PrimitivesAttributeActualFillCostsPerTrade(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entry, exit := base.Add(time.Minute), base.Add(2*time.Minute)
	candidate := Stage05StrategyResult{
		Metrics: ComparableMetrics{StartingCapital: "100", AverageGrossExposure: availableMetric(.5), TurnoverRatio: availableMetric(.11)},
		Equity:  []EquityPoint{{Time: base, Value: 100}, {Time: exit, Value: 110}},
		Trades:  []Trade{{Symbol: "AAA", EntryTime: entry, ExitTime: exit, EntryPrice: 11, ExitPrice: 14, Size: 1, Pnl: 10, RegimeState: "risk_on"}},
		Artifacts: BacktestArtifacts{Fills: []FillArtifact{
			{FillAt: entry.Format(time.RFC3339Nano), Symbol: "AAA", Fee: "1", Price: "11", Quantity: "1"},
			{FillAt: exit.Format(time.RFC3339Nano), Symbol: "AAA", Fee: "2", Price: "14", Quantity: "1"},
		}},
	}
	baseline := Stage05StrategyResult{Metrics: ComparableMetrics{AverageGrossExposure: availableMetric(.5), TurnoverRatio: availableMetric(.11)}, Equity: []EquityPoint{{Time: base, Value: 100}, {Time: exit, Value: 100}}}
	series := map[string][]services.OHLCV{"AAA": {{OpenTime: entry.UnixMilli(), Open: 10, Close: 10, Volume: 100}, {OpenTime: exit.UnixMilli(), Open: 15, Close: 15, Volume: 100}}}
	primitives, err := stage07Primitives(candidate, baseline, 0, 4, series)
	if err != nil {
		t.Fatal(err)
	}
	if got := primitives.Trades[0]; got.Cost != 5 || got.GrossPnL != 15 || got.NetPnL != 10 {
		t.Fatalf("cost was not attributed to actual fills: %+v", got)
	}
}

func TestStage07RunnerRejectsMutatedPartitionSample(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	interval := validation.Interval{Start: base, End: base.Add(time.Hour)}
	runner := stage07Runner{source: stage07FoldArtifact{series: map[string][]services.OHLCV{"AAA": {{OpenTime: base.UnixMilli(), Close: 10}}}}}
	sample := validation.Sample{ID: "AAA", ObservedAt: base, Symbol: "AAA", CoverageOK: true, BenchmarkSeen: true, Values: map[string]float64{"close": 10, "bar_open_ms": float64(base.UnixMilli())}}
	if err := runner.validateSamples([]validation.Sample{sample}, interval); err != nil {
		t.Fatal(err)
	}
	sample.Values["close"] = 999
	if err := runner.validateSamples([]validation.Sample{sample}, interval); err == nil {
		t.Fatal("mutated supplied partition was accepted")
	}
}

func TestStage07ParameterChoicesAreExhaustiveAndDeterministic(t *testing.T) {
	choices, err := stage07ParameterChoices(map[string]string{"fixed": "x"}, map[string][]string{"fast": {"10", "20"}, "slow": {"30", "40"}})
	if err != nil || len(choices) != 4 {
		t.Fatalf("choices=%v err=%v", choices, err)
	}
	if stage07ParameterKey(choices[0]) != "fast=10;fixed=x;slow=30" {
		t.Fatalf("unexpected deterministic first choice: %s", stage07ParameterKey(choices[0]))
	}
}
