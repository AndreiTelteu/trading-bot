package pointintime

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/services"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UniverseBuildRequest struct {
	ManifestID         string
	EffectiveAt        time.Time
	PolicyVersion      string
	Policy             services.UniversePolicy
	DecisionTimeframe  string
	LiquidityTimeframe string
	BenchmarkSymbolID  string
	BenchmarkAssetID   string
	BenchmarkTradable  bool
}

type UniverseBuildResult struct {
	Snapshot database.UniverseSnapshot `json:"snapshot"`
	Members  []database.UniverseMember `json:"members"`
	Coverage CoverageReport            `json:"coverage"`
}

func BuildUniverseSnapshot(db *gorm.DB, request UniverseBuildRequest) (UniverseBuildResult, error) {
	if request.DecisionTimeframe == "" {
		request.DecisionTimeframe = "1d"
	}
	if request.LiquidityTimeframe == "" {
		request.LiquidityTimeframe = "1h"
	}
	if request.PolicyVersion == "" {
		return UniverseBuildResult{}, fmt.Errorf("policy version is required")
	}
	manifest, report, err := ValidateManifest(db, ManifestRequirement{ManifestID: request.ManifestID, Start: request.EffectiveAt, End: request.EffectiveAt, RequireComplete: false})
	if err != nil {
		return UniverseBuildResult{Coverage: report}, err
	}
	repo := Repository{DB: db}
	symbols, err := repo.SymbolsAsOf(request.ManifestID, request.EffectiveAt, true)
	if err != nil {
		return UniverseBuildResult{Coverage: report}, err
	}
	benchmarkReturn7D := 0.0
	benchmarkHigher, benchmarkDaily := false, false
	coverageComplete := true
	if request.BenchmarkSymbolID != "" {
		dailyStart := maxTime(request.EffectiveAt.Add(-90*24*time.Hour), mustTime(manifest.RequestedStart))
		daily, e := repo.Bars(request.ManifestID, request.BenchmarkSymbolID, RoleBenchmark, request.DecisionTimeframe, dailyStart, request.EffectiveAt, request.EffectiveAt)
		if e != nil || !completeBarsAsOf(daily, dailyStart, request.EffectiveAt, mustDuration(request.DecisionTimeframe)) {
			coverageComplete = false
			report.Failures = append(report.Failures, CoverageFailure{"benchmark_coverage_missing", request.BenchmarkSymbolID, "benchmark daily data unavailable"})
		} else {
			benchmarkReturn7D = services.CalculateReturn(closes(daily), min(7, len(daily)-1))
			benchmarkDaily = trendUp(daily, 20, 50)
		}
		hourlyStart := maxTime(request.EffectiveAt.Add(-30*24*time.Hour), mustTime(manifest.RequestedStart))
		hourly, e := repo.Bars(request.ManifestID, request.BenchmarkSymbolID, RoleBenchmark, request.LiquidityTimeframe, hourlyStart, request.EffectiveAt, request.EffectiveAt)
		if e != nil || !completeBarsAsOf(hourly, hourlyStart, request.EffectiveAt, mustDuration(request.LiquidityTimeframe)) {
			coverageComplete = false
			report.Failures = append(report.Failures, CoverageFailure{"benchmark_coverage_missing", request.BenchmarkSymbolID, "benchmark regime data unavailable"})
		} else {
			benchmarkHigher = trendUp(hourly, 50, 200)
		}
	}
	type item struct {
		symbol database.ExchangeSymbol
		metric services.UniverseCandidateMetrics
	}
	items := []item{}
	candidateIDs := []string{}
	assetSeen := map[string]bool{}
	for _, symbol := range symbols {
		if assetSeen[symbol.AssetID] {
			continue
		}
		assetSeen[symbol.AssetID] = true
		if !request.BenchmarkTradable && symbol.AssetID == request.BenchmarkAssetID {
			continue
		}
		candidateIDs = append(candidateIDs, symbol.ID)
		start := maxTime(request.EffectiveAt.Add(-90*24*time.Hour), mustTime(manifest.RequestedStart))
		daily, dErr := repo.Bars(request.ManifestID, symbol.ID, RoleDecision, request.DecisionTimeframe, start, request.EffectiveAt, request.EffectiveAt)
		hourly, hErr := repo.Bars(request.ManifestID, symbol.ID, RoleDecision, request.LiquidityTimeframe, start, request.EffectiveAt, request.EffectiveAt)
		metric := services.UniverseCandidateMetrics{Symbol: symbol.Ticker, BaseAsset: symbol.BaseAssetID, QuoteAsset: symbol.QuoteAssetID}
		effectiveStart := maxTime(start, symbol.ListedAt)
		if dErr != nil || hErr != nil || !completeBarsAsOf(daily, effectiveStart, request.EffectiveAt, mustDuration(request.DecisionTimeframe)) || !completeBarsAsOf(hourly, effectiveStart, request.EffectiveAt, mustDuration(request.LiquidityTimeframe)) {
			metric.RejectionReason = "coverage_incomplete"
			coverageComplete = false
			items = append(items, item{symbol, metric})
			continue
		}
		last := daily[len(daily)-1].Close
		quote24 := sumRecentQuote(hourly, 24)
		change := services.CalculateReturn(closes(hourly), min(24, len(hourly)-1))
		metric = services.BuildUniverseCandidateMetrics(symbol.Ticker, symbol.BaseAssetID, symbol.QuoteAssetID, last, change, quote24, daily, hourly, benchmarkReturn7D)
		metric.ListingAgeDays = int(request.EffectiveAt.Sub(symbol.ListedAt).Hours() / 24)
		if rejection := services.UniverseHardFilterReason(metric, request.Policy); rejection != "" {
			metric.RejectionReason = rejection
		}
		items = append(items, item{symbol, metric})
	}
	accepted := []services.UniverseCandidateMetrics{}
	for _, it := range items {
		if it.metric.RejectionReason == "" {
			accepted = append(accepted, it.metric)
		}
	}
	breadth := services.ComputeUniverseBreadth(accepted)
	regime := services.DetermineUniverseRegime(benchmarkHigher, benchmarkDaily, breadth)
	ranked := services.RankUniverseCandidates(accepted, request.Policy)
	active, shortlist := services.SelectUniverseCandidates(ranked, request.Policy, regime)
	rankBySymbol := map[string]services.UniverseCandidateMetrics{}
	for _, m := range ranked {
		rankBySymbol[m.Symbol] = m
	}
	activeSet := map[string]bool{}
	shortSet := map[string]bool{}
	for _, m := range active {
		activeSet[m.Symbol] = true
	}
	for _, m := range shortlist {
		shortSet[m.Symbol] = true
	}
	members := make([]database.UniverseMember, 0, len(items))
	for _, it := range items {
		m := it.metric
		if rankedMetric, ok := rankBySymbol[m.Symbol]; ok {
			m = rankedMetric
		}
		stage := "ranked"
		if m.RejectionReason != "" {
			stage = "rejected"
		} else if shortSet[m.Symbol] {
			stage = "shortlist"
		} else if activeSet[m.Symbol] {
			stage = "active"
		}
		assetID, symbolID := it.symbol.AssetID, it.symbol.ID
		member := database.UniverseMember{AssetID: &assetID, ExchangeSymbolID: &symbolID, Symbol: it.symbol.Ticker, Stage: stage, LastPrice: m.LastPrice, Change24h: m.Change24h, QuoteVolume24h: m.QuoteVolume24h, ListingAgeDays: m.ListingAgeDays, MedianDailyQuoteVolume: m.MedianDailyQuoteVolume, MedianIntradayQuoteVolume: m.MedianIntradayQuoteVolume, GapRatio: m.GapRatio, VolatilityRatio: m.VolatilityRatio, Return1D: m.Return1D, Return3D: m.Return3D, Return7D: m.Return7D, Return30D: m.Return30D, RelativeStrength: m.RelativeStrength, TrendQuality: m.TrendQuality, BreakoutProximity: m.BreakoutProximity, VolumeAcceleration: m.VolumeAcceleration, OverextensionPenalty: m.OverextensionPenalty, RankScore: m.RankScore, RankComponentsJSON: EncodeJSON(m.RankComponents), Shortlisted: shortSet[m.Symbol]}
		if m.RejectionReason != "" {
			reason := m.RejectionReason
			member.RejectionReason = &reason
		}
		members = append(members, member)
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].RankScore != members[j].RankScore {
			return members[i].RankScore > members[j].RankScore
		}
		return members[i].Symbol < members[j].Symbol
	})
	rank := 0
	for idx := range members {
		if members[idx].RejectionReason == nil {
			rank++
			members[idx].Rank = rank
		}
	}
	state := "complete"
	if !coverageComplete {
		state = "coverage_failed"
		report.Compatible = false
	}
	coverageJSON := EncodeJSON(report)
	manifestID := manifest.ID
	snapshot := database.UniverseSnapshot{SnapshotTime: request.EffectiveAt.UTC(), PolicyVersion: request.PolicyVersion, DatasetManifestID: &manifestID, CoverageState: state, CoverageJSON: coverageJSON, BenchmarkAssetID: stringPointer(request.BenchmarkAssetID), BenchmarkSymbolID: stringPointer(request.BenchmarkSymbolID), CandidatePoolJSON: EncodeJSON(candidateIDs), RebalanceInterval: request.Policy.RebalanceIntervalLabel, RegimeState: regime, BreadthRatio: breadth, EligibleCount: len(accepted), CandidateCount: len(items), RankedCount: len(ranked), ShortlistCount: len(shortlist)}
	err = db.Transaction(func(tx *gorm.DB) error {
		var existing database.UniverseSnapshot
		find := tx.Where("snapshot_time=? AND policy_version=? AND dataset_manifest_id=?", snapshot.SnapshotTime, snapshot.PolicyVersion, manifestID).Preload("Members").First(&existing).Error
		if find == nil {
			snapshot = existing
			members = existing.Members
			return nil
		}
		if !errors.Is(find, gorm.ErrRecordNotFound) {
			return find
		}
		created := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&snapshot)
		if created.Error != nil {
			return created.Error
		}
		if created.RowsAffected == 0 {
			if err := tx.Where("snapshot_time=? AND policy_version=? AND dataset_manifest_id=?", snapshot.SnapshotTime, snapshot.PolicyVersion, manifestID).Preload("Members").First(&existing).Error; err != nil {
				return err
			}
			snapshot = existing
			members = existing.Members
			return nil
		}
		for idx := range members {
			members[idx].UniverseSnapshotID = snapshot.ID
		}
		if len(members) > 0 {
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&members).Error
		}
		return nil
	})
	if err != nil {
		return UniverseBuildResult{}, err
	}
	// Always return the canonical persisted representation. PostgreSQL rounds
	// timestamps to microseconds, while the just-created GORM values may still
	// carry nanoseconds; a read-back also gives members a stable order on both
	// first build and idempotent resume.
	var persisted database.UniverseSnapshot
	if err := db.Where("snapshot_time=? AND policy_version=? AND dataset_manifest_id=?", snapshot.SnapshotTime, snapshot.PolicyVersion, manifestID).First(&persisted).Error; err != nil {
		return UniverseBuildResult{}, err
	}
	if err := db.Where("universe_snapshot_id=?", persisted.ID).Order("rank_score DESC, symbol ASC").Find(&members).Error; err != nil {
		return UniverseBuildResult{}, err
	}
	persisted.Members = members
	snapshot = persisted
	result := UniverseBuildResult{snapshot, members, report}
	if !coverageComplete {
		return result, &CoverageError{report}
	}
	return result, nil
}

