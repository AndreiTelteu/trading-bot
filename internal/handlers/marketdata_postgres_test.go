package handlers

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/pointintime"
	"trading-go/internal/testutil"

	"github.com/gofiber/fiber/v2"
)

func TestCoverageAPIIsMachineReadableAndSchemaChecked(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	assets := []database.Asset{{ID: "a", CanonicalCode: "AAA", Source: "fixture", AvailableAt: start.Add(-time.Hour), RetrievedAt: end}, {ID: "q", CanonicalCode: "USDT", Source: "fixture", AvailableAt: start.Add(-time.Hour), RetrievedAt: end}}
	symbol := database.ExchangeSymbol{ID: "s", VenueID: "fixture", Ticker: "AAAUSDT", AssetID: "a", BaseAssetID: "a", QuoteAssetID: "q", ListedAt: start.Add(-time.Hour), AvailableAt: start.Add(-time.Hour), Version: 1, Source: "fixture", RetrievedAt: end}
	if err := pointintime.UpsertAssetLifecycle(db, assets, []database.ExchangeSymbol{symbol}, nil, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		at := start.Add(time.Duration(i) * 15 * time.Minute)
		row := database.HistoricalBar{ExchangeSymbolID: "s", Timeframe: "15m", OpenTime: at, DatasetVersion: "v1", Role: pointintime.RoleDecision, Open: "1", High: "1", Low: "1", Close: "1", Volume: "1", QuoteVolume: "1", QualityStatus: "valid", QualityFlagsJSON: "[]", Source: "fixture", ProvenanceJSON: "{}", AvailableAt: at.Add(15 * time.Minute).Add(-time.Millisecond), RetrievedAt: end, ContentHash: strings.Repeat(fmt.Sprint(i+1), 64)}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	manifest, err := pointintime.BuildManifest(db, pointintime.BuildRequest{DatasetVersion: "v1", RequestedStart: start, RequestedEnd: end, KnowledgeCutoff: end, Source: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/coverage", InspectDatasetCoverage)
	app.Get("/bars", ListHistoricalBars)
	req := httptest.NewRequest("GET", "/coverage?manifest_id="+manifest.ID+"&start=2024-01-01T00:00:00Z&end=2024-01-01T01:00:00Z&symbols=AAAUSDT&roles=decision:15m", nil)
	response, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("status=%d", response.StatusCode)
	}
	var report pointintime.CoverageReport
	if err := json.NewDecoder(response.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != pointintime.CoverageSchemaVersion || !report.Compatible {
		t.Fatalf("report=%+v", report)
	}
	req = httptest.NewRequest("GET", "/coverage?manifest_id=missing&roles=decision:15m", nil)
	response, err = app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 422 {
		t.Fatalf("failure status=%d", response.StatusCode)
	}
	if err := json.NewDecoder(response.Body).Decode(&report); err != nil || report.SchemaVersion != pointintime.CoverageSchemaVersion || report.Compatible {
		t.Fatalf("failure report=%+v err=%v", report, err)
	}
	response, err = app.Test(httptest.NewRequest("GET", "/bars?manifest_id="+manifest.ID+"&symbol_id=s&role=decision&timeframe=15m&start=2024-01-01T00:00:00Z&end=2024-01-01T01:00:00Z&as_of=2024-01-01T01:00:00Z&limit=2", nil))
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		SchemaVersion string `json:"schema_version"`
		Count         int    `json:"count"`
		Next          string `json:"next_cursor"`
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 || page.SchemaVersion != pointintime.BarsSchemaVersion || page.Count != 2 || page.Next == "" {
		t.Fatalf("page=%+v status=%d", page, response.StatusCode)
	}
	response, err = app.Test(httptest.NewRequest("GET", "/bars?manifest_id="+manifest.ID+"&symbol_id=s&role=decision&timeframe=15m&start=2024-01-01T00:00:00Z&end=2024-01-01T01:00:00Z&as_of=2024-01-01T01:00:00Z&limit=2&cursor="+page.Next, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if page.Count != 2 || page.Next != "" {
		t.Fatalf("second page=%+v", page)
	}
	response, err = app.Test(httptest.NewRequest("GET", "/bars?start=2024-01-01T00:00:00Z&end=2024-01-01T01:00:00Z&limit=0", nil))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatalf("malformed limit status=%d", response.StatusCode)
	}
}
