package backtest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/tradingcore"
)

type stage06SubmissionSpy struct{ submitted bool }

func (spy *stage06SubmissionSpy) BuildRequests(batch tradingcore.DecisionBatch) ([]tradingcore.LiveOrderRequest, error) {
	return (tradingcore.LiveBroker{}).BuildRequests(batch)
}

func (spy *stage06SubmissionSpy) Submit(context.Context, tradingcore.DecisionBatch) (tradingcore.BrokerBatchOutcome, error) {
	spy.submitted = true
	panic("external submission attempted from Stage 06 dry-run")
}

func TestStage06CausalDecisionInvariantToFutureExecutionOpen(t *testing.T) {
	config, _ := stage05Fixture(map[string][]float64{"AAAUSDT": linearPrices(100, 8)}, linearPrices(100, 8), 8, 3)
	config.StrategyID, config.StrategyVersion = StrategyTrendMomentumCandidate, "1.0.0"
	config.StrategyParameters = selectedCandidate(t, nil).Parameters
	config.EconomicAssetIdentities = map[string]string{"AAAUSDT": "asset-aaa"}
	config.SymbolIdentities = map[string]string{"AAAUSDT": "symbol-aaa"}
	signalAt := config.Start.Add(time.Hour)
	fillAt := signalAt.Add(time.Minute)
	run := func(open float64) *backtestMemoryLedger {
		ledger := newBacktestMemoryLedger(config)
		err := runStage05Target(ledger, config, feedbackCandidateStrategy(t), "AAAUSDT", tradingcore.Buy, 2, 100, open, signalAt, fillAt, 1, .2, "rebalance_addition", "risk_on", map[string]float64{"AAAUSDT": 100}, nil, ExitReasonTrace{Primary: "rebalance_addition"})
		if err != nil {
			t.Fatal(err)
		}
		return ledger
	}
	low, high := run(101), run(140)
	lowRecord, highRecord := low.runRecords[0], high.runRecords[0]
	if !reflect.DeepEqual(stage06Semantics(lowRecord.Result.Strategy.Intents()), stage06Semantics(highRecord.Result.Strategy.Intents())) || !reflect.DeepEqual(stage06Semantics(lowRecord.Result.Risk.Approved()), stage06Semantics(highRecord.Result.Risk.Approved())) {
		t.Fatal("future execution open changed alpha intent or decision-time risk approval")
	}
	quote, _ := lowRecord.Snapshot.Quote(lowRecord.Snapshot.QuoteInstruments()[0])
	if quote.ObservedAt.After(signalAt) || !low.events[0].At.Equal(fillAt) || !signalAt.Before(low.events[0].At) {
		t.Fatalf("clocks quote=%s decision=%s fill=%s", quote.ObservedAt, signalAt, low.events[0].At)
	}
	if low.events[0].Price == high.events[0].Price || low.events[0].Fee == high.events[0].Fee || low.cash == high.cash {
		t.Fatal("different future opens did not remain confined to fill/cost/achieved capital")
	}
	spy := &stage06SubmissionSpy{}
	requests, err := (stage06LiveDryRunAdapter{builder: spy}).Build(lowRecord.Result.Risk.Approved())
	if err != nil || len(requests) != 1 || spy.submitted {
		t.Fatalf("live dry-run submission fence failed: requests=%v submitted=%t err=%v", requests, spy.submitted, err)
	}
}

