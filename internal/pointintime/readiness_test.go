package pointintime

import (
	"testing"
	"time"

	"trading-go/internal/database"
)

func TestAssessResearchReadinessRequiresExecutionUniverseAndIndependentFolds(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 21, 0)
	policy := DefaultResearchReadinessPolicy()
	policy.MinSymbols = 2
	policy.MinDecisionRowsPerSymbol = 10
	policy.MinExecutionRowsPerSymbol = 10
	policy.MinUniverseCandidates = 2
	policy.MinUniverseMembers = 1
	policy.MinRegimeSnapshots = 2
	policy.MaxUniverseGap = 2 * 365 * 24 * time.Hour
	coverage := func(ticker, role, frame string, rows int) SeriesCoverage {
		return SeriesCoverage{SeriesKey: SeriesKey{ExchangeSymbolID: ticker + "-id-" + role + frame, AssetID: ticker + "-asset", Ticker: ticker, Role: role, Timeframe: frame}, Rows: rows, Complete: true, ConstraintsComplete: true, SymbolRetrievedAt: end.Format(time.RFC3339), AssetRetrievedAt: end.Format(time.RFC3339)}
	}
	manifest := Manifest{ID: "manifest", KnowledgeCutoff: end.Format(time.RFC3339), Series: []SeriesCoverage{
		coverage("AAAUSDT", RoleDecision, "15m", 100), coverage("AAAUSDT", RoleExecution, "1m", 100),
		coverage("BBBUSDT", RoleDecision, "15m", 100), coverage("BBBUSDT", RoleExecution, "1m", 100),
		coverage("BTCUSDT", RoleBenchmark, "15m", 100), coverage("BTCUSDT", RoleExecution, "1m", 100),
	}}
	snapshots := []database.UniverseSnapshot{}
	for i := 0; i < 4; i++ {
		regime := "risk_on"
		if i%2 == 1 {
			regime = "neutral"
		}
		snapshots = append(snapshots, database.UniverseSnapshot{SnapshotTime: start.AddDate(0, i*3, 0), RegimeState: regime, CandidateCount: 2, ShortlistCount: 1})
	}
	request := ResearchReadinessRequest{ManifestID: manifest.ID, Start: start, End: end, Symbols: []string{"AAAUSDT", "BBBUSDT"}, Benchmark: "BTCUSDT", DecisionTimeframe: "15m", ExecutionTimeframe: "1m", Policy: policy}
	report := assessResearchReadiness(manifest, snapshots, request)
	if !report.Passed || report.FoldCount != 3 {
		t.Fatalf("ready report=%+v", report)
	}

	manifest.Series[1].Rows = 0
	report = assessResearchReadiness(manifest, snapshots, request)
	if report.Passed || !hasReadinessFailure(report, "execution_sample_insufficient") {
		t.Fatalf("missing execution series passed: %+v", report)
	}

	request.End = start.AddDate(0, 15, 0)
	report = assessResearchReadiness(manifest, snapshots, request)
	if !hasReadinessFailure(report, "fold_count_insufficient") || !hasReadinessFailure(report, "calendar_span_insufficient") {
		t.Fatalf("short interval did not fail explicitly: %+v", report.Failures)
	}
}

func TestResearchReadinessPolicySettingsCannotReduceDefaultsImplicitly(t *testing.T) {
	policy := ResearchReadinessPolicyFromSettings(map[string]string{"research_readiness_min_folds": "invalid", "research_readiness_max_metadata_age": "0s"})
	if policy.MinFolds != 3 || policy.MaxMetadataAge != 7*24*time.Hour {
		t.Fatalf("invalid settings weakened policy: %+v", policy)
	}
}

func hasReadinessFailure(report ResearchReadinessReport, code string) bool {
	for _, failure := range report.Failures {
		if failure.Code == code {
			return true
		}
	}
	return false
}
