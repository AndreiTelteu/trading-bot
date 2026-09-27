package backtest

import (
	"strings"
	"testing"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/validation"
)

func TestStage07PrimitivesAttributeActualFillCostsPerTrade(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entry, exit := base.Add(time.Minute), base.Add(2*time.Minute)
	candidate := Stage05StrategyResult{
		Metrics: ComparableMetrics{StartingCapital: "100", EndingEquity: "100", AverageGrossExposure: availableMetric(.5), Turnover: "25", TurnoverRatio: availableMetric(.25), FeeCosts: "3", SlippageCosts: "2", FillCount: 2, TradeCount: 1},
		Equity:  []EquityPoint{{Time: base, Value: 100}, {Time: exit, Value: 100}},
		Trades:  []Trade{{Symbol: "AAA", EntryTime: entry, ExitTime: exit, EntryPrice: 11, ExitPrice: 14, Size: 1, Pnl: 0, RegimeState: "risk_on"}},
		Artifacts: BacktestArtifacts{Ledger: []LedgerArtifact{{CashAfter: "88"}, {CashAfter: "100"}}, Fills: []FillArtifact{
			{FillID: "buy", FillAt: entry.Format(time.RFC3339Nano), Symbol: "AAA", Side: "buy", Fee: "1", Price: "11", Quantity: "1", ExecutionReferencePrice: "10"},
			{FillID: "sell", FillAt: exit.Format(time.RFC3339Nano), Symbol: "AAA", Side: "sell", Fee: "2", Price: "14", Quantity: "1", ExecutionReferencePrice: "15"},
		}},
	}
	baseline := Stage05StrategyResult{Metrics: ComparableMetrics{AverageGrossExposure: availableMetric(.5), TurnoverRatio: availableMetric(.25)}, Equity: []EquityPoint{{Time: base, Value: 100}, {Time: exit, Value: 100}}}
	series := map[string][]services.OHLCV{"AAA": {{OpenTime: base.UnixMilli(), Open: 10, Close: 10, Volume: 100}}}
	execution := map[string][]services.OHLCV{"AAA": {{OpenTime: entry.UnixMilli(), Open: 11, Close: 11, Volume: 100}, {OpenTime: exit.UnixMilli(), Open: 14, Close: 14, Volume: 100}}}
	primitives, err := stage07Primitives(candidate, baseline, 0, 4, series, execution)
	if err != nil {
		t.Fatal(err)
	}
	if got := primitives.Trades[0]; got.Cost != 5 || got.GrossPnL != 5 || got.NetPnL != 0 {
		t.Fatalf("cost was not attributed to actual fills: %+v", got)
	}
	if len(primitives.Fills) != 2 || primitives.Fills[0].Notional != 11 || primitives.Fills[1].Notional != 14 {
		t.Fatalf("fill turnover evidence missing: %+v", primitives.Fills)
	}
	for name, bars := range map[string][]services.OHLCV{
		"missing execution bar": nil,
		"zero execution volume": {{OpenTime: entry.UnixMilli(), Open: 11, Volume: 0}},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := stage07EconomicPrimitives(candidate, 0, 100, map[string][]services.OHLCV{"AAA": bars})
			if err == nil || !strings.Contains(err.Error(), "execution bar ") {
				t.Fatalf("execution liquidity failure was not explicit: %v", err)
			}
		})
	}
}

