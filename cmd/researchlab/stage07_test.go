package main

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestStage07DiffAllowlistFailsClosed(t *testing.T) {
	if err := validateStage07DiffPaths([]string{"cmd/researchlab/main.go", "cmd/researchlab/stage07.go", "docs/operations/stage05-07-next-exploratory-iteration.md"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"internal/backtest/job.go", "internal/validation/manifest.go", "cmd/server/main.go", "go.mod", "go.sum", "AGENTS.md", "cmd/researchlab-other/main.go", "docs"} {
		if err := validateStage07DiffPaths([]string{"cmd/researchlab/main.go", path}); err == nil {
			t.Fatalf("forbidden source-to-driver change accepted: %s", path)
		}
	}
}

func TestStage07ReviewedPlanRejectsAlteredSpec(t *testing.T) {
	plan := []byte(`{"schema_version":"researchlab-stage07-plan-v1","spec":{"execution_policy_version":"backtest-execution-v3"}}`)
	sha := fmt.Sprintf("%x", sha256.Sum256(plan))
	if err := verifyStage07PlanSHA(plan, sha); err != nil {
		t.Fatal(err)
	}
	mutated := []byte(`{"schema_version":"researchlab-stage07-plan-v1","spec":{"execution_policy_version":"backtest-execution-v2"}}`)
	if err := verifyStage07PlanSHA(mutated, sha); err == nil {
		t.Fatal("altered spec accepted under reviewed plan SHA")
	}
	if err := verifyStage07PlanSHA(plan, "not-a-digest"); err == nil {
		t.Fatal("malformed digest accepted")
	}
}

func TestStage07RequiresThreeDistinctNewSources(t *testing.T) {
	ids, err := parseStage07SourceIDs("66,67,68")
	if err != nil || len(ids) != 3 {
		t.Fatalf("new sources rejected: %v", err)
	}
	for _, value := range []string{"63,67,68", "66,66,68", "66,67", "66,67,68,69", "66,abc,68"} {
		if _, err := parseStage07SourceIDs(value); err == nil {
			t.Fatalf("unsafe sources accepted: %s", value)
		}
	}
}
