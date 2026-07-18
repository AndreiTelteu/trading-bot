package services

import (
	"encoding/json"
	"fmt"
	"strings"
	"trading-go/internal/cutover"
	"trading-go/internal/database"
	"trading-go/internal/validation"
)

const Stage05FallbackPolicyVersion = "stage05-rule-fallback-v1"

// ResolveStrategyAuthority is the production capital-order fence for strategy
// identities. The empty legacy identity maps only to the narrow, versioned
// Stage 05 rule fallback; every experimental identity needs an exact paper/live
// deployment and runtime policy match.
func ResolveStrategyAuthority(settings map[string]string) error {
	id := strings.TrimSpace(settings["strategy_id"])
	version := strings.TrimSpace(settings["strategy_version"])
	if id == "" {
		id = BaselineStrategyID
		version = BaselineStrategyVersion
	}
	if id == BaselineStrategyID && version == BaselineStrategyVersion {
		policy := strings.TrimSpace(settings["baseline_fallback_policy_version"])
		if policy == "" || policy == Stage05FallbackPolicyVersion {
			return nil
		}
		return fmt.Errorf("baseline fallback policy mismatch")
	}
	if flags, active := cutover.Active(); active {
		if flags.CandidateStrategy != "paper" && flags.CandidateStrategy != "limited_live" && flags.CandidateStrategy != "full_live" {
			return fmt.Errorf("Stage 06 candidate capital authority is disabled by Stage 08 envelope")
		}
	}
	if id == "cash" || id == "benchmark_buy_hold" || id == "benchmark_trend" || id == "equal_weight" || id == "momentum" {
		return fmt.Errorf("research baseline %s cannot directly authorize runtime orders", id)
	}
	if database.DB == nil {
		return fmt.Errorf("experimental strategy governance unavailable")
	}
	contextKey := "strategy:" + id + "@" + version
	var deployment database.GovernanceDeployment
	if err := database.DB.Where("context_key=?", contextKey).First(&deployment).Error; err != nil {
		return fmt.Errorf("experimental strategy has no governance deployment")
	}
	if deployment.State != "paper" && deployment.State != "limited_live" && deployment.State != "full_live" {
		return fmt.Errorf("experimental strategy state %s cannot authorize orders", deployment.State)
	}
	manifest, err := (validation.Repository{DB: database.DB}).LoadManifest(deployment.ExperimentID)
	if err != nil || deployment.ArtifactVersion != version || manifest.Spec.Candidate.ID != id || manifest.Spec.Candidate.Version != version {
		return fmt.Errorf("experimental strategy manifest mismatch")
	}
	if configured := strings.TrimSpace(settings["strategy_digest"]); configured != "" && configured != manifest.Spec.Candidate.Digest {
		return fmt.Errorf("experimental strategy digest mismatch")
	}
	runtime, err := BuildRuntimeAuthorityPolicy(settings, deployment.State)
	if err != nil || runtime.Digest != deployment.AuthorityPolicyDigest {
		return fmt.Errorf("experimental strategy runtime policy mismatch")
	}
	stored := validation.AuthorityPolicyEnvelope{}
	if json.Unmarshal([]byte(deployment.AuthorityPolicyJSON), &stored) != nil || stored.Verify() != nil || stored.Digest != runtime.Digest {
		return fmt.Errorf("experimental strategy deployment integrity failed")
	}
	return nil
}
