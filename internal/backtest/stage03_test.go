package backtest

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"strconv"
	"testing"
	"time"

	"trading-go/internal/services"
)

func TestStage03CoverageFailuresAreTyped(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := buildBacktestSeries(start, 8, 10, .1, 10)
	base := BacktestConfig{EngineMode: EngineShared, InitialBalance: 1000, Symbols: []string{"AAAUSDT"}, Start: start, End: time.UnixMilli(bars[len(bars)-1].CloseTime), TimeframeMinutes: 15, CoveragePolicy: CoveragePolicy{Version: "coverage-test-v1", DecisionInterval: 15 * time.Minute, RequireRequestedBounds: true}}
	tests := []struct {
		name   string
		config BacktestConfig
		series map[string][]services.OHLCV
		reason CoverageReason
	}{
		{"missing decision", base, map[string][]services.OHLCV{}, CoverageMissingSeries},
		{"duplicate", base, map[string][]services.OHLCV{"AAAUSDT": append(append([]services.OHLCV{}, bars[:2]...), bars[1:]...)}, CoverageDuplicateTimestamp},
		{"non monotonic", base, map[string][]services.OHLCV{"AAAUSDT": append([]services.OHLCV{bars[1], bars[0]}, bars[2:]...)}, CoverageNonMonotonic},
		{"gap", base, map[string][]services.OHLCV{"AAAUSDT": append(append([]services.OHLCV{}, bars[:3]...), bars[4:]...)}, CoverageInternalGap},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := RunBacktest(test.config, test.series)
			var coverageErr *CoverageError
			if !errors.As(err, &coverageErr) {
				t.Fatalf("error = %T %v", err, err)
			}
			if result.Classification != RunCoverageFailed || !containsReason(coverageErr.Report.Reasons, test.reason) {
				t.Fatalf("result=%+v reasons=%v", result, coverageErr.Report.Reasons)
			}
		})
	}
}

func TestStage03ReplayAndBenchmarkCoverageFailClosed(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := buildBacktestSeries(start, 4, 10, .1, 10)
	config := BacktestConfig{EngineMode: EngineShared, InitialBalance: 1000, Symbols: []string{"AAAUSDT"}, Start: start, End: time.UnixMilli(bars[len(bars)-1].CloseTime), TimeframeMinutes: 15, UniverseMode: UniverseDynamicReplay, ReplaySnapshotsProvided: true}
	_, err := RunBacktest(config, map[string][]services.OHLCV{"AAAUSDT": bars})
	assertCoverageReason(t, err, CoverageReplayEmpty)
	config.ReplaySnapshots = []ReplaySnapshot{{Timestamp: start, Members: []ReplayMember{}}}
	_, err = RunBacktest(config, map[string][]services.OHLCV{"AAAUSDT": bars})
	assertCoverageReason(t, err, CoverageReplayMembersEmpty)
	config.UniverseMode, config.ReplaySnapshots = UniverseStatic, nil
	config.BenchmarkRequired, config.BenchmarkSymbol = true, "BTCUSDT"
	_, err = RunBacktest(config, map[string][]services.OHLCV{"AAAUSDT": bars})
	assertCoverageReason(t, err, CoverageBenchmarkMissing)
}

func TestStage03ValidNoTradeClassification(t *testing.T) {
	config, series := stage03SharedFixture()
	config.MinConfidenceToBuy = 999
	result, err := RunBacktest(config, series)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != RunStrategyZeroTrades || !result.Coverage.Passed {
		t.Fatalf("classification=%s coverage=%+v", result.Classification, result.Coverage)
	}
}

