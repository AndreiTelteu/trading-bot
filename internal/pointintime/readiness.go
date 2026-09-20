package pointintime

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"trading-go/internal/database"

	"gorm.io/gorm"
)

// ResearchReadinessPolicy is an explicit, versioned minimum-evidence policy.
// It is deliberately separate from a strategy's parameters: relaxing it
// changes the admissibility of research evidence and therefore must create a
// new governed research context rather than being changed through settings.
type ResearchReadinessPolicy struct {
	Version                   string        `json:"version"`
	TrainMonths               int           `json:"train_months"`
	TestMonths                int           `json:"test_months"`
	MinFolds                  int           `json:"min_folds"`
	MinSymbols                int           `json:"min_symbols"`
	MinDecisionRowsPerSymbol  int           `json:"min_decision_rows_per_symbol"`
	MinExecutionRowsPerSymbol int           `json:"min_execution_rows_per_symbol"`
	MinRegimeSnapshots        int           `json:"min_regime_snapshots"`
	MinUniverseCandidates     int           `json:"min_universe_candidates"`
	MinUniverseMembers        int           `json:"min_universe_members"`
	MaxMetadataAge            time.Duration `json:"max_metadata_age"`
	MaxUniverseGap            time.Duration `json:"max_universe_gap"`
}

func DefaultResearchReadinessPolicy() ResearchReadinessPolicy {
	return ResearchReadinessPolicy{
		Version:                   "research-readiness-v1",
		TrainMonths:               12,
		TestMonths:                3,
		MinFolds:                  3,
		MinSymbols:                8,
		MinDecisionRowsPerSymbol:  1_000,
		MinExecutionRowsPerSymbol: 10_000,
		MinRegimeSnapshots:        3,
		MinUniverseCandidates:     8,
		MinUniverseMembers:        3,
		MaxMetadataAge:            7 * 24 * time.Hour,
		MaxUniverseGap:            48 * time.Hour,
	}
}

// ResearchReadinessPolicyFromSettings only reads the immutable research
// snapshot. The generic settings endpoint rejects this namespace.
func ResearchReadinessPolicyFromSettings(settings map[string]string) ResearchReadinessPolicy {
	p := DefaultResearchReadinessPolicy()
	readInt := func(key string, dst *int) {
		if value, err := strconv.Atoi(strings.TrimSpace(settings[key])); err == nil && value > 0 {
			*dst = value
		}
	}
	readInt("research_readiness_train_months", &p.TrainMonths)
	readInt("research_readiness_test_months", &p.TestMonths)
	readInt("research_readiness_min_folds", &p.MinFolds)
	readInt("research_readiness_min_symbols", &p.MinSymbols)
	readInt("research_readiness_min_decision_rows_per_symbol", &p.MinDecisionRowsPerSymbol)
	readInt("research_readiness_min_execution_rows_per_symbol", &p.MinExecutionRowsPerSymbol)
	readInt("research_readiness_min_regime_snapshots", &p.MinRegimeSnapshots)
	readInt("research_readiness_min_universe_candidates", &p.MinUniverseCandidates)
	readInt("research_readiness_min_universe_members", &p.MinUniverseMembers)
	if value, err := time.ParseDuration(strings.TrimSpace(settings["research_readiness_max_metadata_age"])); err == nil && value > 0 {
		p.MaxMetadataAge = value
	}
	if value, err := time.ParseDuration(strings.TrimSpace(settings["research_readiness_max_universe_gap"])); err == nil && value > 0 {
		p.MaxUniverseGap = value
	}
	return p
}

type ResearchReadinessRequest struct {
	ManifestID         string                  `json:"manifest_id"`
	Start              time.Time               `json:"start"`
	End                time.Time               `json:"end"`
	Symbols            []string                `json:"symbols"`
	Benchmark          string                  `json:"benchmark"`
	DecisionTimeframe  string                  `json:"decision_timeframe"`
	ExecutionTimeframe string                  `json:"execution_timeframe"`
	Policy             ResearchReadinessPolicy `json:"policy"`
}

