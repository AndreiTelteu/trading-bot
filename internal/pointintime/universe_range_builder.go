package pointintime

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/services"

	"gorm.io/gorm"
)

// universeRangeBuilder loads immutable manifest inputs once per bounded range
// and serves each snapshot from binary-searched in-memory windows. This avoids
// validating the same manifest and transferring overlapping 90-day windows
// for every snapshot.
type universeRangeBuilder struct {
	db           *gorm.DB
	request      UniverseBuildRequest
	manifest     Manifest
	baseReport   CoverageReport
	manifestFrom time.Time
	cutoff       time.Time
	symbols      []database.ExchangeSymbol
	assets       map[string]database.Asset
	tradability  map[string][]database.TradabilityInterval
	bars         map[rangeSeries][]cachedUniverseBar
}

type rangeSeries struct {
	symbolID  string
	role      string
	timeframe string
}

type cachedUniverseBar struct {
	bar         services.OHLCV
	openTime    time.Time
	availableAt time.Time
}

func newUniverseRangeBuilder(db *gorm.DB, request UniverseRangeRequest) (*universeRangeBuilder, error) {
	build := request.Build
	if build.DecisionTimeframe == "" {
		build.DecisionTimeframe = "1d"
	}
	if build.LiquidityTimeframe == "" {
		build.LiquidityTimeframe = "1h"
	}
	if build.PolicyVersion == "" || build.BenchmarkSymbolID == "" || build.BenchmarkAssetID == "" {
		return nil, fmt.Errorf("policy version and exact benchmark symbol/asset identities are required")
	}
	manifest, report, err := ValidateManifest(db, ManifestRequirement{
		ManifestID: build.ManifestID,
		Start:      request.Start,
		End:        request.End,
	})
	if err != nil {
		return nil, err
	}
	builder := &universeRangeBuilder{
		db:           db,
		request:      build,
		manifest:     manifest,
		baseReport:   report,
		manifestFrom: mustTime(manifest.RequestedStart),
		cutoff:       mustTime(manifest.KnowledgeCutoff),
		assets:       make(map[string]database.Asset),
		tradability:  make(map[string][]database.TradabilityInterval),
		bars:         make(map[rangeSeries][]cachedUniverseBar),
	}
	if err := builder.validateRequiredSeries(); err != nil {
		return nil, err
	}
	if err := builder.load(request.Start, request.End); err != nil {
		return nil, err
	}
	return builder, nil
}

func (b *universeRangeBuilder) validateRequiredSeries() error {
	var benchmark database.ExchangeSymbol
	if err := b.db.Where("id=? AND asset_id=?", b.request.BenchmarkSymbolID, b.request.BenchmarkAssetID).First(&benchmark).Error; err != nil {
		return &CoverageError{CoverageReport{SchemaVersion: CoverageSchemaVersion, ManifestID: b.request.ManifestID, Compatible: false, Failures: []CoverageFailure{{Code: "benchmark_identity_mismatch", Series: b.request.BenchmarkSymbolID, Details: "benchmark symbol and asset identity do not match"}}}}
	}
	for _, key := range []SeriesKey{
		{ExchangeSymbolID: b.request.BenchmarkSymbolID, Role: RoleBenchmark, Timeframe: b.request.DecisionTimeframe},
		{ExchangeSymbolID: b.request.BenchmarkSymbolID, Role: RoleBenchmark, Timeframe: b.request.LiquidityTimeframe},
	} {
		found := false
		for _, series := range b.manifest.Series {
			if sameSeries(series.SeriesKey, key) {
				found = true
				if !series.Complete {
					return &CoverageError{CoverageReport{SchemaVersion: CoverageSchemaVersion, ManifestID: b.request.ManifestID, Compatible: false, Failures: []CoverageFailure{{Code: "series_incomplete", Series: seriesID(key), Details: "required benchmark series is incomplete"}}}}
				}
				break
			}
		}
		if !found {
			return &CoverageError{CoverageReport{SchemaVersion: CoverageSchemaVersion, ManifestID: b.request.ManifestID, Compatible: false, Failures: []CoverageFailure{{Code: "exact_series_missing", Series: seriesID(key), Details: "series absent from manifest"}}}}
		}
	}
	return nil
}