func TestStage03CloseSignalsFillStrictlyLaterAndDeterministically(t *testing.T) {
	config, series := stage03SharedFixture()
	first, err := RunBacktest(config, series)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunBacktest(config, cloneSeries(series))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Artifacts.Fills) == 0 {
		t.Fatal("expected fills")
	}
	for _, fill := range first.Artifacts.Fills {
		signal, _ := time.Parse(time.RFC3339Nano, fill.SignalAt)
		decision, _ := time.Parse(time.RFC3339Nano, fill.DecisionAt)
		ordered, _ := time.Parse(time.RFC3339Nano, fill.OrderAt)
		filled, _ := time.Parse(time.RFC3339Nano, fill.FillAt)
		if decision.Before(signal) || ordered.Before(decision) || !filled.After(ordered) {
			t.Fatalf("fill did not follow information: %+v", fill)
		}
	}
	a, err := MarshalArtifactBytes(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := MarshalArtifactBytes(second)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("identical inputs did not produce byte-stable artifacts")
	}
	if _, err := UnmarshalRunManifest(a.Manifest); err != nil {
		t.Fatalf("versioned manifest reader: %v", err)
	}
	invalid := append([]byte(nil), a.Manifest...)
	invalid = bytes.Replace(invalid, []byte(ManifestSchemaVersion), []byte("unknown-manifest-v9"), 1)
	if _, err := UnmarshalRunManifest(invalid); err == nil {
		t.Fatal("manifest reader accepted unknown schema")
	}
}

func TestStage03FutureMutationDoesNotChangeEarlierArtifacts(t *testing.T) {
	config, series := stage03SharedFixture()
	baseline, err := RunBacktest(config, series)
	if err != nil {
		t.Fatal(err)
	}
	mutated := cloneSeries(series)
	future := mutated["AAAUSDT"][len(mutated["AAAUSDT"])-1].OpenTime
	mutated["AAAUSDT"][len(mutated["AAAUSDT"])-1].Open *= 10
	mutated["AAAUSDT"][len(mutated["AAAUSDT"])-1].Close *= 10
	changed, err := RunBacktest(config, mutated)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fillsBefore(baseline.Artifacts.Fills, future), fillsBefore(changed.Artifacts.Fills, future)) {
		t.Fatal("future bar mutation changed earlier fills")
	}
	if !reflect.DeepEqual(decisionsBefore(baseline.Artifacts.Decisions, future), decisionsBefore(changed.Artifacts.Decisions, future)) {
		t.Fatal("future bar mutation changed earlier decisions")
	}
}

func TestStage03FutureMembershipInvisible(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	snapshots := fixtureReplaySnapshots([]ReplaySnapshot{{Timestamp: start, Members: []ReplayMember{{Symbol: "AAAUSDT", Rank: 1}}}, {Timestamp: start.Add(time.Hour), Members: []ReplayMember{{Symbol: "BBBUSDT", Rank: 1}}}})
	before := resolveReplayUniverse(snapshots, start.Add(30*time.Minute))
	if len(before.ActiveUniverse) != 1 || before.ActiveUniverse[0].Symbol != "AAAUSDT" {
		t.Fatalf("future membership leaked: %+v", before)
	}
}

func TestStage03CostsRoundingAndLedgerEquityReconcile(t *testing.T) {
	config, series := stage03SharedFixture()
	config.FeeBps, config.SlippageBps = 10, 5
	config.TimeStopBars, config.SellOnSignal = 0, false
	config.ExecutionPolicy.Constraints = map[string]SymbolConstraints{"AAAUSDT": {QuantityStep: .01, PriceTick: .01}, "BTCUSDT": {QuantityStep: .01, PriceTick: .01}}
	result, err := RunBacktest(config, series)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts.Fills) == 0 {
		t.Fatal("expected fills")
	}
	for _, fill := range result.Artifacts.Fills {
		quantity, price, fee := parseFloat(fill.Quantity), parseFloat(fill.Price), parseFloat(fill.Fee)
		if math.Abs(quantity/.01-math.Round(quantity/.01)) > 1e-8 || math.Abs(price/.01-math.Round(price/.01)) > 1e-8 {
			t.Fatalf("constraints not applied: %+v", fill)
		}
		if math.Abs(fee-quantity*price*.001) > 1e-8 {
			t.Fatalf("fee=%v want=%v fill=%+v", fee, quantity*price*.001, fill)
		}
	}
	lastEquity := result.Equity[len(result.Equity)-1].Value
	lastCash := parseFloat(result.Artifacts.Ledger[len(result.Artifacts.Ledger)-1].CashAfter)
	exposure := 0.0
	for _, item := range result.Artifacts.Exposure {
		exposure += parseFloat(item.Value)
	}
	if math.Abs(lastEquity-(lastCash+exposure)) > 1e-6 {
		t.Fatalf("equity=%v cash=%v exposure=%v", lastEquity, lastCash, exposure)
	}
	foundLiquidation := false
	for _, trade := range result.Trades {
		if trade.Reason == "final_liquidation" {
			foundLiquidation = true
		}
	}
	if !foundLiquidation || result.SharedLedgerEvents < len(result.Trades)*2 {
		t.Fatal("final liquidation did not traverse shared broker/ledger")
	}
}

