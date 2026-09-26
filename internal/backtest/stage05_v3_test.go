package backtest

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"trading-go/internal/services"
)

func TestStage05V3OneShotZeroVolumeEntryIsTerminal(t *testing.T) {
	config, series := stage05Fixture(nil, []float64{100, 100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	config.BenchmarkSeries[1].Volume = 0
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.NoFills) != 1 || len(first.Artifacts.Fills) != 0 || first.Metrics.FillCount != 0 || first.Metrics.Turnover != "0" {
		t.Fatalf("one-shot entry must remain cash with one rejection: no_fills=%+v fills=%d metrics=%+v", first.NoFills, len(first.Artifacts.Fills), first.Metrics)
	}
	n := first.NoFills[0]
	if n.Reason != "simulated_no_fill_zero_trades" || n.FilledQuantity != "0" || n.ApprovedQuantity == "0" || n.OrderID == "" || n.LiquidityEvidence != "zero_base_volume" || n.EvaluatedAt != canonicalTime(time.UnixMilli(config.BenchmarkSeries[1].CloseTime)) {
		t.Fatalf("incomplete no-fill evidence: %+v", n)
	}
	for _, order := range first.Artifacts.Orders {
		if order.OrderID == n.OrderID {
			goto correlated
		}
	}
	t.Fatal("no-fill has no order")
correlated:
	second, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first.NoFills)
	b, _ := json.Marshal(second.NoFills)
	if string(a) != string(b) {
		t.Fatalf("no-fill evidence changed: %s vs %s", a, b)
	}
}

