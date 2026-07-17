package backtest

import (
	"reflect"
	"testing"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/tradingcore"
)

func TestTrendMomentumDescriptorBoundsAblationsAndFence(t *testing.T) {
	descriptor := descriptorByID(t, StrategyTrendMomentumCandidate)
	if descriptor.Baseline || !descriptor.ResearchOnly || descriptor.FactorTraceSchema != FactorTraceSchemaVersion || descriptor.DecisionCadence != "4h" || descriptor.RebalanceCadence != "24h" || !descriptor.BenchmarkRequired {
		t.Fatalf("descriptor=%+v", descriptor)
	}
	for _, variant := range []string{"absolute_trend_only", "relative_momentum_only", "combined"} {
		for _, normalized := range []string{"false", "true"} {
			if _, _, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.0.0", map[string]string{"variant": variant, "vol_normalization": normalized}); err != nil {
				t.Fatalf("variant=%s normalized=%s: %v", variant, normalized, err)
			}
		}
	}
	if _, _, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.0.0", map[string]string{"rebalance": "6h"}); !IsStrategyDiagnostic(err, DiagnosticInvalidParameter) {
		t.Fatalf("arbitrary rebalance accepted: %v", err)
	}
	_, _, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.0.0", map[string]string{"execution_intent": "live_submit"})
	if !IsStrategyDiagnostic(err, DiagnosticExecutionFenced) {
		t.Fatalf("live fence=%v", err)
	}
}

func TestTrendMomentumStableTieCapsAndShadowIsolation(t *testing.T) {
	selected := selectedCandidate(t, map[string]string{"lookback_bars": "20", "trend_bars": "20", "regime_bars": "20", "top_n": "2", "max_positions": "2", "position_cap": "0.2", "cash_reserve": "0.25", "max_gross": "0.75", "max_net": "0.75", "target_gross": "0.75", "turnover_budget": "0.25"})
	benchmark := rising4H(100, 61, 1)
	tied := rising4H(20, 61, .2)
	series := map[string][]services.OHLCV{"ZZZUSDT": tied, "AAAUSDT": tied}
	ctx := candidateContext(t, selected, benchmark, series, nil)
	ctx.Replays[0].members = []ReplayMember{{Symbol: "ZZZUSDT", AssetID: "asset-a", ExchangeSymbolID: "symbol-z", Stage: "active"}, {Symbol: "AAAUSDT", AssetID: "asset-b", ExchangeSymbolID: "symbol-a", Stage: "active"}}
	plan, err := (trendMomentumPlanner{}).Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Rankings) != 2 || plan.Rankings[0].AssetID != "asset-a" {
		t.Fatalf("stable identity tie=%+v", plan.Rankings)
	}
	total := 0.0
	for _, weight := range plan.TargetWeights {
		total += weight
		if weight > .2+1e-12 {
			t.Fatalf("position cap=%f", weight)
		}
	}
	if total > .75+1e-12 || len(plan.Targets) > 2 {
		t.Fatalf("portfolio caps targets=%v weights=%v", plan.Targets, plan.TargetWeights)
	}
	shadow := selectedCandidate(t, map[string]string{"lookback_bars": "20", "trend_bars": "20", "regime_bars": "20", "top_n": "2", "max_positions": "2", "position_cap": "0.2", "model_observation": "0.99", "execution_intent": "shadow"})
	shadowContext := ctx
	shadowContext.Selected = shadow
	shadowPlan, err := (trendMomentumPlanner{}).Plan(shadowContext)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Targets, shadowPlan.Targets) || !reflect.DeepEqual(plan.TargetWeights, shadowPlan.TargetWeights) {
		t.Fatalf("shadow model changed rule decision")
	}
}

func TestTrendMomentumFactorTraceGolden(t *testing.T) {
	selected := selectedCandidate(t, map[string]string{"lookback_bars": "20", "trend_bars": "20", "regime_bars": "20", "vol_normalization": "false"})
	ctx := candidateContext(t, selected, rising4H(100, 61, 1), map[string][]services.OHLCV{"AAAUSDT": rising4H(20, 61, .2)}, nil)
	plan, err := (trendMomentumPlanner{}).Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Factors) != 1 {
		t.Fatalf("factors=%+v", plan.Factors)
	}
	factor := plan.Factors[0]
	wantMomentum := 4.0 / 28.0
	if absFloat(factor.LookbackReturns["20x4h"]-wantMomentum) > 1e-12 || factor.CompositeMomentum != factor.NormalizedMomentum || absFloat(factor.AbsoluteTrendMean-30.1) > 1e-12 || !factor.AbsoluteTrend || factor.RelativeRank != 1 || factor.Reason != "selected" || absFloat(factor.TargetWeight-.25) > 1e-12 {
		t.Fatalf("golden factor=%+v", factor)
	}
}

