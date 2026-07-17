package backtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/services"
	"trading-go/internal/tradingcore"
)

const (
	CoverageSchemaVersion       = "backtest-coverage-v1"
	ManifestSchemaVersion       = "backtest-run-manifest-v4"
	LegacyManifestSchemaVersion = "backtest-run-manifest-v3"
	ArtifactSchemaVersion       = "backtest-artifacts-v1"
)

type CoverageError struct{ Report CoverageReport }

func (err *CoverageError) Error() string {
	return fmt.Sprintf("backtest coverage failed: %s", joinCoverageReasons(err.Report.Reasons))
}

type UnsupportedRealismError struct {
	Policy string
	Reason string
}

func (err *UnsupportedRealismError) Error() string {
	return fmt.Sprintf("unsupported backtest realism policy %q: %s", err.Policy, err.Reason)
}

func IsCoverageError(err error) bool {
	var target *CoverageError
	return errors.As(err, &target)
}

func defaultStage03Policies(config *BacktestConfig) {
	defaultCoverage := config.CoveragePolicy.Version == ""
	if defaultCoverage {
		config.CoveragePolicy.Version = CoverageSchemaVersion
		config.CoveragePolicy.RequireRequestedBounds = true
	}
	if config.CoveragePolicy.DecisionInterval <= 0 && config.TimeframeMinutes > 0 {
		config.CoveragePolicy.DecisionInterval = time.Duration(config.TimeframeMinutes) * time.Minute
	}
	if config.CoveragePolicy.ExecutionInterval <= 0 && config.ExecutionTimeframeMins > 0 {
		config.CoveragePolicy.ExecutionInterval = time.Duration(config.ExecutionTimeframeMins) * time.Minute
	}
	if config.CoveragePolicy.ReplayInterval <= 0 {
		config.CoveragePolicy.ReplayInterval = config.UniversePolicy.RebalanceInterval
		if config.CoveragePolicy.ReplayInterval <= 0 {
			config.CoveragePolicy.ReplayInterval = config.CoveragePolicy.DecisionInterval
		}
	}
	if config.ExecutionPolicy.Version == "" {
		config.ExecutionPolicy.Version = "backtest-execution-v1"
	}
	if config.ExecutionPolicy.Timing == "" {
		config.ExecutionPolicy.Timing = ExecutionNextExecutable
	}
	if config.ExecutionPolicy.Liquidity == "" {
		config.ExecutionPolicy.Liquidity = LiquidityFullFillOHLCV
	}
	if config.ExecutionPolicy.CostVersion == "" {
		config.ExecutionPolicy.CostVersion = "backtest-cost-v1"
	}
	if config.ConfigVersion == "" {
		config.ConfigVersion = "backtest-config-v1"
	}
	if config.StrategyVersion == "" {
		config.StrategyVersion = "legacy-rule-strategy-v1"
	}
}

func validateRealismPolicy(config BacktestConfig) error {
	if config.FeeBps != math.Trunc(config.FeeBps) || config.SlippageBps != math.Trunc(config.SlippageBps) {
		return &UnsupportedRealismError{Policy: "fractional_basis_points", Reason: "fee and slippage basis points must be integral"}
	}
	switch config.ExecutionPolicy.Timing {
	case ExecutionNextExecutable:
	case ExecutionMarketOnClose:
		return &UnsupportedRealismError{Policy: string(config.ExecutionPolicy.Timing), Reason: "market-on-close requires auction/close-order coverage not represented by OHLCV"}
	default:
		return &UnsupportedRealismError{Policy: string(config.ExecutionPolicy.Timing), Reason: "unknown execution timing"}
	}
	switch config.ExecutionPolicy.Liquidity {
	case LiquidityFullFillOHLCV:
		return nil
	case LiquidityVolumeCapped, LiquidityPartialFill:
		return &UnsupportedRealismError{Policy: string(config.ExecutionPolicy.Liquidity), Reason: "OHLCV has no order-book or trade-level liquidity needed for this policy"}
	default:
		return &UnsupportedRealismError{Policy: string(config.ExecutionPolicy.Liquidity), Reason: "unknown liquidity policy"}
	}
}

func ValidateCoverage(config BacktestConfig, decision map[string][]services.OHLCV) CoverageReport {
	return validateCoverage(config, decision, fixtureReplaySnapshots(config.ReplaySnapshots))
}