func TestStage05V3RepeatedNormalDecisionsHaveDistinctOrderIDs(t *testing.T) {
	config, series := stage05Fixture(nil, []float64{100, 100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	config.BenchmarkSeries[1].Volume = 0
	config.BenchmarkSeries[2].Volume = 0
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	planner := Stage05PlannerFunc(func(c Stage05PlanningContext) (Stage05Plan, error) {
		return Stage05Plan{Targets: []string{"BTCUSDT"}, Decide: len(c.Reference) <= 2}, nil
	})
	result, err := runStage05StrategyWithPlanner(config, series, selected, strategy, planner, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.NoFills) != 2 || result.NoFills[0].OrderID == result.NoFills[1].OrderID || len(result.Artifacts.Fills) != 0 {
		t.Fatalf("no-fill decisions reused order identity or filled: %+v", result.NoFills)
	}
}

func TestStage05V3FlatEmptyDecisionKeepsCadenceAndLaterEntry(t *testing.T) {
	config, series := stage05Fixture(nil, []float64{100, 100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	config.ExecutionSeriesRequired = true
	config.ExecutionSeries = map[string][]services.OHLCV{"BTCUSDT": append([]services.OHLCV(nil), config.BenchmarkSeries[2:]...)}
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	firstDecision := stage05CloseAt(config.Start, 0)
	planner := Stage05PlannerFunc(func(c Stage05PlanningContext) (Stage05Plan, error) {
		switch len(c.Reference) {
		case 1:
			return Stage05Plan{Decide: true}, nil
		case 2:
			if !c.LastRebalance.Equal(firstDecision) || len(c.LastTargets) != 0 {
				t.Fatalf("flat decision lost cadence: last=%s targets=%v", c.LastRebalance, c.LastTargets)
			}
			return Stage05Plan{Targets: []string{"BTCUSDT"}, Decide: true}, nil
		default:
			return Stage05Plan{Targets: append([]string(nil), c.LastTargets...), Decide: false}, nil
		}
	})
	result, err := runStage05StrategyWithPlanner(config, series, selected, strategy, planner, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts.Fills) != 1 || result.Artifacts.Fills[0].FillAt != canonicalTime(time.UnixMilli(config.BenchmarkSeries[2].OpenTime)) || len(result.NoFills) != 0 {
		t.Fatalf("later scheduled entry was skipped or deferred: fills=%+v no_fills=%+v", result.Artifacts.Fills, result.NoFills)
	}
	found := false
	for _, decision := range result.Artifacts.Decisions {
		if decision.Code == "empty_target_no_execution" {
			found = true
		}
	}
	if !found {
		t.Fatal("flat decision has no no-action evidence")
	}
}

func TestStage05V3FinalLiquidationZeroVolumeFails(t *testing.T) {
	config, series := stage05Fixture(nil, []float64{100, 100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	config.BenchmarkSeries[3].Volume = 0
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "liquidate"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runStage05Strategy(config, series, selected, strategy, true)
	if !IsStrategyDiagnostic(err, DiagnosticExecutionLiquidity) || !strings.Contains(err.Error(), "simulated_no_fill_zero_trades") {
		t.Fatalf("final liquidation should fail with no-fill evidence: %v", err)
	}
}

func TestStage05V3SelectedBarValidationAndV2Parity(t *testing.T) {
	config, series := stage05Fixture(nil, []float64{100, 100, 100, 100}, 0, 0)
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	config.ExecutionPolicy.Version = "backtest-execution-v2"
	v2, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	v3, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(v3.NoFills) != 0 || len(v3.Artifacts.Fills) != len(v2.Artifacts.Fills) || v3.Metrics.EndingEquity != v2.Metrics.EndingEquity {
		t.Fatalf("positive-volume economics diverged")
	}
	for _, volume := range []float64{-1, math.NaN(), math.Inf(1)} {
		config.BenchmarkSeries[1].Volume = volume
		_, err = runStage05Strategy(config, series, selected, strategy, true)
		if !IsStrategyDiagnostic(err, DiagnosticExecutionLiquidity) {
			t.Fatalf("volume %v: %v", volume, err)
		}
	}
	config.ExecutionSeries = map[string][]services.OHLCV{"BTCUSDT": config.BenchmarkSeries[:1]}
	config.ExecutionSeriesRequired = true
	_, err = runStage05Strategy(config, series, selected, strategy, true)
	if err == nil {
		t.Fatal("missing selected execution bar must fail")
	}
}

func TestStage05V3CannotLabelLegacyReplay(t *testing.T) {
	config, series := stage05Fixture(nil, []float64{100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	_, err := RunBacktest(config, series)
	if err == nil || !strings.Contains(err.Error(), "only by Stage 05/06") {
		t.Fatalf("legacy/shared Stage03 accepted v3 label: %v", err)
	}
}

func TestStage05V3ReplaySettingsPreserveExplicitPolicy(t *testing.T) {
	settings := stage07ReplaySettings(map[string]string{"backtest_execution_policy_version": "backtest-execution-v3", "backtest_dataset_manifest_id": "fixture-manifest"})
	if configuredBacktestExecutionPolicyVersion(settings) != "backtest-execution-v3" || configuredBacktestExecutionPolicyVersion(nil) != "backtest-execution-v2" {
		t.Fatalf("PIT replay lost explicit v3 selection: %+v", settings)
	}
	request := Stage05RunRequest{StrategyID: StrategyBenchmarkHoldID, ExecutionPolicyVersion: "backtest-execution-v3"}
	if err := ValidateStage05RunRequest(request); err != nil {
		t.Fatal(err)
	}
	request.ExecutionPolicyVersion = "backtest-execution-v4"
	if err := ValidateStage05RunRequest(request); err == nil {
		t.Fatal("unsupported policy passed API allowlist")
	}
}

func TestStage05V3MixedSymbolsDoNotRedistribute(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10, 10}, "BBBUSDT": {10, 10, 10, 10}}, []float64{100, 100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	config.ExecutionSeries = map[string][]services.OHLCV{"AAAUSDT": series["AAAUSDT"], "BBBUSDT": series["BBBUSDT"], "BTCUSDT": config.BenchmarkSeries}
	config.ExecutionSeries["AAAUSDT"][1].Volume = 0
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	planner := Stage05PlannerFunc(func(c Stage05PlanningContext) (Stage05Plan, error) {
		if len(c.Reference) == 1 {
			return Stage05Plan{Targets: []string{"AAAUSDT", "BBBUSDT"}, Decide: true}, nil
		}
		return Stage05Plan{Targets: append([]string(nil), c.LastTargets...), Decide: false}, nil
	})
	result, err := runStage05StrategyWithPlanner(config, series, selected, strategy, planner, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.NoFills) != 1 || result.NoFills[0].Symbol != "AAAUSDT" || len(result.Artifacts.Fills) != 1 || result.Artifacts.Fills[0].Symbol != "BBBUSDT" || result.Metrics.Turnover != "500" {
		t.Fatalf("mixed outcomes changed frozen allocation: no_fills=%+v fills=%+v turnover=%s", result.NoFills, result.Artifacts.Fills, result.Metrics.Turnover)
	}
}

func TestStage05V3NoFillDoesNotRefundDecisionTurnoverBudget(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10}, "BBBUSDT": {10, 10, 10}}, []float64{100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	config.StrategyID = StrategyBenchmarkHoldID
	config.StrategyParameters = map[string]string{"target_gross": "1", "turnover_budget": "0.5"}
	_, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	fillAt := time.UnixMilli(series["AAAUSDT"][1].OpenTime)
	var secondCounts []int
	for _, zero := range []bool{false, true} {
		config.ExecutionSeries = map[string][]services.OHLCV{"AAAUSDT": append([]services.OHLCV(nil), series["AAAUSDT"]...), "BBBUSDT": append([]services.OHLCV(nil), series["BBBUSDT"]...)}
		if zero {
			config.ExecutionSeries["AAAUSDT"][1].Volume = 0
		}
		ledger := newBacktestMemoryLedger(config)
		err := rebalanceStage05(ledger, config, strategy, []string{"AAAUSDT", "BBBUSDT"}, nil, nil, nil, map[string]float64{"AAAUSDT": 10, "BBBUSDT": 10}, map[string]float64{"AAAUSDT": 10, "BBBUSDT": 10}, stage05CloseAt(config.Start, 0), fillAt, config.StrategyParameters, "unknown")
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, event := range ledger.events {
			if event.Symbol == "BBBUSDT" {
				count++
			}
		}
		secondCounts = append(secondCounts, count)
		if zero && (len(ledger.noFills) != 1 || ledger.turnover != 0) {
			t.Fatalf("no-fill economics: %+v turnover=%v", ledger.noFills, ledger.turnover)
		}
	}
	if secondCounts[0] != secondCounts[1] || secondCounts[0] != 0 {
		t.Fatalf("later intent enlarged by no-fill: %v", secondCounts)
	}
}

func TestStage05V3FailedSellDoesNotFundLaterBuy(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10}, "BBBUSDT": {10, 10, 10}}, []float64{100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	config.StrategyID = StrategyBenchmarkHoldID
	config.StrategyParameters = map[string]string{"target_gross": "1"}
	config.ExecutionSeries = map[string][]services.OHLCV{"AAAUSDT": series["AAAUSDT"], "BBBUSDT": series["BBBUSDT"]}
	config.ExecutionSeries["AAAUSDT"][1].Volume = 0
	_, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	ledger := newBacktestMemoryLedger(config)
	ledger.cash = 0
	ledger.positions["AAAUSDT"] = &positionState{Symbol: "AAAUSDT", Size: 10, EntryPrice: 10, EntryTime: config.Start}
	fillAt := time.UnixMilli(series["AAAUSDT"][1].OpenTime)
	err = rebalanceStage05(ledger, config, strategy, []string{"BBBUSDT"}, nil, nil, nil, map[string]float64{"AAAUSDT": 10, "BBBUSDT": 10}, map[string]float64{"AAAUSDT": 10, "BBBUSDT": 10}, stage05CloseAt(config.Start, 0), fillAt, config.StrategyParameters, "unknown")
	if len(ledger.noFills) != 1 || ledger.cash != 0 || ledger.positions["AAAUSDT"] == nil || ledger.positions["BBBUSDT"] != nil || ledger.turnover != 0 {
		t.Fatalf("failed sell fabricated proceeds or buy: err=%v cash=%v positions=%v turnover=%v no_fills=%+v", err, ledger.cash, ledger.positions, ledger.turnover, ledger.noFills)
	}
}

func TestStage05V3StopReevaluatesOnlyAtNormalDecision(t *testing.T) {
	config, series := stage05Fixture(nil, []float64{100, 100, 100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	config.BenchmarkSeries[2].Volume = 0
	selected, strategy, err := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "final_policy": "mark_to_market"})
	if err != nil {
		t.Fatal(err)
	}
	planner := Stage05PlannerFunc(func(c Stage05PlanningContext) (Stage05Plan, error) {
		switch len(c.Reference) {
		case 1:
			return Stage05Plan{Targets: []string{"BTCUSDT"}, Decide: true}, nil
		case 2:
			return Stage05Plan{Targets: nil, Decide: true, RiskStopOnly: true, ExitReasons: map[string]ExitReasonTrace{"BTCUSDT": {Primary: "risk_stop", Concurrent: []string{"time_stop"}}}}, nil
		case 3:
			return Stage05Plan{Targets: nil, Decide: false}, nil
		case 4:
			return Stage05Plan{Targets: nil, Decide: true}, nil
		default:
			return Stage05Plan{Decide: false}, nil
		}
	})
	result, err := runStage05StrategyWithPlanner(config, series, selected, strategy, planner, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.NoFills) != 1 || result.NoFills[0].Side != "sell" || len(result.Artifacts.Fills) != 2 || len(result.Trades) != 1 {
		t.Fatalf("stop cadence changed: no_fills=%+v fills=%+v trades=%+v", result.NoFills, result.Artifacts.Fills, result.Trades)
	}
	trace := result.ExitReasons[canonicalTime(stage05CloseAt(config.Start, 1))+"|BTCUSDT"]
	if trace.Primary != "risk_stop" || len(trace.Concurrent) != 1 || trace.RequestedQuantity == "0" || trace.ApprovedQuantity == "0" || trace.FilledQuantity != "0" || trace.ResultingExposure == "0" {
		t.Fatalf("stop rejection lost Q/Q/0 and exposure: %+v", trace)
	}
}
