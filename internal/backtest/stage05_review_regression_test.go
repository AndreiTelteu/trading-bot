package backtest

import (
	"math"
	"strings"
	"testing"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/tradingcore"
)

func TestStage05PersistedMembershipStagesFailClosed(t *testing.T) {
	members := []ReplayMember{{AssetID: "a", Symbol: "ACTIVE", Stage: "active"}, {AssetID: "s", Symbol: "SHORT", Stage: "shortlist", Shortlisted: true}, {AssetID: "r", Symbol: "RANKED", Stage: "ranked"}, {AssetID: "x", Symbol: "REJECT", Stage: "rejected"}}
	got, err := eligibleReplaySymbols(members)
	if err != nil || strings.Join(got, ",") != "ACTIVE,SHORT" {
		t.Fatalf("members=%v err=%v", got, err)
	}
	if _, err := eligibleReplaySymbols([]ReplayMember{{Symbol: "BAD", Stage: "eligible"}}); err == nil {
		t.Fatal("unknown persisted stage was accepted")
	}
}

type testCandidatePlanner struct{}

func (testCandidatePlanner) Plan(context Stage05PlanningContext) (Stage05Plan, error) {
	if len(context.LastTargets) > 0 {
		return Stage05Plan{}, nil
	}
	return Stage05Plan{Targets: []string{context.Config.BenchmarkSymbol}, Decide: true}, nil
}

func TestStage05RegisteredCandidateExecutesWithoutComparisonSwitch(t *testing.T) {
	id := "test_candidate_registry"
	descriptor := StrategyDescriptor{SchemaVersion: StrategyDescriptorSchemaVersion, ID: id, Version: "1.0.0", Description: "test-only independently registered candidate", RequiredData: []StrategyDataRequirement{{Role: "benchmark", Timeframe: "15m"}, {Role: "execution", Timeframe: "1m"}}, BenchmarkRequired: true, DecisionCadence: "15m", RebalanceCadence: "once", WarmupBars: 1, Risk: StrategyRiskDeclaration{MaxGrossExposure: "1", MaxNetExposure: "1", LongOnly: true, UsesSharedRisk: true}, Parameters: []StrategyParameterSpec{{Name: "target_gross", Type: "decimal", Default: "0.5", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Default: "liquidate", Enum: []string{"liquidate", "mark_to_market"}}}}
	if _, _, _, err := DefaultStrategyRegistry.ResolveExecutable(id, "1.0.0", nil); err != nil {
		if err := DefaultStrategyRegistry.RegisterExecutable(descriptor, func(map[string]string) (tradingcore.Strategy, error) {
			return tradingcore.TargetAllocationStrategy{}, nil
		}, testCandidatePlanner{}); err != nil {
			t.Fatal(err)
		}
	}
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": linearPrices(10, 84)}, linearPrices(100, 84), 0, 0)
	comparison, err := RunStage05Comparison(config, series, Stage05RunRequest{StrategyID: id, StrategyVersion: "1.0.0", TargetGrossExposure: "0.5", MaxNetExposure: "0.5", FinalPolicy: "liquidate", AllowInMemoryFixture: true})
	if err != nil {
		t.Fatal(err)
	}
	result, ok := comparison.Results[id]
	if !ok || result.Manifest.Strategy.Descriptor.Baseline || result.Manifest.Strategy.Parameters["target_gross"] != "0.5" {
		t.Fatalf("candidate evidence=%+v", result.Manifest.Strategy)
	}
	if comparison.Governance.PromotionAllowed {
		t.Fatal("Stage 05 claimed promotion")
	}
}

