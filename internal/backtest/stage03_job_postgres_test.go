package backtest

import (
	"testing"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/services"
	"trading-go/internal/testutil"
)

func TestStage03FailedJobPersistsCoverageManifestsAndValidationWindows(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	job := database.BacktestJob{Status: "running", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	coverage := CoverageReport{SchemaVersion: CoverageSchemaVersion, PolicyVersion: "p", Passed: false, Reasons: []CoverageReason{CoverageInternalGap}}
	manifest := RunManifest{SchemaVersion: ManifestSchemaVersion, Classification: RunCoverageFailed, Coverage: coverage, Artifacts: ArtifactRefs{SchemaVersion: ArtifactSchemaVersion}}
	baseline := BacktestResult{Classification: RunCoverageFailed, Coverage: coverage, Manifest: manifest}
	vol := BacktestResult{Classification: RunCoverageFailed, Coverage: coverage, Manifest: manifest}
	validation := ValidationSummary{FailedWindows: []ValidationWindowFailure{{Window: WalkForwardWindow{TrainStart: time.Now()}, Lane: "test_baseline", Reason: "coverage", Classification: RunCoverageFailed, Coverage: coverage}}}
	failBacktestJobWithResults(job.ID, BacktestConfig{EngineMode: EngineShared}, nil, baseline, vol, validation, "validation", &CoverageError{Report: coverage})
	response, err := GetBacktestJobResponse(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "failed" || response.Summary == nil || response.Summary.Baseline.Classification != RunCoverageFailed || response.Summary.VolSizing.Classification != RunCoverageFailed || len(response.Summary.Validation.FailedWindows) != 1 {
		t.Fatalf("response=%+v", response)
	}
}

func TestStage03ReplayLoaderIncludesEffectiveBeforeStartAndStableMemberOrder(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	start := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	for _, symbol := range []string{"AAAUSDT", "ZZZUSDT"} {
		if err := db.Create(&database.UniverseSymbol{Symbol: symbol, Status: "TRADING", SpotTradable: true, FirstSeenAt: start.Add(-24 * time.Hour), LastSeenAt: start}).Error; err != nil {
			t.Fatal(err)
		}
	}
	snapshot := database.UniverseSnapshot{SnapshotTime: start.Add(-time.Hour), Members: []database.UniverseMember{{Symbol: "ZZZUSDT", RankScore: 1}, {Symbol: "AAAUSDT", RankScore: 2}}}
	if err := db.Create(&snapshot).Error; err != nil {
		t.Fatal(err)
	}
	entries, err := loadReplaySnapshots(start, start.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || len(entries[0].Members) != 2 || entries[0].Members[0].Symbol != "AAAUSDT" {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestStage03PreparationAnchorsBenchmarkAndLoadsConstraints(t *testing.T) {
	testutil.SetupPostgresDB(t)
	oldFetch, oldRevision, oldConstraints := fetchBacktestBars, resolveBacktestRevision, loadBacktestConstraints
	defer func() {
		fetchBacktestBars, resolveBacktestRevision, loadBacktestConstraints = oldFetch, oldRevision, oldConstraints
	}()
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := buildBacktestSeries(start, 800, 10, .1, 1)
	benchmarkBounded := false
	fetchBacktestBars = func(symbol, frame string, from, to time.Time) ([]services.OHLCV, error) {
		if symbol == "BTCUSDT" {
			if from.IsZero() || to.IsZero() {
				t.Fatal("benchmark fetched unbounded")
			}
			benchmarkBounded = true
		}
		return bars, nil
	}
	resolveBacktestRevision = func() (string, error) { return "revision-fixture", nil }
	loadBacktestConstraints = func([]string) (map[string]SymbolConstraints, error) {
		return map[string]SymbolConstraints{"AAAUSDT": {QuantityStep: .01, PriceTick: .01, MinQuantity: .1}}, nil
	}
	config, _, err := prepareBacktestInputsWithSettings(map[string]string{"backtest_symbols": "AAAUSDT", "backtest_engine_mode": "shared"})
	if err != nil {
		t.Fatal(err)
	}
	if !benchmarkBounded || config.EngineMode != EngineShared || config.CodeRevision != "revision-fixture" || !config.ConstraintsAvailable {
		t.Fatalf("config=%+v", config)
	}
	baseline, vol, err := runValidationPair(config, map[string][]services.OHLCV{"AAAUSDT": bars}, config.Start, config.End)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Manifest.SchemaVersion != ManifestSchemaVersion || vol.Manifest.SchemaVersion != ManifestSchemaVersion || baseline.SharedEngineRuns == 0 || vol.SharedEngineRuns == 0 {
		t.Fatalf("shared runtime not wired: baseline=%+v vol=%+v", baseline.Manifest, vol.Manifest)
	}
}