func TestTrendMomentumRegimesMissingWarmupAndExitPrecedence(t *testing.T) {
	selected := selectedCandidate(t, map[string]string{"lookback_bars": "20", "trend_bars": "20", "regime_bars": "20", "position_cap": "0.5", "turnover_budget": "1"})
	asset := rising4H(20, 61, .2)
	tests := []struct {
		name      string
		benchmark []services.OHLCV
		want      string
		gross     float64
	}{
		{"risk_on", rising4H(100, 61, 1), "risk_on", .75},
		{"neutral", flat4H(100, 61), "neutral", .25},
		{"risk_off", rising4H(160, 61, -1), "risk_off", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := candidateContext(t, selected, test.benchmark, map[string][]services.OHLCV{"AAAUSDT": asset}, nil)
			if test.want == "risk_off" {
				ctx.Positions = map[string]float64{"AAAUSDT": 1}
				ctx.PositionEntries = map[string]float64{"AAAUSDT": 100}
				ctx.Marks = map[string]float64{"AAAUSDT": 80}
			}
			plan, err := (trendMomentumPlanner{}).Plan(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Regime != test.want {
				t.Fatalf("regime=%s", plan.Regime)
			}
			if plan.RegimeObservation == nil || plan.RegimeObservation.TargetGross != test.gross || plan.RegimeObservation.TargetNet != test.gross {
				t.Fatalf("regime exposure=%+v", plan.RegimeObservation)
			}
			if test.want == "risk_off" {
				exit := plan.ExitReasons["AAAUSDT"]
				if exit.Primary != "risk_stop" || len(exit.Concurrent) == 0 || exit.Concurrent[0] != "regime_risk_off" {
					t.Fatalf("precedence=%+v", exit)
				}
			}
		})
	}
	ctx := candidateContext(t, selected, rising4H(100, 61, 1), map[string][]services.OHLCV{"AAAUSDT": rising4H(20, 5, .2)}, nil)
	plan, err := (trendMomentumPlanner{}).Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != 0 || len(plan.Diagnostics) != 1 || plan.Diagnostics[0].Code != DiagnosticInsufficientWarmup {
		t.Fatalf("missing-data evidence=%+v %+v", plan.Targets, plan.Diagnostics)
	}
	stale := rising4H(20, 61, .2)
	for i := range stale {
		stale[i].OpenTime -= int64(8 * time.Hour / time.Millisecond)
		stale[i].CloseTime -= int64(8 * time.Hour / time.Millisecond)
	}
	ctx = candidateContext(t, selected, rising4H(100, 61, 1), map[string][]services.OHLCV{"AAAUSDT": stale}, nil)
	plan, err = (trendMomentumPlanner{}).Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Diagnostics) != 1 || plan.Diagnostics[0].Code != DiagnosticStaleEvidence {
		t.Fatalf("stale evidence=%+v", plan.Diagnostics)
	}
}

func TestTrendMomentumIgnoresFutureObservations(t *testing.T) {
	selected := selectedCandidate(t, map[string]string{"lookback_bars": "20", "trend_bars": "20", "regime_bars": "20"})
	benchmark, asset := rising4H(100, 61, 1), rising4H(20, 61, .2)
	ctx := candidateContext(t, selected, benchmark, map[string][]services.OHLCV{"AAAUSDT": asset}, nil)
	before, err := (trendMomentumPlanner{}).Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	future := stage05Bars(ctx.At.Add(time.Millisecond), []float64{999999, 1, 999999, 1})
	ctx.Series = map[string][]services.OHLCV{"AAAUSDT": append(append([]services.OHLCV(nil), asset...), future...)}
	after, err := (trendMomentumPlanner{}).Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Targets, after.Targets) || !reflect.DeepEqual(before.TargetWeights, after.TargetWeights) || !reflect.DeepEqual(before.Factors, after.Factors) {
		t.Fatal("future observations changed the decision")
	}
}

