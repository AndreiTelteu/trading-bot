package backtest

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"trading-go/internal/decisionmodel"
	"trading-go/internal/services"
	"trading-go/internal/tradingcore"
)

type fakeCouncilModel struct {
	choice        string
	scoreResolved string
	finalResolved string
	err           error
	calls         int
	cache         bool
}

func (m *fakeCouncilModel) Identity() string { return decisionmodel.DefaultIdentity }
func (m *fakeCouncilModel) Decide(_ context.Context, request decisionmodel.Request) (decisionmodel.Response, error) {
	m.calls++
	if m.err != nil {
		return decisionmodel.Response{}, m.err
	}
	response := decisionmodel.Response{ResolvedModel: "fixture", RequestDigest: strings.Repeat("a", 64), Cached: m.cache, Answers: map[string]decisionmodel.Answer{}}
	if _, ok := request.Questions["decision_final"]; ok {
		if m.finalResolved != "" {
			response.ResolvedModel = m.finalResolved
		}
		response.Answers["decision_final"] = decisionmodel.Answer{Type: decisionmodel.QuestionChoice, Choice: m.choice, Probabilities: map[string]float64{m.choice: 1}}
		return response, nil
	}
	if m.scoreResolved != "" {
		response.ResolvedModel = m.scoreResolved
	}
	for _, name := range []string{"decision_bull", "decision_bear", "decision_hodl"} {
		level := "very_strong"
		if name == "decision_hodl" {
			level = "none"
		}
		response.Answers[name] = decisionmodel.Answer{Type: decisionmodel.QuestionChoice, Choice: level, Probabilities: map[string]float64{level: 1}}
	}
	return response, nil
}

func TestDecisionCouncilRegistryAndHistoricalDigests(t *testing.T) {
	for version, want := range map[string]string{
		"1.0.0": "a8c8af80b5132978989f445d5cad34cda9fb6643c2d6d7ae2f5a80fb13bd83d4",
		"1.1.0": "739ab88cf6aac578d552958921569896bad0c77fa3a660691cba051d616ba2ed",
		"1.2.0": "d37e571b7124996180ebd90a332cb378a72b65e4d61ac6c68a087b26d023f4c9",
	} {
		if got := strategyImplementationDigest(StrategyTrendMomentumCandidate, version); got != want {
			t.Errorf("%s digest=%s want=%s", version, got, want)
		}
	}
	if strategyImplementationDigest(StrategyTrendMomentumCandidate, "1.3.0") == strategyImplementationDigest(StrategyTrendMomentumCandidate, "1.1.0") {
		t.Fatal("council implementation identity reused")
	}
	selected, _, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.3.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Parameters["decision_model"] != decisionmodel.DefaultIdentity || selected.Parameters["decision_council_policy"] != "observe_v1" {
		t.Fatalf("defaults=%v", selected.Parameters)
	}
	for key, value := range map[string]string{"decision_model": "unknown/model", "decision_council_policy": "unreviewed", "variant": "absolute_trend_only", "vol_normalization": "false"} {
		if _, _, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.3.0", map[string]string{key: value}); err == nil {
			t.Errorf("accepted %s=%s", key, value)
		}
	}
}

func councilReplayFixture(t *testing.T) (BacktestConfig, map[string][]services.OHLCV) {
	t.Helper()
	count := 16*64 + 8
	prices, market := make([]float64, count), make([]float64, count)
	for i := range prices {
		prices[i] = 20 + float64(i)*0.04
		market[i] = 100 + float64(i)*0.1
	}
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": prices}, market, 8, 3)
	config.ExecutionPolicy.Version = "backtest-execution-v4"
	config.EconomicAssetIdentities = map[string]string{"AAAUSDT": "asset-a", "BTCUSDT": "asset-btc"}
	config.SymbolIdentities = map[string]string{"AAAUSDT": "symbol-a", "BTCUSDT": "symbol-btc"}
	for at := config.Start; at.Before(config.End); at = at.Add(24 * time.Hour) {
		config.ReplaySnapshots = append(config.ReplaySnapshots, ReplaySnapshot{Timestamp: at, ObservedComplete: true, Members: []ReplayMember{{Symbol: "AAAUSDT", AssetID: "asset-a", ExchangeSymbolID: "symbol-a", Stage: "active"}}})
	}
	return config, series
}

