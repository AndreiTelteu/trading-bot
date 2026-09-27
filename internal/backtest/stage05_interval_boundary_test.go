package backtest

import (
	"testing"
	"time"

	"trading-go/internal/services"
)

func TestStage07TruncateBarsExcludesFoldEndOpen(t *testing.T) {
	start := time.Date(2025, 11, 30, 23, 30, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	bars := stage05Bars(start, []float64{100, 100, 100})
	got := stage07TruncateBars(bars, end)
	if len(got) != 2 || !time.UnixMilli(got[len(got)-1].OpenTime).Before(end) {
		t.Fatalf("half-open fold retained bar at or after end: %+v", got)
	}
	// End need not be millisecond-aligned; compare instants rather than
	// truncating End with UnixMilli before selecting source bars.
	if got := stage07TruncateBars(bars, end.Add(500*time.Microsecond)); len(got) != 3 {
		t.Fatalf("bar opened before a sub-millisecond end was dropped: %+v", got)
	}
}

func TestStage05V3FoldEndDecisionDoesNotRequireAfterEndExecution(t *testing.T) {
	config, _ := stage05Fixture(nil, []float64{100, 100, 100}, 0, 0)
	config.Start = time.Date(2025, 11, 30, 23, 30, 0, 0, time.UTC)
	config.End = config.Start.Add(30 * time.Minute)
	bars := stage05Bars(config.Start, []float64{100, 100, 100})
	config.BenchmarkSeries = stage07TruncateBars(bars, config.End)
	config.ExecutionSeriesRequired = true
	config.ExecutionSeries = map[string][]services.OHLCV{"BTCUSDT": stage07TruncateBars(bars, config.End)}
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	planner := Stage05PlannerFunc(func(c Stage05PlanningContext) (Stage05Plan, error) {
		return Stage05Plan{Targets: []string{"BTCUSDT"}, Decide: len(c.Reference) == 2}, nil
	})
	result, err := runStage05StrategyWithPlanner(config, nil, selected, strategy, planner, true)
	if err != nil {
		t.Fatalf("last in-window signal has no legal execution bar: %v", err)
	}
	if len(result.Artifacts.Fills) != 0 || len(result.NoFills) != 0 || result.Metrics.EndingEquity != "1000" {
		t.Fatalf("after-end execution or no-fill was manufactured: fills=%+v no_fills=%+v ending=%s", result.Artifacts.Fills, result.NoFills, result.Metrics.EndingEquity)
	}
}

func TestStage05V3MissingInteriorExecutionFailsAtLastReferenceBar(t *testing.T) {
	config, _ := stage05Fixture(nil, []float64{100, 100, 100}, 0, 0)
	config.End = config.Start.Add(60 * time.Minute)
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	config.ExecutionSeriesRequired = true
	config.ExecutionSeries = map[string][]services.OHLCV{"BTCUSDT": config.BenchmarkSeries[:2]}
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	planner := Stage05PlannerFunc(func(c Stage05PlanningContext) (Stage05Plan, error) {
		return Stage05Plan{Targets: []string{"BTCUSDT"}, Decide: len(c.Reference) == 3}, nil
	})
	_, err = runStage05StrategyWithPlanner(config, nil, selected, strategy, planner, true)
	if !IsStrategyDiagnostic(err, DiagnosticExecutionLiquidity) {
		t.Fatalf("missing execution bar inside [start,end) was silently skipped: %v", err)
	}
}

func TestStage05V3SelectedExecutionAtFoldEndIsIneligible(t *testing.T) {
	config, _ := stage05Fixture(nil, []float64{100, 100, 100}, 0, 0)
	config.End = config.Start.Add(30 * time.Minute)
	config.ExecutionSeriesRequired = true
	config.ExecutionSeries = map[string][]services.OHLCV{"BTCUSDT": config.BenchmarkSeries[2:]}
	_, _, ok := nextFillPrices(config, nil, []string{"BTCUSDT"}, stage05CloseAt(config.Start, 1))
	if ok {
		t.Fatal("selected fill opened at fold end")
	}
}

func TestStage05V3FinalLiquidationCannotUseFoldEndBar(t *testing.T) {
	config, _ := stage05Fixture(nil, []float64{100, 100, 100, 100}, 0, 0)
	config.End = config.Start.Add(45 * time.Minute)
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	config.ExecutionSeriesRequired = true
	config.ExecutionSeries = map[string][]services.OHLCV{"BTCUSDT": {config.BenchmarkSeries[1], config.BenchmarkSeries[3]}}
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "liquidate"})
	if err != nil {
		t.Fatal(err)
	}
	planner := Stage05PlannerFunc(func(c Stage05PlanningContext) (Stage05Plan, error) {
		return Stage05Plan{Targets: []string{"BTCUSDT"}, Decide: len(c.Reference) == 1}, nil
	})
	_, err = runStage05StrategyWithPlanner(config, nil, selected, strategy, planner, true)
	if !IsStrategyDiagnostic(err, DiagnosticManifestIncompatible) {
		t.Fatalf("final liquidation used an after-end bar or silently retained a position: %v", err)
	}
}