func TestStage05DeltaRebalanceAvoidsUnchangedChurn(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10, 10, 10}, "BBBUSDT": {20, 20, 20, 20, 20}}, []float64{100, 100, 100, 100, 100}, 0, 0)
	for i := 0; i < 4; i++ {
		config.ReplaySnapshots = append(config.ReplaySnapshots, ReplaySnapshot{Timestamp: stage05CloseAt(config.Start, i), ObservedComplete: true, Members: replayMembers("AAAUSDT", "BBBUSDT")})
	}
	selected, strategy, _ := DefaultStrategyRegistry.Resolve(StrategyEqualWeightID, "", map[string]string{"rebalance": "15m", "target_gross": "0.8", "final_policy": "liquidate"})
	result, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts.Fills) != 4 {
		t.Fatalf("unchanged rebalance produced churn: %+v", result.Artifacts.Fills)
	}
}

func TestStage05DeltaBelowExchangeMinimumIsRecordedWithoutAborting(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 9.9, 9.8, 9.7, 9.6}}, []float64{100, 100, 100, 100, 100}, 0, 0)
	config.ExecutionPolicy.Constraints["AAAUSDT"] = SymbolConstraints{QuantityStep: .00000001, PriceTick: .00000001, MinQuantity: .00000001, MinNotional: 50}
	for i := 0; i < 4; i++ {
		config.ReplaySnapshots = append(config.ReplaySnapshots, ReplaySnapshot{Timestamp: stage05CloseAt(config.Start, i), ObservedComplete: true, Members: replayMembers("AAAUSDT")})
	}
	selected, strategy, _ := DefaultStrategyRegistry.Resolve(StrategyEqualWeightID, "", map[string]string{"rebalance": "15m", "target_gross": "0.8", "final_policy": "liquidate"})
	result, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == DiagnosticConstraintResidual && diagnostic.Symbol == "AAAUSDT" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing exchange-minimum residual diagnostic: %+v", result.Diagnostics)
	}
	if len(result.Artifacts.Fills) != 2 {
		t.Fatalf("minimum residual should leave only entry and final liquidation fills: %+v", result.Artifacts.Fills)
	}
}

func TestStage05RiskTrimBelowExchangeMinimumIsRecordedWithoutAborting(t *testing.T) {
	config, _ := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10}}, []float64{100, 100, 100}, 10, 0)
	config.StrategyID = StrategyEqualWeightID
	config.StrategyVersion = "1.0.0"
	config.StrategyParameters = map[string]string{"target_gross": "1"}
	config.ExecutionPolicy.Constraints["AAAUSDT"] = SymbolConstraints{QuantityStep: .01, PriceTick: .01, MinQuantity: .01, MinNotional: 5}
	ledger := newBacktestMemoryLedger(config)
	strategy := tradingcore.TargetAllocationStrategy{}
	signalAt := config.Start.Add(15*time.Minute - time.Millisecond)
	fillAt := config.Start.Add(15 * time.Minute)
	err := runStage05Target(ledger, config, strategy, "AAAUSDT", tradingcore.Buy, 100, 10, 10, signalAt, fillAt, 1, 1, "rebalance_addition", "unknown", map[string]float64{"AAAUSDT": 10}, nil, ExitReasonTrace{Primary: "rebalance_addition"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.positions) != 1 || ledger.positions["AAAUSDT"].Size <= 0 || ledger.positions["AAAUSDT"].Size >= 100 {
		t.Fatalf("expected a positive risk-trimmed position: %+v", ledger.positions["AAAUSDT"])
	}
	if len(ledger.allocationDiagnostics) != 1 || ledger.allocationDiagnostics[0].Code != DiagnosticAllocationConstrained {
		t.Fatalf("missing constrained-underfill diagnostic: %+v", ledger.allocationDiagnostics)
	}
}