func TestDecisionCouncilObservePreservesStage06Economics(t *testing.T) {
	config, series := councilReplayFixture(t)
	params := map[string]string{"lookback_bars": "20", "trend_bars": "20", "regime_bars": "20", "turnover_budget": "1"}
	old, strategy, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.1.0", params)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := runStage05Strategy(config, series, old, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	config.CouncilModel = &fakeCouncilModel{choice: "buy"}
	next, strategy, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.3.0", params)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := runStage05Strategy(config, series, next, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(baseline.Trades, observed.Trades) || !reflect.DeepEqual(baseline.Equity, observed.Equity) {
		t.Fatal("observe changed trades or equity")
	}
	if len(observed.CouncilTraces) == 0 || observed.CouncilSummary == nil || observed.CouncilDiagnostic == nil || observed.CouncilDiagnostic.Label != "post_hoc_only_not_used_by_decisions" {
		t.Fatal("council evidence missing after replay")
	}
	encoded, err := json.Marshal(observed.CouncilTraces[0])
	if err != nil || !strings.Contains(string(encoded), `"bull":`) || !strings.Contains(string(encoded), `"bear":`) || !strings.Contains(string(encoded), `"hodl":`) {
		t.Fatalf("score trace encoding=%s err=%v", encoded, err)
	}
}

func TestDecisionCouncilComparisonMatchesOldBaselineAndSkipsRiskOnlyPlans(t *testing.T) {
	config, series := councilReplayFixture(t)
	model := &fakeCouncilModel{choice: "buy"}
	config.CouncilModel = model
	request := Stage05RunRequest{StrategyID: StrategyTrendMomentumCandidate, StrategyVersion: "1.3.0", Parameters: map[string]string{"lookback_bars": "20", "trend_bars": "20", "regime_bars": "20", "turnover_budget": "1"}, TargetGrossExposure: "0.75", MaxNetExposure: "0.75", FinalPolicy: "mark_to_market", ExecutionPolicyVersion: "backtest-execution-v4", AllowInMemoryFixture: true}
	comparison, err := RunStage05Comparison(config, series, request)
	if err != nil {
		t.Fatal(err)
	}
	baseline := comparison.Results[StrategyMatchedMomentumID]
	candidate := comparison.Results[StrategyTrendMomentumCandidate]
	if baseline.Manifest.Strategy.Descriptor.Version != "1.0.0" || baseline.Manifest.Strategy.Parameters["decision_model"] != "" || len(candidate.Sensitivity) != 0 || candidate.Factors[0].StrategyVersion != "1.3.0" {
		t.Fatal("council comparison identity or baseline changed")
	}
	encoded, err := MarshalComparisonArtifact(comparison)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalComparisonArtifact(encoded)
	if err != nil || len(decoded.CouncilTraces) == 0 || decoded.CouncilSummary == nil || decoded.CouncilDiagnostic == nil {
		t.Fatalf("council evidence did not persist: err=%v", err)
	}
	if !reflect.DeepEqual(decoded.CouncilSummary.ResolvedModels, []string{"fixture"}) {
		t.Fatalf("resolved model provenance=%v", decoded.CouncilSummary.ResolvedModels)
	}
	warm := comparison
	warm.CouncilTraces = append([]DecisionCouncilTrace(nil), comparison.CouncilTraces...)
	for i := range warm.CouncilTraces {
		warm.CouncilTraces[i].Cached = !warm.CouncilTraces[i].Cached
	}
	warmSummary := *comparison.CouncilSummary
	warmSummary.CacheHits++
	warm.CouncilSummary = &warmSummary
	warm.Results = make(map[string]Stage05StrategyResult, len(comparison.Results))
	for id, result := range comparison.Results {
		if id == StrategyTrendMomentumCandidate {
			result.CouncilTraces = append([]DecisionCouncilTrace(nil), result.CouncilTraces...)
			for i := range result.CouncilTraces {
				result.CouncilTraces[i].Cached = !result.CouncilTraces[i].Cached
			}
			resultSummary := *result.CouncilSummary
			resultSummary.CacheHits++
			result.CouncilSummary = &resultSummary
		}
		warm.Results[id] = result
	}
	warmDigest, err := comparisonDigest(warm)
	if err != nil || warmDigest != comparison.ArtifactDigest {
		t.Fatalf("cache telemetry changed digest: %s vs %s: %v", warmDigest, comparison.ArtifactDigest, err)
	}
	warmEncoded, err := MarshalComparisonArtifact(warm)
	if err != nil {
		t.Fatal(err)
	}
	warmDecoded, err := UnmarshalComparisonArtifact(warmEncoded)
	if err != nil || warmDecoded.CouncilSummary.CacheHits != warmSummary.CacheHits || warmDecoded.CouncilTraces[0].Cached != warm.CouncilTraces[0].Cached {
		t.Fatalf("cache telemetry lost from report: %v", err)
	}
	selected, strategy, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.3.0", request.Parameters)
	if err != nil {
		t.Fatal(err)
	}
	model.calls = 0
	riskOnly := Stage05PlannerFunc(func(Stage05PlanningContext) (Stage05Plan, error) {
		return Stage05Plan{Decide: true, RiskStopOnly: true, Regime: "risk_on"}, nil
	})
	if _, err := runStage05StrategyWithPlanner(config, series, selected, strategy, riskOnly, true); err != nil {
		t.Fatal(err)
	}
	if model.calls != 0 {
		t.Fatalf("risk-only plan made %d model calls", model.calls)
	}
}

