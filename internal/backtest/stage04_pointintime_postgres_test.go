package backtest

import (
	"strings"
	"testing"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/pointintime"
	"trading-go/internal/testutil"
)

func TestStage04PointInTimePreparationUsesExactSeriesRoles(t *testing.T) {
	t.Setenv("BACKTEST_CODE_REVISION", "stage04-runtime-fixture")
	db := testutil.SetupPostgresDB(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	assets := []database.Asset{
		{ID: "asset-aaa", CanonicalCode: "AAA", Source: "fixture", AvailableAt: base.Add(-time.Hour), RetrievedAt: base},
		{ID: "asset-btc", CanonicalCode: "BTC", Source: "fixture", AvailableAt: base.Add(-time.Hour), RetrievedAt: base},
		{ID: "asset-usdt", CanonicalCode: "USDT", Source: "fixture", AvailableAt: base.Add(-time.Hour), RetrievedAt: base},
	}
	if err := db.Create(&assets).Error; err != nil {
		t.Fatal(err)
	}
	symbols := []database.ExchangeSymbol{
		{ID: "symbol-aaa", VenueID: "binance", Ticker: "AAAUSDT", AssetID: "asset-aaa", BaseAssetID: "asset-aaa", QuoteAssetID: "asset-usdt", ListedAt: base.Add(-time.Hour), AvailableAt: base.Add(-time.Hour), Version: 1, Source: "fixture", RetrievedAt: base},
		{ID: "symbol-btc", VenueID: "binance", Ticker: "BTCUSDT", AssetID: "asset-btc", BaseAssetID: "asset-btc", QuoteAssetID: "asset-usdt", ListedAt: base.Add(-time.Hour), AvailableAt: base.Add(-time.Hour), Version: 1, Source: "fixture", RetrievedAt: base},
	}
	if err := db.Create(&symbols).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.SymbolConstraintVersion{ExchangeSymbolID: "symbol-aaa", EffectiveFrom: base.Add(-time.Hour), QuantityStep: "0.01", PriceTick: "0.01", MinQuantity: "0.01", MinNotional: "1", Source: "fixture", AvailableAt: base.Add(-time.Hour), RetrievedAt: base}).Error; err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		symbolID string
		role     string
		price    string
	}{{"symbol-aaa", pointintime.RoleDecision, "10"}, {"symbol-btc", pointintime.RoleBenchmark, "100"}} {
		for index := 0; index < 2; index++ {
			at := base.Add(time.Duration(index) * 15 * time.Minute)
			bar := database.HistoricalBar{ExchangeSymbolID: sample.symbolID, Timeframe: "15m", OpenTime: at, DatasetVersion: "runtime-fixture-v1", Role: sample.role, Open: sample.price, High: sample.price, Low: sample.price, Close: sample.price, Volume: "100", QuoteVolume: "1000", QualityStatus: "valid", QualityFlagsJSON: "[]", Source: "fixture", ProvenanceJSON: "{}", AvailableAt: at.Add(15 * time.Minute).Add(-time.Millisecond), RetrievedAt: at.Add(15 * time.Minute), ContentHash: strings.Repeat(string(rune('a'+index)), 64), CreatedAt: at}
			if err := db.Create(&bar).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	manifest, err := pointintime.BuildManifest(db, pointintime.BuildRequest{DatasetVersion: "runtime-fixture-v1", RequestedStart: base, RequestedEnd: base.Add(30 * time.Minute), Source: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	escape := database.ExchangeSymbol{ID: "symbol-aaa-unmanifested", VenueID: "binance", Ticker: "AAAUSDT", AssetID: "asset-aaa", BaseAssetID: "asset-aaa", QuoteAssetID: "asset-usdt", ListedAt: base, AvailableAt: base.Add(time.Hour), Version: 2, Source: "fixture", RetrievedAt: base.Add(time.Hour)}
	if err := db.Create(&escape).Error; err != nil {
		t.Fatal(err)
	}
	escapeBar := database.HistoricalBar{ExchangeSymbolID: escape.ID, Timeframe: "15m", OpenTime: base, DatasetVersion: "runtime-fixture-v1", Role: pointintime.RoleDecision, Open: "999", High: "999", Low: "999", Close: "999", Volume: "999", QuoteVolume: "999", QualityStatus: "valid", QualityFlagsJSON: "[]", Source: "fixture", ProvenanceJSON: "{}", AvailableAt: base.Add(15 * time.Minute).Add(-time.Millisecond), RetrievedAt: base.Add(time.Hour), ContentHash: strings.Repeat("f", 64)}
	if err := db.Create(&escapeBar).Error; err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{
		"backtest_dataset_manifest_id": manifest.ID,
		"backtest_start":               base.Format(time.RFC3339),
		"backtest_end":                 base.Add(30 * time.Minute).Format(time.RFC3339),
		"backtest_benchmark_symbol":    "BTCUSDT",
		"backtest_engine_mode":         string(EngineShared),
	}
	config, series, err := preparePointInTimeBacktestInputs(settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Symbols) != 1 || config.Symbols[0] != "AAAUSDT" || len(series["AAAUSDT"]) != 2 {
		t.Fatalf("default tradable selection/config=%v series=%v", config.Symbols, series)
	}
	if config.SymbolIdentities["AAAUSDT"] != "symbol-aaa" || len(config.DatasetSeries) != 2 {
		t.Fatalf("unmanifested ticker version escaped exact binding: identities=%v audit=%+v", config.SymbolIdentities, config.DatasetSeries)
	}
	runManifest := buildManifest(config, CoverageReport{SchemaVersion: CoverageSchemaVersion, Passed: true}, RunSuccessfulExecution, manifest.ID)
	if runManifest.Dataset.ManifestID != manifest.ID || runManifest.Dataset.KnowledgeCutoff == "" || len(runManifest.Dataset.Series) != 2 || runManifest.Dataset.Series[0].ExchangeSymbolID == "symbol-aaa-unmanifested" {
		t.Fatalf("run audit provenance=%+v", runManifest.Dataset)
	}

	settings["backtest_symbols"] = "BTCUSDT"
	_, _, err = preparePointInTimeBacktestInputs(settings)
	if !pointintime.IsCoverageError(err) {
		t.Fatalf("benchmark-only ticker was accepted as tradable: %v", err)
	}
}

func TestStage04ConstraintGapFailsTypedCoverageAndPersistsJobSummary(t *testing.T) {
	t.Setenv("BACKTEST_CODE_REVISION", "stage04-constraint-gap")
	db := testutil.SetupPostgresDB(t)
	base := time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC)
	assets := []database.Asset{{ID: "a", CanonicalCode: "AAA", Source: "f", AvailableAt: base.Add(-time.Hour), RetrievedAt: base}, {ID: "btc", CanonicalCode: "BTC", Source: "f", AvailableAt: base.Add(-time.Hour), RetrievedAt: base}, {ID: "q", CanonicalCode: "USDT", Source: "f", AvailableAt: base.Add(-time.Hour), RetrievedAt: base}}
	symbols := []database.ExchangeSymbol{{ID: "a-s", VenueID: "f", Ticker: "AAAUSDT", AssetID: "a", BaseAssetID: "a", QuoteAssetID: "q", ListedAt: base.Add(-time.Hour), AvailableAt: base.Add(-time.Hour), Version: 1, Source: "f", RetrievedAt: base}, {ID: "btc-s", VenueID: "f", Ticker: "BTCUSDT", AssetID: "btc", BaseAssetID: "btc", QuoteAssetID: "q", ListedAt: base.Add(-time.Hour), AvailableAt: base.Add(-time.Hour), Version: 1, Source: "f", RetrievedAt: base}}
	if err := pointintime.UpsertAssetLifecycle(db, assets, symbols, nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct{ id, role string }{{"a-s", pointintime.RoleDecision}, {"btc-s", pointintime.RoleBenchmark}} {
		for n := 0; n < 2; n++ {
			at := base.Add(time.Duration(n) * 15 * time.Minute)
			row := database.HistoricalBar{ExchangeSymbolID: sample.id, Timeframe: "15m", OpenTime: at, DatasetVersion: "gap-v1", Role: sample.role, Open: "10", High: "10", Low: "10", Close: "10", Volume: "1", QuoteVolume: "10", QualityStatus: "valid", QualityFlagsJSON: "[]", Source: "f", ProvenanceJSON: "{}", AvailableAt: at.Add(15 * time.Minute).Add(-time.Millisecond), RetrievedAt: base.Add(30 * time.Minute), ContentHash: strings.Repeat(string(rune('a'+n)), 64)}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	manifest, err := pointintime.BuildManifest(db, pointintime.BuildRequest{DatasetVersion: "gap-v1", RequestedStart: base, RequestedEnd: base.Add(30 * time.Minute), KnowledgeCutoff: base.Add(30 * time.Minute), Source: "f"})
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{"backtest_dataset_manifest_id": manifest.ID, "backtest_start": base.Format(time.RFC3339), "backtest_end": base.Add(30 * time.Minute).Format(time.RFC3339), "backtest_benchmark_symbol": "BTCUSDT", "backtest_engine_mode": string(EngineShared)}
	_, _, err = preparePointInTimeBacktestInputs(settings)
	if !pointintime.IsCoverageError(err) {
		t.Fatalf("constraint gap was not typed coverage failure: %v", err)
	}
	rows := make([]database.Setting, 0, len(settings))
	for key, value := range settings {
		rows = append(rows, database.Setting{Key: key, Value: value})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	job := database.BacktestJob{Status: "pending", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	runBacktestJob(job.ID)
	response, err := GetBacktestJobResponse(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != "failed" || response.Summary == nil || response.Summary.Baseline.Classification != RunCoverageFailed || response.DatasetManifestID == nil || *response.DatasetManifestID != manifest.ID {
		t.Fatalf("failed job response=%+v", response)
	}
}