func TestStage05ExistingPositionDeltaBelowLotIsRecordedWithoutAborting(t *testing.T) {
	config, _ := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10}}, []float64{100, 100, 100}, 0, 0)
	config.StrategyID = StrategyEqualWeightID
	config.StrategyVersion = "1.0.0"
	config.StrategyParameters = map[string]string{"target_gross": "1"}
	config.ExecutionPolicy.Constraints["AAAUSDT"] = SymbolConstraints{QuantityStep: .01, PriceTick: .01, MinQuantity: .01, MinNotional: 5}
	ledger := newBacktestMemoryLedger(config)
	ledger.cash = 500
	ledger.positions["AAAUSDT"] = &positionState{Symbol: "AAAUSDT", EntryPrice: 10, Size: 50, EntryTime: config.Start}
	signalAt := config.Start.Add(15*time.Minute - time.Millisecond)
	fillAt := config.Start.Add(15 * time.Minute)
	err := runStage05Target(ledger, config, tradingcore.TargetAllocationStrategy{}, "AAAUSDT", tradingcore.Buy, .005, 10, 10, signalAt, fillAt, 1, .5, "rebalance_addition", "unknown", map[string]float64{"AAAUSDT": 10}, nil, ExitReasonTrace{Primary: "rebalance_addition"})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.positions["AAAUSDT"].Size != 50 {
		t.Fatalf("sub-lot residual changed the position: %+v", ledger.positions["AAAUSDT"])
	}
	if len(ledger.allocationDiagnostics) != 1 || ledger.allocationDiagnostics[0].Code != DiagnosticConstraintResidual {
		t.Fatalf("missing sub-lot residual diagnostic: %+v", ledger.allocationDiagnostics)
	}
}

func TestStage05ExistingDustDeltaAtGrossLimitIsRecordedWithoutAborting(t *testing.T) {
	config, _ := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10}}, []float64{100, 100, 100}, 0, 0)
	config.StrategyID = StrategyEqualWeightID
	config.StrategyVersion = "1.0.0"
	config.StrategyParameters = map[string]string{"target_gross": "0.5"}
	config.ExecutionPolicy.Constraints["AAAUSDT"] = SymbolConstraints{QuantityStep: .001, PriceTick: .01, MinQuantity: .001, MinNotional: 5}
	ledger := newBacktestMemoryLedger(config)
	ledger.cash = 500
	ledger.positions["AAAUSDT"] = &positionState{Symbol: "AAAUSDT", EntryPrice: 10, Size: 50, EntryTime: config.Start}
	signalAt := config.Start.Add(15*time.Minute - time.Millisecond)
	fillAt := config.Start.Add(15 * time.Minute)
	err := runStage05Target(ledger, config, tradingcore.TargetAllocationStrategy{}, "AAAUSDT", tradingcore.Buy, .1, 10, 10, signalAt, fillAt, 1, .5, "rebalance_addition", "unknown", map[string]float64{"AAAUSDT": 10}, nil, ExitReasonTrace{Primary: "rebalance_addition"})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.positions["AAAUSDT"].Size != 50 {
		t.Fatalf("gross-limit dust residual changed the position: %+v", ledger.positions["AAAUSDT"])
	}
	if len(ledger.allocationDiagnostics) != 1 || ledger.allocationDiagnostics[0].Code != DiagnosticConstraintResidual {
		t.Fatalf("missing gross-limit dust diagnostic: %+v", ledger.allocationDiagnostics)
	}
}

func TestStage05NewTargetWithoutExecutableGrossCapacityIsRecorded(t *testing.T) {
	config, _ := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10}, "BBBUSDT": {10, 10, 10}}, []float64{100, 100, 100}, 0, 0)
	config.StrategyID = StrategyEqualWeightID
	config.StrategyVersion = "1.0.0"
	config.StrategyParameters = map[string]string{"target_gross": "0.5"}
	config.ExecutionPolicy.Constraints["BBBUSDT"] = SymbolConstraints{QuantityStep: .001, PriceTick: .01, MinQuantity: .001, MinNotional: 5}
	ledger := newBacktestMemoryLedger(config)
	ledger.cash = 500
	ledger.positions["AAAUSDT"] = &positionState{Symbol: "AAAUSDT", EntryPrice: 10, Size: 50, EntryTime: config.Start}
	signalAt := config.Start.Add(15*time.Minute - time.Millisecond)
	fillAt := config.Start.Add(15 * time.Minute)
	err := runStage05Target(ledger, config, tradingcore.TargetAllocationStrategy{}, "BBBUSDT", tradingcore.Buy, 10, 10, 10, signalAt, fillAt, 1, .5, "rebalance_addition", "unknown", map[string]float64{"AAAUSDT": 10, "BBBUSDT": 10}, nil, ExitReasonTrace{Primary: "rebalance_addition"})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.positions["BBBUSDT"] != nil {
		t.Fatalf("gross-capacity residual unexpectedly opened a position: %+v", ledger.positions["BBBUSDT"])
	}
	if len(ledger.allocationDiagnostics) != 1 || ledger.allocationDiagnostics[0].Code != DiagnosticConstraintResidual {
		t.Fatalf("missing gross-capacity diagnostic: %+v", ledger.allocationDiagnostics)
	}
}