func TestTrendMomentumRiskStopRunsBetweenScheduledRebalances(t *testing.T) {
	selected := selectedCandidate(t, map[string]string{"lookback_bars": "20", "trend_bars": "20", "regime_bars": "20", "rebalance": "24h", "hard_stop": "0.1"})
	ctx := candidateContext(t, selected, rising4H(100, 61, 1), map[string][]services.OHLCV{"AAAUSDT": rising4H(20, 61, .2)}, map[string]float64{"AAAUSDT": 1})
	ctx.LastTargets, ctx.LastRebalance = []string{"AAAUSDT"}, ctx.At.Add(-4*time.Hour)
	ctx.PositionEntries, ctx.Marks = map[string]float64{"AAAUSDT": 100}, map[string]float64{"AAAUSDT": 89}
	plan, err := (trendMomentumPlanner{}).Plan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.RiskStopOnly || !plan.Decide || len(plan.Targets) != 0 || plan.ExitReasons["AAAUSDT"].Primary != "risk_stop" {
		t.Fatalf("stop plan=%+v", plan)
	}
}

func TestTrendMomentumTurnoverBudgetAndImmaterialSkip(t *testing.T) {
	selected := selectedCandidate(t, map[string]string{"turnover_budget": "0.1", "skip_delta": "0.005", "max_gross": "0.75", "max_net": "0.75"})
	config, _ := stage05Fixture(map[string][]float64{"AAAUSDT": linearPrices(10, 10)}, linearPrices(100, 10), 0, 0)
	config.StrategyID, config.StrategyVersion, config.StrategyParameters = StrategyTrendMomentumCandidate, "1.0.0", selected.Parameters
	ledger := newBacktestMemoryLedger(config)
	at := config.Start.Add(time.Hour)
	marks, fills := map[string]float64{"AAAUSDT": 10}, map[string]float64{"AAAUSDT": 10}
	if err := rebalanceStage05(ledger, config, tradingcore.TargetAllocationStrategy{}, []string{"AAAUSDT"}, map[string]float64{"AAAUSDT": .004}, nil, nil, marks, fills, at, at.Add(time.Minute), selected.Parameters, "risk_on"); err != nil {
		t.Fatal(err)
	}
	if len(ledger.events) != 0 {
		t.Fatalf("immaterial delta traded: %+v", ledger.events)
	}
	if err := rebalanceStage05(ledger, config, tradingcore.TargetAllocationStrategy{}, []string{"AAAUSDT"}, map[string]float64{"AAAUSDT": .2}, nil, nil, marks, fills, at.Add(24*time.Hour), at.Add(24*time.Hour+time.Minute), selected.Parameters, "risk_on"); err != nil {
		t.Fatal(err)
	}
	if len(ledger.events) != 1 || ledger.turnover > 100+1e-8 {
		t.Fatalf("turnover budget events=%+v turnover=%f", ledger.events, ledger.turnover)
	}
}