func validateCoverage(config BacktestConfig, decision map[string][]services.OHLCV, replay []replaySnapshotEntry) CoverageReport {
	report := CoverageReport{SchemaVersion: CoverageSchemaVersion, PolicyVersion: config.CoveragePolicy.Version, Passed: true, Diagnostics: []CoverageDiagnostic{}}
	symbolSet := map[string]struct{}{}
	for _, symbol := range config.Symbols {
		symbolSet[strings.ToUpper(symbol)] = struct{}{}
	}
	if len(symbolSet) == 0 {
		for symbol := range decision {
			symbolSet[strings.ToUpper(symbol)] = struct{}{}
		}
	}
	symbols := make([]string, 0, len(symbolSet))
	for symbol := range symbolSet {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	for _, symbol := range symbols {
		seriesStart, seriesEnd := lifecycleCoverageBounds(config, symbol, config.CoveragePolicy.DecisionInterval)
		report = validateBarCoverage(report, "decision", symbol, decision[symbol], config.CoveragePolicy.DecisionInterval, seriesStart, seriesEnd, config.CoveragePolicy)
		if _, configured := config.ExecutionSeries[symbol]; configured || len(config.ExecutionSeries) > 0 || config.ExecutionSeriesRequired {
			execStart, execEnd := lifecycleCoverageBounds(config, symbol, config.CoveragePolicy.ExecutionInterval)
			report = validateBarCoverage(report, "execution", symbol, config.ExecutionSeries[symbol], config.CoveragePolicy.ExecutionInterval, execStart, execEnd, config.CoveragePolicy)
		} else {
			report = validateBarCoverage(report, "execution", symbol, decision[symbol], config.CoveragePolicy.DecisionInterval, seriesStart, seriesEnd, config.CoveragePolicy)
		}
	}
	if config.BenchmarkRequired {
		symbol := strings.ToUpper(config.BenchmarkSymbol)
		if symbol == "" {
			symbol = "BTCUSDT"
		}
		before := len(report.Reasons)
		report = validateBarCoverage(report, "benchmark", symbol, config.BenchmarkSeries, config.CoveragePolicy.DecisionInterval, config.Start, config.End, config.CoveragePolicy)
		if len(report.Reasons) > before && len(config.BenchmarkSeries) == 0 {
			report.Reasons[len(report.Reasons)-1] = CoverageBenchmarkMissing
			report.Diagnostics[len(report.Diagnostics)-1].Reason = CoverageBenchmarkMissing
		}
	}
	if config.UniverseMode == UniverseDynamicReplay {
		if len(replay) == 0 {
			report.add(CoverageDiagnostic{Dataset: "universe", Status: "failed", Reason: CoverageReplayEmpty})
		} else {
			minimum := config.CoveragePolicy.RequiredReplayMembers
			if minimum <= 0 {
				minimum = 1
			}
			previous := time.Time{}
			effective := 0
			for i, snapshot := range replay {
				if i > 0 && snapshot.Timestamp.Equal(previous) {
					report.add(CoverageDiagnostic{Dataset: "universe", Status: "failed", Reason: CoverageReplayDuplicate, First: canonicalTime(snapshot.Timestamp)})
				}
				if i > 0 && snapshot.Timestamp.Before(previous) {
					report.add(CoverageDiagnostic{Dataset: "universe", Status: "failed", Reason: CoverageNonMonotonic, First: canonicalTime(snapshot.Timestamp)})
				}
				if !snapshot.Timestamp.After(config.Start) {
					effective++
				}
				memberCount := replayEligibleMemberCount(snapshot.Members)
				if memberCount < minimum && !snapshot.ObservedComplete {
					report.add(CoverageDiagnostic{Dataset: "universe", Status: "failed", Reason: CoverageReplayMembersEmpty, Count: memberCount, First: canonicalTime(snapshot.Timestamp)})
				}
				seen := map[string]bool{}
				for _, member := range snapshot.Members {
					symbol := strings.ToUpper(member.Symbol)
					if seen[symbol] {
						report.add(CoverageDiagnostic{Dataset: "universe", Symbol: symbol, Status: "failed", Reason: CoverageReplayMemberDup, First: canonicalTime(snapshot.Timestamp)})
					}
					seen[symbol] = true
				}
				if i > 0 && config.CoveragePolicy.ReplayInterval > 0 {
					gaps := int(snapshot.Timestamp.Sub(previous)/config.CoveragePolicy.ReplayInterval) - 1
					if gaps > config.CoveragePolicy.MaxReplayGapIntervals {
						report.add(CoverageDiagnostic{Dataset: "universe", Status: "failed", Reason: CoverageReplayGap, Gaps: gaps, First: canonicalTime(previous), Last: canonicalTime(snapshot.Timestamp)})
					}
				}
				previous = snapshot.Timestamp
			}
			if effective == 0 {
				report.add(CoverageDiagnostic{Dataset: "universe", Status: "failed", Reason: CoverageReplayNoEffective, Count: effective})
			}
			if report.Passed || !containsCoverageDataset(report.Diagnostics, "universe") {
				report.Diagnostics = append(report.Diagnostics, CoverageDiagnostic{Dataset: "universe", Status: "ok", Count: len(replay), First: canonicalTime(replay[0].Timestamp), Last: canonicalTime(replay[len(replay)-1].Timestamp)})
			}
		}
	}
	for _, feature := range config.CoveragePolicy.RequiredModelFeatures {
		var found *FeatureSeries
		for i := range config.FeatureSeries {
			if config.FeatureSeries[i].Name == feature {
				found = &config.FeatureSeries[i]
				break
			}
		}
		if found == nil || len(found.Observations) == 0 || found.Version == "" || found.Provenance == "" || found.Interval <= 0 {
			report.add(CoverageDiagnostic{Dataset: "feature", Symbol: feature, Status: "failed", Reason: CoverageFeatureMissing})
		} else {
			bars := make([]services.OHLCV, len(found.Observations))
			for i, o := range found.Observations {
				if o.AvailableAt.Before(o.EventAt) {
					report.add(CoverageDiagnostic{Dataset: "feature", Symbol: feature, Status: "failed", Reason: CoverageFeatureMissing, First: canonicalTime(o.AvailableAt)})
				}
				bars[i] = services.OHLCV{OpenTime: o.AvailableAt.UnixMilli(), CloseTime: o.AvailableAt.UnixMilli() + 1}
			}
			featureStart := config.Start
			if !featureStart.IsZero() {
				featureStart = featureStart.Add(time.Duration(computeSignalLookback(config)) * found.Interval)
			}
			report = validateBarCoverage(report, "feature", feature, bars, found.Interval, featureStart, config.End, config.CoveragePolicy)
		}
	}
	report.Reasons = uniqueCoverageReasons(report.Reasons)
	sort.SliceStable(report.Diagnostics, func(i, j int) bool {
		a, b := report.Diagnostics[i], report.Diagnostics[j]
		if a.Dataset != b.Dataset {
			return a.Dataset < b.Dataset
		}
		if a.Symbol != b.Symbol {
			return a.Symbol < b.Symbol
		}
		return a.First < b.First
	})
	return report
}

func replayEligibleMemberCount(members []database.UniverseMember) int {
	count := 0
	for _, member := range members {
		if member.RejectionReason != nil || member.Stage == "rejected" || member.Stage == "ranked" {
			continue
		}
		count++
	}
	return count
}

func lifecycleCoverageBounds(config BacktestConfig, symbol string, interval time.Duration) (time.Time, time.Time) {
	start, end := config.Start, config.End
	if lifecycle, ok := config.SymbolLifecycles[strings.ToUpper(symbol)]; ok {
		if lifecycle.ListedAt.After(start) {
			start = lifecycle.ListedAt
		}
		if lifecycle.DelistedAt != nil && lifecycle.DelistedAt.Before(end) {
			end = *lifecycle.DelistedAt
		}
	}
	return start, end
}

func validateBarCoverage(report CoverageReport, dataset, symbol string, bars []services.OHLCV, interval time.Duration, start, end time.Time, policy CoveragePolicy) CoverageReport {
	d := CoverageDiagnostic{Dataset: dataset, Symbol: symbol, Status: "ok", Count: len(bars)}
	if len(bars) == 0 {
		d.Status, d.Reason = "failed", CoverageMissingSeries
		report.add(d)
		return report
	}
	d.First, d.Last = canonicalMillis(bars[0].OpenTime), canonicalMillis(bars[len(bars)-1].CloseTime)
	seen := map[int64]struct{}{}
	previous := int64(0)
	for i, bar := range bars {
		width := time.Duration(bar.CloseTime-bar.OpenTime) * time.Millisecond
		validWidth := width > 0
		if interval > 0 && dataset != "feature" {
			// Binance klines use an inclusive close timestamp (interval - 1ms),
			// while synthetic/internal bars commonly use the exclusive boundary.
			validWidth = width == interval || interval >= time.Millisecond && width == interval-time.Millisecond
		}
		if !validWidth {
			d.Status, d.Reason = "failed", CoverageInvalidBarWidth
		}
		if _, exists := seen[bar.OpenTime]; exists {
			d.Status, d.Reason = "failed", CoverageDuplicateTimestamp
		}
		seen[bar.OpenTime] = struct{}{}
		if i > 0 && bar.OpenTime < previous {
			d.Status, d.Reason = "failed", CoverageNonMonotonic
		}
		if i > 0 && interval > 0 {
			missing := int((time.Duration(bar.OpenTime-previous)*time.Millisecond)/interval) - 1
			if missing > 0 {
				d.Gaps += missing
			}
		}
		previous = bar.OpenTime
	}
	if d.Status == "ok" && d.Gaps > policy.MaxMissingIntervals {
		d.Status, d.Reason = "failed", CoverageInternalGap
	}
	startMissing := !start.IsZero() && time.UnixMilli(bars[0].OpenTime).After(start)
	lastClose := time.UnixMilli(bars[len(bars)-1].CloseTime)
	endMissing := !end.IsZero() && lastClose.Before(end) && lastClose.Add(time.Millisecond).Before(end)
	if d.Status == "ok" && policy.RequireRequestedBounds && (startMissing || endMissing) {
		d.Status, d.Reason = "failed", CoverageBounds
	}
	if d.Status == "ok" && d.Gaps > 0 {
		d.Status = "warning"
	}
	if d.Status == "failed" {
		report.add(d)
	} else {
		report.Diagnostics = append(report.Diagnostics, d)
	}
	return report
}

func (report *CoverageReport) add(d CoverageDiagnostic) {
	report.Passed = false
	report.Reasons = append(report.Reasons, d.Reason)
	report.Diagnostics = append(report.Diagnostics, d)
}

func nextExecutable(config BacktestConfig, state *symbolState, symbol string, informationAt time.Time) (services.OHLCV, time.Time, bool) {
	if bars := config.ExecutionSeries[symbol]; len(bars) > 0 {
		idx := sort.Search(len(bars), func(i int) bool { return bars[i].OpenTime > informationAt.UnixMilli() })
		if idx < len(bars) {
			return bars[idx], time.UnixMilli(bars[idx].OpenTime), true
		}
		return services.OHLCV{}, time.Time{}, false
	}
	if state == nil {
		return services.OHLCV{}, time.Time{}, false
	}
	idx := sort.Search(len(state.series), func(i int) bool { return state.series[i].OpenTime > informationAt.UnixMilli() })
	if idx < len(state.series) {
		at := time.UnixMilli(state.series[idx].OpenTime)
		return state.series[idx], at, true
	}
	return services.OHLCV{}, time.Time{}, false
}

func isLastLiquidationOpportunity(config BacktestConfig, state *symbolState, symbol string, bar services.OHLCV) bool {
	informationAt := time.UnixMilli(bar.CloseTime)
	_, fillAt, ok := nextExecutable(config, state, symbol, informationAt)
	if !ok || !config.End.IsZero() && !fillAt.Before(config.End) {
		return false
	}
	if bars := config.ExecutionSeries[symbol]; len(bars) > 0 {
		nextDecision := state.currentIndex + 1
		if nextDecision >= len(state.series) {
			return true
		}
		_, laterFill, later := nextExecutable(config, state, symbol, time.UnixMilli(state.series[nextDecision].CloseTime))
		return !later || !config.End.IsZero() && !laterFill.Before(config.End)
	}
	_, laterAt, later := nextExecutable(config, state, symbol, time.UnixMilli(state.series[state.currentIndex+1].CloseTime))
	return !later || !config.End.IsZero() && !laterAt.Before(config.End)
}

func inlineDatasetManifestID(config BacktestConfig, decision map[string][]services.OHLCV, replay []replaySnapshotEntry) string {
	type dataset struct {
		Kind, Symbol string
		Bars         []services.OHLCV
		Snapshots    []ReplaySnapshot
		Features     []FeatureSeries
	}
	items := make([]dataset, 0, len(decision)+2)
	for symbol, bars := range decision {
		items = append(items, dataset{Kind: "decision", Symbol: symbol, Bars: bars})
	}
	for symbol, bars := range config.ExecutionSeries {
		items = append(items, dataset{Kind: "execution", Symbol: symbol, Bars: bars})
	}
	if len(config.BenchmarkSeries) > 0 {
		items = append(items, dataset{Kind: "benchmark", Symbol: config.BenchmarkSymbol, Bars: config.BenchmarkSeries})
	}
	if len(replay) > 0 {
		items = append(items, dataset{Kind: "universe", Snapshots: canonicalReplaySnapshots(replay)})
	}
	if len(config.FeatureSeries) > 0 {
		features := append([]FeatureSeries(nil), config.FeatureSeries...)
		sort.Slice(features, func(i, j int) bool { return features[i].Name < features[j].Name })
		items = append(items, dataset{Kind: "features", Features: features})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind < items[j].Kind
		}
		return items[i].Symbol < items[j].Symbol
	})
	encoded, _ := json.Marshal(items)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func buildManifest(config BacktestConfig, coverage CoverageReport, classification RunClassification, hash string) RunManifest {
	limitations := append([]string(nil), config.DatasetLimitations...)
	if config.DatasetManifestID == "" {
		config.DatasetManifestID = hash
		limitations = append(limitations, "in_memory_fixture_manifest_not_persisted")
	}
	if !config.ConstraintsAvailable {
		limitations = append(limitations, "legacy_non_manifest_fixture_symbol_constraints_fallback")
	}
	limitations = append(limitations, "ohlcv_full_fill_no_order_book_model")
	sort.Strings(limitations)
	selected := selectedStrategyForManifest(config)
	return RunManifest{SchemaVersion: ManifestSchemaVersion, Classification: classification, CodeRevision: config.CodeRevision, ConfigVersion: config.ConfigVersion, StrategyVersion: selected.Descriptor.Version, Strategy: selected, PolicyVersion: backtestPolicyVersion(config), CostVersion: config.ExecutionPolicy.CostVersion, DatasetManifestID: config.DatasetManifestID, Dataset: DatasetAudit{ManifestID: config.DatasetManifestID, KnowledgeCutoff: config.DatasetKnowledgeCutoff, Series: append([]DatasetSeriesIdentity(nil), config.DatasetSeries...)}, UniverseMode: config.UniverseMode, BenchmarkSymbol: config.BenchmarkSymbol, Seed: config.Seed, FeeBPS: config.FeeBps, SlippageBPS: config.SlippageBps, CoveragePolicy: config.CoveragePolicy, ExecutionPolicy: config.ExecutionPolicy, Start: canonicalTime(config.Start), End: canonicalTime(config.End), Coverage: coverage, Limitations: limitations, Artifacts: ArtifactRefs{SchemaVersion: ArtifactSchemaVersion, Manifest: "manifest.json", Decisions: "decisions.json", Orders: "orders.json", Fills: "fills.json", Trades: "trades.json", Ledger: "ledger.json", Equity: "equity.json", Metrics: "metrics.json", Exposure: "exposure.json"}}
}