func mustTime(v string) time.Time { t, _ := time.Parse(time.RFC3339Nano, v); return t }
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
func closes(v []services.OHLCV) []float64 {
	r := make([]float64, len(v))
	for i := range v {
		r[i] = v[i].Close
	}
	return r
}
func trendUp(v []services.OHLCV, fast, slow int) bool {
	if len(v) < 2 {
		return false
	}
	c := closes(v)
	f := services.CalculateEMA(c, min(fast, len(c)))
	s := services.CalculateEMA(c, min(slow, len(c)))
	return f > s
}
func sumRecentQuote(v []services.OHLCV, n int) float64 {
	start := len(v) - n
	if start < 0 {
		start = 0
	}
	sum := 0.0
	for _, b := range v[start:] {
		sum += b.Close * b.Volume
	}
	return sum
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func stringPointer(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
func mustDuration(frame string) time.Duration { d, _ := timeframeDuration(frame); return d }
func completeBarsAsOf(values []services.OHLCV, start, end time.Time, interval time.Duration) bool {
	if len(values) == 0 || interval <= 0 {
		return false
	}
	if time.UnixMilli(values[0].OpenTime).After(start) {
		return false
	}
	if time.UnixMilli(values[len(values)-1].OpenTime).Before(end.Add(-interval)) {
		return false
	}
	for idx := 1; idx < len(values); idx++ {
		if time.Duration(values[idx].OpenTime-values[idx-1].OpenTime)*time.Millisecond != interval {
			return false
		}
	}
	return true
}