func TestStage05LaterNewTargetBelowMinimumIsRecorded(t *testing.T) {
	config, _ := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10}, "BBBUSDT": {10, 10, 10}}, []float64{100, 100, 100}, 0, 0)
	config.StrategyID = StrategyEqualWeightID
	config.StrategyVersion = "1.0.0"
	config.StrategyParameters = map[string]string{"target_gross": "1"}
	config.ExecutionPolicy.Constraints["BBBUSDT"] = SymbolConstraints{QuantityStep: .001, PriceTick: .01, MinQuantity: .001, MinNotional: 5}
	ledger := newBacktestMemoryLedger(config)
	signalAt := config.Start.Add(15*time.Minute - time.Millisecond)
	fillAt := config.Start.Add(15 * time.Minute)
	err := runStage05Target(ledger, config, tradingcore.TargetAllocationStrategy{}, "BBBUSDT", tradingcore.Buy, .1, 10, 10, signalAt, fillAt, 1, .001, "rebalance_addition", "unknown", map[string]float64{"BBBUSDT": 10}, nil, ExitReasonTrace{Primary: "rebalance_addition"})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.positions["BBBUSDT"] != nil {
		t.Fatalf("below-minimum target unexpectedly opened a position: %+v", ledger.positions["BBBUSDT"])
	}
	if len(ledger.allocationDiagnostics) != 1 || ledger.allocationDiagnostics[0].Code != DiagnosticConstraintResidual {
		t.Fatalf("missing below-minimum target diagnostic: %+v", ledger.allocationDiagnostics)
	}
}

func TestStage05FullDustLiquidationIsRetainedAndRecorded(t *testing.T) {
	config, _ := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10}}, []float64{100, 100, 100}, 0, 0)
	config.StrategyID = StrategyEqualWeightID
	config.StrategyVersion = "1.0.0"
	config.StrategyParameters = map[string]string{"target_gross": "1"}
	config.ExecutionPolicy.Constraints["AAAUSDT"] = SymbolConstraints{QuantityStep: .001, PriceTick: .01, MinQuantity: .001, MinNotional: 5}
	ledger := newBacktestMemoryLedger(config)
	ledger.cash = 999
	ledger.positions["AAAUSDT"] = &positionState{Symbol: "AAAUSDT", EntryPrice: 10, Size: .1, EntryTime: config.Start}
	ledger.events = append(ledger.events, backtestLedgerEvent{Side: "buy", Symbol: "AAAUSDT", Quantity: ".1", Price: "10", At: config.Start})
	signalAt := config.Start.Add(15*time.Minute - time.Millisecond)
	fillAt := config.Start.Add(15 * time.Minute)
	err := runStage05Target(ledger, config, tradingcore.TargetAllocationStrategy{}, "AAAUSDT", tradingcore.Sell, .1, 10, 10, signalAt, fillAt, 1, 0, "final_liquidation", "unknown", map[string]float64{"AAAUSDT": 10}, nil, ExitReasonTrace{Primary: "final_liquidation"})
	if err != nil {
		t.Fatal(err)
	}
	if ledger.positions["AAAUSDT"] == nil || ledger.positions["AAAUSDT"].Size != .1 {
		t.Fatalf("unexecutable dust should remain marked: %+v", ledger.positions["AAAUSDT"])
	}
	if len(ledger.allocationDiagnostics) != 1 || ledger.allocationDiagnostics[0].Code != DiagnosticConstraintResidual {
		t.Fatalf("missing final dust diagnostic: %+v", ledger.allocationDiagnostics)
	}
}