func selectedStrategyForManifest(config BacktestConfig) SelectedStrategy {
	id := strings.TrimSpace(config.StrategyID)
	version := strings.TrimSpace(config.StrategyVersion)
	if id == "" {
		id = StrategyLegacyCompatibility
	}
	if version == "" || strings.HasPrefix(version, "legacy-rule") {
		version = "1.0.0"
	}
	selected, _, err := DefaultStrategyRegistry.Resolve(id, version, config.StrategyParameters)
	if err == nil {
		return selected
	}
	return SelectedStrategy{Descriptor: StrategyDescriptor{SchemaVersion: StrategyDescriptorSchemaVersion, ID: id, Version: version, Description: "Unregistered compatibility strategy", DecisionCadence: config.Timeframe, RebalanceCadence: config.Timeframe, Risk: StrategyRiskDeclaration{UsesSharedRisk: true}, LegacyCompatibility: true}, Parameters: cloneStringMap(config.StrategyParameters)}
}

func MarshalArtifactBytes(result BacktestResult) (ArtifactBytes, error) {
	if result.Manifest.SchemaVersion != ManifestSchemaVersion {
		return ArtifactBytes{}, fmt.Errorf("unsupported manifest schema %q", result.Manifest.SchemaVersion)
	}
	if result.Artifacts.SchemaVersion != ArtifactSchemaVersion {
		return ArtifactBytes{}, fmt.Errorf("unsupported artifact schema %q", result.Artifacts.SchemaVersion)
	}
	encode := func(value any) ([]byte, error) {
		return json.Marshal(ArtifactEnvelope{SchemaVersion: ArtifactSchemaVersion, Payload: value})
	}
	var output ArtifactBytes
	var err error
	if output.Manifest, err = json.Marshal(result.Manifest); err != nil {
		return ArtifactBytes{}, err
	}
	if output.Decisions, err = encode(result.Artifacts.Decisions); err != nil {
		return ArtifactBytes{}, err
	}
	if output.Orders, err = encode(result.Artifacts.Orders); err != nil {
		return ArtifactBytes{}, err
	}
	if output.Fills, err = encode(result.Artifacts.Fills); err != nil {
		return ArtifactBytes{}, err
	}
	if output.Trades, err = encode(result.Trades); err != nil {
		return ArtifactBytes{}, err
	}
	if output.Ledger, err = encode(result.Artifacts.Ledger); err != nil {
		return ArtifactBytes{}, err
	}
	if output.Exposure, err = encode(result.Artifacts.Exposure); err != nil {
		return ArtifactBytes{}, err
	}
	if output.Equity, err = encode(result.Equity); err != nil {
		return ArtifactBytes{}, err
	}
	if output.Metrics, err = encode(result.Metrics); err != nil {
		return ArtifactBytes{}, err
	}
	return output, nil
}

