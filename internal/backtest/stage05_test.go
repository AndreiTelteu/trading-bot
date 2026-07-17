package backtest

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"

	"trading-go/internal/services"
)

func TestStage05RegistryDeclarationsAndTypedValidation(t *testing.T) {
	descriptors := DefaultStrategyRegistry.List()
	if len(descriptors) != 6 {
		t.Fatalf("descriptors=%d", len(descriptors))
	}
	for _, descriptor := range descriptors {
		if descriptor.SchemaVersion != StrategyDescriptorSchemaVersion || descriptor.ID == "" || descriptor.Version == "" || !descriptor.Risk.UsesSharedRisk || descriptor.DecisionCadence == "" || descriptor.RebalanceCadence == "" {
			t.Fatalf("incomplete descriptor: %+v", descriptor)
		}
	}
	for i := range descriptors {
		for j := range descriptors[i].Parameters {
			if descriptors[i].Parameters[j].Minimum != nil {
				original := *descriptors[i].Parameters[j].Minimum
				*descriptors[i].Parameters[j].Minimum = 999
				fresh := DefaultStrategyRegistry.List()
				for _, candidate := range fresh {
					if candidate.ID == descriptors[i].ID {
						for _, parameter := range candidate.Parameters {
							if parameter.Name == descriptors[i].Parameters[j].Name && (parameter.Minimum == nil || *parameter.Minimum != original) {
								t.Fatal("registry descriptor minimum was externally mutable")
							}
						}
					}
				}
				goto immutableChecked
			}
		}
	}
immutableChecked:
	_, _, err := DefaultStrategyRegistry.Resolve("missing", "1.0.0", nil)
	if !IsStrategyDiagnostic(err, DiagnosticUnknownStrategy) {
		t.Fatalf("unknown error=%v", err)
	}
	_, _, err = DefaultStrategyRegistry.Resolve(StrategyMomentumID, "1.0.0", map[string]string{"top_n": "0"})
	if !IsStrategyDiagnostic(err, DiagnosticInvalidParameter) {
		t.Fatalf("parameter error=%v", err)
	}
	_, _, err = DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "1.0.0", map[string]string{"hidden": "1"})
	if !IsStrategyDiagnostic(err, DiagnosticInvalidParameter) {
		t.Fatalf("unknown parameter error=%v", err)
	}
}

func TestStage05ManifestReaderPreservesV3CompatibilityAsLegacyEvidence(t *testing.T) {
	legacy := []byte(`{"schema_version":"backtest-run-manifest-v3","strategy_version":"legacy-rule-v1","artifacts":{"schema_version":"backtest-artifacts-v1"}}`)
	manifest, err := UnmarshalRunManifest(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Strategy.Descriptor.ID != StrategyLegacyCompatibility || !manifest.Strategy.Descriptor.LegacyCompatibility || !manifest.Strategy.Descriptor.Baseline {
		t.Fatalf("legacy descriptor=%+v", manifest.Strategy.Descriptor)
	}
}

func TestStage05CashIsAuditableExactZeroRiskReference(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 11, 12, 13}}, []float64{100, 110, 120, 130}, 0, 0)
	selected, strategy, _ := DefaultStrategyRegistry.Resolve(StrategyCashID, "", nil)
	result, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Metrics.EndingEquity != "1000" || result.Metrics.Turnover != "0" || result.Metrics.TotalCosts != "0" || result.Metrics.FillCount != 0 || !result.Metrics.Reconciled {
		t.Fatalf("metrics=%+v", result.Metrics)
	}
	if !result.Metrics.TotalReturn.Available || result.Metrics.TotalReturn.Value != 0 || !result.Metrics.MaxDrawdown.Available || result.Metrics.MaxDrawdown.Value != 0 {
		t.Fatalf("zero metrics=%+v", result.Metrics)
	}
	if len(result.Artifacts.Decisions) == 0 || result.Artifacts.Decisions[0].Code != "cash_preserved" {
		t.Fatalf("decisions=%+v", result.Artifacts.Decisions)
	}
	if result.Manifest.Strategy.Descriptor.ID != StrategyCashID || result.Manifest.Strategy.Descriptor.Version != "1.0.0" {
		t.Fatalf("manifest strategy=%+v", result.Manifest.Strategy)
	}
}

