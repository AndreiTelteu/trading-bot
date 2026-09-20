package pointintime

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

func TestTwoClockKnowledgeContractPreventsEarlyMetadataAndBarUse(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	retrieved := base.AddDate(1, 0, 0)
	known := base.Add(2 * time.Hour)
	assets := []database.Asset{{ID: "asset", CanonicalCode: "AAA", Source: "fixture", AvailableAt: base, RetrievedAt: retrieved}, {ID: "quote", CanonicalCode: "USDT", Source: "fixture", AvailableAt: base, RetrievedAt: retrieved}}
	symbol := database.ExchangeSymbol{ID: "symbol", VenueID: "fixture", Ticker: "AAAUSDT", AssetID: "asset", BaseAssetID: "asset", QuoteAssetID: "quote", ListedAt: base, AvailableAt: known, Version: 1, Source: "fixture", RetrievedAt: retrieved}
	tradable := database.TradabilityInterval{ExchangeSymbolID: "symbol", EffectiveFrom: base, SpotTradable: true, Status: "TRADING", Source: "fixture", AvailableAt: known, RetrievedAt: retrieved}
	constraint := database.SymbolConstraintVersion{ExchangeSymbolID: "symbol", EffectiveFrom: base, QuantityStep: "0.1", PriceTick: "0.01", MinQuantity: "0.1", MinNotional: "1", Source: "fixture", AvailableAt: known, RetrievedAt: retrieved}
	if err := UpsertAssetLifecycle(db, assets, []database.ExchangeSymbol{symbol}, []database.TradabilityInterval{tradable}, []database.SymbolConstraintVersion{constraint}); err != nil {
		t.Fatal(err)
	}
	bar := Bar{OpenTime: base, AvailableAt: known, Open: "10", High: "11", Low: "9", Close: "10", Volume: "1", QuoteVolume: "10", Quality: "valid"}
	if _, err := (Ingester{DB: db, Client: &fixtureClient{bars: []Bar{bar}}, Now: func() time.Time { return retrieved }}).Run(context.Background(), IngestRequest{DatasetVersion: "two-clock-v1", ExchangeSymbolID: "symbol", Timeframe: "15m", Role: RoleDecision, Source: "fixture", Start: base, End: base.Add(15 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	manifest, err := BuildManifest(db, BuildRequest{DatasetVersion: "two-clock-v1", RequestedStart: base, RequestedEnd: base.Add(4 * time.Hour), KnowledgeCutoff: retrieved, Source: "fixture", Series: []SeriesKey{{ExchangeSymbolID: "symbol", AssetID: "asset", Ticker: "AAAUSDT", Role: RoleDecision, Timeframe: "15m"}}})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.KnowledgeCutoff != canonicalTime(retrieved) || manifest.Series[0].Complete || !containsString(manifest.Series[0].QualityFlags, "delayed_bar_availability") {
		t.Fatalf("manifest=%+v", manifest)
	}
	repo := Repository{DB: db}
	early, err := repo.SymbolsAsOf(manifest.ID, base.Add(time.Hour), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(early) != 0 {
		t.Fatalf("future-known metadata leaked: %+v", early)
	}
	later, err := repo.SymbolsAsOf(manifest.ID, base.Add(3*time.Hour), true)
	if err != nil || len(later) != 1 {
		t.Fatalf("post-availability symbols=%+v err=%v", later, err)
	}
	earlyBars, err := repo.Bars(manifest.ID, "symbol", RoleDecision, "15m", base, base.Add(15*time.Minute), base.Add(time.Hour))
	if err != nil || len(earlyBars) != 0 {
		t.Fatalf("future-known bar leaked: %v %v", earlyBars, err)
	}
	laterBars, err := repo.Bars(manifest.ID, "symbol", RoleDecision, "15m", base, base.Add(15*time.Minute), base.Add(3*time.Hour))
	if err != nil || len(laterBars) != 1 {
		t.Fatalf("post-hoc backfill not usable after event availability: %v %v", laterBars, err)
	}
	_, err = BuildManifest(db, BuildRequest{DatasetVersion: "two-clock-v1", RequestedStart: base, RequestedEnd: base.Add(15 * time.Minute), KnowledgeCutoff: retrieved.Add(-time.Hour), Source: "fixture", Series: []SeriesKey{{ExchangeSymbolID: "symbol", Role: RoleDecision, Timeframe: "15m"}}})
	if err == nil {
		t.Fatal("retrieval after manifest knowledge cutoff was accepted")
	}
}

func TestMetadataDryRunValidatesParentsWithoutMutation(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	assets := []database.Asset{{ID: "a", CanonicalCode: "AAA", Source: "fixture", AvailableAt: at, RetrievedAt: at}, {ID: "q", CanonicalCode: "USDT", Source: "fixture", AvailableAt: at, RetrievedAt: at}}
	symbol := database.ExchangeSymbol{ID: "s", VenueID: "fixture", Ticker: "AAAUSDT", AssetID: "a", BaseAssetID: "a", QuoteAssetID: "q", ListedAt: at, AvailableAt: at, Version: 1, Source: "fixture", RetrievedAt: at}
	request := MetadataIngestRequest{Assets: assets, Symbols: []database.ExchangeSymbol{symbol}, Start: at.Add(-time.Hour), End: at.Add(time.Hour), DryRun: true}
	if err := IngestMetadata(db, request); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&database.Asset{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("dry-run mutated assets: %d %v", count, err)
	}
	bad := symbol
	bad.ID = "bad"
	bad.AssetID = "missing"
	request.Symbols = []database.ExchangeSymbol{bad}
	if err := IngestMetadata(db, request); err == nil {
		t.Fatal("invalid metadata parent accepted")
	}
	request.Symbols = []database.ExchangeSymbol{symbol}
	request.End = at
	if err := IngestMetadata(db, request); err == nil {
		t.Fatal("out-of-bounds metadata was accepted")
	}
}

func TestSparseResumeTickerValidationAndConcurrentInsertAreSafe(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	base := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	retrieved := base.Add(time.Hour)
	assets := []database.Asset{{ID: "asset", CanonicalCode: "AAA", Source: "f", AvailableAt: base, RetrievedAt: retrieved}, {ID: "quote", CanonicalCode: "USDT", Source: "f", AvailableAt: base, RetrievedAt: retrieved}}
	symbol := database.ExchangeSymbol{ID: "symbol", VenueID: "f", Ticker: "AAAUSDT", AssetID: "asset", BaseAssetID: "asset", QuoteAssetID: "quote", ListedAt: base, AvailableAt: base, Version: 1, Source: "f", RetrievedAt: retrieved}
	if err := UpsertAssetLifecycle(db, assets, []database.ExchangeSymbol{symbol}, nil, nil); err != nil {
		t.Fatal(err)
	}
	all := bars(base, 3, time.Hour, 10, 1)
	sparse := []Bar{all[0], all[2]}
	request := IngestRequest{DatasetVersion: "resume-v2", ExchangeSymbolID: "symbol", Ticker: "AAAUSDT", Timeframe: "1h", Role: RoleDecision, Source: "f", Start: base, End: base.Add(3 * time.Hour), PageSize: 3}
	first, err := (Ingester{DB: db, Client: &fixtureClient{bars: sparse}, Now: func() time.Time { return retrieved }}).Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Unresolved) == 0 || first.Inserted != 1 {
		t.Fatalf("sparse result=%+v", first)
	}
	second, err := (Ingester{DB: db, Client: &fixtureClient{bars: all}, Now: func() time.Time { return retrieved }}).Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if second.ResumedFrom == nil {
		t.Fatalf("restart did not resume: %+v", second)
	}
	var count int64
	if err := db.Model(&database.HistoricalBar{}).Where("dataset_version=?", "resume-v2").Count(&count).Error; err != nil || count != 3 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	bad := request
	bad.DatasetVersion = "bad-ticker"
	bad.Ticker = "BTCUSDT"
	if _, err := (Ingester{DB: db, Client: &fixtureClient{bars: all}}).Run(context.Background(), bad); err == nil {
		t.Fatal("mismatched ticker was accepted")
	}
	concurrent := request
	concurrent.DatasetVersion = "race-v1"
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := (Ingester{DB: db, Client: &fixtureClient{bars: all}, Now: func() time.Time { return retrieved }}).Run(context.Background(), concurrent)
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("concurrent ingest: %v", e)
		}
	}
	if err := db.Model(&database.HistoricalBar{}).Where("dataset_version=?", "race-v1").Count(&count).Error; err != nil || count != 3 {
		t.Fatalf("race count=%d err=%v", count, err)
	}
	conflict := append([]Bar(nil), all...)
	conflict[1].Close = "999"
	conflictRequest := concurrent
	conflictRequest.Start, conflictRequest.End = base.Add(time.Hour), base.Add(2*time.Hour)
	if _, err := (Ingester{DB: db, Client: &fixtureClient{bars: conflict}, Now: func() time.Time { return retrieved }}).Run(context.Background(), conflictRequest); !errors.Is(err, ErrBarConflict) {
		t.Fatalf("content conflict=%v", err)
	}
}

func TestExclusionConstraintsSerializeConcurrentOverlaps(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	base := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	retrieved := base.Add(time.Hour)
	assets := []database.Asset{{ID: "asset", CanonicalCode: "AAA", Source: "f", AvailableAt: base, RetrievedAt: retrieved}, {ID: "quote", CanonicalCode: "USDT", Source: "f", AvailableAt: base, RetrievedAt: retrieved}}
	symbol := database.ExchangeSymbol{ID: "symbol", VenueID: "f", Ticker: "AAAUSDT", AssetID: "asset", BaseAssetID: "asset", QuoteAssetID: "quote", ListedAt: base, AvailableAt: base, Version: 1, Source: "f", RetrievedAt: retrieved}
	if err := UpsertAssetLifecycle(db, assets, []database.ExchangeSymbol{symbol}, nil, nil); err != nil {
		t.Fatal(err)
	}
	type insert func(*database.TradabilityInterval, *database.SymbolConstraintVersion) error
	run := func(kind string, fn insert) {
		t.Run(kind, func(t *testing.T) {
			start := make(chan struct{})
			errs := make(chan error, 2)
			for n := 0; n < 2; n++ {
				n := n
				go func() {
					<-start
					from := base.Add(time.Duration(n) * time.Hour)
					to := base.Add(time.Duration(n+2) * time.Hour)
					var effectiveTo *time.Time
					if n != 0 {
						effectiveTo = &to
					}
					trad := &database.TradabilityInterval{ExchangeSymbolID: "symbol", EffectiveFrom: from, EffectiveTo: effectiveTo, SpotTradable: true, Status: "TRADING", Source: "f", AvailableAt: from, RetrievedAt: retrieved}
					constraint := &database.SymbolConstraintVersion{ExchangeSymbolID: "symbol", EffectiveFrom: from, EffectiveTo: effectiveTo, QuantityStep: "0.1", PriceTick: "0.01", MinQuantity: "0.1", MinNotional: "1", Source: "f", AvailableAt: from, RetrievedAt: retrieved}
					errs <- fn(trad, constraint)
				}()
			}
			close(start)
			success, failed := 0, 0
			for n := 0; n < 2; n++ {
				if <-errs == nil {
					success++
				} else {
					failed++
				}
			}
			if success != 1 || failed != 1 {
				t.Fatalf("success=%d failed=%d", success, failed)
			}
		})
	}
	run("tradability", func(v *database.TradabilityInterval, _ *database.SymbolConstraintVersion) error {
		return db.Create(v).Error
	})
	run("constraints", func(_ *database.TradabilityInterval, v *database.SymbolConstraintVersion) error {
		return db.Create(v).Error
	})
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
