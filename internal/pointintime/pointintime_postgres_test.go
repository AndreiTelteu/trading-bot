package pointintime

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/services"
	"trading-go/internal/testutil"
)

type fixtureClient struct {
	bars      []Bar
	failAfter int
	calls     int
}

func (f *fixtureClient) FetchBars(_ context.Context, _ string, _ string, start, end time.Time, limit int) ([]Bar, error) {
	f.calls++
	if f.failAfter > 0 && f.calls > f.failAfter {
		return nil, errors.New("interrupted")
	}
	out := []Bar{}
	for _, b := range f.bars {
		if !b.OpenTime.Before(start) && !b.OpenTime.After(end) {
			out = append(out, b)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func TestPointInTimeLifecycleImmutableDataManifestResumeAndConstraints(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	retrieved := base.Add(365 * 24 * time.Hour)
	assets := []database.Asset{{ID: "asset-usdt", CanonicalCode: "USDT", Source: "fixture", RetrievedAt: retrieved}, {ID: "asset-a", CanonicalCode: "AAA", Source: "fixture", RetrievedAt: retrieved}, {ID: "asset-btc", CanonicalCode: "BTC", Source: "fixture", RetrievedAt: retrieved}}
	delist := base.Add(5 * 24 * time.Hour)
	symbols := []database.ExchangeSymbol{{ID: "symbol-a-old", VenueID: "fixture", Ticker: "AAAOLDUSDT", AssetID: "asset-a", BaseAssetID: "asset-a", QuoteAssetID: "asset-usdt", ListedAt: base, DelistedAt: &delist, Version: 1, Source: "fixture", RetrievedAt: retrieved}, {ID: "symbol-a-new", VenueID: "fixture", Ticker: "AAAUSDT", AssetID: "asset-a", BaseAssetID: "asset-a", QuoteAssetID: "asset-usdt", ListedAt: delist, Version: 1, Source: "fixture", RetrievedAt: retrieved}, {ID: "symbol-btc", VenueID: "fixture", Ticker: "BTCUSDT", AssetID: "asset-btc", BaseAssetID: "asset-btc", QuoteAssetID: "asset-usdt", ListedAt: base.Add(-1000 * 24 * time.Hour), Version: 1, Source: "fixture", RetrievedAt: retrieved}}
	intervals := []database.TradabilityInterval{{ExchangeSymbolID: "symbol-a-old", EffectiveFrom: base, EffectiveTo: &delist, SpotTradable: true, Status: "TRADING", Source: "fixture", RetrievedAt: retrieved}, {ExchangeSymbolID: "symbol-a-new", EffectiveFrom: delist, SpotTradable: true, Status: "TRADING", Source: "fixture", RetrievedAt: retrieved}, {ExchangeSymbolID: "symbol-btc", EffectiveFrom: base.Add(-1000 * 24 * time.Hour), SpotTradable: true, Status: "TRADING", Source: "fixture", RetrievedAt: retrieved}}
	constraintEnd := base.Add(3 * 24 * time.Hour)
	constraints := []database.SymbolConstraintVersion{{ExchangeSymbolID: "symbol-a-old", EffectiveFrom: base, EffectiveTo: &constraintEnd, QuantityStep: "0.1", PriceTick: "0.01", MinQuantity: "1", MinNotional: "0", Source: "fixture", RetrievedAt: retrieved}, {ExchangeSymbolID: "symbol-a-old", EffectiveFrom: constraintEnd, QuantityStep: "1", PriceTick: "0.1", MinQuantity: "2", MinNotional: "0", Source: "fixture", RetrievedAt: retrieved}}
	if err := UpsertAssetLifecycle(db, assets, symbols, intervals, constraints); err != nil {
		t.Fatal(err)
	}
	request := func(id, ticker, role, frame string, bars []Bar) IngestResult {
		client := &fixtureClient{bars: bars}
		result, err := (Ingester{DB: db, Client: client, Sleep: func(context.Context, time.Duration) error { return nil }, Now: func() time.Time { return retrieved }}).Run(context.Background(), IngestRequest{DatasetVersion: "fixture-v1", ExchangeSymbolID: id, Ticker: ticker, Timeframe: frame, Role: role, Source: "fixture", Start: bars[0].OpenTime, End: bars[len(bars)-1].OpenTime, PageSize: 2})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	daily := bars(base, 7, 24*time.Hour, 10, 100)
	hourly := bars(base, 7*24, time.Hour, 10, 100)
	request("symbol-a-old", "AAAOLDUSDT", RoleDecision, "1d", daily[:5])
	request("symbol-a-old", "AAAOLDUSDT", RoleDecision, "1h", hourly[:5*24])
	request("symbol-a-new", "AAAUSDT", RoleDecision, "1d", daily[5:])
	request("symbol-a-new", "AAAUSDT", RoleDecision, "1h", hourly[5*24:])
	request("symbol-btc", "BTCUSDT", RoleBenchmark, "1d", daily)
	request("symbol-btc", "BTCUSDT", RoleBenchmark, "1h", hourly)
	manifest, err := BuildManifest(db, BuildRequest{DatasetVersion: "fixture-v1", RequestedStart: base, RequestedEnd: base.Add(6 * 24 * time.Hour), Source: "fixture", BuildVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	again, err := BuildManifest(db, BuildRequest{DatasetVersion: "fixture-v1", RequestedStart: base, RequestedEnd: base.Add(6 * 24 * time.Hour), Source: "fixture", BuildVersion: "test"})
	if err != nil || again.ID != manifest.ID {
		t.Fatalf("manifest not deterministic: %s %s %v", manifest.ID, again.ID, err)
	}
	repo := Repository{DB: db}
	before, err := repo.SymbolsAsOf(manifest.ID, base.Add(4*24*time.Hour), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 || !containsSymbol(before, "AAAOLDUSDT") {
		t.Fatalf("historical delisted visibility=%v", tickers(before))
	}
	after, err := repo.SymbolsAsOf(manifest.ID, base.Add(6*24*time.Hour), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 2 || containsSymbol(after, "AAAOLDUSDT") || !containsSymbol(after, "AAAUSDT") {
		t.Fatalf("rename lifecycle=%v", tickers(after))
	}
	seen := map[string]bool{}
	for _, s := range after {
		if seen[s.AssetID] {
			t.Fatal("duplicate economic identity")
		}
		seen[s.AssetID] = true
	}
	c1, err := repo.ConstraintAsOf("symbol-a-old", base.Add(2*24*time.Hour))
	if err != nil || c1.QuantityStep != .1 {
		t.Fatalf("old constraint=%+v %v", c1, err)
	}
	c2, err := repo.ConstraintAsOf("symbol-a-old", base.Add(4*24*time.Hour))
	if err != nil || c2.QuantityStep != 1 {
		t.Fatalf("new constraint=%+v %v", c2, err)
	}
	conflict := daily[0]
	conflict.Close = "999"
	_, err = (Ingester{DB: db, Client: &fixtureClient{bars: []Bar{conflict}}, Now: func() time.Time { return retrieved }}).Run(context.Background(), IngestRequest{DatasetVersion: "fixture-v1", ExchangeSymbolID: "symbol-a-old", Ticker: "AAAOLDUSDT", Timeframe: "1d", Role: RoleDecision, Source: "fixture", Start: base, End: base})
	if !errors.Is(err, ErrBarConflict) {
		t.Fatalf("conflict=%v", err)
	}
	resumeBars := bars(base, 5, time.Hour, 20, 10)
	interrupted := &fixtureClient{bars: resumeBars, failAfter: 1}
	first, err := (Ingester{DB: db, Client: interrupted, Sleep: func(context.Context, time.Duration) error { return nil }}).Run(context.Background(), IngestRequest{DatasetVersion: "resume-v1", ExchangeSymbolID: "symbol-a-old", Ticker: "AAAOLDUSDT", Timeframe: "1h", Role: RoleExecution, Source: "fixture", Start: base, End: base.Add(4 * time.Hour), PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Unresolved) == 0 {
		t.Fatal("interruption not explicit")
	}
	second, err := (Ingester{DB: db, Client: &fixtureClient{bars: resumeBars}}).Run(context.Background(), IngestRequest{DatasetVersion: "resume-v1", ExchangeSymbolID: "symbol-a-old", Ticker: "AAAOLDUSDT", Timeframe: "1h", Role: RoleExecution, Source: "fixture", Start: base, End: base.Add(4 * time.Hour), PageSize: 2})
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&database.HistoricalBar{}).Where("dataset_version='resume-v1'").Count(&count)
	if count != 5 || second.ResumedFrom == nil {
		t.Fatalf("resume count=%d result=%+v", count, second)
	}
}

func TestManifestGapsAndUniverseNoFutureRankOrBenchmarkTrade(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	base := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	retrieved := base.AddDate(1, 0, 0)
	assets := []database.Asset{{ID: "q", CanonicalCode: "USDT", Source: "f", RetrievedAt: retrieved}, {ID: "a", CanonicalCode: "AAA", Source: "f", RetrievedAt: retrieved}, {ID: "b", CanonicalCode: "BBB", Source: "f", RetrievedAt: retrieved}, {ID: "btc", CanonicalCode: "BTC", Source: "f", RetrievedAt: retrieved}}
	symbols := []database.ExchangeSymbol{{ID: "a-s", VenueID: "f", Ticker: "AAAUSDT", AssetID: "a", BaseAssetID: "a", QuoteAssetID: "q", ListedAt: base, Source: "f", RetrievedAt: retrieved}, {ID: "b-s", VenueID: "f", Ticker: "BBBUSDT", AssetID: "b", BaseAssetID: "b", QuoteAssetID: "q", ListedAt: base.Add(3 * 24 * time.Hour), Source: "f", RetrievedAt: retrieved}, {ID: "btc-s", VenueID: "f", Ticker: "BTCUSDT", AssetID: "btc", BaseAssetID: "btc", QuoteAssetID: "q", ListedAt: base.Add(-1000 * 24 * time.Hour), Source: "f", RetrievedAt: retrieved}}
	intervals := []database.TradabilityInterval{}
	for _, s := range symbols {
		intervals = append(intervals, database.TradabilityInterval{ExchangeSymbolID: s.ID, EffectiveFrom: s.ListedAt, SpotTradable: true, Status: "TRADING", Source: "f", RetrievedAt: retrieved})
	}
	if err := UpsertAssetLifecycle(db, assets, symbols, intervals, nil); err != nil {
		t.Fatal(err)
	}
	ingest := func(id, role, frame string, values []Bar) {
		_, err := (Ingester{DB: db, Client: &fixtureClient{bars: values}}).Run(context.Background(), IngestRequest{DatasetVersion: "u-v1", ExchangeSymbolID: id, Ticker: id, Timeframe: frame, Role: role, Source: "f", Start: values[0].OpenTime, End: values[len(values)-1].OpenTime, PageSize: 1000})
		if err != nil {
			t.Fatal(err)
		}
	}
	ingest("a-s", RoleDecision, "1d", bars(base, 8, 24*time.Hour, 10, 100))
	ingest("a-s", RoleDecision, "1h", bars(base, 8*24, time.Hour, 10, 100))
	ingest("b-s", RoleDecision, "1d", bars(base.Add(3*24*time.Hour), 5, 24*time.Hour, 10, 100))
	ingest("b-s", RoleDecision, "1h", bars(base.Add(3*24*time.Hour), 5*24, time.Hour, 10, 100))
	ingest("btc-s", RoleBenchmark, "1d", bars(base, 8, 24*time.Hour, 20, 100))
	ingest("btc-s", RoleBenchmark, "1h", bars(base, 8*24, time.Hour, 20, 100))
	manifest, err := BuildManifest(db, BuildRequest{DatasetVersion: "u-v1", RequestedStart: base, RequestedEnd: base.Add(7 * 24 * time.Hour), Source: "f"})
	if err != nil {
		t.Fatal(err)
	}
	policy := services.UniversePolicy{MinListingDays: 0, MaxGapRatio: 1, VolRatioMax: 100, Max24hMove: 100, TopK: 10, AnalyzeTopN: 10, RebalanceIntervalLabel: "1d"}
	snapAt := base.Add(2 * 24 * time.Hour)
	first, err := BuildUniverseSnapshot(db, UniverseBuildRequest{ManifestID: manifest.ID, EffectiveAt: snapAt, PolicyVersion: "p1", Policy: policy, BenchmarkSymbolID: "btc-s", BenchmarkAssetID: "btc"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range first.Members {
		if m.Symbol == "BBBUSDT" {
			t.Fatal("mid-fixture listing visible early")
		}
		if m.Symbol == "BTCUSDT" {
			t.Fatal("benchmark entered tradable shortlist")
		}
	}
	// Future bars are already persisted, but rebuilding the same as-of snapshot is byte-stable/idempotent.
	second, err := BuildUniverseSnapshot(db, UniverseBuildRequest{ManifestID: manifest.ID, EffectiveAt: snapAt, PolicyVersion: "p1", Policy: policy, BenchmarkSymbolID: "btc-s", BenchmarkAssetID: "btc"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Snapshot.ID != first.Snapshot.ID || EncodeJSON(second.Members) != EncodeJSON(first.Members) {
		t.Fatalf("snapshot not deterministic/idempotent\nfirst=%s\nsecond=%s\nfirst_snapshot=%+v\nsecond_snapshot=%+v", EncodeJSON(first.Members), EncodeJSON(second.Members), first.Snapshot, second.Snapshot)
	}
	gapValues := bars(base, 3, time.Hour, 1, 1)
	gapValues = append(gapValues[:1], gapValues[2:]...)
	ingest("a-s", RoleExecution, "1h", gapValues)
	gapManifest, err := BuildManifest(db, BuildRequest{DatasetVersion: "u-v1", RequestedStart: base, RequestedEnd: base.Add(2 * time.Hour), Source: "f", Series: []SeriesKey{{ExchangeSymbolID: "a-s", AssetID: "a", Ticker: "AAAUSDT", Role: RoleExecution, Timeframe: "1h"}}})
	if err != nil {
		t.Fatal(err)
	}
	if gapManifest.Series[0].Gaps != 1 || gapManifest.Series[0].Rows != 2 {
		t.Fatalf("gap diagnostic=%+v", gapManifest.Series[0])
	}
}

func bars(start time.Time, count int, step time.Duration, price, volume float64) []Bar {
	out := make([]Bar, count)
	for i := range out {
		p := price + float64(i)/10
		out[i] = Bar{OpenTime: start.Add(time.Duration(i) * step), Open: fmt.Sprint(p), High: fmt.Sprint(p + 1), Low: fmt.Sprint(p - 1), Close: fmt.Sprint(p + .5), Volume: fmt.Sprint(volume), QuoteVolume: fmt.Sprint(volume * p), Quality: "valid"}
	}
	return out
}
func containsSymbol(v []database.ExchangeSymbol, ticker string) bool {
	for _, s := range v {
		if s.Ticker == ticker {
			return true
		}
	}
	return false
}
func tickers(v []database.ExchangeSymbol) []string {
	r := []string{}
	for _, s := range v {
		r = append(r, s.Ticker)
	}
	return r
}