func TestStage05RisingBenchmarkBuyHoldGoldenTimingEquityTurnover(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10, 10}}, []float64{100, 110, 120, 130}, 0, 0)
	selected, strategy, _ := DefaultStrategyRegistry.Resolve(StrategyBenchmarkHoldID, "", map[string]string{"warmup_bars": "1", "target_gross": "1", "final_policy": "liquidate"})
	result, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts.Fills) != 2 {
		t.Fatalf("fills=%+v", result.Artifacts.Fills)
	}
	first := result.Artifacts.Fills[0]
	decisionAt, _ := time.Parse(time.RFC3339Nano, first.DecisionAt)
	fillAt, _ := time.Parse(time.RFC3339Nano, first.FillAt)
	if !fillAt.After(decisionAt) {
		t.Fatalf("same-close fill: decision=%s fill=%s", decisionAt, fillAt)
	}
	wantEnding := 1000.0 / 110.0 * 130.0
	if math.Abs(ratFloat(result.Metrics.EndingEquity)-wantEnding) > 1e-5 {
		t.Fatalf("ending=%s want %.8f", result.Metrics.EndingEquity, wantEnding)
	}
	if math.Abs(result.Metrics.TotalReturn.Value-(wantEnding/1000-1)) > 1e-8 {
		t.Fatalf("return=%+v", result.Metrics.TotalReturn)
	}
	wantTurnover := 1000 + wantEnding
	if math.Abs(ratFloat(result.Metrics.Turnover)-wantTurnover) > 1e-5 {
		t.Fatalf("turnover=%s want %.8f", result.Metrics.Turnover, wantTurnover)
	}
	if result.Metrics.MaximumGrossExposure.Value <= 0 || result.Metrics.ExposureTime.Value <= 0 {
		t.Fatalf("exposure=%+v", result.Metrics)
	}
	mutated := cloneOHLCVSeries(series)
	for i := range mutated["AAAUSDT"] {
		mutated["AAAUSDT"][i].Close *= 1000
		mutated["AAAUSDT"][i].Open *= 1000
	}
	independent, err := runStage05Strategy(config, mutated, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Artifacts.Fills, independent.Artifacts.Fills) || result.Metrics.EndingEquity != independent.Metrics.EndingEquity {
		t.Fatal("benchmark result depended on candidate trade series")
	}
}

func TestStage05BenchmarkTrendExplicitEntryAndFallingExit(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10, 10, 10, 10}}, []float64{100, 110, 120, 105, 90, 80}, 0, 0)
	selected, strategy, _ := DefaultStrategyRegistry.Resolve(StrategyBenchmarkTrendID, "", map[string]string{"lookback_bars": "2", "sample_bars": "1", "target_gross": "1", "final_policy": "liquidate"})
	result, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Artifacts.Fills) != 2 {
		t.Fatalf("expected transition entry/exit fills: %+v", result.Artifacts.Fills)
	}
	if result.Artifacts.Fills[0].Side != "buy" || result.Artifacts.Fills[1].Side != "sell" {
		t.Fatalf("fills=%+v", result.Artifacts.Fills)
	}
	if !result.Metrics.Reconciled {
		t.Fatal("trend result did not reconcile")
	}
}

