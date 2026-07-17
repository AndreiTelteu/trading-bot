package services

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"trading-go/internal/database"

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

func TestStage05CanonicalGovernanceRejectsForgedEvidence(t *testing.T) {
	summary, digest, manifest := canonicalGovernanceFixture(t)
	job := database.BacktestJob{Status: "completed", JobType: "stage05_comparison", SummaryJSON: &summary, ArtifactDigest: &digest, DatasetManifestID: &manifest}
	if err := requireBaselineRelativeOptimizationEvidence(summary, job); err != nil {
		t.Fatalf("canonical evidence rejected: %v", err)
	}
	mutations := []func(map[string]interface{}){
		func(value map[string]interface{}) { value["artifact_digest"] = strings.Repeat("0", 64) },
		func(value map[string]interface{}) {
			rows := value["rows"].([]interface{})
			value["rows"] = append(rows, rows[0])
		},
		func(value map[string]interface{}) {
			rows := value["rows"].([]interface{})
			rows[3].(map[string]interface{})["descriptor"].(map[string]interface{})["baseline"] = true
		},
		func(value map[string]interface{}) {
			rows := value["rows"].([]interface{})
			delete(rows[3].(map[string]interface{})["metrics"].(map[string]interface{})["total_return"].(map[string]interface{}), "value")
		},
	}
	for i, mutate := range mutations {
		var value map[string]interface{}
		_ = json.Unmarshal([]byte(summary), &value)
		mutate(value)
		encoded, _ := json.Marshal(value)
		if err := requireBaselineRelativeOptimizationEvidence(string(encoded), job); err == nil {
			t.Fatalf("forgery %d accepted", i)
		}
	}
}

func canonicalGovernanceFixture(t *testing.T) (string, string, string) {
	t.Helper()
	manifest := strings.Repeat("c", 64)
	assumptions := map[string]interface{}{"dataset_manifest_id": manifest}
	row := func(id, version string, baseline bool, value float64) map[string]interface{} {
		params := map[string]string{"target_gross": "1"}
		normalized := map[string]interface{}{"schema_version": "normalized-run-manifest-v1", "dataset_manifest_id": manifest, "strategy_id": id, "strategy_version": version, "parameters": params, "assumptions": assumptions}
		raw, _ := json.Marshal(normalized)
		return map[string]interface{}{"strategy_id": id, "strategy_version": version, "manifest_identity": fmt.Sprintf("%x", sha256.Sum256(raw)), "dataset_manifest_id": manifest, "parameters": params, "normalized_run_manifest": normalized, "descriptor": map[string]interface{}{"id": id, "version": version, "baseline": baseline}, "metrics": map[string]interface{}{"reconciled": true, "total_return": map[string]interface{}{"available": true, "value": value}}}
	}
	artifact := map[string]interface{}{"schema_version": "strategy-comparison-v1", "manifest_id": manifest, "candidate": "candidate@1.0.0", "normalized_assumptions": assumptions, "rows": []map[string]interface{}{row("cash", "1.0.0", true, 0), row("benchmark_buy_hold", "1.0.0", true, .01), row("benchmark_trend", "1.0.0", true, .005), row("candidate", "1.0.0", false, .02)}, "governance": map[string]interface{}{"schema_version": "baseline-governance-gate-v1", "optimization_allowed": true, "promotion_allowed": false}}
	unsigned, _ := json.Marshal(artifact)
	digest := fmt.Sprintf("%x", sha256.Sum256(unsigned))
	artifact["artifact_digest"] = digest
	encoded, _ := json.Marshal(artifact)
	return string(encoded), digest, manifest
}

func TestStage05OptimizationEvidenceParserRejectsSelfAssertedPass(t *testing.T) {
	summary := `{"schema_version":"strategy-comparison-v1","manifest_id":"m1","candidate":"candidate@1.0.0","normalized_assumptions":{"dataset_manifest_id":"m1"},"rows":[{"strategy_id":"cash","manifest_identity":"m1","metrics":{"reconciled":true,"total_return":{"available":true}}},{"strategy_id":"benchmark_buy_hold","manifest_identity":"m1","metrics":{"reconciled":true,"total_return":{"available":true}}},{"strategy_id":"candidate","manifest_identity":"m1","metrics":{"reconciled":true,"total_return":{"available":true}}}],"governance":{"schema_version":"baseline-governance-gate-v1","optimization_allowed":true}}`
	if err := requireBaselineRelativeOptimizationEvidence(summary); err == nil || !strings.Contains(err.Error(), "canonical completed job binding") {
		t.Fatalf("forged evidence was accepted: %v", err)
	}
}
