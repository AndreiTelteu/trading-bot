package backtest

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/services"
)

type BacktestJobStrategySummary struct {
	Classification RunClassification   `json:"classification,omitempty"`
	Coverage       CoverageReport      `json:"coverage,omitempty"`
	Manifest       RunManifest         `json:"manifest,omitempty"`
	Mode           StrategyMode        `json:"mode"`
	Metrics        Metrics             `json:"metrics"`
	RankingMetrics *RankingMetrics     `json:"ranking_metrics,omitempty"`
	Diagnostics    StrategyDiagnostics `json:"diagnostics"`
}

type BacktestJobValidationSummary struct {
	Passed           bool                      `json:"passed"`
	Windows          int                       `json:"windows"`
	AcceptedMetrics  []string                  `json:"accepted_metrics,omitempty"`
	RecommendedStage string                    `json:"recommended_stage,omitempty"`
	FailedWindows    []ValidationWindowFailure `json:"failed_windows,omitempty"`
}

type BacktestJobSummary struct {
	FailedLane        string                       `json:"failed_lane,omitempty"`
	Symbols           []string                     `json:"symbols,omitempty"`
	BacktestMode      BacktestMode                 `json:"backtest_mode,omitempty"`
	ModelVersion      string                       `json:"model_version,omitempty"`
	PolicyVersion     string                       `json:"policy_version,omitempty"`
	DatasetManifestID string                       `json:"dataset_manifest_id,omitempty"`
	RolloutState      string                       `json:"rollout_state,omitempty"`
	ExperimentID      string                       `json:"experiment_id,omitempty"`
	UniverseMode      UniverseMode                 `json:"universe_mode,omitempty"`
	PolicyContext     services.GovernanceContext   `json:"policy_context"`
	Baseline          BacktestJobStrategySummary   `json:"baseline"`
	VolSizing         BacktestJobStrategySummary   `json:"vol_sizing"`
	Validation        BacktestJobValidationSummary `json:"validation"`
	// PhaseTimers is operator wall-clock telemetry only.
	PhaseTimers PhaseTimers `json:"phase_timers,omitempty"`
}

type BacktestJobResponse struct {
	ID                uint                `json:"id"`
	Status            string              `json:"status"`
	Progress          float64             `json:"progress"`
	Message           *string             `json:"message,omitempty"`
	Summary           *BacktestJobSummary `json:"summary,omitempty"`
	Comparison        *ComparisonArtifact `json:"comparison,omitempty"`
	Error             *string             `json:"error,omitempty"`
	StartedAt         *time.Time          `json:"started_at,omitempty"`
	FinishedAt        *time.Time          `json:"finished_at,omitempty"`
	CreatedAt         time.Time           `json:"created_at"`
	UpdatedAt         time.Time           `json:"updated_at"`
	DatasetManifestID *string             `json:"dataset_manifest_id,omitempty"`
	JobType           string              `json:"job_type"`
	ArtifactDigest    *string             `json:"artifact_digest,omitempty"`
	Diagnostic        json.RawMessage     `json:"diagnostic,omitempty"`
}

func ListBacktestJobResponses() ([]BacktestJobResponse, error) {
	responses, _, err := ListBacktestJobResponsePage(time.Time{}, 0, 200)
	return responses, err
}

func ListBacktestJobResponsePage(cursorAt time.Time, cursorID uint, limit int) ([]BacktestJobResponse, *database.BacktestJob, error) {
	if limit < 1 || limit > 1000 {
		return nil, nil, fmt.Errorf("backtest page limit out of range")
	}
	query := database.DB.Select(backtestJobResponseColumns()).Order("created_at DESC,id DESC")
	if !cursorAt.IsZero() {
		query = query.Where("created_at < ? OR (created_at = ? AND id < ?)", cursorAt.UTC(), cursorAt.UTC(), cursorID)
	}
	var jobs []database.BacktestJob
	if err := query.Limit(limit + 1).Find(&jobs).Error; err != nil {
		return nil, nil, err
	}
	var next *database.BacktestJob
	if len(jobs) > limit {
		jobs = jobs[:limit]
		copy := jobs[len(jobs)-1]
		next = &copy
	}

	responses := make([]BacktestJobResponse, 0, len(jobs))
	for i := range jobs {
		response, err := BuildBacktestJobResponse(&jobs[i])
		if err != nil {
			return nil, nil, err
		}
		if response != nil {
			responses = append(responses, *response)
		}
	}

	return responses, next, nil
}

func GetBacktestJobResponse(id uint) (*BacktestJobResponse, error) {
	var job database.BacktestJob
	if err := database.DB.Select(backtestJobResponseColumns()).First(&job, id).Error; err != nil {
		return nil, err
	}
	return BuildBacktestJobResponse(&job)
}

func GetLatestBacktestJobResponse() (*BacktestJobResponse, error) {
	var job database.BacktestJob
	if err := database.DB.Select(backtestJobResponseColumns()).Order("created_at DESC,id DESC").First(&job).Error; err != nil {
		return nil, err
	}
	return BuildBacktestJobResponse(&job)
}