func TestStage05MomentumRotatingLeadersAndFlatAssetsDeterministic(t *testing.T) {
	prices := map[string][]float64{
		"AAAUSDT": {10, 11, 12, 12, 12, 12},
		"BBBUSDT": {10, 10, 10, 12, 14, 16},
		"CCCUSDT": {10, 10, 10, 10, 10, 10},
	}
	config, series := stage05Fixture(prices, []float64{100, 101, 102, 103, 104, 105}, 0, 0)
	config.ReplaySnapshots = []ReplaySnapshot{
		{Timestamp: stage05CloseAt(config.Start, 1), ObservedComplete: true, Members: replayMembers("AAAUSDT", "BBBUSDT", "CCCUSDT")},
		{Timestamp: stage05CloseAt(config.Start, 3), ObservedComplete: true, Members: replayMembers("AAAUSDT", "BBBUSDT", "CCCUSDT")},
	}
	selected, strategy, _ := DefaultStrategyRegistry.Resolve(StrategyMomentumID, "", map[string]string{"lookback_bars": "2", "top_n": "1", "rebalance": "30m", "target_gross": "0.8", "final_policy": "liquidate"})
	first, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("repeated momentum run was not byte deterministic")
	}
	selectedByTime := map[string][]string{}
	for _, rank := range first.Rankings {
		if rank.Selected {
			selectedByTime[rank.DecisionAt] = append(selectedByTime[rank.DecisionAt], rank.Symbol)
		}
		if rank.Symbol == "CCCUSDT" && rank.Score == 0 && rank.Selected {
			t.Fatal("flat asset gained artificial momentum edge")
		}
	}
	foundAAA, foundBBB := false, false
	for _, values := range selectedByTime {
		for _, symbol := range values {
			foundAAA = foundAAA || symbol == "AAAUSDT"
			foundBBB = foundBBB || symbol == "BBBUSDT"
		}
	}
	if !foundAAA || !foundBBB {
		t.Fatalf("rotating leaders not observed: %+v rankings=%+v", selectedByTime, first.Rankings)
	}
	for _, fill := range first.Artifacts.Fills {
		decisionAt, _ := time.Parse(time.RFC3339Nano, fill.DecisionAt)
		fillAt, _ := time.Parse(time.RFC3339Nano, fill.FillAt)
		if !fillAt.After(decisionAt) {
			t.Fatalf("lookahead fill=%+v", fill)
		}
	}
	if first.Artifacts.Fills[0].Symbol != "AAAUSDT" {
		t.Fatalf("future leader leaked into first decision: %+v", first.Artifacts.Fills[0])
	}
}