func fixtureReplaySnapshots(values []ReplaySnapshot) []replaySnapshotEntry {
	result := make([]replaySnapshotEntry, 0, len(values))
	for _, value := range values {
		members := make([]database.UniverseMember, 0, len(value.Members))
		for _, member := range value.Members {
			stage := strings.ToLower(strings.TrimSpace(member.Stage))
			if stage == "" || stage == "eligible" || stage == "accepted" {
				stage = "active"
			}
			member.Stage = stage
			rankScore := member.RankScore
			if rankScore == 0 && member.Rank > 0 {
				rankScore = -float64(member.Rank)
			}
			var rejection *string
			if member.RejectionReason != "" {
				v := member.RejectionReason
				rejection = &v
			}
			assetID, exchangeSymbolID := member.AssetID, member.ExchangeSymbolID
			members = append(members, database.UniverseMember{AssetID: &assetID, ExchangeSymbolID: &exchangeSymbolID, Symbol: strings.ToUpper(member.Symbol), Stage: member.Stage, ListingAgeDays: member.ListingAgeDays, MedianDailyQuoteVolume: member.MedianDailyQuoteVolume, MedianIntradayQuoteVolume: member.MedianIntradayQuoteVolume, RankComponentsJSON: member.RankComponentsJSON, RejectionReason: rejection, RankScore: rankScore, Shortlisted: member.Shortlisted, LastPrice: member.LastPrice, Change24h: member.Change24h, QuoteVolume24h: member.QuoteVolume24h, GapRatio: member.GapRatio, VolatilityRatio: member.VolatilityRatio, Return1D: member.Return1D, Return3D: member.Return3D, Return7D: member.Return7D, Return30D: member.Return30D, RelativeStrength: member.RelativeStrength, TrendQuality: member.TrendQuality, BreakoutProximity: member.BreakoutProximity, VolumeAcceleration: member.VolumeAcceleration, OverextensionPenalty: member.OverextensionPenalty})
		}
		observedComplete := value.ObservedComplete || len(value.Members) > 0
		result = append(result, replaySnapshotEntry{Timestamp: value.Timestamp.UTC(), RegimeState: value.RegimeState, BreadthRatio: value.BreadthRatio, Members: members, ObservedComplete: observedComplete})
	}
	return result
}