func TestStage07SourceBindsV3ExecutionPolicyAndRule(t *testing.T) {
	policy := ExecutionPolicy{Version: "backtest-execution-v3", CostVersion: "backtest-cost-v1", NoFillRule: "selected_zero_base_volume_cancel_at_bar_close_v1"}
	settings := map[string]string{"backtest_execution_policy_version": policy.Version}
	semantics := map[string]string{"execution_policy_version": policy.Version, "no_fill_rule": "selected_zero_base_volume_cancel_at_bar_close_v1"}
	if err := stage07SourceExecutionIdentity(policy, policy, policy, settings, semantics); err != nil {
		t.Fatal(err)
	}
	wrongRule := policy
	wrongRule.NoFillRule = ""
	if err := stage07SourceExecutionIdentity(wrongRule, wrongRule, wrongRule, settings, semantics); err == nil {
		t.Fatal("source policies with no v3 no-fill rule were accepted")
	}
	for name, mutate := range map[string]func(*ExecutionPolicy, *ExecutionPolicy, map[string]string, map[string]string){
		"baseline policy": func(_ *ExecutionPolicy, baseline *ExecutionPolicy, _ map[string]string, _ map[string]string) {
			baseline.Version = "backtest-execution-v2"
		},
		"source cost": func(source *ExecutionPolicy, _ *ExecutionPolicy, _ map[string]string, _ map[string]string) {
			source.CostVersion = "other"
		},
		"source rule": func(source *ExecutionPolicy, _ *ExecutionPolicy, _ map[string]string, _ map[string]string) {
			source.NoFillRule = "other"
		},
		"replay setting": func(_ *ExecutionPolicy, _ *ExecutionPolicy, values map[string]string, _ map[string]string) {
			values["backtest_execution_policy_version"] = "backtest-execution-v2"
		},
		"manifest rule": func(_ *ExecutionPolicy, _ *ExecutionPolicy, _ map[string]string, values map[string]string) {
			values["no_fill_rule"] = "other"
		},
		"manifest version": func(_ *ExecutionPolicy, _ *ExecutionPolicy, _ map[string]string, values map[string]string) {
			values["execution_policy_version"] = "backtest-execution-v2"
		},
	} {
		t.Run(name, func(t *testing.T) {
			source, baseline := policy, policy
			replay := map[string]string{"backtest_execution_policy_version": policy.Version}
			manifest := map[string]string{"execution_policy_version": policy.Version, "no_fill_rule": "selected_zero_base_volume_cancel_at_bar_close_v1"}
			mutate(&source, &baseline, replay, manifest)
			if err := stage07SourceExecutionIdentity(policy, baseline, source, replay, manifest); err == nil {
				t.Fatal("mismatched v3 execution source was accepted")
			}
		})
	}
}

func TestStage07V4SourceRequiresExactCapacitySemantics(t *testing.T) {
	policy := ExecutionPolicy{Version: "backtest-execution-v4", Timing: ExecutionSelectedBarClose, Liquidity: LiquidityVolumeCapped, MaxParticipationBPS: 1000, NoFillRule: "selected_volume_cap_all_or_none_cancel_v1"}
	settings := map[string]string{"backtest_execution_policy_version": policy.Version}
	semantics := map[string]string{"execution_policy_version": policy.Version, "no_fill_rule": policy.NoFillRule, "timing": string(policy.Timing), "liquidity": string(policy.Liquidity), "max_participation_bps": "1000"}
	if err := stage07SourceExecutionIdentity(policy, policy, policy, settings, semantics); err != nil {
		t.Fatalf("complete v4 source rejected: %v", err)
	}
	for _, key := range []string{"execution_policy_version", "no_fill_rule", "timing", "liquidity", "max_participation_bps"} {
		copy := cloneStringMap(semantics)
		delete(copy, key)
		if err := stage07SourceExecutionIdentity(policy, policy, policy, settings, copy); err == nil {
			t.Fatalf("v4 source without %s was accepted", key)
		}
	}
	wrong := policy
	wrong.MaxParticipationBPS = 1001
	if err := stage07SourceExecutionIdentity(wrong, wrong, wrong, settings, semantics); err == nil {
		t.Fatal("modified capacity limit was accepted")
	}
}