func TestStage05EqualWeightUsesPointInTimeEntriesExitsEmptyCompleteAndCosts(t *testing.T) {
	prices := map[string][]float64{"AAAUSDT": {10, 10, 10, 10, 10, 10}, "BBBUSDT": {20, 20, 20, 20, 20, 20}}
	config, series := stage05Fixture(prices, []float64{100, 100, 100, 100, 100, 100}, 25, 25)
	config.ReplaySnapshots = []ReplaySnapshot{
		{Timestamp: stage05CloseAt(config.Start, 0), ObservedComplete: true, Members: replayMembers("AAAUSDT")},
		{Timestamp: stage05CloseAt(config.Start, 1), ObservedComplete: true, Members: replayMembers("BBBUSDT")},
		{Timestamp: stage05CloseAt(config.Start, 2), ObservedComplete: true, Members: nil},
		{Timestamp: stage05CloseAt(config.Start, 3), ObservedComplete: true, Members: replayMembers("AAAUSDT", "BBBUSDT")},
	}
	selected, strategy, _ := DefaultStrategyRegistry.Resolve(StrategyEqualWeightID, "", map[string]string{"rebalance": "15m", "target_gross": "0.8", "final_policy": "liquidate"})
	costly, err := runStage05Strategy(config, series, selected, strategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if costly.Metrics.TotalCosts == "0" || costly.Metrics.Turnover == "0" {
		t.Fatalf("cost metrics=%+v", costly.Metrics)
	}
	for _, fill := range costly.Artifacts.Fills {
		if fill.CostVersion != config.ExecutionPolicy.CostVersion || ratFloat(fill.Fee) <= 0 {
			t.Fatalf("shared broker cost evidence missing: %+v", fill)
		}
	}
	if !(costly.Metrics.TotalReturn.Value < 0) {
		t.Fatalf("high turnover costs should reduce flat-series result: %+v", costly.Metrics.TotalReturn)
	}
	hasAAA, hasBBB := false, false
	for _, fill := range costly.Artifacts.Fills {
		hasAAA = hasAAA || fill.Symbol == "AAAUSDT"
		hasBBB = hasBBB || fill.Symbol == "BBBUSDT"
	}
	if !hasAAA || !hasBBB {
		t.Fatalf("point-in-time membership transitions missing: %+v", costly.Artifacts.Fills)
	}
	zeroConfig := config
	zeroConfig.FeeBps, zeroConfig.SlippageBps = 0, 0
	zeroSelected, zeroStrategy, _ := DefaultStrategyRegistry.Resolve(StrategyEqualWeightID, "", map[string]string{"rebalance": "15m", "target_gross": "0.8", "final_policy": "liquidate"})
	free, err := runStage05Strategy(zeroConfig, series, zeroSelected, zeroStrategy, true)
	if err != nil {
		t.Fatal(err)
	}
	if !(costly.Metrics.TotalReturn.Value < free.Metrics.TotalReturn.Value) {
		t.Fatalf("cost parity failed costly=%v free=%v", costly.Metrics.TotalReturn.Value, free.Metrics.TotalReturn.Value)
	}
}

func TestStage05FailuresAndGovernanceGate(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 11}}, []float64{100, 101}, 0, 0)
	selected, strategy, _ := DefaultStrategyRegistry.Resolve(StrategyBenchmarkTrendID, "", map[string]string{"lookback_bars": "20"})
	_, err := runStage05Strategy(config, series, selected, strategy, true)
	if !IsStrategyDiagnostic(err, DiagnosticInsufficientWarmup) {
		t.Fatalf("warmup error=%v", err)
	}
	incompleteConfig, incompleteSeries := stage05Fixture(map[string][]float64{"AAAUSDT": linearPrices(10, 4)}, linearPrices(100, 4), 0, 0)
	incompleteConfig.ReplaySnapshots = []ReplaySnapshot{{Timestamp: stage05CloseAt(incompleteConfig.Start, 0), ObservedComplete: false}}
	equal, equalStrategy, _ := DefaultStrategyRegistry.Resolve(StrategyEqualWeightID, "", map[string]string{"rebalance": "15m"})
	_, err = runStage05Strategy(incompleteConfig, incompleteSeries, equal, equalStrategy, true)
	if !IsStrategyDiagnostic(err, DiagnosticUniverseCoverage) {
		t.Fatalf("incomplete universe error=%v", err)
	}
	productionConfig := config
	productionConfig.DatasetManifestRequired = false
	productionConfig.DatasetManifestValidated = false
	_, err = RunStage05Comparison(productionConfig, series, Stage05RunRequest{StrategyID: StrategyBenchmarkHoldID})
	if !IsStrategyDiagnostic(err, DiagnosticManifestRequired) {
		t.Fatalf("manifest failure=%v", err)
	}

	config, series = stage05Fixture(map[string][]float64{"AAAUSDT": linearPrices(10, 84)}, linearPrices(100, 84), 0, 0)
	comparison, err := RunStage05Comparison(config, series, Stage05RunRequest{StrategyID: StrategyBenchmarkHoldID, TargetGrossExposure: "1", MaxNetExposure: "1", FinalPolicy: "liquidate", AllowInMemoryFixture: true})
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Governance.OptimizationAllowed || comparison.Governance.PromotionAllowed || len(comparison.Governance.Reasons) == 0 {
		t.Fatalf("baseline should not pass promotion: %+v", comparison.Governance)
	}
	encoded, err := MarshalComparisonArtifact(comparison)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := UnmarshalComparisonArtifact(encoded)
	if err != nil || decoded.ManifestID != comparison.ManifestID || len(decoded.Rows) != len(comparison.Rows) {
		t.Fatalf("comparison round trip=%+v err=%v", decoded, err)
	}
}