func TestDecisionCouncilForwardReturnsAnchorToLatestCompletedBar(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	prices := make([]float64, 16*14)
	for i := range prices {
		prices[i] = 100 + float64(i/16)
	}
	series := map[string][]services.OHLCV{"AAAUSDT": stage05Bars(start, prices)}
	decisionAt := stage05CloseAt(start, 16*2) // A 15m close after the second 4h bar.
	traces := []DecisionCouncilTrace{{DecisionAt: canonicalTime(decisionAt), Symbol: "AAAUSDT", V4Action: "buy", Proposed: "admit", FinalChoice: "buy", Bull: 0.7, Bear: 0.2}}
	diagnostic := diagnoseCouncilAfterReplay(traces, series, start.Add(time.Duration(len(prices))*15*time.Minute))
	if len(diagnostic.Forward) != 1 || diagnostic.Forward[0].Return1 == nil || diagnostic.Forward[0].Return6 == nil || diagnostic.Forward[0].Return12 == nil {
		t.Fatalf("missing forward labels for 15m decision: %+v", diagnostic.Forward)
	}
	want := 1.0 / 101.0 // Latest completed 4h close is 101; the next is 102.
	if math.Abs(*diagnostic.Forward[0].Return1-want) > 1e-12 {
		t.Fatalf("forward +1=%v want %v", *diagnostic.Forward[0].Return1, want)
	}
}

func TestDecisionCouncilSummaryCollectsBothResolvedModels(t *testing.T) {
	ctx, plan := councilPlanningFixture(t, false)
	summary := &DecisionCouncilSummary{}
	runtime := &councilRuntime{core: tradingCouncil(&fakeCouncilModel{choice: "buy", scoreResolved: "z-score", finalResolved: "a-final"}), ctx: context.Background()}
	if _, _, err := runtime.apply(ctx, plan, summary); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(summary.ResolvedModels, []string{"a-final", "z-score"}) {
		t.Fatalf("resolved models=%v", summary.ResolvedModels)
	}
}

func councilPlanningFixture(t *testing.T, held bool) (Stage05PlanningContext, Stage05Plan) {
	t.Helper()
	config, series := councilReplayFixture(t)
	at := time.UnixMilli(config.BenchmarkSeries[len(config.BenchmarkSeries)-1].CloseTime).UTC()
	selected, _, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.3.0", map[string]string{"decision_council_policy": "veto_v1"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := Stage05PlanningContext{Selected: selected, Reference: config.BenchmarkSeries, Series: series, At: at, Positions: map[string]float64{}, PositionEntries: map[string]float64{}, Marks: map[string]float64{"AAAUSDT": series["AAAUSDT"][len(series["AAAUSDT"])-1].Close}}
	if held {
		ctx.Positions["AAAUSDT"] = 1
		ctx.PositionEntries["AAAUSDT"] = 20
	}
	plan := Stage05Plan{Targets: []string{"AAAUSDT"}, TargetWeights: map[string]float64{"AAAUSDT": 0.25}, Regime: "risk_on", Factors: []FactorTrace{{Symbol: "AAAUSDT", RelativeRank: 1, CompositeMomentum: 0.2, NormalizedMomentum: 1, RealizedVolatility: 0.1, AbsoluteTrend: true}}, Decide: true}
	return ctx, plan
}