func BuildBacktestJobResponse(job *database.BacktestJob) (*BacktestJobResponse, error) {
	if job == nil {
		return nil, nil
	}

	response := &BacktestJobResponse{
		ID:                job.ID,
		Status:            job.Status,
		Progress:          job.Progress,
		Message:           job.Message,
		Error:             job.Error,
		StartedAt:         job.StartedAt,
		FinishedAt:        job.FinishedAt,
		CreatedAt:         job.CreatedAt,
		UpdatedAt:         job.UpdatedAt,
		DatasetManifestID: job.DatasetManifestID,
		JobType:           job.JobType,
		ArtifactDigest:    job.ArtifactDigest,
	}
	if job.DiagnosticJSON != nil && len(*job.DiagnosticJSON) <= 64<<10 && json.Valid([]byte(*job.DiagnosticJSON)) {
		response.Diagnostic = json.RawMessage(*job.DiagnosticJSON)
	}

	compactJSON, err := compactSummaryJSON(job)
	if err != nil {
		return nil, err
	}
	if compactJSON != "" {
		var header struct {
			SchemaVersion string `json:"schema_version"`
		}
		if err := json.Unmarshal([]byte(compactJSON), &header); err != nil {
			return nil, err
		}
		if header.SchemaVersion == ComparisonSchemaVersion {
			comparison, err := UnmarshalComparisonArtifact([]byte(compactJSON))
			if err != nil {
				return nil, err
			}
			response.Comparison = &comparison
		} else {
			var summary BacktestJobSummary
			if err := json.Unmarshal([]byte(compactJSON), &summary); err != nil {
				return nil, err
			}
			response.Summary = &summary
		}
	}

	return response, nil
}

func BuildBacktestJobSummary(summary BacktestRunSummary) BacktestJobSummary {
	symbolSet := make(map[string]struct{})
	for symbol := range summary.Baseline.EquityBySymbol {
		symbolSet[symbol] = struct{}{}
	}
	for symbol := range summary.VolSizing.EquityBySymbol {
		symbolSet[symbol] = struct{}{}
	}
	if len(symbolSet) == 0 {
		for _, symbol := range summary.CandidateSymbols {
			symbolSet[symbol] = struct{}{}
		}
	}
	if len(symbolSet) == 0 {
		for _, symbol := range parseSymbols(summary.SettingsSnapshot["backtest_symbols"]) {
			symbolSet[symbol] = struct{}{}
		}
	}

	symbols := make([]string, 0, len(symbolSet))
	for symbol := range symbolSet {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)

	return BacktestJobSummary{
		FailedLane:        summary.FailedLane,
		Symbols:           symbols,
		BacktestMode:      summary.BacktestMode,
		ModelVersion:      summary.ModelVersion,
		PolicyVersion:     summary.PolicyVersion,
		DatasetManifestID: summary.DatasetManifestID,
		RolloutState:      summary.PolicyContext.RolloutState,
		ExperimentID:      summary.ExperimentID,
		UniverseMode:      summary.UniverseMode,
		PolicyContext:     summary.PolicyContext,
		Baseline: BacktestJobStrategySummary{
			Classification: summary.Baseline.Classification,
			Coverage:       summary.Baseline.Coverage,
			Manifest:       summary.Baseline.Manifest,
			Mode:           summary.Baseline.Mode,
			Metrics:        summary.Baseline.Metrics,
			RankingMetrics: summary.Baseline.RankingMetrics,
			Diagnostics:    summary.Baseline.Diagnostics,
		},
		VolSizing: BacktestJobStrategySummary{
			Classification: summary.VolSizing.Classification,
			Coverage:       summary.VolSizing.Coverage,
			Manifest:       summary.VolSizing.Manifest,
			Mode:           summary.VolSizing.Mode,
			Metrics:        summary.VolSizing.Metrics,
			RankingMetrics: summary.VolSizing.RankingMetrics,
			Diagnostics:    summary.VolSizing.Diagnostics,
		},
		Validation: BacktestJobValidationSummary{
			Passed:           summary.Validation.Passed,
			Windows:          summary.Validation.Windows,
			AcceptedMetrics:  summary.Validation.AcceptedMetrics,
			RecommendedStage: summary.Validation.PromotionReadiness.RecommendedStage,
			FailedWindows:    summary.Validation.FailedWindows,
		},
		PhaseTimers: summary.PhaseTimers,
	}
}

func MarshalBacktestJobSummary(summary BacktestRunSummary) (string, error) {
	compact, err := json.Marshal(BuildBacktestJobSummary(summary))
	if err != nil {
		return "", err
	}
	return string(compact), nil
}

func compactSummaryJSON(job *database.BacktestJob) (string, error) {
	if job == nil {
		return "", nil
	}
	if job.SummaryCompactJSON != nil && *job.SummaryCompactJSON != "" {
		return *job.SummaryCompactJSON, nil
	}
	if job.SummaryJSON == nil || *job.SummaryJSON == "" {
		return "", nil
	}

	var summary BacktestRunSummary
	if err := json.Unmarshal([]byte(*job.SummaryJSON), &summary); err != nil {
		return "", err
	}

	compact, err := MarshalBacktestJobSummary(summary)
	if err != nil {
		return "", err
	}
	return compact, nil
}

func backtestJobResponseColumns() string {
	return `id, status, progress, message, summary_compact_json,
		CASE WHEN COALESCE(summary_compact_json, '') = '' THEN summary_json ELSE NULL END AS summary_json,
		error, dataset_manifest_id, job_type, artifact_digest, diagnostic_json, started_at, finished_at, created_at, updated_at`
}