func TestStage05EqualExposureAssumptionsSerialized(t *testing.T) {
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": linearPrices(10, 84), "BBBUSDT": linearPrices(20, 84)}, linearPrices(100, 84), 0, 0)
	config.ReplaySnapshots = []ReplaySnapshot{{Timestamp: stage05CloseAt(config.Start, 0), ObservedComplete: true, Members: replayMembers("AAAUSDT", "BBBUSDT")}}
	comparison, err := RunStage05Comparison(config, series, Stage05RunRequest{StrategyID: StrategyEqualWeightID, Parameters: map[string]string{"rebalance": "24h"}, TargetGrossExposure: "0.6", MaxNetExposure: "0.6", FinalPolicy: "liquidate", AllowInMemoryFixture: true})
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Assumptions.MaxGrossExposure != "0.6" || comparison.Assumptions.MaxNetExposure != "0.6" {
		t.Fatalf("assumptions=%+v", comparison.Assumptions)
	}
	for _, row := range comparison.Rows {
		result := comparison.Results[row.StrategyID]
		if row.StrategyID != StrategyCashID && result.Manifest.Strategy.Parameters["target_gross"] != "0.6" {
			t.Fatalf("strategy %s target=%q", row.StrategyID, result.Manifest.Strategy.Parameters["target_gross"])
		}
		if result.Manifest.DatasetManifestID != config.DatasetManifestID || result.Manifest.FeeBPS != config.FeeBps || result.Manifest.ExecutionPolicy.CostVersion != config.ExecutionPolicy.CostVersion {
			t.Fatalf("non-comparable manifest for %s: %+v", row.StrategyID, result.Manifest)
		}
	}
	if !reflect.DeepEqual(comparison.Results[StrategyBenchmarkHoldID].Manifest.Dataset.Series, comparison.Results[StrategyEqualWeightID].Manifest.Dataset.Series) {
		t.Fatal("manifest series identities differ")
	}
}

func stage05Fixture(prices map[string][]float64, benchmark []float64, fee, slippage float64) (BacktestConfig, map[string][]services.OHLCV) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	series := map[string][]services.OHLCV{}
	constraints := map[string]SymbolConstraints{}
	symbols := []string{}
	for symbol, values := range prices {
		series[symbol] = stage05Bars(start, values)
		constraints[symbol] = SymbolConstraints{QuantityStep: .00000001, PriceTick: .00000001, MinQuantity: .00000001}
		symbols = append(symbols, symbol)
	}
	constraints["BTCUSDT"] = SymbolConstraints{QuantityStep: .00000001, PriceTick: .00000001, MinQuantity: .00000001}
	config := BacktestConfig{EngineMode: EngineShared, AccountID: "backtest", SettlementCurrency: "USDT", VenueID: "fixture", Symbols: symbols, UniverseMode: UniverseDynamicReplay, Start: start, End: start.Add(time.Duration(len(benchmark)) * 15 * time.Minute), Timeframe: "15m", TimeframeMinutes: 15, InitialBalance: 1000, FeeBps: fee, SlippageBps: slippage, MaxPositions: 10, ConstraintsAvailable: true, BenchmarkSymbol: "BTCUSDT", BenchmarkSeries: stage05Bars(start, benchmark), BenchmarkRequired: true, DatasetManifestID: "fixture-manifest", DatasetManifestValidated: true, DatasetManifestRequired: true, CodeRevision: "fixture", ConfigVersion: "fixture-v1", StrategyVersion: "1.0.0", ExecutionPolicy: ExecutionPolicy{Version: "next-executable-v1", Timing: ExecutionNextExecutable, Liquidity: LiquidityFullFillOHLCV, CostVersion: "fixture-cost-v1", Constraints: constraints}, DatasetSeries: []DatasetSeriesIdentity{{ExchangeSymbolID: "btc", AssetID: "btc", Ticker: "BTCUSDT", Role: "benchmark", Timeframe: "15m", Rows: len(benchmark), SeriesHash: "fixture"}}}
	return config, series
}

func stage05Bars(start time.Time, closes []float64) []services.OHLCV {
	result := make([]services.OHLCV, 0, len(closes))
	for i, close := range closes {
		openAt := start.Add(time.Duration(i) * 15 * time.Minute)
		result = append(result, services.OHLCV{OpenTime: openAt.UnixMilli(), Open: close, High: close, Low: close, Close: close, Volume: 1000, CloseTime: openAt.Add(15*time.Minute - time.Millisecond).UnixMilli()})
	}
	return result
}

func stage05CloseAt(start time.Time, index int) time.Time {
	return start.Add(time.Duration(index+1)*15*time.Minute - time.Millisecond)
}

func replayMembers(symbols ...string) []ReplayMember {
	result := make([]ReplayMember, 0, len(symbols))
	for i, symbol := range symbols {
		result = append(result, ReplayMember{Symbol: symbol, Rank: i + 1, Stage: "eligible", Shortlisted: true})
	}
	return result
}

func linearPrices(start float64, count int) []float64 {
	result := make([]float64, count)
	for i := range result {
		result[i] = start + float64(i)
	}
	return result
}
