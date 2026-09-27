package backtest

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestStage05V4CancelsOrderAboveSelectedBarCapacity(t *testing.T) {
	config, series := stage05Fixture(nil, []float64{100, 100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v4"
	config.ExecutionPolicy.Liquidity = LiquidityVolumeCapped
	config.BenchmarkSeries[1].Volume = 5.19 // at 10%, only 0.519 BTC is available
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts.Fills) != 0 || len(result.NoFills) != 1 {
		t.Fatalf("capacity violation must cancel rather than overfill: fills=%+v no_fills=%+v", result.Artifacts.Fills, result.NoFills)
	}
	n := result.NoFills[0]
	if n.SchemaVersion != SimulatedCapacityNoFillSchemaVersion || n.Reason != "simulated_no_fill_volume_cap" || n.LiquidityEvidence != "selected_bar_base_volume_10pct" || n.CapacityQuantity != "0.519" || n.BarVolume != "5.19" {
		t.Fatalf("capacity evidence missing: %+v", n)
	}
}

func TestStage05V4ZeroVolumeHasZeroCapacityAndNoEconomicFill(t *testing.T) {
	config, series := stage05Fixture(nil, []float64{100, 100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v4"
	config.ExecutionPolicy.Liquidity = LiquidityVolumeCapped
	config.BenchmarkSeries[1].Volume = 0
	config.BenchmarkSeries[1].Close = 101
	config.BenchmarkSeries[1].High = 101
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.NoFills) != 1 || result.NoFills[0].BarVolume != "0" || result.NoFills[0].CapacityQuantity != "0" || result.NoFills[0].SelectedOpenPrice != "100" || result.NoFills[0].SelectedClosePrice != "101" || result.Metrics.FillCount != 0 || result.Metrics.TotalCosts != "0" {
		t.Fatalf("zero-volume v4 order gained economic execution: no_fills=%+v metrics=%+v", result.NoFills, result.Metrics)
	}
}

func TestStage05V4FinalLiquidationFailsWhenCapacityIsInsufficient(t *testing.T) {
	config, series := stage05Fixture(nil, []float64{100, 100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v4"
	config.ExecutionPolicy.Liquidity = LiquidityVolumeCapped
	config.BenchmarkSeries[3].Volume = 5.19
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "liquidate"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runStage05Strategy(config, series, selected, strategy, true)
	if !IsStrategyDiagnostic(err, DiagnosticExecutionLiquidity) || !strings.Contains(err.Error(), "simulated_no_fill_volume_cap") {
		t.Fatalf("unliquidated position cannot become a successful liquidate run: %v", err)
	}
}

func TestStage05V4CapacityRoundsDownToLot(t *testing.T) {
	cap, err := stage05VolumeCap(5.19, 0.01)
	if err != nil || cap != "0.51" {
		t.Fatalf("capacity must round down to executable lot: %q %v", cap, err)
	}
	if _, err := stage05VolumeCap(-1, 0.01); err == nil {
		t.Fatal("negative volume accepted")
	}
}

func TestStage05V4ExecutesOrderWithinCapacity(t *testing.T) {
	config, series := stage05Fixture(nil, []float64{100, 100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v4"
	config.ExecutionPolicy.Liquidity = LiquidityVolumeCapped
	config.BenchmarkSeries[1].Volume = 100 // exact approved==capacity boundary
	config.BenchmarkSeries[1].Close = 101
	config.BenchmarkSeries[1].High = 101
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts.Fills) != 1 || len(result.NoFills) != 0 || result.Artifacts.Fills[0].Quantity == "0" {
		t.Fatalf("feasible order must fill: fills=%+v no_fills=%+v", result.Artifacts.Fills, result.NoFills)
	}
	if result.Artifacts.Fills[0].FillAt != canonicalTime(time.UnixMilli(config.BenchmarkSeries[1].CloseTime)) || result.Artifacts.Fills[0].ExecutionReferencePrice != "101" {
		t.Fatalf("v4 must fill at selected bar close after its capacity is known: %+v", result.Artifacts.Fills[0])
	}
	if result.Manifest.ExecutionPolicy.Version != "backtest-execution-v4" || result.Manifest.ExecutionPolicy.Timing != ExecutionSelectedBarClose || result.Manifest.ExecutionPolicy.MaxParticipationBPS != 1000 {
		t.Fatalf("capacity policy missing from manifest: %+v", result.Manifest.ExecutionPolicy)
	}
}

func TestStage05V4ComparisonBindsSamePolicyToEveryRow(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": linearPrices(10, 84)}, linearPrices(100, 84), 0, 0)
	comparison, err := RunStage05Comparison(config, series, Stage05RunRequest{StrategyID: StrategyBenchmarkHoldID, ExecutionPolicyVersion: "backtest-execution-v4", TargetGrossExposure: "1", MaxNetExposure: "1", FinalPolicy: "mark_to_market", AllowInMemoryFixture: true})
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Assumptions.ExecutionPolicy.Version != "backtest-execution-v4" || comparison.Assumptions.ExecutionPolicy.Timing != ExecutionSelectedBarClose || comparison.Assumptions.ExecutionPolicy.MaxParticipationBPS != 1000 {
		t.Fatalf("comparison lost capacity policy: %+v", comparison.Assumptions.ExecutionPolicy)
	}
	for id, result := range comparison.Results {
		if !reflect.DeepEqual(result.Manifest.ExecutionPolicy, comparison.Assumptions.ExecutionPolicy) {
			t.Fatalf("strategy %s did not share v4 economics: %+v", id, result.Manifest.ExecutionPolicy)
		}
		if !result.Metrics.Reconciled {
			t.Fatalf("strategy %s did not reconcile", id)
		}
	}
}