func TestStage06MandatoryReductionsBypassBudgetAndSkip(t *testing.T) {
	reasons := []string{"risk_stop", "regime_risk_off", "lost_eligibility", "lost_absolute_trend", "lost_relative_rank"}
	for _, reason := range reasons {
		t.Run(reason, func(t *testing.T) {
			config, _ := stage05Fixture(map[string][]float64{"AAAUSDT": linearPrices(100, 8)}, linearPrices(100, 8), 0, 0)
			config.StrategyID, config.StrategyVersion = StrategyTrendMomentumCandidate, "1.0.0"
			config.StrategyParameters = selectedCandidate(t, nil).Parameters
			ledger := newBacktestMemoryLedger(config)
			ledger.cash = 900
			ledger.positions["AAAUSDT"] = &positionState{Symbol: "AAAUSDT", Size: 1, EntryPrice: 100, EntryTime: config.Start}
			at, fillAt := config.Start.Add(time.Hour), config.Start.Add(time.Hour+time.Minute)
			parameters := cloneStringMap(config.StrategyParameters)
			parameters["target_gross"], parameters["turnover_budget"], parameters["skip_delta"], parameters["allocation_tolerance"] = "0", "0", "1", "0"
			err := rebalanceStage05(ledger, config, feedbackCandidateStrategy(t), nil, map[string]float64{}, nil, map[string]ExitReasonTrace{"AAAUSDT": {Primary: reason, Concurrent: []string{"scheduled_rebalance"}}}, map[string]float64{"AAAUSDT": 100}, map[string]float64{"AAAUSDT": 100}, at, fillAt, parameters, "risk_off")
			if err != nil {
				t.Fatal(err)
			}
			if len(ledger.positions) != 0 || len(ledger.events) != 1 || ledger.events[0].Side != string(tradingcore.Sell) || ratFloat(ledger.events[0].Quantity) != 1 || ledger.events[0].ReasonMetadata.Primary != reason || !reflect.DeepEqual(ledger.events[0].ReasonMetadata.Concurrent, []string{"scheduled_rebalance"}) {
				t.Fatalf("mandatory exit retained exposure or lost reasons: positions=%v events=%+v", ledger.positions, ledger.events)
			}
		})
	}
}