func canonicalReplaySnapshots(values []replaySnapshotEntry) []ReplaySnapshot {
	result := make([]ReplaySnapshot, 0, len(values))
	for _, value := range values {
		members := append([]database.UniverseMember(nil), value.Members...)
		sort.SliceStable(members, func(i, j int) bool {
			if members[i].RankScore != members[j].RankScore {
				return members[i].RankScore > members[j].RankScore
			}
			return members[i].Symbol < members[j].Symbol
		})
		public := make([]ReplayMember, 0, len(members))
		for i, m := range members {
			rejection := ""
			if m.RejectionReason != nil {
				rejection = *m.RejectionReason
			}
			assetID, exchangeSymbolID := "", ""
			if m.AssetID != nil {
				assetID = *m.AssetID
			}
			if m.ExchangeSymbolID != nil {
				exchangeSymbolID = *m.ExchangeSymbolID
			}
			public = append(public, ReplayMember{AssetID: assetID, ExchangeSymbolID: exchangeSymbolID, Symbol: m.Symbol, Rank: i + 1, Shortlisted: m.Shortlisted, Stage: m.Stage, ListingAgeDays: m.ListingAgeDays, MedianDailyQuoteVolume: m.MedianDailyQuoteVolume, MedianIntradayQuoteVolume: m.MedianIntradayQuoteVolume, RankComponentsJSON: m.RankComponentsJSON, RejectionReason: rejection, LastPrice: m.LastPrice, Change24h: m.Change24h, QuoteVolume24h: m.QuoteVolume24h, GapRatio: m.GapRatio, VolatilityRatio: m.VolatilityRatio, Return1D: m.Return1D, Return3D: m.Return3D, Return7D: m.Return7D, Return30D: m.Return30D, RelativeStrength: m.RelativeStrength, TrendQuality: m.TrendQuality, BreakoutProximity: m.BreakoutProximity, VolumeAcceleration: m.VolumeAcceleration, OverextensionPenalty: m.OverextensionPenalty, RankScore: m.RankScore})
		}
		result = append(result, ReplaySnapshot{Timestamp: value.Timestamp.UTC(), RegimeState: value.RegimeState, BreadthRatio: value.BreadthRatio, ObservedComplete: value.ObservedComplete, Members: public})
	}
	return result
}

