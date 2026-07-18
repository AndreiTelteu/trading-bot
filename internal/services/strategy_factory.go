package services

import (
	"fmt"
	"strings"

	"trading-go/internal/database"
	"trading-go/internal/tradingcore"
	"trading-go/internal/validation"
)

const (
	BaselineStrategyID            = "stage05_rule_baseline"
	BaselineStrategyVersion       = "1.0.0"
	TrendMomentumCandidateID      = "trend_momentum_candidate"
	TrendMomentumCandidateVersion = "1.0.0"
)

var (
	BaselineStrategyDigest       = tradingcore.StrategyArtifactDigest("legacy")
	TrendMomentumCandidateDigest = tradingcore.StrategyArtifactDigest("target")
	legacyStrategyCodeIdentity   = "tradingcore-source-v1:" + BaselineStrategyDigest
	targetStrategyCodeIdentity   = "tradingcore-source-v1:" + TrendMomentumCandidateDigest
)

type executedStrategyIdentity struct {
	ID, Version, Digest, CodeIdentity string
}

func (identity executedStrategyIdentity) String() string {
	return identity.ID + "@" + identity.Version + "#" + identity.Digest + "+code=" + identity.CodeIdentity
}

type registeredRuntimeStrategy struct {
	identity executedStrategyIdentity
	build    func() tradingcore.Strategy
}

var runtimeStrategyRegistry = map[string]registeredRuntimeStrategy{
	BaselineStrategyID + "@" + BaselineStrategyVersion + "#" + BaselineStrategyDigest: {
		identity: executedStrategyIdentity{ID: BaselineStrategyID, Version: BaselineStrategyVersion, Digest: BaselineStrategyDigest, CodeIdentity: legacyStrategyCodeIdentity},
		build:    func() tradingcore.Strategy { return tradingcore.LegacyRuleStrategy{} },
	},
	TrendMomentumCandidateID + "@" + TrendMomentumCandidateVersion + "#" + TrendMomentumCandidateDigest: {
		identity: executedStrategyIdentity{ID: TrendMomentumCandidateID, Version: TrendMomentumCandidateVersion, Digest: TrendMomentumCandidateDigest, CodeIdentity: targetStrategyCodeIdentity},
		build:    func() tradingcore.Strategy { return tradingcore.TargetAllocationStrategy{} },
	},
}

// buildDeploymentStrategy binds governance identity to an exact in-process
// implementation. An approved but unknown digest is never treated as a request
// to run the legacy rules.
func buildDeploymentStrategy(settings map[string]string) (executedStrategyIdentity, tradingcore.Strategy, error) {
	id := strings.TrimSpace(settings["strategy_id"])
	version := strings.TrimSpace(settings["strategy_version"])
	if id == "" {
		id, version = BaselineStrategyID, BaselineStrategyVersion
	}
	if id == BaselineStrategyID && version == BaselineStrategyVersion {
		digest := strings.TrimSpace(settings["strategy_digest"])
		if digest == "" {
			digest = BaselineStrategyDigest
		}
		return instantiateRegisteredStrategy(id, version, digest)
	}
	if database.DB == nil {
		return executedStrategyIdentity{}, nil, fmt.Errorf("deployment strategy registry requires governance database")
	}
	var deployment database.GovernanceDeployment
	if err := database.DB.Where("context_key = ?", "strategy:"+id+"@"+version).First(&deployment).Error; err != nil {
		return executedStrategyIdentity{}, nil, fmt.Errorf("load strategy deployment: %w", err)
	}
	manifest, err := (validation.Repository{DB: database.DB}).LoadManifest(deployment.ExperimentID)
	if err != nil {
		return executedStrategyIdentity{}, nil, fmt.Errorf("load deployed strategy manifest: %w", err)
	}
	if deployment.ArtifactVersion != version || manifest.Spec.Candidate.ID != id || manifest.Spec.Candidate.Version != version {
		return executedStrategyIdentity{}, nil, fmt.Errorf("deployed strategy identity mismatch")
	}
	digest := manifest.Spec.Candidate.Digest
	if configured := strings.TrimSpace(settings["strategy_digest"]); configured != "" && configured != digest {
		return executedStrategyIdentity{}, nil, fmt.Errorf("configured strategy digest does not match approved deployment")
	}
	return instantiateRegisteredStrategy(id, version, digest)
}

func instantiateRegisteredStrategy(id, version, digest string) (executedStrategyIdentity, tradingcore.Strategy, error) {
	entry, ok := runtimeStrategyRegistry[id+"@"+version+"#"+digest]
	if !ok {
		return executedStrategyIdentity{}, nil, fmt.Errorf("strategy implementation is not registered for %s@%s digest %s", id, version, digest)
	}
	if !strings.HasSuffix(entry.identity.CodeIdentity, entry.identity.Digest) {
		return executedStrategyIdentity{}, nil, fmt.Errorf("registered strategy code identity digest mismatch for %s@%s", id, version)
	}
	return entry.identity, entry.build(), nil
}