func TestStage06DisabledComponentInvariance(t *testing.T) {
	abs := selectedCandidate(t, map[string]string{"variant": "absolute_trend_only", "vol_normalization": "false", "lookback_bars": "20", "trend_bars": "20", "regime_bars": "20"})
	asset := rising4H(20, 61, .2)
	on, err := (trendMomentumPlanner{}).Plan(candidateContext(t, abs, rising4H(100, 61, 1), map[string][]services.OHLCV{"AAAUSDT": asset}, nil))
	if err != nil {
		t.Fatal(err)
	}
	off, err := (trendMomentumPlanner{}).Plan(candidateContext(t, abs, rising4H(160, 61, -1), map[string][]services.OHLCV{"AAAUSDT": asset}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(on.Targets, off.Targets) || !reflect.DeepEqual(on.TargetWeights, off.TargetWeights) {
		t.Fatal("absolute-trend-only changed when disabled benchmark regime changed")
	}
	abs = selectedCandidate(t, map[string]string{"variant": "absolute_trend_only", "vol_normalization": "false", "lookback_bars": "20", "trend_bars": "20", "regime_bars": "20", "top_n": "1", "max_positions": "1"})
	identityFirst := rising4H(20, 61, .1)
	identitySecond := rising4H(20, 61, .4)
	firstPlan, err := (trendMomentumPlanner{}).Plan(candidateContext(t, abs, rising4H(100, 61, 1), map[string][]services.OHLCV{"AAAUSDT": identityFirst, "BBBUSDT": identitySecond}, nil))
	if err != nil {
		t.Fatal(err)
	}
	secondPlan, err := (trendMomentumPlanner{}).Plan(candidateContext(t, abs, rising4H(100, 61, 1), map[string][]services.OHLCV{"AAAUSDT": identitySecond, "BBBUSDT": identityFirst}, nil))
	if err != nil || !reflect.DeepEqual(firstPlan.Targets, secondPlan.Targets) {
		t.Fatalf("absolute-only selection changed when disabled relative momentum changed: before=%v after=%v err=%v", firstPlan.Targets, secondPlan.Targets, err)
	}
	rel := selectedCandidate(t, map[string]string{"variant": "relative_momentum_only", "vol_normalization": "false", "lookback_bars": "20", "trend_bars": "20", "regime_bars": "20"})
	declining := rising4H(80, 61, -.2)
	plan, err := (trendMomentumPlanner{}).Plan(candidateContext(t, rel, rising4H(100, 61, 1), map[string][]services.OHLCV{"AAAUSDT": declining}, nil))
	if err != nil || len(plan.Targets) != 1 || plan.Factors[0].Components.AssetAbsoluteTrend {
		t.Fatalf("relative-only was influenced by disabled asset trend: targets=%v err=%v factor=%+v", plan.Targets, err, plan.Factors)
	}
}

func TestStage06IdentityAndExactBucketFailClosed(t *testing.T) {
	selected := selectedCandidate(t, map[string]string{"lookback_bars": "20", "trend_bars": "20", "regime_bars": "20"})
	benchmark, asset := rising4H(100, 61, 1), rising4H(20, 61, .2)
	ctx := candidateContext(t, selected, benchmark, map[string][]services.OHLCV{"AAAUSDT": asset}, nil)
	ctx.Replays[0].members[0].AssetID = ""
	if _, err := (trendMomentumPlanner{}).Plan(ctx); !IsStrategyDiagnostic(err, DiagnosticUniverseIdentity) {
		t.Fatalf("missing stable identity accepted: %v", err)
	}
	ctx = candidateContext(t, selected, benchmark, map[string][]services.OHLCV{"AAAUSDT": asset, "BBBUSDT": asset}, nil)
	ctx.Replays[0].members[1].AssetID = ctx.Replays[0].members[0].AssetID
	if _, err := (trendMomentumPlanner{}).Plan(ctx); !IsStrategyDiagnostic(err, DiagnosticUniverseIdentity) {
		t.Fatalf("duplicate economic identity accepted: %v", err)
	}
	ctx = candidateContext(t, selected, benchmark, map[string][]services.OHLCV{"AAAUSDT": asset, "BBBUSDT": asset}, nil)
	ctx.Replays[0].members[1].ExchangeSymbolID = ctx.Replays[0].members[0].ExchangeSymbolID
	if _, err := (trendMomentumPlanner{}).Plan(ctx); !IsStrategyDiagnostic(err, DiagnosticUniverseIdentity) {
		t.Fatalf("duplicate symbol identity accepted: %v", err)
	}
	ctx = candidateContext(t, selected, benchmark, map[string][]services.OHLCV{"AAAUSDT": asset}, nil)
	ctx.Replays[0].members[0].Stage = "future_magic_stage"
	if _, err := (trendMomentumPlanner{}).Plan(ctx); !IsStrategyDiagnostic(err, DiagnosticUniverseIdentity) {
		t.Fatalf("unknown lifecycle stage accepted: %v", err)
	}
	ctx = candidateContext(t, selected, benchmark, map[string][]services.OHLCV{"AAAUSDT": asset[:len(asset)-16]}, nil)
	plan, err := (trendMomentumPlanner{}).Plan(ctx)
	if err != nil || len(plan.Diagnostics) != 1 || plan.Diagnostics[0].Code != DiagnosticFeatureBucket {
		t.Fatalf("one missing 4h bucket was not excluded: plan=%+v err=%v", plan, err)
	}
}

func TestStage06RenameDelistPreservesSingleEconomicExposure(t *testing.T) {
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	listed := at.Add(-24 * time.Hour)
	delisted := at
	config := BacktestConfig{EconomicAssetIdentities: map[string]string{"OLDUSDT": "asset-a", "NEWUSDT": "asset-a"}, SymbolIdentities: map[string]string{"OLDUSDT": "symbol-old", "NEWUSDT": "symbol-new"}, SymbolLifecycles: map[string]SymbolLifecycle{"OLDUSDT": {ListedAt: listed, DelistedAt: &delisted}, "NEWUSDT": {ListedAt: at}}}
	ledger := &backtestMemoryLedger{positions: map[string]*positionState{"OLDUSDT": {Symbol: "OLDUSDT", Size: 2, EntryPrice: 10, EntryTime: listed}}}
	transitionEconomicPositions(ledger, []string{"NEWUSDT"}, config, at)
	if len(ledger.positions) != 1 || ledger.positions["NEWUSDT"] == nil || ledger.positions["NEWUSDT"].Size != 2 || ledger.positions["OLDUSDT"] != nil {
		t.Fatalf("rename/delist duplicated or dropped economic exposure: %+v", ledger.positions)
	}
}

func TestStage06DescriptorWarmupTraceSchemaAndIntentMatrix(t *testing.T) {
	descriptor := descriptorByID(t, StrategyTrendMomentumCandidate)
	maxWarmup := 0
	for _, lookback := range []string{"20", "30", "60"} {
		for _, trend := range []string{"20", "30", "60"} {
			for _, regime := range []string{"20", "30", "60"} {
				selected := selectedCandidate(t, map[string]string{"lookback_bars": lookback, "trend_bars": trend, "regime_bars": regime})
				warmup := effectiveStage06Warmup(selected.Parameters)
				if warmup > maxWarmup {
					maxWarmup = warmup
				}
			}
		}
	}
	if descriptor.WarmupFormula == "" || maxWarmup != descriptor.MaximumWarmupBars || maxWarmup != 976 {
		t.Fatalf("warmup metadata formula=%q declared=%d effective=%d", descriptor.WarmupFormula, descriptor.MaximumWarmupBars, maxWarmup)
	}
	traceJSON, _ := json.Marshal(FactorTrace{})
	regimeJSON, _ := json.Marshal(RegimeObservation{})
	for _, feature := range descriptor.Features {
		if !strings.Contains(string(traceJSON), `"`+feature.TraceField+`"`) && !strings.Contains(string(regimeJSON), `"`+feature.TraceField+`"`) {
			t.Fatalf("declared trace field %q has no versioned schema field", feature.TraceField)
		}
	}
	for _, allowed := range []string{"research", "shadow", "backtest", "live_dry_run"} {
		if _, _, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.0.0", map[string]string{"execution_intent": allowed}); err != nil {
			t.Fatalf("allowed intent %s rejected: %v", allowed, err)
		}
	}
	for _, denied := range []string{"paper_capital", "live_submit", "promotion"} {
		if _, _, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.0.0", map[string]string{"execution_intent": denied}); !IsStrategyDiagnostic(err, DiagnosticExecutionFenced) {
			t.Fatalf("forbidden intent %s not fenced: %v", denied, err)
		}
	}
}