func classifySharedRun(ledger *backtestMemoryLedger) RunClassification {
	if ledger.evidence.Fills > 0 {
		return RunSuccessfulExecution
	}
	if ledger.evidence.CandidateEvaluations == 0 || ledger.evidence.PreOrchestratorGates > 0 || ledger.evidence.RiskRejections > 0 || ledger.evidence.BrokerRejections > 0 {
		return RunGatingZeroTrades
	}
	for _, observation := range ledger.observations {
		if observation.Stage == "strategy" && isGatingCode(observation.Code) {
			return RunGatingZeroTrades
		}
	}
	return RunStrategyZeroTrades
}

func featuresAvailableAsOf(config BacktestConfig, at time.Time) bool {
	for _, name := range config.CoveragePolicy.RequiredModelFeatures {
		found := false
		for _, series := range config.FeatureSeries {
			if series.Name == name && len(series.AsOf(at)) > 0 {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func UnmarshalArtifact(data []byte, target any) error {
	var envelope struct {
		SchemaVersion string          `json:"schema_version"`
		Payload       json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	if envelope.SchemaVersion != ArtifactSchemaVersion {
		return fmt.Errorf("unsupported artifact schema %q", envelope.SchemaVersion)
	}
	return json.Unmarshal(envelope.Payload, target)
}

func isGatingCode(code string) bool {
	switch code {
	case "signal_not_qualified", "signal_below_confidence", "confidence_not_qualified", "neutral_signal":
		return false
	default:
		return code != ""
	}
}

func UnmarshalRunManifest(data []byte) (RunManifest, error) {
	var manifest RunManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return RunManifest{}, err
	}
	if manifest.SchemaVersion != ManifestSchemaVersion && manifest.SchemaVersion != LegacyManifestSchemaVersion {
		return RunManifest{}, fmt.Errorf("unsupported manifest schema %q", manifest.SchemaVersion)
	}
	if manifest.SchemaVersion == LegacyManifestSchemaVersion {
		version := manifest.StrategyVersion
		if version == "" {
			version = "unknown-legacy-version"
		}
		manifest.Strategy = SelectedStrategy{Descriptor: StrategyDescriptor{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyLegacyCompatibility, Version: version, Description: "Legacy v3 run manifest compatibility evidence", DecisionCadence: "legacy", RebalanceCadence: "legacy", Risk: StrategyRiskDeclaration{UsesSharedRisk: true}, Baseline: true, LegacyCompatibility: true}, Parameters: map[string]string{}}
	} else if manifest.Strategy.Descriptor.SchemaVersion != StrategyDescriptorSchemaVersion || manifest.Strategy.Descriptor.ID == "" || manifest.Strategy.Descriptor.Version == "" {
		return RunManifest{}, fmt.Errorf("manifest strategy descriptor is missing or unsupported")
	}
	if manifest.Artifacts.SchemaVersion != ArtifactSchemaVersion {
		return RunManifest{}, fmt.Errorf("unsupported artifact schema %q", manifest.Artifacts.SchemaVersion)
	}
	return manifest, nil
}

func buildBacktestArtifacts(ledger *backtestMemoryLedger, positions map[string]*positionState, states map[string]*symbolState, timeline []int64) BacktestArtifacts {
	artifacts := BacktestArtifacts{SchemaVersion: ArtifactSchemaVersion, Decisions: []DecisionArtifact{}, Orders: []OrderArtifact{}, Fills: []FillArtifact{}, Ledger: []LedgerArtifact{}, Exposure: []ExposureArtifact{}}
	for _, record := range ledger.runRecords {
		run := record.Result
		intents := map[string]tradingcore.OrderIntent{}
		for _, noAction := range run.Strategy.NoActions() {
			artifacts.Decisions = append(artifacts.Decisions, DecisionArtifact{SignalAt: canonicalTime(record.SignalAt), DecisionAt: canonicalTime(record.DecisionAt), Symbol: noAction.Instrument.VenueSymbol, Stage: "strategy", Code: noAction.Code, Reason: noAction.Reason})
		}
		for _, intent := range run.Strategy.Intents().Intents() {
			intents[intent.ID.String()] = intent
			artifacts.Decisions = append(artifacts.Decisions, decisionFromIntent(intent, "strategy", "intent_generated"))
			artifacts.Orders = append(artifacts.Orders, OrderArtifact{SchemaVersion: "order-artifact-v2", IntentID: intent.ID.String(), OrderID: intent.ID.String(), SignalAt: canonicalTime(intent.SignalAt), DecisionAt: canonicalTime(intent.DecisionAt), OrderAt: canonicalTime(intent.CreatedAt), Symbol: intent.Instrument.VenueSymbol, Side: string(intent.Side), Quantity: intent.Quantity.Decimal().String(), Reason: intent.Reason, Metadata: intent.Metadata(), ReasonMetadata: decodeReasonMetadata(intent.Metadata())})
		}
		for _, rejection := range run.Risk.Rejected() {
			intent := intents[rejection.OrderID.String()]
			artifacts.Decisions = append(artifacts.Decisions, decisionFromIntent(intent, "risk", string(rejection.Code)))
		}
		for _, rejection := range run.Broker.Rejected() {
			intent := intents[rejection.OrderID.String()]
			artifacts.Decisions = append(artifacts.Decisions, decisionFromIntent(intent, "broker", string(rejection.Code)))
		}
		for _, accepted := range run.Broker.Accepted() {
			intent := intents[accepted.OrderID.String()]
			artifacts.Decisions = append(artifacts.Decisions, decisionFromIntent(intent, "broker", string(accepted.Status)))
		}
	}
	for _, event := range ledger.events {
		artifacts.Fills = append(artifacts.Fills, FillArtifact{SchemaVersion: "fill-artifact-v2", IntentID: event.IntentID, OrderID: event.OrderID, FillID: event.FillID, SignalAt: canonicalTime(event.SignalAt), DecisionAt: canonicalTime(event.DecisionAt), OrderAt: canonicalTime(event.OrderAt), FillAt: canonicalTime(event.At), Symbol: event.Symbol, Side: event.Side, Quantity: event.Quantity, Price: event.Price, Fee: event.Fee, CostVersion: event.CostVersion, Reason: event.ReasonMetadata})
		artifacts.Ledger = append(artifacts.Ledger, LedgerArtifact{SchemaVersion: "ledger-artifact-v2", IntentID: event.IntentID, OrderID: event.OrderID, FillID: event.FillID, At: canonicalTime(event.At), Symbol: event.Symbol, Side: event.Side, Quantity: event.Quantity, Price: event.Price, Fee: event.Fee, CashAfter: event.CashAfter, Reason: event.ReasonMetadata})
	}
	at := time.Time{}
	if len(timeline) > 0 {
		at = time.UnixMilli(timeline[len(timeline)-1])
	}
	for _, state := range states {
		if state != nil && state.lastIndex >= 0 {
			closeAt := time.UnixMilli(state.series[state.lastIndex].CloseTime)
			if closeAt.After(at) {
				at = closeAt
			}
		}
	}
	symbols := make([]string, 0, len(positions))
	for symbol := range positions {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	for _, symbol := range symbols {
		position := positions[symbol]
		mark := position.EntryPrice
		if state := states[symbol]; state != nil && state.lastPrice > 0 {
			mark = state.lastPrice
		}
		artifacts.Exposure = append(artifacts.Exposure, ExposureArtifact{At: canonicalTime(at), Symbol: symbol, Quantity: decimalString(position.Size), MarkPrice: decimalString(mark), Value: decimalString(position.Size * mark), Status: "marked_unliquidated_no_executable_bar"})
	}
	return artifacts
}

func decisionFromIntent(intent tradingcore.OrderIntent, stage, code string) DecisionArtifact {
	return DecisionArtifact{SchemaVersion: "decision-artifact-v2", IntentID: intent.ID.String(), SignalAt: canonicalTime(intent.SignalAt), DecisionAt: canonicalTime(intent.DecisionAt), Symbol: intent.Instrument.VenueSymbol, Stage: stage, Code: code, Side: string(intent.Side), Quantity: intent.Quantity.Decimal().String(), Reason: intent.Reason, ReasonMetadata: decodeReasonMetadata(intent.Metadata()), PolicyVersion: intent.Versions.Policy}
}

func canonicalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
func canonicalMillis(value int64) string { return canonicalTime(time.UnixMilli(value)) }
func joinCoverageReasons(values []CoverageReason) string {
	parts := make([]string, len(values))
	for i := range values {
		parts[i] = string(values[i])
	}
	return strings.Join(parts, ",")
}
func uniqueCoverageReasons(values []CoverageReason) []CoverageReason {
	seen := map[CoverageReason]bool{}
	out := []CoverageReason{}
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
func containsCoverageDataset(values []CoverageDiagnostic, dataset string) bool {
	for _, v := range values {
		if v.Dataset == dataset {
			return true
		}
	}
	return false
}