func TestStage07EconomicPrimitivesRepeatedBuysAndSameTimePartialCloses(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	at := []time.Time{base, base.Add(time.Minute), base.Add(2 * time.Minute), base.Add(3 * time.Minute)}
	result := Stage05StrategyResult{
		Metrics: ComparableMetrics{Turnover: "460.5", TurnoverRatio: availableMetric(.4605), FeeCosts: "5", SlippageCosts: "4", FillCount: 5, TradeCount: 3},
		Trades: []Trade{
			{Symbol: "AAA", EntryTime: at[0], ExitTime: at[2], EntryPrice: 105, ExitPrice: 120, Size: .5, Pnl: 6.25},
			{Symbol: "AAA", EntryTime: at[0], ExitTime: at[2], EntryPrice: 105, ExitPrice: 121, Size: .5, Pnl: 6.75},
			{Symbol: "AAA", EntryTime: at[0], ExitTime: at[3], EntryPrice: 105, ExitPrice: 130, Size: 1, Pnl: 22.5},
		},
		Artifacts: BacktestArtifacts{Fills: []FillArtifact{
			{FillID: "b1", FillAt: at[0].Format(time.RFC3339Nano), Symbol: "AAA", Side: "buy", Quantity: "1", Price: "100", Fee: "1", ExecutionReferencePrice: "99"},
			{FillID: "b2", FillAt: at[1].Format(time.RFC3339Nano), Symbol: "AAA", Side: "buy", Quantity: "1", Price: "110", Fee: "2", ExecutionReferencePrice: "109"},
			{FillID: "s1", FillAt: at[2].Format(time.RFC3339Nano), Symbol: "AAA", Side: "sell", Quantity: "0.5", Price: "120", Fee: "0.5", ExecutionReferencePrice: "121"},
			{FillID: "s2", FillAt: at[2].Format(time.RFC3339Nano), Symbol: "AAA", Side: "sell", Quantity: "0.5", Price: "121", Fee: "0.5", ExecutionReferencePrice: "122"},
			{FillID: "s3", FillAt: at[3].Format(time.RFC3339Nano), Symbol: "AAA", Side: "sell", Quantity: "1", Price: "130", Fee: "1", ExecutionReferencePrice: "131"},
		}},
	}
	series := map[string][]services.OHLCV{"AAA": {{OpenTime: at[0].UnixMilli(), Volume: 100}, {OpenTime: at[1].UnixMilli(), Volume: 100}, {OpenTime: at[2].UnixMilli(), Volume: 100}, {OpenTime: at[3].UnixMilli(), Volume: 100}}}
	trades, fills, inventory, err := stage07EconomicPrimitives(result, 0, 1000, series)
	if err != nil {
		t.Fatal(err)
	}
	if len(trades) != 3 || len(fills) != 5 || len(inventory.Positions) != 0 || inventory.Cash != 1035.5 || trades[0].Cost != 2.25 || trades[1].Cost != 2.25 || trades[2].Cost != 4.5 {
		t.Fatalf("incorrect partial cost allocation: trades=%+v fills=%+v", trades, fills)
	}
	partial := result
	partial.Trades = partial.Trades[:2]
	partial.Artifacts.Fills = partial.Artifacts.Fills[:4]
	partial.Metrics.Turnover, partial.Metrics.TurnoverRatio = "330.5", availableMetric(.3305)
	partial.Metrics.FeeCosts, partial.Metrics.SlippageCosts = "4", "3"
	partial.Metrics.FillCount, partial.Metrics.TradeCount = 4, 2
	_, _, inventory, err = stage07EconomicPrimitives(partial, 0, 1000, series)
	if err != nil || len(inventory.Positions) != 1 || inventory.Positions[0] != (stage07EndPosition{Symbol: "AAA", Quantity: 1, EntryPrice: 105, EntryFee: 1.5, EntrySlippage: 1}) || inventory.Cash != 906.5 {
		t.Fatalf("verified end inventory: %+v err=%v", inventory, err)
	}
	for name, mutate := range map[string]func(*Stage05StrategyResult){
		"missing sell":      func(v *Stage05StrategyResult) { v.Artifacts.Fills = v.Artifacts.Fills[:4] },
		"duplicate fill id": func(v *Stage05StrategyResult) { v.Artifacts.Fills[3].FillID = "s1" },
		"extra sell": func(v *Stage05StrategyResult) {
			v.Artifacts.Fills = append(v.Artifacts.Fills, v.Artifacts.Fills[4])
			v.Artifacts.Fills[5].FillID = "s4"
		},
		"wrong turnover":    func(v *Stage05StrategyResult) { v.Metrics.Turnover = "1" },
		"missing reference": func(v *Stage05StrategyResult) { v.Artifacts.Fills[0].ExecutionReferencePrice = "" },
	} {
		t.Run(name, func(t *testing.T) {
			copy := result
			copy.Artifacts.Fills = append([]FillArtifact(nil), result.Artifacts.Fills...)
			mutate(&copy)
			if _, _, _, err := stage07EconomicPrimitives(copy, 0, 1000, series); err == nil {
				t.Fatal("invalid fill evidence was accepted")
			}
		})
	}
}

func TestStage07BarAtRequiresExactSortedExecutionTimestamp(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := make([]services.OHLCV, 2000)
	for i := range bars {
		bars[i] = services.OHLCV{OpenTime: base.Add(time.Duration(i) * time.Minute).UnixMilli(), Open: float64(i + 1)}
	}
	if bar, ok := stage07BarAt(bars, base.Add(1700*time.Minute)); !ok || bar.Open != 1701 {
		t.Fatalf("exact execution bar unavailable: %+v %v", bar, ok)
	}
	if _, ok := stage07BarAt(bars, base.Add(1700*time.Minute+time.Second)); ok {
		t.Fatal("non-exact execution time matched a bar")
	}
	if _, ok := stage07BarAt(bars, base.Add(1700*time.Minute+time.Nanosecond)); ok {
		t.Fatal("sub-millisecond execution time matched a bar")
	}
	if _, ok := stage07BarAt(bars, base.Add(-time.Minute)); ok {
		t.Fatal("time before execution coverage matched a bar")
	}
}