func TestStage06AdapterMismatchCounterexamples(t *testing.T) {
	base := []Stage06OrderSemantic{{Symbol: "AAAUSDT", Side: "buy", Quantity: "1", Reason: "selected", PolicyVersion: "p1", ExecutionMode: string(tradingcore.ExecutionBacktest), DecisionAt: "2026-01-01T00:00:00Z"}}
	valid := append([]Stage06OrderSemantic(nil), base...)
	valid[0].ExecutionMode = string(tradingcore.ExecutionShadow)
	if err := validateStage06AdapterParity(base, valid, tradingcore.ExecutionShadow); err != nil {
		t.Fatal(err)
	}
	mutations := []struct {
		name   string
		mutate func(*Stage06OrderSemantic)
	}{
		{"cash", func(v *Stage06OrderSemantic) { v.Quantity = ".5" }},
		{"constraint", func(v *Stage06OrderSemantic) { v.Quantity = ".9" }},
		{"mode", func(v *Stage06OrderSemantic) { v.ExecutionMode = string(tradingcore.ExecutionResearch) }},
		{"policy_version", func(v *Stage06OrderSemantic) { v.PolicyVersion = "p2" }},
		{"timestamp", func(v *Stage06OrderSemantic) { v.DecisionAt = "2026-01-01T00:01:00Z" }},
	}
	for _, test := range mutations {
		t.Run(test.name, func(t *testing.T) {
			candidate := append([]Stage06OrderSemantic(nil), valid...)
			test.mutate(&candidate[0])
			if err := validateStage06AdapterParity(base, candidate, tradingcore.ExecutionShadow); err == nil {
				t.Fatalf("adapter %s mismatch accepted", test.name)
			}
		})
	}
}

func TestStage06SensitivityGridCompleteAndDigestBound(t *testing.T) {
	seen := map[string]bool{}
	for _, spec := range stage06SensitivityGrid {
		if seen[spec.ID] || spec.Variant == "" || spec.Lookback == "" || spec.Rebalance == "" {
			t.Fatalf("invalid predefined sensitivity spec: %+v", spec)
		}
		seen[spec.ID] = true
	}
	row := SensitivityRow{SchemaVersion: "trend-momentum-sensitivity-v2", ID: "x", Parameters: map[string]string{"variant": "combined"}, RiskPolicy: map[string]string{"policy_version": "p"}}
	encoded, _ := json.Marshal(row)
	digest := sha256.Sum256(encoded)
	row.Digest = string(digest[:])
	if row.Digest == "" || len(stage06SensitivityGrid) != 4 {
		t.Fatal("sensitivity grid or digest missing")
	}
}

