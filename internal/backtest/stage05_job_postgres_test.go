package backtest

import (
	"strings"
	"testing"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/services"
	"trading-go/internal/testutil"
	"trading-go/internal/tradingcore"
)

func TestStage05ComparisonPersistenceIsBoundedAndSchemaVersioned(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	comparison := ComparisonArtifact{SchemaVersion: ComparisonSchemaVersion, ManifestID: "manifest-fixture", Candidate: "candidate@1.0.0", Assumptions: NormalizedAssumptions{StartingCapital: "1000", MaxGrossExposure: "1", MaxNetExposure: "1", DatasetManifestID: "manifest-fixture", FinalPolicy: "liquidate"}, Rows: []ComparisonRow{{StrategyID: StrategyCashID, StrategyVersion: "1.0.0", Baseline: true, Metrics: ComparableMetrics{SchemaVersion: EvaluationSchemaVersion, StartingCapital: "1000", EndingEquity: "1000", Reconciled: true}}, {StrategyID: "candidate", StrategyVersion: "1.0.0", Metrics: ComparableMetrics{SchemaVersion: EvaluationSchemaVersion, StartingCapital: "1000", EndingEquity: "1010", Reconciled: true}}}, Governance: GovernanceGate{SchemaVersion: GovernanceSchemaVersion, OptimizationAllowed: true, PromotionAllowed: true}}
	comparison.Governance.PromotionAllowed = false
	comparison.Governance.Reasons = []string{"pending_stage07_validation"}
	comparison.ArtifactDigest, _ = comparisonDigest(comparison)
	encoded, err := MarshalComparisonArtifact(comparison)
	if err != nil {
		t.Fatal(err)
	}
	value := string(encoded)
	job := database.BacktestJob{Status: "completed", Progress: 1, SummaryCompactJSON: &value, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	response, err := GetBacktestJobResponse(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if response.Comparison == nil || response.Summary != nil || response.Comparison.SchemaVersion != ComparisonSchemaVersion || len(response.Comparison.Rows) != 2 {
		t.Fatalf("response=%+v", response)
	}

	unbounded := comparison
	unbounded.Rows = make([]ComparisonRow, 17)
	if _, err := MarshalComparisonArtifact(unbounded); err == nil {
		t.Fatal("unbounded comparison artifact accepted")
	}
}

func TestStage05ProductionReplayUsesExactPersistedStagesAndStableIdentity(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	retrieved := base.Add(24 * time.Hour)
	assets := []database.Asset{{ID: "quote", CanonicalCode: "USDT", Source: "fixture", AvailableAt: base, RetrievedAt: retrieved}}
	for _, id := range []string{"active", "short", "ranked", "rejected", "rename"} {
		assets = append(assets, database.Asset{ID: id, CanonicalCode: strings.ToUpper(id), Source: "fixture", AvailableAt: base, RetrievedAt: retrieved})
	}
	if err := db.Create(&assets).Error; err != nil {
		t.Fatal(err)
	}
	symbols := []database.ExchangeSymbol{}
	for _, id := range []string{"active", "short", "ranked", "rejected"} {
		symbols = append(symbols, database.ExchangeSymbol{ID: "symbol-" + id, VenueID: "fixture", Ticker: strings.ToUpper(id) + "USDT", AssetID: id, BaseAssetID: id, QuoteAssetID: "quote", ListedAt: base, Version: 1, Source: "fixture", AvailableAt: base, RetrievedAt: retrieved})
	}
	renameAt := base.Add(30 * time.Minute)
	symbols = append(symbols, database.ExchangeSymbol{ID: "symbol-rename-old", VenueID: "fixture", Ticker: "OLDUSDT", AssetID: "rename", BaseAssetID: "rename", QuoteAssetID: "quote", ListedAt: base, DelistedAt: &renameAt, Version: 1, Source: "fixture", AvailableAt: base, RetrievedAt: retrieved}, database.ExchangeSymbol{ID: "symbol-rename-new", VenueID: "fixture", Ticker: "NEWUSDT", AssetID: "rename", BaseAssetID: "rename", QuoteAssetID: "quote", ListedAt: renameAt, Version: 1, Source: "fixture", AvailableAt: renameAt, RetrievedAt: retrieved})
	if err := db.Create(&symbols).Error; err != nil {
		t.Fatal(err)
	}
	manifestID := strings.Repeat("b", 64)
	manifest := database.DatasetManifest{ID: manifestID, ContentHash: manifestID, SchemaVersion: "point-in-time-dataset-manifest-v2", DatasetVersion: "stage05-fixture", RequestedStart: base, RequestedEnd: base.Add(time.Hour), EffectiveStart: base, EffectiveEnd: base.Add(time.Hour), KnowledgeCutoff: retrieved, Source: "fixture", ProvenanceJSON: "{}", BuildVersion: "fixture", SymbolsJSON: "[]", AssetsJSON: "[]", RolesTimeframesJSON: "[]", CoverageJSON: "[]", LimitationsJSON: "[]", CreatedAt: retrieved}
	if err := db.Create(&manifest).Error; err != nil {
		t.Fatal(err)
	}
	snapshot := database.UniverseSnapshot{SnapshotTime: base, PolicyVersion: "fixture", DatasetManifestID: &manifestID, CoverageState: "complete", CoverageJSON: "{}", CandidatePoolJSON: "[]", RebalanceInterval: "15m"}
	if err := db.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	rejectedReason := "policy_rejected"
	members := []database.UniverseMember{{UniverseSnapshotID: snapshot.ID, AssetID: strPtrLocal("active"), ExchangeSymbolID: strPtrLocal("symbol-active"), Symbol: "ACTIVEUSDT", Stage: "active", Rank: 1}, {UniverseSnapshotID: snapshot.ID, AssetID: strPtrLocal("short"), ExchangeSymbolID: strPtrLocal("symbol-short"), Symbol: "SHORTUSDT", Stage: "shortlist", Shortlisted: true, Rank: 2}, {UniverseSnapshotID: snapshot.ID, AssetID: strPtrLocal("ranked"), ExchangeSymbolID: strPtrLocal("symbol-ranked"), Symbol: "RANKEDUSDT", Stage: "ranked", Rank: 3}, {UniverseSnapshotID: snapshot.ID, AssetID: strPtrLocal("rejected"), ExchangeSymbolID: strPtrLocal("symbol-rejected"), Symbol: "REJECTEDUSDT", Stage: "rejected", RejectionReason: &rejectedReason, Rank: 4}}
	members = append(members, database.UniverseMember{UniverseSnapshotID: snapshot.ID, AssetID: strPtrLocal("rename"), ExchangeSymbolID: strPtrLocal("symbol-rename-old"), Symbol: "OLDUSDT", Stage: "active", Rank: 5})
	if err := db.Create(&members).Error; err != nil {
		t.Fatal(err)
	}
	second := database.UniverseSnapshot{SnapshotTime: renameAt, PolicyVersion: "fixture", DatasetManifestID: &manifestID, CoverageState: "complete", CoverageJSON: "{}", CandidatePoolJSON: "[]", RebalanceInterval: "15m"}
	if err := db.Create(&second).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.UniverseMember{UniverseSnapshotID: second.ID, AssetID: strPtrLocal("rename"), ExchangeSymbolID: strPtrLocal("symbol-rename-new"), Symbol: "NEWUSDT", Stage: "active", Rank: 1}).Error; err != nil {
		t.Fatal(err)
	}
	entries, err := loadReplaySnapshotsForManifest(base, base.Add(time.Hour), manifestID)
	if err != nil {
		t.Fatal(err)
	}
	public := canonicalReplaySnapshots(entries)
	if len(public) != 2 {
		t.Fatalf("snapshots=%+v", public)
	}
	eligible, err := eligibleReplaySymbols(public[0].Members)
	if err != nil || strings.Join(eligible, ",") != "ACTIVEUSDT,OLDUSDT,SHORTUSDT" {
		t.Fatalf("eligible=%v err=%v", eligible, err)
	}
	if public[0].Members[0].AssetID == "" || public[0].Members[0].ExchangeSymbolID == "" {
		t.Fatalf("stable identities lost: %+v", public[0].Members)
	}
	ledger := &backtestMemoryLedger{positions: map[string]*positionState{"OLDUSDT": {Symbol: "OLDUSDT", Size: 1}}}
	config := BacktestConfig{EconomicAssetIdentities: map[string]string{"OLDUSDT": "rename", "NEWUSDT": "rename"}, SymbolLifecycles: map[string]SymbolLifecycle{"OLDUSDT": {ListedAt: base, DelistedAt: &renameAt}, "NEWUSDT": {ListedAt: renameAt}}}
	transitionEconomicPositions(ledger, []string{"NEWUSDT"}, config, renameAt)
	if ledger.positions["OLDUSDT"] != nil || ledger.positions["NEWUSDT"] == nil || len(ledger.positions) != 1 {
		t.Fatalf("rename duplicated or lost exposure: %+v", ledger.positions)
	}
}

func strPtrLocal(value string) *string { return &value }

type stage05AssetCandidatePlanner struct{}

func (stage05AssetCandidatePlanner) Plan(context Stage05PlanningContext) (Stage05Plan, error) {
	if len(context.LastTargets) > 0 {
		return Stage05Plan{}, nil
	}
	return Stage05Plan{Targets: []string{"AAAUSDT"}, Decide: true}, nil
}

func TestStage05PersistedJobUsesCommonBenchmarkAndCandidateExecutionClock(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	manifestID := strings.Repeat("d", 64)
	manifest := database.DatasetManifest{ID: manifestID, ContentHash: manifestID, SchemaVersion: "point-in-time-dataset-manifest-v2", DatasetVersion: "execution-clock", RequestedStart: base, RequestedEnd: base.Add(21 * time.Hour), EffectiveStart: base, EffectiveEnd: base.Add(21 * time.Hour), KnowledgeCutoff: base.Add(24 * time.Hour), Source: "fixture", ProvenanceJSON: "{}", BuildVersion: "fixture", SymbolsJSON: "[]", AssetsJSON: "[]", RolesTimeframesJSON: "[]", CoverageJSON: "[]", LimitationsJSON: "[]", CreatedAt: base.Add(24 * time.Hour)}
	if err := db.Create(&manifest).Error; err != nil {
		t.Fatal(err)
	}
	id := "test_job_execution_candidate"
	descriptor := StrategyDescriptor{SchemaVersion: StrategyDescriptorSchemaVersion, ID: id, Version: "1.0.0", Description: "job execution clock candidate", RequiredData: []StrategyDataRequirement{{Role: "decision", Timeframe: "15m"}, {Role: "execution", Timeframe: "1m"}}, DecisionCadence: "15m", RebalanceCadence: "once", WarmupBars: 1, Risk: StrategyRiskDeclaration{MaxGrossExposure: "1", MaxNetExposure: "1", LongOnly: true, UsesSharedRisk: true}, Parameters: []StrategyParameterSpec{{Name: "target_gross", Type: "decimal", Default: "0.5", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Default: "liquidate", Enum: []string{"liquidate", "mark_to_market"}}}}
	if _, _, _, err := DefaultStrategyRegistry.ResolveExecutable(id, "1.0.0", nil); err != nil {
		if err := DefaultStrategyRegistry.RegisterExecutable(descriptor, func(map[string]string) (tradingcore.Strategy, error) {
			return tradingcore.TargetAllocationStrategy{}, nil
		}, stage05AssetCandidatePlanner{}); err != nil {
			t.Fatal(err)
		}
	}
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": linearPrices(10, 84)}, linearPrices(100, 84), 0, 0)
	config.DatasetManifestID = manifestID
	config.ExecutionSeriesRequired = true
	config.ExecutionTimeframe = "1m"
	config.ExecutionTimeframeMins = 1
	config.ConstraintResolver = func(string, time.Time) (SymbolConstraints, error) {
		return SymbolConstraints{QuantityStep: .00000001, PriceTick: .00000001, MinQuantity: .00000001}, nil
	}
	config.ExecutionSeries = map[string][]services.OHLCV{"AAAUSDT": stage05MinuteBars(config.Start, 84*15, 50), "BTCUSDT": stage05MinuteBars(config.Start, 84*15, 100)}
	config.DatasetSeries = []DatasetSeriesIdentity{{ExchangeSymbolID: "aaa", AssetID: "aaa", Ticker: "AAAUSDT", Role: "decision", Timeframe: "15m", Rows: 84, SeriesHash: "decision-aaa"}, {ExchangeSymbolID: "aaa", AssetID: "aaa", Ticker: "AAAUSDT", Role: "execution", Timeframe: "1m", Rows: 84 * 15, SeriesHash: "execution-aaa"}, {ExchangeSymbolID: "btc", AssetID: "btc", Ticker: "BTCUSDT", Role: "benchmark", Timeframe: "15m", Rows: 84, SeriesHash: "benchmark-btc"}, {ExchangeSymbolID: "btc", AssetID: "btc", Ticker: "BTCUSDT", Role: "execution", Timeframe: "1m", Rows: 84 * 15, SeriesHash: "execution-btc"}}
	job := database.BacktestJob{Status: "running", JobType: "stage05_comparison", DatasetManifestID: &manifestID, CreatedAt: base, UpdatedAt: base}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	comparison, err := executeAndPersistStage05ComparisonJob(job.ID, config, series, Stage05RunRequest{StrategyID: id, StrategyVersion: "1.0.0", TargetGrossExposure: "0.5", MaxNetExposure: "0.5", FinalPolicy: "liquidate"})
	if err != nil {
		t.Fatal(err)
	}
	benchmarkFills := comparison.Results[StrategyBenchmarkHoldID].Artifacts.Fills
	candidateFills := comparison.Results[id].Artifacts.Fills
	if len(benchmarkFills) == 0 || len(candidateFills) == 0 {
		t.Fatalf("expected executable fills, benchmark=%+v candidate=%+v", comparison.Results[StrategyBenchmarkHoldID], comparison.Results[id])
	}
	benchmarkFill := benchmarkFills[0]
	candidateFill := candidateFills[0]
	if benchmarkFill.FillAt != candidateFill.FillAt || benchmarkFill.Price == candidateFill.Price {
		t.Fatalf("execution parity benchmark=%+v candidate=%+v", benchmarkFill, candidateFill)
	}
	var persisted database.BacktestJob
	if err := db.First(&persisted, job.ID).Error; err != nil || persisted.Status != "completed" || persisted.ArtifactDigest == nil {
		t.Fatalf("persisted=%+v err=%v", persisted, err)
	}
}

func TestStage06CandidateRunsThroughPersistedManifestUniverseAndConstraintPath(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	count := 16*35 + 8
	end := base.Add(time.Duration(count) * 15 * time.Minute)
	manifestID := strings.Repeat("6", 64)
	manifest := database.DatasetManifest{ID: manifestID, ContentHash: manifestID, SchemaVersion: "point-in-time-dataset-manifest-v2", DatasetVersion: "stage06-production-fixture", RequestedStart: base, RequestedEnd: end, EffectiveStart: base, EffectiveEnd: end, KnowledgeCutoff: end.Add(time.Hour), Source: "fixture", ProvenanceJSON: "{}", BuildVersion: "fixture", SymbolsJSON: "[]", AssetsJSON: "[]", RolesTimeframesJSON: "[]", CoverageJSON: "[]", LimitationsJSON: "[]", CreatedAt: end}
	if err := db.Create(&manifest).Error; err != nil {
		t.Fatal(err)
	}
	retrieved := end.Add(time.Hour)
	assets := []database.Asset{
		{ID: "asset-aaa", CanonicalCode: "AAA", Source: "fixture", AvailableAt: base, RetrievedAt: retrieved},
		{ID: "asset-usdt", CanonicalCode: "USDT", Source: "fixture", AvailableAt: base, RetrievedAt: retrieved},
	}
	if err := db.Create(&assets).Error; err != nil {
		t.Fatal(err)
	}
	symbol := database.ExchangeSymbol{ID: "symbol-aaa", VenueID: "fixture", Ticker: "AAAUSDT", AssetID: "asset-aaa", BaseAssetID: "asset-aaa", QuoteAssetID: "asset-usdt", ListedAt: base, Version: 1, Source: "fixture", AvailableAt: base, RetrievedAt: retrieved}
	if err := db.Create(&symbol).Error; err != nil {
		t.Fatal(err)
	}
	for at := base; at.Before(end); at = at.Add(24 * time.Hour) {
		snapshot := database.UniverseSnapshot{SnapshotTime: at, PolicyVersion: "stage06-fixture", DatasetManifestID: &manifestID, CoverageState: "complete", CoverageJSON: "{}", CandidatePoolJSON: "[]", RebalanceInterval: "24h"}
		if err := db.Create(&snapshot).Error; err != nil {
			t.Fatal(err)
		}
		assetID, symbolID := "asset-aaa", "symbol-aaa"
		if err := db.Create(&database.UniverseMember{UniverseSnapshotID: snapshot.ID, AssetID: &assetID, ExchangeSymbolID: &symbolID, Symbol: "AAAUSDT", Stage: "active", Rank: 1}).Error; err != nil {
			t.Fatal(err)
		}
	}
	benchmarkPrices, assetPrices := make([]float64, count), make([]float64, count)
	for i := range benchmarkPrices {
		benchmarkPrices[i], assetPrices[i] = 100+float64(i)*.1, 20+float64(i)*.04
	}
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": assetPrices}, benchmarkPrices, 8, 3)
	config.DatasetManifestID, config.Start, config.End = manifestID, base, end
	config.BenchmarkSeries, series["AAAUSDT"] = stage05Bars(base, benchmarkPrices), stage05Bars(base, assetPrices)
	config.ExecutionSeriesRequired, config.ExecutionTimeframe, config.ExecutionTimeframeMins = true, "1m", 1
	config.ExecutionSeries = map[string][]services.OHLCV{"AAAUSDT": stage05MinuteBars(base, count*15, 20), "BTCUSDT": stage05MinuteBars(base, count*15, 100)}
	config.ConstraintResolver = func(string, time.Time) (SymbolConstraints, error) {
		return SymbolConstraints{QuantityStep: .0001, PriceTick: .01, MinQuantity: .0001, MinNotional: .001}, nil
	}
	config.SymbolIdentities = map[string]string{"AAAUSDT": "symbol-aaa", "BTCUSDT": "symbol-btc"}
	config.EconomicAssetIdentities = map[string]string{"AAAUSDT": "asset-aaa", "BTCUSDT": "asset-btc"}
	config.DatasetSeries = []DatasetSeriesIdentity{{ExchangeSymbolID: "symbol-aaa", AssetID: "asset-aaa", Ticker: "AAAUSDT", Role: "decision", Timeframe: "15m", Rows: count, SeriesHash: "decision-aaa"}, {ExchangeSymbolID: "symbol-aaa", AssetID: "asset-aaa", Ticker: "AAAUSDT", Role: "execution", Timeframe: "1m", Rows: count * 15, SeriesHash: "execution-aaa"}, {ExchangeSymbolID: "symbol-btc", AssetID: "asset-btc", Ticker: "BTCUSDT", Role: "benchmark", Timeframe: "15m", Rows: count, SeriesHash: "benchmark-btc"}, {ExchangeSymbolID: "symbol-btc", AssetID: "asset-btc", Ticker: "BTCUSDT", Role: "execution", Timeframe: "1m", Rows: count * 15, SeriesHash: "execution-btc"}}
	comparison, err := RunStage05Comparison(config, series, Stage05RunRequest{StrategyID: StrategyTrendMomentumCandidate, StrategyVersion: "1.0.0", Parameters: map[string]string{"lookback_bars": "20", "trend_bars": "20", "regime_bars": "20", "turnover_budget": "1"}, TargetGrossExposure: "0.75", MaxNetExposure: "0.75", FinalPolicy: "liquidate"})
	if err != nil {
		t.Fatal(err)
	}
	result := comparison.Results[StrategyTrendMomentumCandidate]
	if result.Manifest.DatasetManifestID != manifestID || len(result.Factors) == 0 || !result.Metrics.Reconciled || comparison.Governance.PromotionAllowed {
		t.Fatalf("stage06 production evidence=%+v governance=%+v", result, comparison.Governance)
	}
}

func stage05MinuteBars(start time.Time, count int, price float64) []services.OHLCV {
	result := make([]services.OHLCV, 0, count)
	for i := 0; i < count; i++ {
		open := start.Add(time.Duration(i) * time.Minute)
		result = append(result, services.OHLCV{OpenTime: open.UnixMilli(), Open: price, High: price, Low: price, Close: price, Volume: 1000, CloseTime: open.Add(time.Minute - time.Millisecond).UnixMilli()})
	}
	return result
}