func TestStage07EconomicPrimitivesReturnsSortedOpenInventoryAndCash(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	result := Stage05StrategyResult{
		Metrics: ComparableMetrics{Turnover: "300", TurnoverRatio: availableMetric(.3), FeeCosts: "0", SlippageCosts: "0", FillCount: 2},
		Artifacts: BacktestArtifacts{Fills: []FillArtifact{
			{FillID: "z", FillAt: at.Format(time.RFC3339Nano), Symbol: "ZZZ", Side: "buy", Quantity: "1", Price: "100", Fee: "0", ExecutionReferencePrice: "100"},
			{FillID: "a", FillAt: at.Format(time.RFC3339Nano), Symbol: "AAA", Side: "buy", Quantity: "1", Price: "200", Fee: "0", ExecutionReferencePrice: "200"},
		}},
	}
	execution := map[string][]services.OHLCV{"AAA": {{OpenTime: at.UnixMilli(), Volume: 100}}, "ZZZ": {{OpenTime: at.UnixMilli(), Volume: 100}}}
	_, _, inventory, err := stage07EconomicPrimitives(result, 0, 1000, execution)
	if err != nil || inventory.Cash != 700 || len(inventory.Positions) != 2 || inventory.Positions[0].Symbol != "AAA" || inventory.Positions[1].Symbol != "ZZZ" {
		t.Fatalf("inventory=%+v err=%v", inventory, err)
	}
}

func TestStage07PrimitivesCausallyAlignBenchmarkAsOfCandidateClock(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	candidate := Stage05StrategyResult{Metrics: ComparableMetrics{StartingCapital: "100", EndingEquity: "100", AverageGrossExposure: availableMetric(.5), Turnover: "0", TurnoverRatio: availableMetric(0), FeeCosts: "0", SlippageCosts: "0"}, Equity: []EquityPoint{{Time: base, Value: 100}, {Time: base.Add(2 * time.Hour), Value: 100}}}
	baseline := Stage05StrategyResult{Metrics: ComparableMetrics{AverageGrossExposure: availableMetric(.5), TurnoverRatio: availableMetric(0)}, Equity: []EquityPoint{{Time: base, Value: 100}, {Time: base.Add(time.Hour), Value: 101}, {Time: base.Add(2 * time.Hour), Value: 103}}}
	primitives, err := stage07Primitives(candidate, baseline, 0, 2, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(primitives.Curve) != 2 || primitives.Curve[0].Benchmark != 100 || primitives.Curve[1].Benchmark != 103 {
		t.Fatalf("curve=%+v", primitives.Curve)
	}
	baseline.Equity = baseline.Equity[:2]
	if _, err := stage07Primitives(candidate, baseline, 0, 2, nil, nil); err == nil {
		t.Fatal("benchmark ending before the candidate was accepted")
	}
}

func TestStage07PrimitivesUsePredeclaredComparabilityTolerances(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	candidate := Stage05StrategyResult{Metrics: ComparableMetrics{StartingCapital: "100", EndingEquity: "100", AverageGrossExposure: availableMetric(.10), AverageNetExposure: availableMetric(.10), Turnover: "0", TurnoverRatio: availableMetric(0), FeeCosts: "0", SlippageCosts: "0"}, Equity: []EquityPoint{{Time: base, Value: 100}, {Time: base.Add(time.Hour), Value: 100}}}
	baseline := Stage05StrategyResult{Metrics: ComparableMetrics{AverageGrossExposure: availableMetric(.12), TurnoverRatio: availableMetric(0)}, Equity: []EquityPoint{{Time: base, Value: 100}, {Time: base.Add(time.Hour), Value: 100}}}
	policy := validation.BaselineComparabilityPolicy{MaxGrossExposureDifference: .02, MaxTurnoverRelativeDiff: .10}
	if _, err := stage07Primitives(candidate, baseline, 0, 2, nil, nil, policy); err != nil {
		t.Fatal(err)
	}
	baseline.Metrics.AverageGrossExposure = availableMetric(.121)
	if _, err := stage07Primitives(candidate, baseline, 0, 2, nil, nil, policy); err == nil {
		t.Fatal("out-of-tolerance exposure was accepted")
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