func TestStage06StructuredReasonsRoundTripFillLedgerTrade(t *testing.T) {
	config, _ := stage05Fixture(map[string][]float64{"AAAUSDT": linearPrices(100, 8)}, linearPrices(100, 8), 0, 0)
	config.StrategyID, config.StrategyVersion = StrategyTrendMomentumCandidate, "1.0.0"
	config.StrategyParameters = selectedCandidate(t, nil).Parameters
	ledger := newBacktestMemoryLedger(config)
	ledger.cash = 900
	ledger.positions["AAAUSDT"] = &positionState{Symbol: "AAAUSDT", Size: 1, EntryPrice: 90, EntryTime: config.Start}
	at, fillAt := config.Start.Add(time.Hour), config.Start.Add(time.Hour+time.Minute)
	reason := ExitReasonTrace{Primary: "risk_stop", Concurrent: []string{"regime_risk_off", "lost_eligibility"}}
	if err := runStage05Target(ledger, config, feedbackCandidateStrategy(t), "AAAUSDT", tradingcore.Sell, 1, 100, 100, at, fillAt, 0, 0, "risk_stop", "risk_off", map[string]float64{"AAAUSDT": 100}, nil, reason); err != nil {
		t.Fatal(err)
	}
	artifacts := buildBacktestArtifacts(ledger, ledger.positions, map[string]*symbolState{}, nil)
	if len(artifacts.Orders) != 1 || len(artifacts.Fills) != 1 || len(artifacts.Ledger) != 1 || len(ledger.trades) != 1 {
		t.Fatalf("artifact cardinality=%+v", artifacts)
	}
	for name, got := range map[string]ReasonMetadata{"decision": artifacts.Decisions[0].ReasonMetadata, "order": artifacts.Orders[0].ReasonMetadata, "fill": artifacts.Fills[0].Reason, "ledger": artifacts.Ledger[0].Reason, "trade": ledger.trades[0].ReasonMetadata} {
		if got.Primary != reason.Primary || !reflect.DeepEqual(got.Concurrent, reason.Concurrent) {
			t.Fatalf("%s structured reasons=%+v", name, got)
		}
	}
	if artifacts.Fills[0].IntentID == "" || artifacts.Fills[0].OrderID == "" || artifacts.Fills[0].FillID == "" || artifacts.Ledger[0].FillID != artifacts.Fills[0].FillID {
		t.Fatalf("stable artifact linkage fill=%+v ledger=%+v", artifacts.Fills[0], artifacts.Ledger[0])
	}
	encoded, _ := json.Marshal(artifacts)
	var decoded BacktestArtifacts
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.Fills[0].Reason.Primary != "risk_stop" {
		t.Fatalf("reason roundtrip err=%v decoded=%+v", err, decoded.Fills)
	}
}

func TestStage06PreviewModesCannotAuthorizeCapital(t *testing.T) {
	for _, mode := range []tradingcore.ExecutionMode{tradingcore.ExecutionResearch, tradingcore.ExecutionShadow, tradingcore.ExecutionLiveDryRun} {
		t.Run(string(mode), func(t *testing.T) {
			config, _ := stage05Fixture(map[string][]float64{"AAAUSDT": linearPrices(100, 8)}, linearPrices(100, 8), 0, 0)
			config.StrategyID = StrategyTrendMomentumCandidate
			ledger := newBacktestMemoryLedger(config)
			if err := runStage05Target(ledger, config, feedbackCandidateStrategy(t), "AAAUSDT", tradingcore.Buy, 1, 100, 100, config.Start.Add(time.Hour), config.Start.Add(time.Hour+time.Minute), 0, .1, "selected", "risk_on", map[string]float64{"AAAUSDT": 100}, nil, ExitReasonTrace{Primary: "selected"}); err != nil {
				t.Fatal(err)
			}
			ctx, err := rebuildStage06AdapterContext(ledger.runRecords[0].Snapshot, mode)
			if err != nil {
				t.Fatal(err)
			}
			decision, _ := ledger.runRecords[0].Strategy.Decide(context.Background(), ctx)
			risk, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), decision.Intents(), ctx.Portfolio(), ledger.runRecords[0].Policy)
			if err != nil || len(risk.Approved().Intents()) != 0 || len(risk.Rejected()) != 1 || risk.Rejected()[0].Code != tradingcore.RiskExecutionNotAuthorized {
				t.Fatalf("preview authorized capital: mode=%s risk=%+v err=%v", mode, risk, err)
			}
		})
	}
}

func TestStage06ComparisonRequestRejectsNonBacktestModesBeforeDataAccess(t *testing.T) {
	for _, intent := range []string{"research", "shadow", "live_dry_run"} {
		_, err := RunStage05Comparison(BacktestConfig{}, nil, Stage05RunRequest{StrategyID: StrategyTrendMomentumCandidate, Parameters: map[string]string{"execution_intent": intent}})
		if !IsStrategyDiagnostic(err, DiagnosticIntentRuntime) {
			t.Fatalf("comparison accepted incompatible %s mode or accessed data first: %v", intent, err)
		}
	}
}

func feedbackCandidateStrategy(t *testing.T) tradingcore.Strategy {
	t.Helper()
	_, strategy, _, err := DefaultStrategyRegistry.ResolveExecutable(StrategyTrendMomentumCandidate, "1.0.0", nil)
	if err != nil {
		t.Fatal(err)
	}
	return strategy
}
