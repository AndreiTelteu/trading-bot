package backtest

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"
	"trading-go/internal/database"
)

func TestCompareCIRequiresExclusion(t *testing.T) {
	set := validationCISet{
		SharpeBaseline:        MetricCI{Lower: 0.1, Upper: 0.4},
		SharpeCandidate:       MetricCI{Lower: 0.5, Upper: 0.8},
		ProfitFactorBaseline:  MetricCI{Lower: 1.0, Upper: 1.2},
		ProfitFactorCandidate: MetricCI{Lower: 1.3, Upper: 1.5},
		MaxDrawdownBaseline:   MetricCI{Lower: 0.2, Upper: 0.3},
		MaxDrawdownCandidate:  MetricCI{Lower: 0.22, Upper: 0.28},
	}

	accepted := compareCI(set)
	want := []string{"sharpe", "profit_factor"}
	if !reflect.DeepEqual(accepted, want) {
		t.Fatalf("compareCI() = %v, want %v", accepted, want)
	}
}

func TestBuildBacktestJobResponseParsesSummary(t *testing.T) {
	now := time.Now().UTC()
	summary := BacktestRunSummary{
		JobID:      7,
		StartedAt:  now,
		FinishedAt: now,
		Validation: ValidationSummary{Passed: true, AcceptedMetrics: []string{"sharpe", "profit_factor"}},
	}
	payload, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	message := "done"
	job := &database.BacktestJob{
		ID:          7,
		Status:      "completed",
		Progress:    1,
		Message:     &message,
		SummaryJSON: ptrString(string(payload)),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	response, err := BuildBacktestJobResponse(job)
	if err != nil {
		t.Fatalf("BuildBacktestJobResponse() error = %v", err)
	}
	if response.Summary == nil {
		t.Fatal("BuildBacktestJobResponse() expected parsed summary")
	}
	if !response.Summary.Validation.Passed {
		t.Fatal("parsed summary should preserve validation result")
	}
}

func ptrString(v string) *string {
	return &v
}