func TestTrendMomentumComparisonPreservesBaselinesMetadataAndGovernance(t *testing.T) {
	count := 16*35 + 8
	benchmarkPrices, assetPrices := make([]float64, count), make([]float64, count)
	for i := range benchmarkPrices {
		benchmarkPrices[i] = 100 + float64(i)*.1
		assetPrices[i] = 20 + float64(i)*.04
	}
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": assetPrices}, benchmarkPrices, 8, 3)
	config.EconomicAssetIdentities = map[string]string{"AAAUSDT": "asset-a", "BTCUSDT": "asset-btc"}
	config.SymbolIdentities = map[string]string{"AAAUSDT": "symbol-a", "BTCUSDT": "symbol-btc"}
	config.ReplaySnapshots = nil
	for at := config.Start; at.Before(config.End); at = at.Add(24 * time.Hour) {
		config.ReplaySnapshots = append(config.ReplaySnapshots, ReplaySnapshot{Timestamp: at, ObservedComplete: true, Members: []ReplayMember{{Symbol: "AAAUSDT", AssetID: "asset-a", ExchangeSymbolID: "symbol-a", Stage: "active"}}})
	}
	comparison, err := RunStage05Comparison(config, series, Stage05RunRequest{StrategyID: StrategyTrendMomentumCandidate, StrategyVersion: "1.0.0", Parameters: map[string]string{"lookback_bars": "20", "trend_bars": "20", "regime_bars": "20", "turnover_budget": "1"}, TargetGrossExposure: "0.75", MaxNetExposure: "0.75", FinalPolicy: "liquidate", AllowInMemoryFixture: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{StrategyCashID, StrategyBenchmarkHoldID, StrategyBenchmarkTrendID, StrategyEqualWeightID, StrategyMomentumID, StrategyTrendMomentumCandidate} {
		if _, ok := comparison.Results[required]; !ok {
			t.Fatalf("missing baseline %s", required)
		}
	}
	if comparison.Governance.PromotionAllowed || !containsString(comparison.Governance.Reasons, "pending_stage07_validation") {
		t.Fatalf("governance=%+v", comparison.Governance)
	}
	result := comparison.Results[StrategyTrendMomentumCandidate]
	if result.Manifest.PromotionAllowed || result.Manifest.ExecutionIntent != "research" || len(result.Factors) == 0 || len(result.Sensitivity) != 1 || !result.Metrics.Reconciled {
		t.Fatalf("candidate evidence=%+v factors=%d sensitivity=%+v", result.Manifest, len(result.Factors), result.Sensitivity)
	}
	if result.Parity == nil || !reflect.DeepEqual(result.Parity.BacktestApproved, result.Parity.PaperShadowApproved) || len(result.Parity.LiveDryRunRequests) != len(result.Parity.BacktestApproved) || result.Parity.ExternalSubmissionPerformed {
		t.Fatalf("parity=%+v", result.Parity)
	}
	for _, code := range result.Parity.LiveFenceCodes {
		if code != "exchange_execution_fenced" {
			t.Fatalf("live fence=%v", result.Parity.LiveFenceCodes)
		}
	}
	if len(result.Artifacts.Orders) == 0 || result.Artifacts.Orders[0].Metadata["factor_trace_schema"] != FactorTraceSchemaVersion || result.Artifacts.Orders[0].Metadata["strategy_version"] != "1.0.0" || result.Artifacts.Orders[0].Metadata["factor_trace"] == "" {
		t.Fatalf("order metadata=%+v", result.Artifacts.Orders)
	}
	if comparison.CandidateEvidence == nil || len(comparison.CandidateEvidence.FactorTraces) == 0 {
		t.Fatalf("bounded API evidence missing")
	}
	encoded, err := MarshalComparisonArtifact(comparison)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalComparisonArtifact(encoded)
	if err != nil || decoded.CandidateEvidence == nil {
		t.Fatalf("candidate evidence roundtrip err=%v evidence=%+v", err, decoded.CandidateEvidence)
	}
}

func descriptorByID(t *testing.T, id string) StrategyDescriptor {
	t.Helper()
	for _, descriptor := range DefaultStrategyRegistry.List() {
		if descriptor.ID == id {
			return descriptor
		}
	}
	t.Fatalf("descriptor %s missing", id)
	return StrategyDescriptor{}
}
func selectedCandidate(t *testing.T, overrides map[string]string) SelectedStrategy {
	t.Helper()
	selected, _, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.0.0", overrides)
	if err != nil {
		t.Fatal(err)
	}
	return selected
}

func candidateContext(t *testing.T, selected SelectedStrategy, benchmark []services.OHLCV, series map[string][]services.OHLCV, positions map[string]float64) Stage05PlanningContext {
	t.Helper()
	at := time.UnixMilli(benchmark[len(benchmark)-1].CloseTime).UTC()
	members := []ReplayMember{}
	for symbol := range series {
		members = append(members, ReplayMember{Symbol: symbol, AssetID: "asset-" + symbol, ExchangeSymbolID: "symbol-" + symbol, Stage: "active"})
	}
	return Stage05PlanningContext{Selected: selected, Reference: benchmark, Series: series, At: at, Replays: []stage05Replay{{at: at.Add(-time.Hour), complete: true, members: members}}, Positions: positions, PositionEntries: map[string]float64{}, Marks: map[string]float64{}, Fixture: true}
}

func rising4H(start float64, count int, change float64) []services.OHLCV {
	values := make([]float64, count*16)
	for i := range values {
		values[i] = start + float64(i/16)*change
	}
	return stage05Bars(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), values)
}
func flat4H(value float64, count int) []services.OHLCV { return rising4H(value, count, 0) }
func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
