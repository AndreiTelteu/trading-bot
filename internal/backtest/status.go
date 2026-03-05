package backtest

import (
	"encoding/json"
	"time"
	"trading-go/internal/database"
)

type BacktestJobResponse struct {
	ID          uint                `json:"id"`
	Status      string              `json:"status"`
	Progress    float64             `json:"progress"`
	Message     *string             `json:"message,omitempty"`
	SummaryJSON *string             `json:"summary_json,omitempty"`
	Summary     *BacktestRunSummary `json:"summary,omitempty"`
	Error       *string             `json:"error,omitempty"`
	StartedAt   *time.Time          `json:"started_at,omitempty"`
	FinishedAt  *time.Time          `json:"finished_at,omitempty"`
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

func BuildBacktestJobResponse(job *database.BacktestJob) (*BacktestJobResponse, error) {
	if job == nil {
		return nil, nil
	}

	response := &BacktestJobResponse{
		ID:          job.ID,
		Status:      job.Status,
		Progress:    job.Progress,
		Message:     job.Message,
		SummaryJSON: job.SummaryJSON,
		Error:       job.Error,
		StartedAt:   job.StartedAt,
		FinishedAt:  job.FinishedAt,
		CreatedAt:   job.CreatedAt,
		UpdatedAt:   job.UpdatedAt,
	}

	if job.SummaryJSON != nil && *job.SummaryJSON != "" {
		var summary BacktestRunSummary
		if err := json.Unmarshal([]byte(*job.SummaryJSON), &summary); err != nil {
			return nil, err
		}
		response.Summary = &summary
	}

	return response, nil
}