func TestStage03GatingZeroTradesClassification(t *testing.T) {
	config, series := stage03SharedFixture()
	config.MaxPositions = 0
	result, err := RunBacktest(config, series)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != RunGatingZeroTrades {
		t.Fatalf("classification=%s", result.Classification)
	}
}

func TestStage03UnsupportedLiquidityFailsExplicitly(t *testing.T) {
	config, series := stage03SharedFixture()
	config.ExecutionPolicy.Liquidity = LiquidityPartialFill
	_, err := RunBacktest(config, series)
	var unsupported *UnsupportedRealismError
	if !errors.As(err, &unsupported) {
		t.Fatalf("error=%T %v", err, err)
	}
}

func TestStage03EndOfPeriodNeverUsesLaterExecutionPrice(t *testing.T) {
	config, series := stage03SharedFixture()
	config.End = time.UnixMilli(series["AAAUSDT"][len(series["AAAUSDT"])-1].CloseTime)
	late := services.OHLCV{OpenTime: config.End.Add(time.Minute).UnixMilli(), CloseTime: config.End.Add(2 * time.Minute).UnixMilli(), Open: 999999, High: 999999, Low: 999999, Close: 999999, Volume: 1}
	config.ExecutionSeries = map[string][]services.OHLCV{"AAAUSDT": {late}, "BTCUSDT": {late}}
	config.CoveragePolicy = CoveragePolicy{Version: "allow-sparse-execution-v1", DecisionInterval: 15 * time.Minute, MaxMissingIntervals: 1000}
	result, err := RunBacktest(config, series)
	if err != nil {
		t.Fatal(err)
	}
	for _, fill := range result.Artifacts.Fills {
		at, _ := time.Parse(time.RFC3339Nano, fill.FillAt)
		if at.After(config.End) || fill.Price == "999999" {
			t.Fatalf("borrowed post-period price: %+v", fill)
		}
	}
}

func TestStage03EndBoundExcludesBarClosingAfterEnd(t *testing.T) {
	config, series := stage03SharedFixture()
	bars := series["AAAUSDT"]
	end := time.UnixMilli(bars[len(bars)-2].CloseTime)
	leaked := bars[len(bars)-1]
	leaked.OpenTime = end.UnixMilli()
	leaked.CloseTime = end.Add(15 * time.Minute).UnixMilli()
	config.End = end
	base := cloneSeries(series)
	base["AAAUSDT"] = append([]services.OHLCV(nil), bars[:len(bars)-1]...)
	mutated := cloneSeries(base)
	leaked.Close = 999999
	leaked.High = 999999
	mutated["AAAUSDT"] = append(mutated["AAAUSDT"], leaked)
	first, err := RunBacktest(config, base)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RunBacktest(config, mutated)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := MarshalArtifactBytes(first)
	b, _ := MarshalArtifactBytes(second)
	if !bytes.Equal(a.Decisions, b.Decisions) || !reflect.DeepEqual(first.Equity, second.Equity) {
		t.Fatal("post-end close information changed run")
	}
}

