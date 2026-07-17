package backtest

import (
	"testing"
	"time"

	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

func TestStage05ComparisonPersistenceIsBoundedAndSchemaVersioned(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	comparison := ComparisonArtifact{SchemaVersion: ComparisonSchemaVersion, ManifestID: "manifest-fixture", Candidate: "candidate@1.0.0", Assumptions: NormalizedAssumptions{StartingCapital: "1000", MaxGrossExposure: "1", MaxNetExposure: "1", DatasetManifestID: "manifest-fixture", FinalPolicy: "liquidate"}, Rows: []ComparisonRow{{StrategyID: StrategyCashID, StrategyVersion: "1.0.0", Baseline: true, Metrics: ComparableMetrics{SchemaVersion: EvaluationSchemaVersion, StartingCapital: "1000", EndingEquity: "1000", Reconciled: true}}, {StrategyID: "candidate", StrategyVersion: "1.0.0", Metrics: ComparableMetrics{SchemaVersion: EvaluationSchemaVersion, StartingCapital: "1000", EndingEquity: "1010", Reconciled: true}}}, Governance: GovernanceGate{SchemaVersion: GovernanceSchemaVersion, OptimizationAllowed: true, PromotionAllowed: true}}
	encoded, err := MarshalComparisonArtifact(comparison)
	if err != nil {
		t.Fatal(err)
	}
	value := string(encoded)
	job := database.BacktestJob{Status: "completed", Progress: 1, SummaryCompactJSON: &value, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	response, err := GetBacktestJobResponse(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if response.Comparison == nil || response.Summary != nil || response.Comparison.SchemaVersion != ComparisonSchemaVersion || len(response.Comparison.Rows) != 2 {
		t.Fatalf("response=%+v", response)
	}

	unbounded := comparison
	unbounded.Rows = make([]ComparisonRow, 17)
	if _, err := MarshalComparisonArtifact(unbounded); err == nil {
		t.Fatal("unbounded comparison artifact accepted")
	}
}
