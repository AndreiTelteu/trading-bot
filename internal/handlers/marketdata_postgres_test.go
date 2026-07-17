package handlers

import (
	"encoding/json"
	"net/http/httptest"
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
	series := []pointintime.SeriesCoverage{{SeriesKey: pointintime.SeriesKey{ExchangeSymbolID: "s", AssetID: "a", Ticker: "AAAUSDT", Role: pointintime.RoleDecision, Timeframe: "15m"}, Rows: 5, ExpectedRows: 5, Complete: true, Quality: "valid"}}
	row := database.DatasetManifest{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SchemaVersion: pointintime.ManifestSchemaVersion, DatasetVersion: "v1", RequestedStart: start, RequestedEnd: end, EffectiveStart: start, EffectiveEnd: end, Source: "fixture", ProvenanceJSON: "{}", BuildVersion: "test", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", SymbolsJSON: `["AAAUSDT"]`, AssetsJSON: `["a"]`, RolesTimeframesJSON: pointintime.EncodeJSON([]pointintime.SeriesKey{series[0].SeriesKey}), CoverageJSON: pointintime.EncodeJSON(series), LimitationsJSON: "[]"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Get("/coverage", InspectDatasetCoverage)
	req := httptest.NewRequest("GET", "/coverage?manifest_id="+row.ID+"&start=2024-01-01T00:00:00Z&end=2024-01-01T01:00:00Z&symbols=AAAUSDT&roles=decision:15m", nil)
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
}