func TestStage03ReplayRawValidationAndStableOrder(t *testing.T) {
	config, series := stage03SharedFixture()
	config.Symbols = []string{"AAAUSDT"}
	delete(series, "BTCUSDT")
	config.UniverseMode = UniverseDynamicReplay
	config.ReplaySnapshotsProvided = true
	config.CoveragePolicy = CoveragePolicy{Version: "replay-v1", DecisionInterval: 15 * time.Minute, ReplayInterval: time.Hour}
	start := config.Start
	member := ReplayMember{Symbol: "AAAUSDT", Rank: 1, Shortlisted: true}
	cases := []struct {
		name      string
		snapshots []ReplaySnapshot
		reason    CoverageReason
	}{{"duplicate timestamp", []ReplaySnapshot{{Timestamp: start, Members: []ReplayMember{member}}, {Timestamp: start, Members: []ReplayMember{member}}}, CoverageReplayDuplicate}, {"nonmonotonic", []ReplaySnapshot{{Timestamp: start, Members: []ReplayMember{member}}, {Timestamp: start.Add(-time.Hour), Members: []ReplayMember{member}}}, CoverageNonMonotonic}, {"duplicate member", []ReplaySnapshot{{Timestamp: start, Members: []ReplayMember{member, member}}}, CoverageReplayMemberDup}, {"gap", []ReplaySnapshot{{Timestamp: start, Members: []ReplayMember{member}}, {Timestamp: start.Add(3 * time.Hour), Members: []ReplayMember{member}}}, CoverageReplayGap}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			config.ReplaySnapshots = tc.snapshots
			_, err := RunBacktest(config, series)
			assertCoverageReason(t, err, tc.reason)
		})
	}
	entries := fixtureReplaySnapshots([]ReplaySnapshot{{Timestamp: start, Members: []ReplayMember{{Symbol: "ZZZUSDT", Rank: 2, Shortlisted: true}, {Symbol: "AAAUSDT", Rank: 1, Shortlisted: true}}}})
	resolved := resolveReplayUniverse(entries, start)
	if len(resolved.Shortlist) != 2 || resolved.Shortlist[0].Symbol != "AAAUSDT" {
		t.Fatalf("unstable replay order: %+v", resolved.Shortlist)
	}
}

func TestStage03FeatureCoverageAsOfAndFutureMutation(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	series := FeatureSeries{Name: "f", Version: "v1", Provenance: "fixture", Interval: time.Minute, Observations: []FeatureObservation{{EventAt: start, AvailableAt: start.Add(time.Minute), Value: 1}, {EventAt: start.Add(time.Minute), AvailableAt: start.Add(2 * time.Minute), Value: 2}}}
	if got := series.AsOf(start.Add(time.Minute)); len(got) != 1 || got[0].Value != 1 {
		t.Fatalf("AsOf leaked future: %+v", got)
	}
	mutated := series
	mutated.Observations = append([]FeatureObservation(nil), series.Observations...)
	mutated.Observations[1].Value = 999
	if !reflect.DeepEqual(series.AsOf(start.Add(time.Minute)), mutated.AsOf(start.Add(time.Minute))) {
		t.Fatal("future feature mutation changed earlier view")
	}
}

func TestStage03ArtifactEnvelopeRejectsUnknownVersion(t *testing.T) {
	config, series := stage03SharedFixture()
	result, err := RunBacktest(config, series)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := MarshalArtifactBytes(result)
	if err != nil {
		t.Fatal(err)
	}
	var decisions []DecisionArtifact
	if err := UnmarshalArtifact(artifacts.Decisions, &decisions); err != nil {
		t.Fatal(err)
	}
	invalid := bytes.Replace(artifacts.Decisions, []byte(ArtifactSchemaVersion), []byte("artifact-v99"), 1)
	if err := UnmarshalArtifact(invalid, &decisions); err == nil {
		t.Fatal("accepted unknown artifact schema")
	}
}

func TestStage03ProductionEngineResolution(t *testing.T) {
	mode, err := resolveBacktestEngine(map[string]string{"trading_engine_mode": "legacy"})
	if err != nil || mode != EngineShared {
		t.Fatalf("default mode=%s err=%v", mode, err)
	}
	if _, err := resolveBacktestEngine(map[string]string{"backtest_engine_mode": "legacy"}); err == nil {
		t.Fatal("production job accepted legacy backtest engine")
	}
}

func TestStage03ClassificationUsesWholeRunEvidence(t *testing.T) {
	first := newBacktestMemoryLedger(BacktestConfig{})
	first.evidence = RunEvidence{UniverseEvaluations: 10, CandidateEvaluations: 4, StrategyNoActions: 4}
	second := newBacktestMemoryLedger(BacktestConfig{})
	second.evidence = first.evidence
	second.evidence.UniverseUnavailable = 1
	if classifySharedRun(first) != RunStrategyZeroTrades || classifySharedRun(second) != RunStrategyZeroTrades {
		t.Fatal("final universe state reclassified identical earlier evidence")
	}
}