func TestStage05RetainedSellResidualAddsOneRiskSlot(t *testing.T) {
	ledger := &backtestMemoryLedger{
		positions: map[string]*positionState{
			"DUSTUSDT": {Symbol: "DUSTUSDT", Size: .001},
			"LIVEUSDT": {Symbol: "LIVEUSDT", Size: 1},
		},
		allocationDiagnostics: []StrategyTraceDiagnostic{
			{Code: DiagnosticConstraintResidual, Symbol: "DUSTUSDT", Details: "side=sell requested=0.001 existing=0.001 provider=below_minimum_notional"},
			{Code: DiagnosticConstraintResidual, Symbol: "LIVEUSDT", Details: "side=buy requested=0.001 existing=1 provider=below_minimum_notional"},
		},
	}
	if got := stage05RetainedResidualSlots(ledger); got != 1 {
		t.Fatalf("residual slots=%d want 1", got)
	}
}

func TestStage05DeltaRebalanceTradesOnlyMemberReplacement(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10, 10}, "BBBUSDT": {10, 10, 10, 10}, "CCCUSDT": {10, 10, 10, 10}}, []float64{100, 100, 100, 100}, 0, 0)
	config.ReplaySnapshots = []ReplaySnapshot{{Timestamp: stage05CloseAt(config.Start, 0), ObservedComplete: true, Members: replayMembers("AAAUSDT", "BBBUSDT")}, {Timestamp: stage05CloseAt(config.Start, 1), ObservedComplete: true, Members: replayMembers("BBBUSDT", "CCCUSDT")}, {Timestamp: stage05CloseAt(config.Start, 2), ObservedComplete: true, Members: replayMembers("BBBUSDT", "CCCUSDT")}}
	selected, strategy, _ := DefaultStrategyRegistry.Resolve(StrategyEqualWeightID, "", map[string]string{"rebalance": "15m", "target_gross": "0.8", "final_policy": "liquidate"})
	result, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	bbbFills := 0
	for _, fill := range result.Artifacts.Fills {
		if fill.Symbol == "BBBUSDT" {
			bbbFills++
		}
	}
	if bbbFills != 2 {
		t.Fatalf("unchanged member was churned: %+v", result.Artifacts.Fills)
	}
}

func TestStage05ExecutableGapRespectsGrossAndRejectsMinimum(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10, 10}}, []float64{10, 10, 10, 10}, 0, 0)
	config.BenchmarkSeries[1].Open = 20
	selected, strategy, _ := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "target_gross": "0.5", "final_policy": "liquidate"})
	result, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Metrics.MaximumGrossExposure.Available || result.Metrics.MaximumGrossExposure.Value > .5000001 {
		t.Fatalf("gross=%+v", result.Metrics.MaximumGrossExposure)
	}
	config.ExecutionPolicy.Constraints["BTCUSDT"] = SymbolConstraints{QuantityStep: .00000001, PriceTick: .00000001, MinQuantity: .00000001, MinNotional: 2000}
	_, err = runStage05Strategy(config, series, selected, strategy, true)
	if !IsStrategyDiagnostic(err, DiagnosticAllocationRejected) {
		t.Fatalf("minimum rejection=%v", err)
	}
}

