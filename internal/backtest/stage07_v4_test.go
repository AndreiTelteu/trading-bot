package backtest

import (
	"testing"
	"time"

	"trading-go/internal/services"
)

func stage07V4TestConfig() BacktestConfig {
	return BacktestConfig{DatasetManifestRequired: true, ConstraintResolver: func(string, time.Time) (SymbolConstraints, error) {
		return SymbolConstraints{QuantityStep: 0.1, PriceTick: 0.01, MinQuantity: 0.1}, nil
	}}
}

func TestStage07V4NoFillRecomputesPointInTimeCapacity(t *testing.T) {
	signal := time.Date(2026, 2, 14, 3, 59, 0, 0, time.UTC)
	selected := signal.Add(time.Minute)
	closeAt := selected.Add(time.Minute - time.Millisecond)
	noFill := SimulatedNoFill{SchemaVersion: SimulatedCapacityNoFillSchemaVersion, OrderID: "order-1", Symbol: "BTCUSDT", Side: "buy", SignalAt: canonicalTime(signal), SelectedOpenAt: canonicalTime(selected), EvaluatedAt: canonicalTime(closeAt), RequestedQuantity: "1", ApprovedQuantity: "1", FilledQuantity: "0", ReferencePrice: "10", SelectedOpenPrice: "10", SelectedClosePrice: "11", ExecutionPolicyVersion: "backtest-execution-v4", DatasetManifestID: "dataset-1", Reason: "simulated_no_fill_volume_cap", LiquidityEvidence: "selected_bar_base_volume_10pct", BarVolume: "5.19", CapacityQuantity: "0.5"}
	result := Stage05StrategyResult{
		Manifest:  RunManifest{Start: canonicalTime(signal), End: canonicalTime(selected.Add(2 * time.Minute)), DatasetManifestID: "dataset-1", ExecutionPolicy: ExecutionPolicy{Version: "backtest-execution-v4", MaxParticipationBPS: 1000, NoFillRule: "selected_volume_cap_all_or_none_cancel_v1"}},
		NoFills:   []SimulatedNoFill{noFill},
		Artifacts: BacktestArtifacts{Orders: []OrderArtifact{{IntentID: "order-1", OrderID: "order-1", SignalAt: canonicalTime(signal), Symbol: "BTCUSDT", Side: "buy", Quantity: "1", Metadata: map[string]string{"decision_reference_price": "10", "execution_event_at": canonicalTime(closeAt)}}}, Decisions: []DecisionArtifact{{IntentID: "order-1", Stage: "broker", Code: "simulated_no_fill_volume_cap", ApprovedQuantity: "1"}}, NoFills: []SimulatedNoFill{noFill}},
	}
	bar := services.OHLCV{OpenTime: selected.UnixMilli(), CloseTime: closeAt.UnixMilli(), Open: 10, High: 11, Low: 10, Close: 11, Volume: 5.19}
	bars := map[string][]services.OHLCV{"BTCUSDT": {bar}}
	if got, err := stage07NoFillPrimitives(result, bars, signal, selected.Add(time.Minute), stage07V4TestConfig()); err != nil || len(got) != 1 || got[0].CapacityQuantity != "0.5" {
		t.Fatalf("valid v4 cancellation rejected: %+v %v", got, err)
	}
	for name, change := range map[string]func(*Stage05StrategyResult){
		"cap":             func(v *Stage05StrategyResult) { v.NoFills[0].CapacityQuantity = "0.6" },
		"volume":          func(v *Stage05StrategyResult) { v.NoFills[0].BarVolume = "6" },
		"approved":        func(v *Stage05StrategyResult) { v.NoFills[0].ApprovedQuantity = "0.5" },
		"close":           func(v *Stage05StrategyResult) { v.NoFills[0].SelectedClosePrice = "10" },
		"broker approval": func(v *Stage05StrategyResult) { v.Artifacts.Decisions[0].ApprovedQuantity = "0.5" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := result
			copy.NoFills = append([]SimulatedNoFill(nil), result.NoFills...)
			copy.Artifacts.Decisions = append([]DecisionArtifact(nil), result.Artifacts.Decisions...)
			change(&copy)
			copy.Artifacts.NoFills = append([]SimulatedNoFill(nil), copy.NoFills...)
			if _, err := stage07NoFillPrimitives(copy, bars, signal, selected.Add(time.Minute), stage07V4TestConfig()); err == nil {
				t.Fatal("tampered v4 capacity cancellation accepted")
			}
		})
	}
}

