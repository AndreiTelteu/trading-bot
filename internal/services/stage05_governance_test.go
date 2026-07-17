package services

import (
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestStage05OptimizationFailsClosedWithoutBaselineRelativeEvidence(t *testing.T) {
	cases := []string{"", `{}`, `{"schema_version":"strategy-comparison-v1","governance":{"schema_version":"baseline-governance-gate-v1","optimization_allowed":false,"reasons":["candidate_does_not_beat_cash_after_costs"]}}`}
	for _, summary := range cases {
		_, err := GenerateBacktestOptimizationProposals(BacktestOptimizationInput{SummaryJSON: summary})
		if err == nil || !strings.Contains(err.Error(), "optimization_blocked") {
			t.Fatalf("summary=%s err=%v", summary, err)
		}
		fiberErr, ok := err.(*fiber.Error)
		if !ok || fiberErr.Code != fiber.StatusConflict {
			t.Fatalf("error=%T %+v", err, err)
		}
	}
}

func TestStage05OptimizationEvidenceParserAcceptsExplicitPass(t *testing.T) {
	summary := `{"schema_version":"strategy-comparison-v1","manifest_id":"m1","candidate":"candidate@1.0.0","normalized_assumptions":{"dataset_manifest_id":"m1"},"rows":[{"strategy_id":"cash","manifest_identity":"m1","metrics":{"reconciled":true,"total_return":{"available":true}}},{"strategy_id":"benchmark_buy_hold","manifest_identity":"m1","metrics":{"reconciled":true,"total_return":{"available":true}}},{"strategy_id":"candidate","manifest_identity":"m1","metrics":{"reconciled":true,"total_return":{"available":true}}}],"governance":{"schema_version":"baseline-governance-gate-v1","optimization_allowed":true}}`
	if err := requireBaselineRelativeOptimizationEvidence(summary); err != nil {
		t.Fatal(err)
	}
}