func TestDecisionCouncilVetoFallbackAndInsufficientData(t *testing.T) {
	ctx, plan := councilPlanningFixture(t, false)
	summary := &DecisionCouncilSummary{}
	r := &councilRuntime{core: tradingCouncil(&fakeCouncilModel{choice: "sell"}), ctx: context.Background()}
	got, traces, err := r.apply(ctx, plan, summary)
	if err != nil || len(got.Targets) != 0 || len(got.TargetWeights) != 0 || len(got.ExitReasons) != 0 || len(traces) != 1 || summary.Vetoes != 1 {
		t.Fatalf("entry veto: plan=%+v traces=%+v summary=%+v err=%v", got, traces, summary, err)
	}
	if summary.Calls != 2 {
		t.Fatalf("successful evaluation made %d requests", summary.Calls)
	}
	ctx, plan = councilPlanningFixture(t, true)
	summary = &DecisionCouncilSummary{}
	got, traces, err = r.apply(ctx, plan, summary)
	if err != nil || len(got.Targets) != 0 || got.ExitReasons["AAAUSDT"].Primary != "decision_council_exit" || summary.EarlyExits != 1 || traces[0].Proposed != "early_exit" {
		t.Fatalf("held exit: plan=%+v traces=%+v err=%v", got, traces, err)
	}
	ctx, plan = councilPlanningFixture(t, true)
	plan.Targets = nil // The v4 planner already deselected the held position.
	plan.ExitReasons = map[string]ExitReasonTrace{"AAAUSDT": {Primary: "rank_loss"}}
	summary = &DecisionCouncilSummary{}
	got, traces, err = r.apply(ctx, plan, summary)
	if err != nil || len(traces) != 0 || got.ExitReasons["AAAUSDT"].Primary != "rank_loss" || summary.Calls != 0 {
		t.Fatalf("v4 exit was changed: plan=%+v traces=%+v summary=%+v err=%v", got, traces, summary, err)
	}
	ctx, plan = councilPlanningFixture(t, false)
	ctx.Reference = ctx.Reference[:16*30]
	ctx.Series["AAAUSDT"] = ctx.Series["AAAUSDT"][:16*30]
	ctx.At = time.UnixMilli(ctx.Reference[len(ctx.Reference)-1].CloseTime).UTC()
	summary = &DecisionCouncilSummary{}
	got, traces, err = r.apply(ctx, plan, summary)
	if err != nil || len(got.Targets) != 0 || traces[0].Reason != "insufficient_council_data" || summary.Failures != 0 {
		t.Fatalf("insufficient entry: plan=%+v traces=%+v err=%v", got, traces, err)
	}
	if summary.Calls != 0 {
		t.Fatalf("insufficient data made %d model requests", summary.Calls)
	}
	ctx, plan = councilPlanningFixture(t, false)
	r.core = tradingCouncil(&fakeCouncilModel{err: decisionmodel.ErrUnavailable})
	summary = &DecisionCouncilSummary{}
	for i := 0; i < 9; i++ {
		got, traces, err = r.apply(ctx, plan, summary)
		if err != nil || len(got.Targets) != 1 || !traces[0].Fallback {
			t.Fatalf("fallback %d: %+v %+v %v", i, got, traces, err)
		}
	}
	if _, _, err = r.apply(ctx, plan, summary); err == nil || summary.Failures != 10 {
		t.Fatalf("10th failure=%v summary=%+v", err, summary)
	}
	r.core = tradingCouncil(&fakeCouncilModel{err: decisionmodel.ErrCacheCorrupt})
	if _, _, err = r.apply(ctx, plan, &DecisionCouncilSummary{}); !errors.Is(err, decisionmodel.ErrCacheCorrupt) {
		t.Fatalf("cache corruption swallowed: %v", err)
	}
	r.core = tradingCouncil(&fakeCouncilModel{err: decisionmodel.ErrInvalidResponse})
	summary = &DecisionCouncilSummary{}
	if got, traces, err = r.apply(ctx, plan, summary); err != nil || len(got.Targets) != 1 || !traces[0].Fallback || summary.Failures != 1 {
		t.Fatalf("invalid response did not fall back: plan=%+v traces=%+v summary=%+v err=%v", got, traces, summary, err)
	}
	r.core = tradingCouncil(&fakeCouncilModel{err: errors.Join(decisionmodel.ErrUnavailable, context.Canceled)})
	if _, _, err = r.apply(ctx, plan, &DecisionCouncilSummary{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation fell back: %v", err)
	}
}

func tradingCouncil(model decisionmodel.Model) tradingcore.DecisionCouncil {
	return tradingcore.DecisionCouncil{Model: model, Policy: tradingcore.CouncilVetoV1}
}