func TestStage07V4FillRequiresSelectedCloseAndCapacity(t *testing.T) {
	signal := time.Date(2026, 2, 14, 3, 59, 0, 0, time.UTC)
	selected := signal.Add(time.Minute)
	closeAt := selected.Add(time.Minute - time.Millisecond)
	result := Stage05StrategyResult{
		Manifest:  RunManifest{ExecutionPolicy: ExecutionPolicy{Version: "backtest-execution-v4", CostVersion: "cost-v1"}},
		Metrics:   ComparableMetrics{Turnover: "10", TurnoverRatio: availableMetric(0.1), FeeCosts: "0", SlippageCosts: "0", FillCount: 1},
		Artifacts: BacktestArtifacts{Orders: []OrderArtifact{{IntentID: "order-1", OrderID: "order-1", SignalAt: canonicalTime(signal), Symbol: "BTCUSDT", Side: "buy", Quantity: "1", Metadata: map[string]string{"execution_event_at": canonicalTime(closeAt)}}}, Decisions: []DecisionArtifact{{IntentID: "order-1", Stage: "broker", Code: "filled", ApprovedQuantity: "1"}}, Fills: []FillArtifact{{FillID: "fill-1", IntentID: "order-1", OrderID: "order-1", FillAt: canonicalTime(closeAt), OrderAt: canonicalTime(signal), Symbol: "BTCUSDT", Side: "buy", Quantity: "1", Price: "10", Fee: "0", ExecutionReferencePrice: "10", CostVersion: "cost-v1"}}},
	}
	bar := services.OHLCV{OpenTime: selected.UnixMilli(), CloseTime: closeAt.UnixMilli(), Open: 10, High: 10, Low: 10, Close: 10, Volume: 10}
	bars := map[string][]services.OHLCV{"BTCUSDT": {bar}}
	if _, fills, inventory, err := stage07EconomicPrimitives(result, 0, 100, bars, stage07V4TestConfig()); err != nil || len(fills) != 1 || inventory.Cash != 90 || len(inventory.Positions) != 1 {
		t.Fatalf("valid v4 fill rejected: %+v %+v %v", fills, inventory, err)
	}
	tooLarge := result
	tooLarge.Artifacts.Fills = append([]FillArtifact(nil), result.Artifacts.Fills...)
	tooLarge.Artifacts.Fills[0].Quantity = "1.1"
	tooLarge.Artifacts.Orders = append([]OrderArtifact(nil), result.Artifacts.Orders...)
	tooLarge.Artifacts.Orders[0].Quantity = "1.1"
	if _, _, _, err := stage07EconomicPrimitives(tooLarge, 0, 100, bars, stage07V4TestConfig()); err == nil {
		t.Fatal("over-cap v4 fill accepted")
	}
	if _, _, _, err := stage07EconomicPrimitives(result, 0, 100, bars); err == nil {
		t.Fatal("v4 fill accepted without point-in-time constraints")
	}
}

