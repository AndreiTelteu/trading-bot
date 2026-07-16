package backtest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/services"
)

const (
	CoverageSchemaVersion = "backtest-coverage-v1"
	ManifestSchemaVersion = "backtest-run-manifest-v1"
	ArtifactSchemaVersion = "backtest-artifacts-v1"
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
		report = validateBarCoverage(report, "decision", symbol, decision[symbol], config.CoveragePolicy.DecisionInterval, config.Start, config.End, config.CoveragePolicy)
		if _, configured := config.ExecutionSeries[symbol]; configured || len(config.ExecutionSeries) > 0 || config.ExecutionSeriesRequired {
			report = validateBarCoverage(report, "execution", symbol, config.ExecutionSeries[symbol], config.CoveragePolicy.ExecutionInterval, config.Start, config.End, config.CoveragePolicy)
		} else {
			report = validateBarCoverage(report, "execution", symbol, decision[symbol], config.CoveragePolicy.DecisionInterval, config.Start, config.End, config.CoveragePolicy)
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
		inRange := make([]replaySnapshotEntry, 0, len(replay))
		for _, snapshot := range replay {
			if !config.Start.IsZero() && snapshot.Timestamp.Before(config.Start) || !config.End.IsZero() && snapshot.Timestamp.After(config.End) {
				continue
			}
			inRange = append(inRange, snapshot)
		}
		if len(inRange) == 0 {
			report.add(CoverageDiagnostic{Dataset: "universe", Status: "failed", Reason: CoverageReplayEmpty})
		} else {
			minimum := config.CoveragePolicy.RequiredReplayMembers
			if minimum <= 0 {
				minimum = 1
			}
			for _, snapshot := range inRange {
				if len(snapshot.Members) < minimum {
					report.add(CoverageDiagnostic{Dataset: "universe", Status: "failed", Reason: CoverageReplayMembersEmpty, Count: len(snapshot.Members), First: canonicalTime(snapshot.Timestamp)})
				}
			}
			if report.Passed || !containsCoverageDataset(report.Diagnostics, "universe") {
				report.Diagnostics = append(report.Diagnostics, CoverageDiagnostic{Dataset: "universe", Status: "ok", Count: len(inRange), First: canonicalTime(inRange[0].Timestamp), Last: canonicalTime(inRange[len(inRange)-1].Timestamp)})
			}
		}
	}
	for _, feature := range config.CoveragePolicy.RequiredModelFeatures {
		values := config.RequiredModelFeatures[feature]
		if len(values) == 0 {
			report.add(CoverageDiagnostic{Dataset: "feature", Symbol: feature, Status: "failed", Reason: CoverageFeatureMissing})
		} else {
			report.Diagnostics = append(report.Diagnostics, CoverageDiagnostic{Dataset: "feature", Symbol: feature, Status: "ok", Count: len(values)})
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
	if d.Status == "ok" && policy.RequireRequestedBounds && (!start.IsZero() && time.UnixMilli(bars[0].OpenTime).After(start) || !end.IsZero() && time.UnixMilli(bars[len(bars)-1].CloseTime).Before(end)) {
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
	idx := sort.Search(len(state.series), func(i int) bool { return state.series[i].OpenTime >= informationAt.UnixMilli() })
	if idx < len(state.series) {
		at := time.UnixMilli(state.series[idx].OpenTime)
		if !at.After(informationAt) {
			at = informationAt.Add(3 * time.Nanosecond)
		}
		return state.series[idx], at, true
	}
	return services.OHLCV{}, time.Time{}, false
}

func isLastLiquidationOpportunity(config BacktestConfig, state *symbolState, symbol string, bar services.OHLCV) bool {
	informationAt := time.UnixMilli(bar.CloseTime)
	_, fillAt, ok := nextExecutable(config, state, symbol, informationAt)
	if !ok || !config.End.IsZero() && fillAt.After(config.End) {
		return false
	}
	if bars := config.ExecutionSeries[symbol]; len(bars) > 0 {
		nextDecision := state.currentIndex + 1
		if nextDecision >= len(state.series) {
			return true
		}
		_, laterFill, later := nextExecutable(config, state, symbol, time.UnixMilli(state.series[nextDecision].CloseTime))
		return !later || !config.End.IsZero() && laterFill.After(config.End)
	}
	return state.currentIndex == len(state.series)-2
}

func datasetManifestHash(config BacktestConfig, decision map[string][]services.OHLCV, replay []replaySnapshotEntry) string {
	type dataset struct {
		Kind, Symbol string
		Bars         []services.OHLCV
		Snapshots    []ReplaySnapshot
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
		snapshots := make([]ReplaySnapshot, 0, len(replay))
		for _, entry := range replay {
			members := make([]string, 0, len(entry.Members))
			for _, member := range entry.Members {
				members = append(members, member.Symbol)
			}
			sort.Strings(members)
			snapshots = append(snapshots, ReplaySnapshot{Timestamp: entry.Timestamp.UTC(), Members: members})
		}
		items = append(items, dataset{Kind: "universe", Snapshots: snapshots})
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
	limitations := []string{}
	if len(config.ExecutionPolicy.Constraints) == 0 {
		limitations = append(limitations, "symbol_constraints_unavailable")
	}
	limitations = append(limitations, "ohlcv_full_fill_no_order_book_model")
	return RunManifest{SchemaVersion: ManifestSchemaVersion, Classification: classification, CodeRevision: config.CodeRevision, ConfigVersion: config.ConfigVersion, StrategyVersion: config.StrategyVersion, PolicyVersion: backtestPolicyVersion(config), CostVersion: config.ExecutionPolicy.CostVersion, DatasetManifestHash: hash, UniverseMode: config.UniverseMode, BenchmarkSymbol: config.BenchmarkSymbol, Seed: config.Seed, FeeBPS: config.FeeBps, SlippageBPS: config.SlippageBps, CoveragePolicy: config.CoveragePolicy, ExecutionPolicy: config.ExecutionPolicy, Start: canonicalTime(config.Start), End: canonicalTime(config.End), Coverage: coverage, Limitations: limitations, Artifacts: ArtifactRefs{SchemaVersion: ArtifactSchemaVersion, Manifest: "manifest.json", Decisions: "decisions.json", Orders: "orders.json", Fills: "fills.json", Trades: "trades.json", Ledger: "ledger.json", Equity: "equity.json", Metrics: "metrics.json"}}
}

func MarshalArtifactBytes(result BacktestResult) (ArtifactBytes, error) {
	if result.Manifest.SchemaVersion != ManifestSchemaVersion {
		return ArtifactBytes{}, fmt.Errorf("unsupported manifest schema %q", result.Manifest.SchemaVersion)
	}
	if result.Artifacts.SchemaVersion != ArtifactSchemaVersion {
		return ArtifactBytes{}, fmt.Errorf("unsupported artifact schema %q", result.Artifacts.SchemaVersion)
	}
	encode := func(value any) ([]byte, error) { return json.Marshal(value) }
	var output ArtifactBytes
	var err error
	if output.Manifest, err = encode(result.Manifest); err != nil {
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
		for _, symbol := range value.Members {
			members = append(members, database.UniverseMember{Symbol: strings.ToUpper(symbol)})
		}
		result = append(result, replaySnapshotEntry{Timestamp: value.Timestamp.UTC(), Members: members})
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Timestamp.Before(result[j].Timestamp) })
	return result
}

func classifySharedRun(ledger *backtestMemoryLedger, shortlistSize int) RunClassification {
	if len(ledger.events) > 0 {
		return RunSuccessfulExecution
	}
	if shortlistSize == 0 {
		return RunGatingZeroTrades
	}
	for _, observation := range ledger.observations {
		if observation.Stage == "risk" || observation.Stage == "broker" || observation.Stage == "strategy" && isGatingCode(observation.Code) {
			return RunGatingZeroTrades
		}
	}
	return RunStrategyZeroTrades
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
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return RunManifest{}, fmt.Errorf("unsupported manifest schema %q", manifest.SchemaVersion)
	}
	if manifest.Artifacts.SchemaVersion != ArtifactSchemaVersion {
		return RunManifest{}, fmt.Errorf("unsupported artifact schema %q", manifest.Artifacts.SchemaVersion)
	}
	return manifest, nil
}

func buildBacktestArtifacts(ledger *backtestMemoryLedger, positions map[string]*positionState, states map[string]*symbolState, timeline []int64) BacktestArtifacts {
	artifacts := BacktestArtifacts{SchemaVersion: ArtifactSchemaVersion, Decisions: []DecisionArtifact{}, Orders: []OrderArtifact{}, Fills: []FillArtifact{}, Ledger: []LedgerArtifact{}, Exposure: []ExposureArtifact{}}
	for _, event := range ledger.events {
		artifacts.Decisions = append(artifacts.Decisions, DecisionArtifact{SignalAt: canonicalTime(event.SignalAt), DecisionAt: canonicalTime(event.DecisionAt), Symbol: event.Symbol, Code: event.Side})
		artifacts.Orders = append(artifacts.Orders, OrderArtifact{SignalAt: canonicalTime(event.SignalAt), DecisionAt: canonicalTime(event.DecisionAt), OrderAt: canonicalTime(event.OrderAt), Symbol: event.Symbol, Side: event.Side, Quantity: event.Quantity, Reason: event.Reason})
		artifacts.Fills = append(artifacts.Fills, FillArtifact{SignalAt: canonicalTime(event.SignalAt), DecisionAt: canonicalTime(event.DecisionAt), OrderAt: canonicalTime(event.OrderAt), FillAt: canonicalTime(event.At), Symbol: event.Symbol, Side: event.Side, Quantity: event.Quantity, Price: event.Price, Fee: event.Fee, CostVersion: event.CostVersion})
		artifacts.Ledger = append(artifacts.Ledger, LedgerArtifact{At: canonicalTime(event.At), Symbol: event.Symbol, Side: event.Side, Quantity: event.Quantity, Price: event.Price, Fee: event.Fee, CashAfter: event.CashAfter})
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
