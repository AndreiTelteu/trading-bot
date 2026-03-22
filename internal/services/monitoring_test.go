package services

import (
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"

	"gorm.io/gorm"
)

func TestRecordTradeOutcomeUpdatesPredictionLogAndCreatesLabel(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	database.DB = db

	prediction := database.PredictionLog{
		PredictionTime:       time.Now().UTC().Add(-2 * time.Hour),
		Symbol:               "ETH",
		ModelVersion:         DefaultActiveModelVersion,
		PolicyVersion:        "policy_bundle_test",
		UniverseMode:         "dynamic",
		PredictedProbability: 0.62,
		PredictedEV:          0.01,
		RawScore:             0.3,
		Rank:                 1,
		RankBucket:           "rank_1",
		ProbabilityBucket:    "0_60_to_0_69",
		Selected:             true,
		DecisionResult:       "selected",
		RolloutState:         ModelRolloutPaper,
	}
	if err := db.Create(&prediction).Error; err != nil {
		t.Fatalf("Create(prediction) error = %v", err)
	}

	closedAt := time.Now().UTC()
	position := database.Position{
		Symbol:              "ETH",
		Amount:              1,
		AvgPrice:            100,
		ExecutionMode:       ExecutionModePaper,
		DecisionTimeframe:   "15m",
		ModelVersion:        DefaultActiveModelVersion,
		PolicyVersion:       "policy_bundle_test",
		UniverseMode:        "dynamic",
		RolloutState:        ModelRolloutPaper,
		PredictionLogID:     &prediction.ID,
		DecisionContextJSON: `{"model_version":"logistic_baseline_v1"}`,
		Pnl:                 12,
		PnlPercent:          6,
		Status:              "closed",
		OpenedAt:            closedAt.Add(-45 * time.Minute),
		ClosedAt:            &closedAt,
	}
	reason := "take_profit"
	position.CloseReason = &reason

	if err := db.Transaction(func(tx *gorm.DB) error {
		return RecordTradeOutcome(tx, position)
	}); err != nil {
		t.Fatalf("RecordTradeOutcome() error = %v", err)
	}

	var label database.TradeLabel
	if err := db.First(&label).Error; err != nil {
		t.Fatalf("expected trade label, got error = %v", err)
	}
	if label.PredictionLogID == nil || *label.PredictionLogID != prediction.ID {
		t.Fatalf("label prediction_log_id = %v, want %d", label.PredictionLogID, prediction.ID)
	}
	if !label.Profitable {
		t.Fatal("expected profitable label")
	}

	var updated database.PredictionLog
	if err := db.First(&updated, prediction.ID).Error; err != nil {
		t.Fatalf("expected updated prediction log, got error = %v", err)
	}
	if updated.OutcomeReturn == nil || *updated.OutcomeReturn <= 0 {
		t.Fatalf("expected positive outcome return, got %v", updated.OutcomeReturn)
	}
	if updated.OutcomeProfitable == nil || !*updated.OutcomeProfitable {
		t.Fatalf("expected profitable outcome flag, got %v", updated.OutcomeProfitable)
	}
}