func TestStage07V4ExtractsActualStage05CapacityEvidence(t *testing.T) {
	for _, volume := range []float64{5.19, 100} {
		t.Run(stage05InputVolumeLabel(volume), func(t *testing.T) {
			config, series := stage05Fixture(nil, []float64{100, 100, 100, 100}, 10, 5)
			config.ExecutionPolicy.Version = "backtest-execution-v4"
			config.ExecutionPolicy.Liquidity = LiquidityVolumeCapped
			selectedOpen := time.UnixMilli(config.BenchmarkSeries[1].OpenTime)
			selectedClose := selectedOpen.Add(time.Minute - time.Millisecond)
			bar := services.OHLCV{OpenTime: selectedOpen.UnixMilli(), CloseTime: selectedClose.UnixMilli(), Open: 100, High: 101, Low: 100, Close: 101, Volume: volume}
			config.ExecutionSeries = map[string][]services.OHLCV{"BTCUSDT": {bar}}
			config.ExecutionSeriesRequired = true
			config.ExecutionTimeframe, config.ExecutionTimeframeMins = "1m", 1
			config.ConstraintResolver = func(string, time.Time) (SymbolConstraints, error) {
				return SymbolConstraints{QuantityStep: .00000001, PriceTick: .00000001, MinQuantity: .00000001}, nil
			}
			selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := runStage05Strategy(config, series, selected, strategy, true)
			if err != nil {
				t.Fatal(err)
			}
			if volume < 10 {
				if len(result.NoFills) != 1 {
					t.Fatalf("expected capacity cancel: %+v", result.NoFills)
				}
				if _, err := stage07NoFillPrimitives(result, config.ExecutionSeries, result.Equity[0].Time, result.Equity[len(result.Equity)-1].Time, config); err != nil {
					t.Fatalf("fresh no-fill verification: %v", err)
				}
			} else {
				if len(result.Artifacts.Fills) != 1 {
					t.Fatalf("expected fill: %+v", result.Artifacts.Fills)
				}
				if _, _, _, err := stage07EconomicPrimitives(result, 0, 1000, config.ExecutionSeries, config); err != nil {
					t.Fatalf("fresh fill verification: %v fill=%+v order=%+v decisions=%+v", err, result.Artifacts.Fills[0], result.Artifacts.Orders, result.Artifacts.Decisions)
				}
				series["BTCUSDT"] = config.BenchmarkSeries
				if _, err := stage07PrimitivesWithConfig(result, result, 0, 4, series, config.ExecutionSeries, config); err != nil {
					t.Fatalf("fresh candidate and baseline fold verification: %v", err)
				}
				badBaseline := result
				badBaseline.Artifacts.Fills = append([]FillArtifact(nil), result.Artifacts.Fills...)
				badBaseline.Artifacts.Fills[0].Quantity = "11"
				badBaseline.Artifacts.Orders = append([]OrderArtifact(nil), result.Artifacts.Orders...)
				badBaseline.Artifacts.Orders[0].Quantity = "11"
				badBaseline.Artifacts.Decisions = append([]DecisionArtifact(nil), result.Artifacts.Decisions...)
				for i := range badBaseline.Artifacts.Decisions {
					if badBaseline.Artifacts.Decisions[i].Stage == "broker" {
						badBaseline.Artifacts.Decisions[i].ApprovedQuantity = "11"
					}
				}
				if _, err := stage07PrimitivesWithConfig(result, badBaseline, 0, 4, series, config.ExecutionSeries, config); err == nil {
					t.Fatal("baseline over-cap fill bypassed v4 fold verification")
				}
				for name, mutate := range map[string]func(*FillArtifact){
					"price": func(fill *FillArtifact) { fill.Price = "99" },
					"fee":   func(fill *FillArtifact) { fill.Fee = "0" },
				} {
					t.Run(name, func(t *testing.T) {
						copy := result
						copy.Artifacts.Fills = append([]FillArtifact(nil), result.Artifacts.Fills...)
						mutate(&copy.Artifacts.Fills[0])
						if _, _, _, err := stage07EconomicPrimitives(copy, 0, 1000, config.ExecutionSeries, config); err == nil {
							t.Fatal("v4 accepted fill with tampered cost evidence")
						}
					})
				}
			}
		})
	}
}

func stage05InputVolumeLabel(volume float64) string {
	if volume < 10 {
		return "cancel"
	}
	return "fill"
}