func (b *universeRangeBuilder) load(start, end time.Time) error {
	ids := make([]string, 0, len(b.manifest.Series))
	seen := make(map[string]struct{}, len(b.manifest.Series))
	for _, series := range b.manifest.Series {
		if _, ok := seen[series.ExchangeSymbolID]; !ok {
			seen[series.ExchangeSymbolID] = struct{}{}
			ids = append(ids, series.ExchangeSymbolID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if err := b.db.Where("id IN ? AND retrieved_at<=?", ids, b.cutoff).Find(&b.symbols).Error; err != nil {
		return err
	}
	assetIDs := make([]string, 0, len(b.symbols))
	for _, symbol := range b.symbols {
		assetIDs = append(assetIDs, symbol.AssetID)
	}
	var assets []database.Asset
	if err := b.db.Where("id IN ? AND retrieved_at<=?", assetIDs, b.cutoff).Find(&assets).Error; err != nil {
		return err
	}
	for _, asset := range assets {
		b.assets[asset.ID] = asset
	}
	var intervals []database.TradabilityInterval
	if err := b.db.Where("exchange_symbol_id IN ? AND effective_from<? AND (effective_to IS NULL OR effective_to>?) AND retrieved_at<=?", ids, end, start, b.cutoff).
		Order("exchange_symbol_id ASC,effective_from ASC,id ASC").Find(&intervals).Error; err != nil {
		return err
	}
	for _, interval := range intervals {
		b.tradability[interval.ExchangeSymbolID] = append(b.tradability[interval.ExchangeSymbolID], interval)
	}

	lookbackStart := maxTime(start.Add(-90*24*time.Hour), b.manifestFrom)
	var rows []database.HistoricalBar
	if err := b.db.Select("exchange_symbol_id,timeframe,role,open_time,available_at,open,high,low,close,volume").
		Where("dataset_version=? AND exchange_symbol_id IN ? AND ((role=? AND timeframe IN ?) OR (role=? AND timeframe IN ?)) AND open_time>=? AND open_time<? AND retrieved_at<=?", b.manifest.DatasetVersion, ids, RoleDecision, []string{b.request.DecisionTimeframe, b.request.LiquidityTimeframe}, RoleBenchmark, []string{b.request.DecisionTimeframe, b.request.LiquidityTimeframe}, lookbackStart, end, b.cutoff).
		Order("exchange_symbol_id ASC,role ASC,timeframe ASC,open_time ASC,id ASC").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		duration, ok := timeframeDuration(row.Timeframe)
		if !ok {
			return fmt.Errorf("unsupported timeframe %q", row.Timeframe)
		}
		parse := func(value string) float64 { parsed, _ := strconv.ParseFloat(value, 64); return parsed }
		key := rangeSeries{symbolID: row.ExchangeSymbolID, role: row.Role, timeframe: row.Timeframe}
		b.bars[key] = append(b.bars[key], cachedUniverseBar{
			openTime: row.OpenTime.UTC(), availableAt: row.AvailableAt.UTC(),
			bar: services.OHLCV{OpenTime: row.OpenTime.UnixMilli(), CloseTime: row.OpenTime.Add(duration).UnixMilli() - 1, Open: parse(row.Open), High: parse(row.High), Low: parse(row.Low), Close: parse(row.Close), Volume: parse(row.Volume)},
		})
	}
	return nil
}

func (b *universeRangeBuilder) build(db *gorm.DB, effectiveAt time.Time) (UniverseBuildResult, error) {
	request := b.request
	request.EffectiveAt = effectiveAt.UTC()
	report := b.baseReport
	report.Failures = append([]CoverageFailure(nil), b.baseReport.Failures...)
	symbols := b.symbolsAsOf(request.EffectiveAt)
	coverageComplete := true

	dailyStart := maxTime(request.EffectiveAt.Add(-90*24*time.Hour), b.manifestFrom)
	benchmarkDaily := b.window(request.BenchmarkSymbolID, RoleBenchmark, request.DecisionTimeframe, dailyStart, request.EffectiveAt)
	benchmarkReturn7D, benchmarkDailyUp := 0.0, false
	if !completeBarsAsOf(benchmarkDaily, dailyStart, request.EffectiveAt, mustDuration(request.DecisionTimeframe)) {
		coverageComplete = false
		report.Failures = append(report.Failures, CoverageFailure{"benchmark_coverage_missing", request.BenchmarkSymbolID, "benchmark daily data unavailable"})
	} else {
		benchmarkReturn7D = services.CalculateReturn(closes(benchmarkDaily), min(7, len(benchmarkDaily)-1))
		benchmarkDailyUp = trendUp(benchmarkDaily, 20, 50)
	}
	hourlyStart := maxTime(request.EffectiveAt.Add(-30*24*time.Hour), b.manifestFrom)
	benchmarkHourly := b.window(request.BenchmarkSymbolID, RoleBenchmark, request.LiquidityTimeframe, hourlyStart, request.EffectiveAt)
	benchmarkHigher := false
	if !completeBarsAsOf(benchmarkHourly, hourlyStart, request.EffectiveAt, mustDuration(request.LiquidityTimeframe)) {
		coverageComplete = false
		report.Failures = append(report.Failures, CoverageFailure{"benchmark_coverage_missing", request.BenchmarkSymbolID, "benchmark regime data unavailable"})
	} else {
		benchmarkHigher = trendUp(benchmarkHourly, 50, 200)
	}

	items := make([]universeBuildItem, 0, len(symbols))
	candidateIDs := make([]string, 0, len(symbols))
	for _, symbol := range symbols {
		if !request.BenchmarkTradable && symbol.AssetID == request.BenchmarkAssetID {
			continue
		}
		candidateIDs = append(candidateIDs, symbol.ID)
		start := maxTime(request.EffectiveAt.Add(-90*24*time.Hour), b.manifestFrom)
		daily := b.window(symbol.ID, RoleDecision, request.DecisionTimeframe, start, request.EffectiveAt)
		hourly := b.window(symbol.ID, RoleDecision, request.LiquidityTimeframe, start, request.EffectiveAt)
		metric := services.UniverseCandidateMetrics{Symbol: symbol.Ticker, BaseAsset: symbol.BaseAssetID, QuoteAsset: symbol.QuoteAssetID}
		effectiveStart := maxTime(start, symbol.ListedAt)
		if !completeBarsAsOf(daily, effectiveStart, request.EffectiveAt, mustDuration(request.DecisionTimeframe)) || !completeBarsAsOf(hourly, effectiveStart, request.EffectiveAt, mustDuration(request.LiquidityTimeframe)) {
			metric.RejectionReason = "coverage_incomplete"
			coverageComplete = false
			items = append(items, universeBuildItem{symbol: symbol, metric: metric})
			continue
		}
		metric = services.BuildUniverseCandidateMetrics(symbol.Ticker, symbol.BaseAssetID, symbol.QuoteAssetID, daily[len(daily)-1].Close, services.CalculateReturn(closes(hourly), min(24, len(hourly)-1)), sumRecentQuote(hourly, 24), daily, hourly, benchmarkReturn7D)
		metric.ListingAgeDays = int(request.EffectiveAt.Sub(symbol.ListedAt).Hours() / 24)
		if rejection := services.UniverseHardFilterReason(metric, request.Policy); rejection != "" {
			metric.RejectionReason = rejection
		}
		items = append(items, universeBuildItem{symbol: symbol, metric: metric})
	}
	return persistUniverseBuild(db, request, b.manifest, report, benchmarkHigher, benchmarkDailyUp, coverageComplete, candidateIDs, items)
}

func (b *universeRangeBuilder) window(symbolID, role, timeframe string, start, end time.Time) []services.OHLCV {
	cached := b.bars[rangeSeries{symbolID: symbolID, role: role, timeframe: timeframe}]
	from := sort.Search(len(cached), func(i int) bool { return !cached[i].openTime.Before(start) })
	to := sort.Search(len(cached), func(i int) bool { return !cached[i].openTime.Before(end) })
	values := make([]services.OHLCV, 0, to-from)
	for _, item := range cached[from:to] {
		if !item.availableAt.After(end) {
			values = append(values, item.bar)
		}
	}
	return values
}

func (b *universeRangeBuilder) symbolsAsOf(asOf time.Time) []database.ExchangeSymbol {
	eligible := make([]database.ExchangeSymbol, 0, len(b.symbols))
	for _, symbol := range b.symbols {
		asset, ok := b.assets[symbol.AssetID]
		if !ok || symbol.ListedAt.After(asOf) || symbol.AvailableAt.After(asOf) || asset.AvailableAt.After(asOf) || (symbol.DelistedAt != nil && !symbol.DelistedAt.After(asOf)) || !b.tradableAsOf(symbol.ID, asOf) {
			continue
		}
		eligible = append(eligible, symbol)
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].AssetID != eligible[j].AssetID {
			return eligible[i].AssetID < eligible[j].AssetID
		}
		if eligible[i].Ticker != eligible[j].Ticker {
			return eligible[i].Ticker < eligible[j].Ticker
		}
		if eligible[i].Version != eligible[j].Version {
			return eligible[i].Version > eligible[j].Version
		}
		return eligible[i].ID < eligible[j].ID
	})
	out := eligible[:0]
	for _, symbol := range eligible {
		if len(out) == 0 || out[len(out)-1].AssetID != symbol.AssetID {
			out = append(out, symbol)
		}
	}
	return out
}

func (b *universeRangeBuilder) tradableAsOf(symbolID string, asOf time.Time) bool {
	for _, interval := range b.tradability[symbolID] {
		if interval.SpotTradable && !interval.EffectiveFrom.After(asOf) && (interval.EffectiveTo == nil || interval.EffectiveTo.After(asOf)) && !interval.AvailableAt.After(asOf) {
			return true
		}
	}
	return false
}