func TestStage05CommonExecutionClockIgnoresDifferingDecisionOpens(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	signalAt := start.Add(15*time.Minute - time.Millisecond)
	minute := func(price float64) []services.OHLCV {
		return []services.OHLCV{{OpenTime: start.Add(15 * time.Minute).UnixMilli(), Open: price, High: price, Low: price, Close: price, CloseTime: start.Add(16*time.Minute - time.Millisecond).UnixMilli()}}
	}
	config := BacktestConfig{End: start.Add(time.Hour), ExecutionSeriesRequired: true, ExecutionSeries: map[string][]services.OHLCV{"BTCUSDT": minute(100), "AAAUSDT": minute(50)}}
	decision := map[string][]services.OHLCV{"BTCUSDT": stage05Bars(start, []float64{10, 999}), "AAAUSDT": stage05Bars(start, []float64{20, 888})}
	fillAt, prices, ok := nextFillPrices(config, decision, []string{"AAAUSDT", "BTCUSDT"}, signalAt)
	if !ok || !fillAt.Equal(start.Add(15*time.Minute)) || prices["BTCUSDT"] != 100 || prices["AAAUSDT"] != 50 {
		t.Fatalf("fill=%s prices=%v ok=%v", fillAt, prices, ok)
	}
}

func TestStage05MetricsSeparateSlippageAndUseElapsedExposure(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	benchmark := []services.OHLCV{}
	for i := 0; i < 3; i++ {
		at := start.Add(time.Duration(i+1) * time.Hour)
		benchmark = append(benchmark, services.OHLCV{OpenTime: at.Add(-time.Hour).UnixMilli(), CloseTime: at.UnixMilli(), Open: 100, High: 100, Low: 100, Close: 100})
	}
	config := BacktestConfig{Start: start, End: start.Add(3 * time.Hour), InitialBalance: 100, TimeframeMinutes: 60, AtrAnnualizationDays: 365, BenchmarkSeries: benchmark}
	ledger := newBacktestMemoryLedger(config)
	ledger.events = []backtestLedgerEvent{{Side: "buy", Symbol: "A", Quantity: "1", Price: "101", ExecutionReferencePrice: "100", Fee: "0", At: start.Add(time.Hour), CashAfter: "-1"}, {Side: "sell", Symbol: "A", Quantity: "1", Price: "99", ExecutionReferencePrice: "100", Fee: "0", At: start.Add(2 * time.Hour), CashAfter: "98"}}
	ledger.cash = 98
	equity := []EquityPoint{{Time: start, Value: 100}, {Time: start.Add(time.Hour), Value: 100}, {Time: start.Add(2 * time.Hour), Value: 98}, {Time: start.Add(3 * time.Hour), Value: 98}}
	metrics := computeComparableMetrics(config, ledger, equity, map[string]float64{}, map[string][]services.OHLCV{"A": stage05Bars(start, []float64{100, 100, 100, 100})})
	if metrics.FeeCosts != "0" || metrics.SlippageCosts != "2" || metrics.TotalCosts != "2" {
		t.Fatalf("costs=%+v", metrics)
	}
	if !metrics.ExposureTime.Available || math.Abs(metrics.ExposureTime.Value-1.0/3.0) > 1e-9 {
		t.Fatalf("exposure=%+v", metrics.ExposureTime)
	}
	withEventPoint := []EquityPoint{equity[0], equity[1], {Time: start.Add(90 * time.Minute), Value: 99}, equity[2], equity[3]}
	eventMetrics := computeComparableMetrics(config, ledger, withEventPoint, map[string]float64{}, map[string][]services.OHLCV{"A": stage05Bars(start, []float64{100, 100, 100, 100})})
	if metrics.Sharpe != eventMetrics.Sharpe || metrics.Sortino != eventMetrics.Sortino {
		t.Fatalf("event insertion changed regular-clock ratios: %+v %+v", metrics, eventMetrics)
	}
}

func TestStage05UTCHourlyAggregationRejectsIncompleteBucket(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := stage05Bars(start, []float64{1, 2, 3, 4, 5, 6, 7})
	complete := completedUTCHourlyCloses(bars, start.Add(2*time.Hour))
	if len(complete) != 1 || complete[0].Close != 4 {
		t.Fatalf("hourly=%+v", complete)
	}
	offset := completedUTCHourlyCloses(bars[1:], start.Add(2*time.Hour))
	if len(offset) != 0 {
		t.Fatalf("incomplete offset bucket was fabricated: %+v", offset)
	}
}