func TestStage03FractionalBPSAndMinimumQuantityFailClosed(t *testing.T) {
	config, series := stage03SharedFixture()
	config.FeeBps = 1.5
	if _, err := RunBacktest(config, series); err == nil {
		t.Fatal("fractional bps silently truncated")
	}
	config.FeeBps = 1
	config.ExecutionPolicy.Constraints = map[string]SymbolConstraints{"AAAUSDT": {QuantityStep: .01, PriceTick: .01, MinQuantity: 1000}, "BTCUSDT": {QuantityStep: .01, PriceTick: .01, MinQuantity: 1000}}
	result, err := RunBacktest(config, series)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != RunGatingZeroTrades || result.SharedLedgerEvents != 0 {
		t.Fatalf("minimum quantity not rejected: %+v", result)
	}
}

func TestStage03ProtectiveOHLCPolicyIsSequentialAndConservative(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	stop, target := 95.0, 105.0
	pos := &positionState{EntryPrice: 100, EntryTime: start.Add(-time.Minute), StopPrice: &stop, TakeProfit: &target}
	both := services.OHLCV{OpenTime: start.UnixMilli(), CloseTime: start.Add(time.Minute).UnixMilli(), Open: 100, High: 106, Low: 94, Close: 100}
	decision := services.OHLCV{OpenTime: start.UnixMilli(), CloseTime: start.Add(15 * time.Minute).UnixMilli()}
	result := evaluateIntrabarProtectiveExit(buildExecutionSymbolStates(map[string][]services.OHLCV{"X": {both}})["X"], decision, pos, BacktestConfig{})
	if result == nil || result.Reason != services.CloseReasonStopLoss || !result.Time.Equal(start.Add(time.Minute)) {
		t.Fatalf("ambiguous bar policy=%+v", result)
	}
	gap := both
	gap.Open = 90
	result = evaluateIntrabarProtectiveExit(buildExecutionSymbolStates(map[string][]services.OHLCV{"X": {gap}})["X"], decision, pos, BacktestConfig{})
	if result.Price != 90 || !result.Time.Equal(start) {
		t.Fatalf("gap policy=%+v", result)
	}
	first := services.OHLCV{OpenTime: start.UnixMilli(), CloseTime: start.Add(time.Minute).UnixMilli(), Open: 100, High: 110, Low: 99, Close: 109}
	later := services.OHLCV{OpenTime: start.Add(time.Minute).UnixMilli(), CloseTime: start.Add(2 * time.Minute).UnixMilli(), Open: 109, High: 110, Low: 100, Close: 101}
	trailPos := &positionState{EntryPrice: 100, EntryTime: start.Add(-time.Minute)}
	cfg := BacktestConfig{TrailingStopEnabled: true, TrailingStopPercent: 5}
	earlyOnly := evaluateIntrabarProtectiveExit(buildExecutionSymbolStates(map[string][]services.OHLCV{"X": {first}})["X"], decision, trailPos, cfg)
	if earlyOnly != nil {
		t.Fatalf("future trailing exit leaked early: %+v", earlyOnly)
	}
	trailPos = &positionState{EntryPrice: 100, EntryTime: start.Add(-time.Minute)}
	all := evaluateIntrabarProtectiveExit(buildExecutionSymbolStates(map[string][]services.OHLCV{"X": {first, later}})["X"], decision, trailPos, cfg)
	if all == nil || !all.Time.Equal(start.Add(2*time.Minute)) {
		t.Fatalf("sequential trailing=%+v", all)
	}
}