type ReadinessFailure struct {
	Code    string `json:"code"`
	Subject string `json:"subject,omitempty"`
	Details string `json:"details"`
}

type ResearchReadinessReport struct {
	SchemaVersion     string                  `json:"schema_version"`
	ManifestID        string                  `json:"manifest_id"`
	Policy            ResearchReadinessPolicy `json:"policy"`
	CalendarMonths    int                     `json:"calendar_months"`
	FoldCount         int                     `json:"fold_count"`
	DecisionRows      map[string]int          `json:"decision_rows_per_symbol"`
	ExecutionRows     map[string]int          `json:"execution_rows_per_symbol"`
	RegimeSnapshots   map[string]int          `json:"regime_snapshots"`
	UniverseSnapshots int                     `json:"universe_snapshots"`
	Passed            bool                    `json:"passed"`
	Failures          []ReadinessFailure      `json:"failures,omitempty"`
}

type ResearchReadinessError struct{ Report ResearchReadinessReport }

func (e *ResearchReadinessError) Error() string {
	if len(e.Report.Failures) == 0 {
		return "research data readiness failed"
	}
	return fmt.Sprintf("research data readiness failed: %s", e.Report.Failures[0].Code)
}

func IsResearchReadinessError(err error) bool {
	_, ok := err.(*ResearchReadinessError)
	return ok
}

// PreflightResearchReadiness is read-only. It never fills a missing bar,
// synthesizes a universe observation, or substitutes current exchange state.
func PreflightResearchReadiness(db *gorm.DB, request ResearchReadinessRequest) (ResearchReadinessReport, error) {
	if db == nil || request.ManifestID == "" || request.Start.IsZero() || !request.End.After(request.Start) {
		return ResearchReadinessReport{}, fmt.Errorf("invalid research readiness request")
	}
	manifest, err := LoadManifest(db, request.ManifestID)
	if err != nil {
		return ResearchReadinessReport{}, err
	}
	var snapshots []database.UniverseSnapshot
	if err := db.Where("dataset_manifest_id=? AND coverage_state='complete' AND snapshot_time>=? AND snapshot_time<?", request.ManifestID, request.Start.UTC(), request.End.UTC()).Order("snapshot_time ASC").Find(&snapshots).Error; err != nil {
		return ResearchReadinessReport{}, err
	}
	report := assessResearchReadiness(manifest, snapshots, request)
	if !report.Passed {
		return report, &ResearchReadinessError{Report: report}
	}
	return report, nil
}