func TestStage05MalformedDescriptorsRejected(t *testing.T) {
	base := StrategyDescriptor{SchemaVersion: StrategyDescriptorSchemaVersion, ID: "x", Version: "1", Description: "x", RequiredData: []StrategyDataRequirement{{Role: "decision", Timeframe: "15m"}, {Role: "execution", Timeframe: "1m"}}, DecisionCadence: "15m", RebalanceCadence: "1h", Risk: StrategyRiskDeclaration{MaxGrossExposure: "1", MaxNetExposure: "1", UsesSharedRisk: true}, Parameters: []StrategyParameterSpec{{Name: "n", Type: "integer", Default: "1"}}}
	cases := []StrategyDescriptor{cloneStrategyDescriptor(base), cloneStrategyDescriptor(base), cloneStrategyDescriptor(base), cloneStrategyDescriptor(base)}
	cases[0].RequiredData[0].Role = "future"
	cases[1].Risk.MaxGrossExposure = "NaN"
	cases[2].Parameters = append(cases[2].Parameters, cases[2].Parameters[0])
	cases[3].Parameters[0].Default = "bad"
	for i, descriptor := range cases {
		registry := NewStrategyRegistry()
		if err := registry.RegisterExecutable(descriptor, func(map[string]string) (tradingcore.Strategy, error) {
			return tradingcore.TargetAllocationStrategy{}, nil
		}, testCandidatePlanner{}); err == nil {
			t.Fatalf("case %d accepted", i)
		}
	}
	registry := NewStrategyRegistry()
	if err := registry.RegisterExecutable(base, nil, testCandidatePlanner{}); err == nil {
		t.Fatal("nil shared Strategy factory accepted")
	}
	if err := registry.RegisterExecutable(base, func(map[string]string) (tradingcore.Strategy, error) {
		return tradingcore.TargetAllocationStrategy{}, nil
	}, nil); err == nil {
		t.Fatal("nil planner accepted")
	}
}

func TestStage05RunManifestDigestChangesWithoutChangingDatasetIdentity(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": linearPrices(10, 84)}, linearPrices(100, 84), 0, 0)
	comparison, err := RunStage05Comparison(config, series, Stage05RunRequest{StrategyID: StrategyBenchmarkHoldID, Parameters: map[string]string{"warmup_bars": "1"}, TargetGrossExposure: "0.5", MaxNetExposure: "0.5", FinalPolicy: "liquidate", AllowInMemoryFixture: true})
	if err != nil {
		t.Fatal(err)
	}
	comparison2, err := RunStage05Comparison(config, series, Stage05RunRequest{StrategyID: StrategyBenchmarkHoldID, Parameters: map[string]string{"warmup_bars": "2"}, TargetGrossExposure: "0.5", MaxNetExposure: "0.5", FinalPolicy: "liquidate", AllowInMemoryFixture: true})
	if err != nil {
		t.Fatal(err)
	}
	var first, second ComparisonRow
	for _, row := range comparison.Rows {
		if row.StrategyID == StrategyBenchmarkHoldID {
			first = row
		}
	}
	for _, row := range comparison2.Rows {
		if row.StrategyID == StrategyBenchmarkHoldID {
			second = row
		}
	}
	if first.DatasetManifestID != second.DatasetManifestID || first.DatasetManifestID != config.DatasetManifestID {
		t.Fatalf("dataset identities changed: %q %q", first.DatasetManifestID, second.DatasetManifestID)
	}
	if first.ManifestIdentity == second.ManifestIdentity {
		t.Fatal("parameter change did not alter exact run manifest digest")
	}
	comparison.ArtifactDigest = strings.Repeat("0", 64)
	if _, err := MarshalComparisonArtifact(comparison); err == nil {
		t.Fatal("altered artifact digest was accepted")
	}
}
