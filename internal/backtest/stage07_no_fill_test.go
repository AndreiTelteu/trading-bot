package backtest

import (
	"testing"
	"time"

	"trading-go/internal/services"
)

func TestStage07NoFillRequiresCompleteBoundSourceOutcome(t *testing.T) {
	start := time.Date(2026, 2, 14, 3, 45, 0, 0, time.UTC)
	signal := start.Add(14 * time.Minute)
	selected := signal.Add(time.Minute)
	close := selected.Add(time.Minute - time.Millisecond)
	end := start.Add(30 * time.Minute)
	result := Stage05StrategyResult{
		Manifest: RunManifest{Start: canonicalTime(start), End: canonicalTime(end), DatasetManifestID: "dataset-1", ExecutionPolicy: ExecutionPolicy{Version: "backtest-execution-v3"}},
		Artifacts: BacktestArtifacts{
			Orders:    []OrderArtifact{{IntentID: "order-1", OrderID: "order-1", SignalAt: canonicalTime(signal), Symbol: "AVAXUSDT", Side: "buy", Quantity: "1", Metadata: map[string]string{"decision_reference_price": "9.2", "execution_event_at": canonicalTime(selected)}}},
			Decisions: []DecisionArtifact{{IntentID: "order-1", Stage: "broker", Code: "simulated_no_fill_zero_trades", ApprovedQuantity: "1"}},
		},
		NoFills: []SimulatedNoFill{{SchemaVersion: "simulated-no-fill-v1", OrderID: "order-1", Symbol: "AVAXUSDT", Side: "buy", SignalAt: canonicalTime(signal), SelectedOpenAt: canonicalTime(selected), EvaluatedAt: canonicalTime(close), RequestedQuantity: "1", ApprovedQuantity: "1", FilledQuantity: "0", ReferencePrice: "9.2", SelectedOpenPrice: "9.2", ExecutionPolicyVersion: "backtest-execution-v3", DatasetManifestID: "dataset-1", Reason: "simulated_no_fill_zero_trades", LiquidityEvidence: "zero_base_volume"}},
	}
	result.Artifacts.NoFills = append([]SimulatedNoFill(nil), result.NoFills...)
	execution := map[string][]services.OHLCV{"AVAXUSDT": {{OpenTime: selected.UnixMilli(), CloseTime: close.UnixMilli(), Open: 9.2, High: 9.2, Low: 9.2, Close: 9.2, Volume: 0}}}
	primitives, err := stage07NoFillPrimitives(result, execution, start, end)
	if err != nil || len(primitives) != 1 || primitives[0].OrderID != "order-1" {
		t.Fatalf("complete no-fill rejected: primitives=%+v err=%v", primitives, err)
	}
	missingArtifact := result
	missingArtifact.Artifacts.NoFills = nil
	if _, err := stage07NoFillPrimitives(missingArtifact, execution, start, end); err == nil {
		t.Fatal("strategy and artifact no-fill copies diverged")
	}
	for name, mutate := range map[string]func(*Stage05StrategyResult, map[string][]services.OHLCV){
		"missing no-fill":   func(v *Stage05StrategyResult, _ map[string][]services.OHLCV) { v.NoFills = nil },
		"missing rejection": func(v *Stage05StrategyResult, _ map[string][]services.OHLCV) { v.Artifacts.Decisions = nil },
		"duplicate": func(v *Stage05StrategyResult, _ map[string][]services.OHLCV) {
			v.NoFills = append(v.NoFills, v.NoFills[0])
		},
		"extra no-fill": func(v *Stage05StrategyResult, _ map[string][]services.OHLCV) { v.NoFills[0].OrderID = "other" },
		"filled order": func(v *Stage05StrategyResult, _ map[string][]services.OHLCV) {
			v.Artifacts.Fills = []FillArtifact{{OrderID: "order-1"}}
		},
		"wrong dataset": func(v *Stage05StrategyResult, _ map[string][]services.OHLCV) {
			v.NoFills[0].DatasetManifestID = "other"
		},
		"wrong policy": func(v *Stage05StrategyResult, _ map[string][]services.OHLCV) {
			v.NoFills[0].ExecutionPolicyVersion = "backtest-execution-v2"
		},
		"wrong approval": func(v *Stage05StrategyResult, _ map[string][]services.OHLCV) {
			v.NoFills[0].ApprovedQuantity = "0.5"
		},
		"wrong close": func(v *Stage05StrategyResult, _ map[string][]services.OHLCV) {
			v.NoFills[0].EvaluatedAt = canonicalTime(selected.Add(time.Minute))
		},
		"positive volume": func(_ *Stage05StrategyResult, bars map[string][]services.OHLCV) { bars["AVAXUSDT"][0].Volume = 1 },
		"wrong open":      func(_ *Stage05StrategyResult, bars map[string][]services.OHLCV) { bars["AVAXUSDT"][0].Open = 10 },
		"malformed high":  func(_ *Stage05StrategyResult, bars map[string][]services.OHLCV) { bars["AVAXUSDT"][0].High = 0 },
		"outside curve": func(v *Stage05StrategyResult, _ map[string][]services.OHLCV) {
			v.NoFills[0].SignalAt = canonicalTime(start.Add(-time.Minute))
		},
	} {
		t.Run(name, func(t *testing.T) {
			copy := result
			copy.NoFills = append([]SimulatedNoFill(nil), result.NoFills...)
			copy.Artifacts.Decisions = append([]DecisionArtifact(nil), result.Artifacts.Decisions...)
			copy.Artifacts.Fills = append([]FillArtifact(nil), result.Artifacts.Fills...)
			bars := map[string][]services.OHLCV{"AVAXUSDT": append([]services.OHLCV(nil), execution["AVAXUSDT"]...)}
			mutate(&copy, bars)
			copy.Artifacts.NoFills = append([]SimulatedNoFill(nil), copy.NoFills...)
			if _, err := stage07NoFillPrimitives(copy, bars, start, end); err == nil {
				t.Fatal("incomplete or contradictory no-fill accepted")
			}
		})
	}
}

func TestStage07NoFillExtractsActualV3Replay(t *testing.T) {
	config, series := stage05Fixture(nil, []float64{100, 100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	executionBar := config.BenchmarkSeries[1]
	executionBar.CloseTime = executionBar.OpenTime + int64(time.Minute/time.Millisecond) - 1
	executionBar.Volume = 0
	config.ExecutionSeries = map[string][]services.OHLCV{"BTCUSDT": {executionBar}}
	config.ExecutionSeriesRequired = true
	config.ExecutionTimeframe, config.ExecutionTimeframeMins = "1m", 1
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	primitives, err := stage07NoFillPrimitives(result, config.ExecutionSeries, result.Equity[0].Time, result.Equity[len(result.Equity)-1].Time)
	if err != nil || len(primitives) != 1 || primitives[0].OrderID != result.NoFills[0].OrderID {
		t.Fatalf("actual v3 rejection failed source extraction: %+v err=%v", primitives, err)
	}
}
