package backtest

import (
	"encoding/json"
	"sort"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/services"
)

type BacktestJobStrategySummary struct {
	Mode           StrategyMode        `json:"mode"`
	Metrics        Metrics             `json:"metrics"`
	RankingMetrics *RankingMetrics     `json:"ranking_metrics,omitempty"`
	Diagnostics    StrategyDiagnostics `json:"diagnostics"`
}

type BacktestJobValidationSummary struct {
	Passed           bool     `json:"passed"`
	Windows          int      `json:"windows"`
	AcceptedMetrics  []string `json:"accepted_metrics,omitempty"`
	RecommendedStage string   `json:"recommended_stage,omitempty"`
}

type BacktestJobSummary struct {
	Symbols       []string                     `json:"symbols,omitempty"`
	BacktestMode  BacktestMode                 `json:"backtest_mode,omitempty"`
	ModelVersion  string                       `json:"model_version,omitempty"`
	PolicyVersion string                       `json:"policy_version,omitempty"`
	RolloutState  string                       `json:"rollout_state,omitempty"`
	ExperimentID  string                       `json:"experiment_id,omitempty"`
	UniverseMode  UniverseMode                 `json:"universe_mode,omitempty"`
	PolicyContext services.GovernanceContext   `json:"policy_context"`
	Baseline      BacktestJobStrategySummary   `json:"baseline"`
	VolSizing     BacktestJobStrategySummary   `json:"vol_sizing"`
	Validation    BacktestJobValidationSummary `json:"validation"`
}

type BacktestJobResponse struct {
	ID         uint                `json:"id"`
	Status     string              `json:"status"`
	Progress   float64             `json:"progress"`
	Message    *string             `json:"message,omitempty"`
	Summary    *BacktestJobSummary `json:"summary,omitempty"`
	Error      *string             `json:"error,omitempty"`
	StartedAt  *time.Time          `json:"started_at,omitempty"`
	FinishedAt *time.Time          `json:"finished_at,omitempty"`
	CreatedAt  time.Time           `json:"created_at"`
	UpdatedAt  time.Time           `json:"updated_at"`
}

func ListBacktestJobResponses() ([]BacktestJobResponse, error) {
	var jobs []database.BacktestJob
	if err := database.DB.Select(backtestJobResponseColumns()).Order("created_at DESC").Find(&jobs).Error; err != nil {
		return nil, err
	}

	responses := make([]BacktestJobResponse, 0, len(jobs))
	for i := range jobs {
		response, err := BuildBacktestJobResponse(&jobs[i])
		if err != nil {
			return nil, err
		}
		if response != nil {
			responses = append(responses, *response)
		}
	}

	return responses, nil
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
	if err := database.DB.Select(backtestJobResponseColumns()).Order("created_at DESC").First(&job).Error; err != nil {
		return nil, err
	}
	return BuildBacktestJobResponse(&job)
}

func BuildBacktestJobResponse(job *database.BacktestJob) (*BacktestJobResponse, error) {
	if job == nil {
		return nil, nil
	}

	response := &BacktestJobResponse{
		ID:         job.ID,
		Status:     job.Status,
		Progress:   job.Progress,
		Message:    job.Message,
		Error:      job.Error,
		StartedAt:  job.StartedAt,
		FinishedAt: job.FinishedAt,
		CreatedAt:  job.CreatedAt,
		UpdatedAt:  job.UpdatedAt,
	}

	compactJSON, err := compactSummaryJSON(job)
	if err != nil {
		return nil, err
	}
	if compactJSON != "" {
		var summary BacktestJobSummary
		if err := json.Unmarshal([]byte(compactJSON), &summary); err != nil {
			return nil, err
		}
		response.Summary = &summary
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
		Symbols:       symbols,
		BacktestMode:  summary.BacktestMode,
		ModelVersion:  summary.ModelVersion,
		PolicyVersion: summary.PolicyVersion,
		RolloutState:  summary.PolicyContext.RolloutState,
		ExperimentID:  summary.ExperimentID,
		UniverseMode:  summary.UniverseMode,
		PolicyContext: summary.PolicyContext,
		Baseline: BacktestJobStrategySummary{
			Mode:           summary.Baseline.Mode,
			Metrics:        summary.Baseline.Metrics,
			RankingMetrics: summary.Baseline.RankingMetrics,
			Diagnostics:    summary.Baseline.Diagnostics,
		},
		VolSizing: BacktestJobStrategySummary{
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
		},
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
		error, started_at, finished_at, created_at, updated_at`
}