func TestStage03SharedEntryCarriesKnownATRIntoProjection(t *testing.T) {
	config, _ := stage03SharedFixture()
	config.Symbols = []string{"AAAUSDT"}
	config.InitialBalance = 1000
	config.MaxPositions = 1
	config.EntryPercent = 10
	defaultStage03Policies(&config)
	ledger := newBacktestMemoryLedger(config)
	at := config.Start.Add(200 * 15 * time.Minute)
	states := map[string]*symbolState{"AAAUSDT": {lastPrice: 50}}
	_, err := runSharedBacktestEntry(
		ledger,
		config,
		entryCandidate{Symbol: "AAAUSDT", Rank: 1},
		barContext{Rating: 10, Signal: "STRONG_BUY", Atr: 2.5},
		backtestUniverseSelection{RegimeState: services.UniverseRegimeRiskOn},
		ledger.positions,
		states,
		ledger.cash,
		at,
		at,
		at.Add(time.Minute),
		50,
		50,
	)
	if err != nil {
		t.Fatal(err)
	}
	position := ledger.positions["AAAUSDT"]
	if position == nil || position.LastAtr != 2.5 {
		t.Fatalf("shared projection lost entry ATR: %+v", position)
	}
}

func TestStage03CoverageAcceptsBinanceInclusiveCloseTimestamp(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := buildBacktestSeries(start, 4, 10, .1, 10)
	for i := range bars {
		bars[i].CloseTime-- // Binance kline close is the inclusive interval endpoint.
	}
	config := BacktestConfig{
		Symbols:          []string{"AAAUSDT"},
		Start:            start,
		End:              time.UnixMilli(bars[len(bars)-1].CloseTime).Add(time.Millisecond),
		TimeframeMinutes: 15,
		CoveragePolicy: CoveragePolicy{
			Version:                "binance-width-v1",
			DecisionInterval:       15 * time.Minute,
			RequireRequestedBounds: true,
		},
	}
	report := ValidateCoverage(config, map[string][]services.OHLCV{"AAAUSDT": bars})
	if !report.Passed {
		t.Fatalf("inclusive Binance close timestamps rejected: %+v", report)
	}
}

func stage03SharedFixture() (BacktestConfig, map[string][]services.OHLCV) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	series := map[string][]services.OHLCV{"BTCUSDT": buildBacktestSeries(start, 800, 100, .01, 20), "AAAUSDT": buildBacktestSeries(start, 800, 50, .08, 30)}
	config := BacktestConfig{EngineMode: EngineShared, CodeRevision: "fixture-revision", ConfigVersion: "fixture-config-v1", StrategyVersion: "fixture-strategy-v1", Seed: 7, Symbols: []string{"BTCUSDT", "AAAUSDT"}, UniverseMode: UniverseStatic, UniversePolicy: services.UniversePolicy{TopK: 2, AnalyzeTopN: 2}, Start: start, End: start.Add(800 * 15 * time.Minute), IndicatorConfig: services.DefaultIndicatorConfig(), IndicatorWeights: map[string]float64{"rsi": 1, "macd": 1, "bollinger": 1, "volume": .5, "momentum": 1}, Timeframe: "15m", TimeframeMinutes: 15, InitialBalance: 1000, FeeBps: 10, SlippageBps: 5, MaxPositions: 1, StrategyMode: StrategyBaseline, EntryPercent: 20, MinConfidenceToBuy: 0, TimeStopBars: 4, AllowSellAtLoss: true}
	return config, series
}

func containsReason(values []CoverageReason, wanted CoverageReason) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func assertCoverageReason(t *testing.T, err error, reason CoverageReason) {
	t.Helper()
	var target *CoverageError
	if !errors.As(err, &target) || !containsReason(target.Report.Reasons, reason) {
		t.Fatalf("error=%T %v", err, err)
	}
}
func cloneSeries(values map[string][]services.OHLCV) map[string][]services.OHLCV {
	out := map[string][]services.OHLCV{}
	for symbol, bars := range values {
		out[symbol] = append([]services.OHLCV(nil), bars...)
	}
	return out
}
func fillsBefore(values []FillArtifact, before int64) []FillArtifact {
	out := []FillArtifact{}
	for _, value := range values {
		at, _ := time.Parse(time.RFC3339Nano, value.FillAt)
		if at.UnixMilli() < before {
			out = append(out, value)
		}
	}
	return out
}
func decisionsBefore(values []DecisionArtifact, before int64) []DecisionArtifact {
	out := []DecisionArtifact{}
	for _, value := range values {
		at, _ := time.Parse(time.RFC3339Nano, value.DecisionAt)
		if at.UnixMilli() < before {
			out = append(out, value)
		}
	}
	return out
}
func parseFloat(value string) float64 { number, _ := strconv.ParseFloat(value, 64); return number }
