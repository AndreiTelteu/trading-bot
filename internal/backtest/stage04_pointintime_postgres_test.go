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
		{ID: "asset-aaa", CanonicalCode: "AAA", Source: "fixture", RetrievedAt: base},
		{ID: "asset-btc", CanonicalCode: "BTC", Source: "fixture", RetrievedAt: base},
		{ID: "asset-usdt", CanonicalCode: "USDT", Source: "fixture", RetrievedAt: base},
	}
	if err := db.Create(&assets).Error; err != nil {
		t.Fatal(err)
	}
	symbols := []database.ExchangeSymbol{
		{ID: "symbol-aaa", VenueID: "binance", Ticker: "AAAUSDT", AssetID: "asset-aaa", BaseAssetID: "asset-aaa", QuoteAssetID: "asset-usdt", ListedAt: base.Add(-time.Hour), Version: 1, Source: "fixture", RetrievedAt: base},
		{ID: "symbol-btc", VenueID: "binance", Ticker: "BTCUSDT", AssetID: "asset-btc", BaseAssetID: "asset-btc", QuoteAssetID: "asset-usdt", ListedAt: base.Add(-time.Hour), Version: 1, Source: "fixture", RetrievedAt: base},
	}
	if err := db.Create(&symbols).Error; err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		symbolID string
		role     string
		price    string
	}{{"symbol-aaa", pointintime.RoleDecision, "10"}, {"symbol-btc", pointintime.RoleBenchmark, "100"}} {
		for index := 0; index < 2; index++ {
			at := base.Add(time.Duration(index) * 15 * time.Minute)
			bar := database.HistoricalBar{ExchangeSymbolID: sample.symbolID, Timeframe: "15m", OpenTime: at, DatasetVersion: "runtime-fixture-v1", Role: sample.role, Open: sample.price, High: sample.price, Low: sample.price, Close: sample.price, Volume: "100", QuoteVolume: "1000", QualityStatus: "valid", QualityFlagsJSON: "[]", Source: "fixture", ProvenanceJSON: "{}", RetrievedAt: at, ContentHash: strings.Repeat(string(rune('a'+index)), 64), CreatedAt: at}
			if err := db.Create(&bar).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	manifest, err := pointintime.BuildManifest(db, pointintime.BuildRequest{DatasetVersion: "runtime-fixture-v1", RequestedStart: base, RequestedEnd: base.Add(15 * time.Minute), Source: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{
		"backtest_dataset_manifest_id": manifest.ID,
		"backtest_start":               base.Format(time.RFC3339),
		"backtest_end":                 base.Add(15 * time.Minute).Format(time.RFC3339),
		"backtest_benchmark_symbol":    "BTCUSDT",
		"backtest_engine_mode":         string(EngineShared),
	}
	config, series, err := preparePointInTimeBacktestInputs(settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Symbols) != 1 || config.Symbols[0] != "AAAUSDT" || len(series["AAAUSDT"]) != 1 {
		t.Fatalf("default tradable selection/config=%v series=%v", config.Symbols, series)
	}

	settings["backtest_symbols"] = "BTCUSDT"
	_, _, err = preparePointInTimeBacktestInputs(settings)
	if !pointintime.IsCoverageError(err) {
		t.Fatalf("benchmark-only ticker was accepted as tradable: %v", err)
	}
}