// assessResearchReadiness is kept data-only to make the failure policy
// regression-testable without a database and to keep the CLI strictly
// observational.
func assessResearchReadiness(manifest Manifest, snapshots []database.UniverseSnapshot, request ResearchReadinessRequest) ResearchReadinessReport {
	p := request.Policy
	if p.Version == "" {
		p = DefaultResearchReadinessPolicy()
	}
	if request.DecisionTimeframe == "" {
		request.DecisionTimeframe = "15m"
	}
	if request.ExecutionTimeframe == "" {
		request.ExecutionTimeframe = "1m"
	}
	report := ResearchReadinessReport{SchemaVersion: "research-readiness-report-v1", ManifestID: manifest.ID, Policy: p, DecisionRows: map[string]int{}, ExecutionRows: map[string]int{}, RegimeSnapshots: map[string]int{}, UniverseSnapshots: len(snapshots), Passed: true}
	add := func(code, subject, details string) {
		report.Passed = false
		report.Failures = append(report.Failures, ReadinessFailure{Code: code, Subject: subject, Details: details})
	}
	if request.Benchmark == "" {
		add("benchmark_missing", "", "an independent benchmark ticker is required")
	}
	request.Benchmark = strings.ToUpper(request.Benchmark)
	symbols := uniqueUpper(request.Symbols)
	if len(symbols) < p.MinSymbols {
		add("symbol_count_insufficient", "", fmt.Sprintf("symbols=%d minimum=%d", len(symbols), p.MinSymbols))
	}
	report.CalendarMonths = fullCalendarMonths(request.Start, request.End)
	if report.CalendarMonths < p.TrainMonths+p.TestMonths*p.MinFolds {
		add("calendar_span_insufficient", "", fmt.Sprintf("months=%d minimum=%d for %d train=%d test=%d folds", report.CalendarMonths, p.TrainMonths+p.TestMonths*p.MinFolds, p.MinFolds, p.TrainMonths, p.TestMonths))
	}
	report.FoldCount = readinessFoldCount(request.Start, request.End, p.TrainMonths, p.TestMonths)
	if report.FoldCount < p.MinFolds {
		add("fold_count_insufficient", "", fmt.Sprintf("folds=%d minimum=%d", report.FoldCount, p.MinFolds))
	}

	latestMetadata := mustParseTime(manifest.KnowledgeCutoff)
	if latestMetadata.IsZero() {
		add("manifest_knowledge_cutoff_invalid", "", "manifest lacks a valid immutable knowledge cutoff")
	}
	for _, covered := range manifest.Series {
		ticker := strings.ToUpper(covered.Ticker)
		if !containsUpper(symbols, ticker) && ticker != request.Benchmark {
			continue
		}
		if (covered.Role == RoleDecision || (ticker == request.Benchmark && covered.Role == RoleBenchmark)) && covered.Timeframe == request.DecisionTimeframe {
			report.DecisionRows[ticker] += covered.Rows
		}
		if covered.Role == RoleExecution && covered.Timeframe == request.ExecutionTimeframe {
			report.ExecutionRows[ticker] += covered.Rows
		}
		if !covered.Complete {
			add("series_incomplete", seriesID(covered.SeriesKey), strings.Join(covered.QualityFlags, ","))
		}
		if covered.Role == RoleDecision && containsUpper(symbols, ticker) && !covered.ConstraintsComplete {
			add("constraints_incomplete", seriesID(covered.SeriesKey), "historical executable constraints are incomplete")
		}
		metadataAt := mustParseTime(covered.SymbolRetrievedAt)
		assetAt := mustParseTime(covered.AssetRetrievedAt)
		if metadataAt.IsZero() || assetAt.IsZero() {
			add("metadata_retrieval_missing", seriesID(covered.SeriesKey), "rebuild the immutable manifest with metadata retrieval timestamps")
		} else if metadataAt.After(latestMetadata) || assetAt.After(latestMetadata) {
			add("metadata_after_cutoff", seriesID(covered.SeriesKey), "metadata retrieval is after the immutable manifest cutoff")
		} else if latestMetadata.Sub(metadataAt) > p.MaxMetadataAge || latestMetadata.Sub(assetAt) > p.MaxMetadataAge {
			add("metadata_stale", seriesID(covered.SeriesKey), fmt.Sprintf("metadata age exceeds %s at manifest cutoff", p.MaxMetadataAge))
		}
	}
	for _, symbol := range symbols {
		if report.DecisionRows[symbol] < p.MinDecisionRowsPerSymbol {
			add("decision_sample_insufficient", symbol, fmt.Sprintf("rows=%d minimum=%d", report.DecisionRows[symbol], p.MinDecisionRowsPerSymbol))
		}
		if report.ExecutionRows[symbol] < p.MinExecutionRowsPerSymbol {
			add("execution_sample_insufficient", symbol, fmt.Sprintf("rows=%d minimum=%d", report.ExecutionRows[symbol], p.MinExecutionRowsPerSymbol))
		}
	}
	if report.DecisionRows[request.Benchmark] == 0 {
		add("benchmark_coverage_missing", request.Benchmark, "benchmark decision series is absent")
	}
	if report.ExecutionRows[request.Benchmark] == 0 {
		add("benchmark_execution_coverage_missing", request.Benchmark, "benchmark execution:1m series is absent")
	}

	previous := time.Time{}
	for _, snapshot := range snapshots {
		if previous.IsZero() && snapshot.SnapshotTime.Sub(request.Start.UTC()) > p.MaxUniverseGap {
			add("universe_snapshot_initial_gap", snapshot.SnapshotTime.UTC().Format(time.RFC3339), fmt.Sprintf("initial gap=%s maximum=%s", snapshot.SnapshotTime.Sub(request.Start.UTC()), p.MaxUniverseGap))
		}
		if !previous.IsZero() && snapshot.SnapshotTime.Sub(previous) > p.MaxUniverseGap {
			add("universe_snapshot_gap", snapshot.SnapshotTime.UTC().Format(time.RFC3339), fmt.Sprintf("gap=%s maximum=%s", snapshot.SnapshotTime.Sub(previous), p.MaxUniverseGap))
		}
		previous = snapshot.SnapshotTime
		regime := strings.TrimSpace(snapshot.RegimeState)
		if regime == "" {
			add("regime_missing", snapshot.SnapshotTime.UTC().Format(time.RFC3339), "complete universe snapshot has no regime")
		} else {
			report.RegimeSnapshots[regime]++
		}
		if snapshot.CandidateCount < p.MinUniverseCandidates {
			add("universe_capacity_insufficient", snapshot.SnapshotTime.UTC().Format(time.RFC3339), fmt.Sprintf("candidates=%d minimum=%d", snapshot.CandidateCount, p.MinUniverseCandidates))
		}
		// ShortlistCount is a strategy/regime output, not input capacity. The
		// canonical universe policy intentionally contracts a risk-off shortlist
		// to two names, so requiring three shortlisted names would make every
		// otherwise healthy risk-off observation permanently inadmissible. Use
		// the pre-contraction ranked population to prove that enough eligible
		// point-in-time members were available to the strategy.
		if snapshot.RankedCount < p.MinUniverseMembers {
			add("universe_members_insufficient", snapshot.SnapshotTime.UTC().Format(time.RFC3339), fmt.Sprintf("eligible=%d minimum=%d", snapshot.RankedCount, p.MinUniverseMembers))
		}
	}
	if len(snapshots) == 0 {
		add("universe_snapshot_missing", "", "no complete point-in-time universe snapshots cover the interval")
	} else if request.End.UTC().Sub(previous) > p.MaxUniverseGap {
		add("universe_snapshot_terminal_gap", previous.UTC().Format(time.RFC3339), fmt.Sprintf("terminal gap=%s maximum=%s", request.End.UTC().Sub(previous), p.MaxUniverseGap))
	}
	if len(report.RegimeSnapshots) < 2 {
		add("regime_diversity_insufficient", "", fmt.Sprintf("regimes=%d minimum=2", len(report.RegimeSnapshots)))
	}
	for regime, samples := range report.RegimeSnapshots {
		if samples < p.MinRegimeSnapshots {
			add("regime_sample_insufficient", regime, fmt.Sprintf("snapshots=%d minimum=%d", samples, p.MinRegimeSnapshots))
		}
	}
	sort.Slice(report.Failures, func(i, j int) bool {
		if report.Failures[i].Code != report.Failures[j].Code {
			return report.Failures[i].Code < report.Failures[j].Code
		}
		return report.Failures[i].Subject < report.Failures[j].Subject
	})
	return report
}

func fullCalendarMonths(start, end time.Time) int {
	n := 0
	for cursor := start.UTC(); !addCalendarMonths(cursor, n+1).After(end.UTC()); n++ {
	}
	return n
}
func readinessFoldCount(start, end time.Time, train, test int) int {
	if train <= 0 || test <= 0 {
		return 0
	}
	count := 0
	for cursor := start.UTC(); !addCalendarMonths(cursor, train+test*(count+1)).After(end.UTC()); count++ {
	}
	return count
}
func addCalendarMonths(at time.Time, months int) time.Time { return at.AddDate(0, months, 0) }
func uniqueUpper(values []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, value := range values {
		value = strings.ToUpper(strings.TrimSpace(value))
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}
func containsUpper(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
func mustParseTime(value string) time.Time { at, _ := time.Parse(time.RFC3339Nano, value); return at }
