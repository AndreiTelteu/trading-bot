package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"trading-go/internal/backtest"
	"trading-go/internal/validation"
)

func stage07SpecFixture(t *testing.T) validation.ManifestSpec {
	t.Helper()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	dataset := strings.Repeat("e", 64)
	authority, err := validation.NewAuthorityPolicyEnvelope(map[string]string{
		"selection_top_k": "3", "selection_min_probability": "0.53", "selection_min_ev": "0.001",
		"fallback_mode": "rule_based", "strategy_parameters": "params", "risk_policy": "risk-v1",
		"turnover_policy": "turnover-v1", "cash_policy": "cash-v1", "universe_policy": "universe-v1",
		"execution_policy": "backtest-execution-v1", "cost_policy": "backtest-cost-v1", "model_version": "none",
		"feature_schema": "none", "rollout_state": "research",
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := validation.ManifestSpec{
		SchemaVersion: validation.ManifestSchemaVersion, StudyType: "exploratory", Exploratory: true,
		FamilyID: strings.Repeat("f", 64), CodeRevision: "old-revision",
		Candidate:        validation.VersionRef{ID: "trend_momentum_candidate", Version: "1.1.0", ImplementationDigest: validation.ImplementationDigest(strings.Repeat("a", 64)), ConfigDigest: validation.ConfigDigest(strings.Repeat("c", 64))},
		Baseline:         validation.VersionRef{ID: "matched_momentum_baseline", Version: "1.0.0", ImplementationDigest: validation.ImplementationDigest(strings.Repeat("b", 64)), ConfigDigest: validation.ConfigDigest(strings.Repeat("d", 64))},
		Policies:         validation.PolicyBundle{Composite: "policy_bundle_old", Execution: "backtest-execution-v1", Universe: "universe-v1", ModelSelection: "none", EntrySelection: "entry-v1", PortfolioRisk: "risk-v1", Rollout: "research", Cost: "backtest-cost-v1"},
		GovernancePolicy: validation.GovernancePolicyVersion, AuthorityPolicy: authority,
		DatasetManifestID: dataset, DatasetManifestHash: dataset, DatasetDigest: validation.DatasetDigest(dataset), UniversePolicy: "universe-v1",
		Interval: validation.Interval{Start: base, End: base.Add(18 * 24 * time.Hour)}, DecisionClock: "4h-close", ExecutionClock: "next-1m-open", Seed: 42,
		ExecutionSemantics:    map[string]string{"fee_bps": "10", "slippage_bps": "5", "timing": "next_executable", "liquidity": "full_fill_ohlcv"},
		CapacityStress:        validation.CapacityStressPolicy{MaxParticipation: .1, ImpactBpsAtMax: 10, StressMultiplier: 2},
		BaselineComparability: &validation.BaselineComparabilityPolicy{MaxGrossExposureDifference: .02, MaxTurnoverRelativeDiff: .1},
		Folds: []validation.Fold{
			{Index: 0, Train: validation.Interval{Start: base, End: base.Add(3 * 24 * time.Hour)}, Validation: validation.Interval{Start: base.Add(3 * 24 * time.Hour), End: base.Add(5 * 24 * time.Hour)}, Test: validation.Interval{Start: base.Add(5 * 24 * time.Hour), End: base.Add(7 * 24 * time.Hour)}},
			{Index: 1, Train: validation.Interval{Start: base.Add(4 * 24 * time.Hour), End: base.Add(8 * 24 * time.Hour)}, Validation: validation.Interval{Start: base.Add(8 * 24 * time.Hour), End: base.Add(10 * 24 * time.Hour)}, Test: validation.Interval{Start: base.Add(10 * 24 * time.Hour), End: base.Add(12 * 24 * time.Hour)}},
			{Index: 2, Train: validation.Interval{Start: base.Add(9 * 24 * time.Hour), End: base.Add(13 * 24 * time.Hour)}, Validation: validation.Interval{Start: base.Add(13 * 24 * time.Hour), End: base.Add(15 * 24 * time.Hour)}, Test: validation.Interval{Start: base.Add(15 * 24 * time.Hour), End: base.Add(17 * 24 * time.Hour)}},
		},
		FoldSourceJobIDs: []uint{63, 64, 65}, FeatureHorizon: time.Hour, LabelHorizon: time.Hour, Purge: time.Hour, Embargo: time.Hour,
		AllowedTuning: map[string][]string{"lookback": {"20", "30"}}, Metrics: []string{"after_cost_return", "coverage", "max_drawdown"}, StatisticalUnit: "chronological_test_window", BootstrapIterations: 500,
		Samples:             validation.SampleRequirements{MinFolds: 3, MinIndependentUnits: 3, MinObservationsPerFold: 10, MinTradesPerFold: 1, MinRegimes: 2},
		PromotionThresholds: []validation.Threshold{{Metric: "after_cost_return", Op: ">", Value: 0}, {Metric: "coverage", Op: ">=", Value: 1}},
		RollbackThresholds:  []validation.Threshold{{Metric: "max_drawdown", Op: ">=", Value: .2}},
		RequiredElapsed:     map[string]time.Duration{"paper": time.Hour},
		Artifacts:           validation.ArtifactLinks{Metrics: "metrics.json", Trades: "trades.json", Curves: "curves.json", Cohorts: "cohorts.json", Factors: "factors.json", Coverage: "coverage.json", Comparison: "comparison.json"},
		Reproduce:           validation.ReproductionInvocation{Command: "go", Args: []string{"run", "./cmd/backtest"}},
	}
	if _, err := validation.NewManifest(spec, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return spec
}

func stage07RefsFixture(old validation.ManifestSpec) []stage07SourceReference {
	refs := make([]stage07SourceReference, 0, 3)
	// 999 is synthetic; the third repaired source has not been audited.
	for _, id := range []uint{78, 79, 999} {
		refs = append(refs, stage07SourceReference{Comparison: backtest.Stage07ComparisonReference{JobID: id, Candidate: old.Candidate.ID + "@" + old.Candidate.Version, DatasetDigest: old.DatasetDigest, ArtifactDigest: stage07AuditedComparisonDigest, Strategies: map[string]backtest.Stage07StrategyRef{
			old.Candidate.ID: {ImplementationDigest: stage07CandidateImplementation, ConfigDigest: old.Candidate.ConfigDigest, RunManifestDigest: validation.RunManifestDigest(strings.Repeat("2", 64))},
			old.Baseline.ID:  {ImplementationDigest: stage07BaselineImplementation, ConfigDigest: old.Baseline.ConfigDigest, RunManifestDigest: validation.RunManifestDigest(strings.Repeat("3", 64))},
		}}, ValidationArtifactDigest: stage07AuditedSourceDigest, SourceCodeRevision: stage07SourceSHA})
	}
	return refs
}

func TestStage07SpecDerivesV3FamilyAndPreservesPinnedGates(t *testing.T) {
	old := stage07SpecFixture(t)
	refs := stage07RefsFixture(old)
	next, err := buildStage07Spec(old, refs, strings.Repeat("9", 40), strings.Repeat("8", 64))
	if err != nil {
		t.Fatal(err)
	}
	if next.FamilyID == old.FamilyID || next.FamilyID == "" || next.Candidate.ImplementationDigest != stage07CandidateImplementation || next.Baseline.ImplementationDigest != stage07BaselineImplementation || next.CodeRevision != strings.Repeat("9", 40) || next.Policies.Execution != "backtest-execution-v3" || next.ExecutionSemantics["liquidity"] != "full_fill_ohlcv" || next.ExecutionSemantics["no_fill_rule"] != stage07NoFillRule || !reflect.DeepEqual(next.FoldSourceJobIDs, []uint{78, 79, 999}) {
		t.Fatal("v3 spec identity or execution semantics incorrect")
	}
	if !reflect.DeepEqual(next.Folds, old.Folds) || !reflect.DeepEqual(next.PromotionThresholds, old.PromotionThresholds) || !reflect.DeepEqual(next.RollbackThresholds, old.RollbackThresholds) || !reflect.DeepEqual(next.Samples, old.Samples) || next.BootstrapIterations != old.BootstrapIterations || !reflect.DeepEqual(next.CapacityStress, old.CapacityStress) || !reflect.DeepEqual(next.BaselineComparability, old.BaselineComparability) || next.Candidate.ConfigDigest != old.Candidate.ConfigDigest || next.Baseline.ConfigDigest != old.Baseline.ConfigDigest || next.Policies.Cost != old.Policies.Cost || old.ExecutionSemantics["no_fill_rule"] != "" {
		t.Fatal("pinned old spec mutated or weakened")
	}
}

func TestStage07ReferenceSetRejectsImplementationOrConfigMismatch(t *testing.T) {
	old := stage07SpecFixture(t)
	for name, mutate := range map[string]func([]stage07SourceReference){
		"pre-repair source": func(refs []stage07SourceReference) {
			refs[1].Comparison.JobID = 77
		},
		"wrong first source": func(refs []stage07SourceReference) {
			refs[0].Comparison.JobID = 79
		},
		"pre-fix baseline implementation": func(refs []stage07SourceReference) {
			r := refs[1].Comparison.Strategies[old.Baseline.ID]
			r.ImplementationDigest = validation.ImplementationDigest("7849c702ff03da0104be460aec00f524c38e0d5481f110d3cadd9b9c5c8d7c8a")
			refs[1].Comparison.Strategies[old.Baseline.ID] = r
		},
		"v1 implementation": func(refs []stage07SourceReference) {
			r := refs[1].Comparison.Strategies[old.Candidate.ID]
			r.ImplementationDigest = old.Candidate.ImplementationDigest
			refs[1].Comparison.Strategies[old.Candidate.ID] = r
		},
		"different config": func(refs []stage07SourceReference) {
			r := refs[2].Comparison.Strategies[old.Baseline.ID]
			r.ConfigDigest = validation.ConfigDigest(strings.Repeat("0", 64))
			refs[2].Comparison.Strategies[old.Baseline.ID] = r
		},
		"different dataset": func(refs []stage07SourceReference) {
			refs[2].Comparison.DatasetDigest = validation.DatasetDigest(strings.Repeat("0", 64))
		},
		"different comparison digest": func(refs []stage07SourceReference) {
			refs[1].Comparison.ArtifactDigest = strings.Repeat("5", 64)
		},
		"different source artifact digest": func(refs []stage07SourceReference) {
			refs[2].ValidationArtifactDigest = strings.Repeat("6", 64)
		},
		"uniform unreviewed comparison digest": func(refs []stage07SourceReference) {
			for i := range refs {
				refs[i].Comparison.ArtifactDigest = strings.Repeat("5", 64)
			}
		},
		"uniform unreviewed source digest": func(refs []stage07SourceReference) {
			for i := range refs {
				refs[i].ValidationArtifactDigest = strings.Repeat("6", 64)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			refs := stage07RefsFixture(old)
			mutate(refs)
			if _, err := buildStage07Spec(old, refs, strings.Repeat("9", 40), strings.Repeat("8", 64)); err == nil {
				t.Fatal("mismatched source accepted")
			}
		})
	}
}

func TestStage07RequiresCanonicalNonNullSourceArtifact(t *testing.T) {
	old := stage07SpecFixture(t)
	settings := map[string]string{"backtest_execution_policy_version": "backtest-execution-v3"}
	settingsRaw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	artifact := stage07CanonicalSourceArtifact{
		SchemaVersion: "stage07-source-artifact-v2", ComparisonDigest: strings.Repeat("1", 64), DatasetManifestID: old.DatasetManifestID,
		ReplaySettings: settings, ReplaySettingsDigest: fmt.Sprintf("%x", sha256.Sum256(settingsRaw)),
		Results: map[string]backtest.Stage05StrategyResult{old.Candidate.ID: {Manifest: backtest.RunManifest{CodeRevision: stage07SourceSHA}}},
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if _, err := verifyStage07CanonicalSourceArtifact(raw, digest, artifact.ComparisonDigest, old.DatasetManifestID); err != nil {
		t.Fatal(err)
	}
	for name, input := range map[string]struct {
		raw        []byte
		digest     string
		comparison string
	}{
		"nil raw":          {nil, digest, artifact.ComparisonDigest},
		"null raw":         {[]byte("null"), digest, artifact.ComparisonDigest},
		"changed raw":      {[]byte(strings.Replace(string(raw), "backtest-execution-v3", "backtest-execution-v2", 1)), digest, artifact.ComparisonDigest},
		"wrong comparison": {raw, digest, strings.Repeat("2", 64)},
		"missing digest":   {raw, "", artifact.ComparisonDigest},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := verifyStage07CanonicalSourceArtifact(input.raw, input.digest, input.comparison, old.DatasetManifestID); err == nil {
				t.Fatal("noncanonical or absent source evidence accepted")
			}
		})
	}
}

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
	ids, err := parseStage07SourceIDs("78,79,999")
	if err != nil || len(ids) != 3 {
		t.Fatalf("new sources rejected: %v", err)
	}
	for _, value := range []string{"66,67,68", "72,73,74", "75,76,77", "77,79,999", "78,77,999", "78,78,999", "78,79", "78,79,999,1000", "78,abc,999", "79,80,999"} {
		if _, err := parseStage07SourceIDs(value); err == nil {
			t.Fatalf("unsafe sources accepted: %s", value)
		}
	}
}

func TestStage07ReviewedSourceCheckpointPins(t *testing.T) {
	if stage07SourceSHA != "d824c0811ecc3199f9125166b3722d29ae5bc0bb" ||
		stage07AuditedComparisonDigest != "d4bc2aee9387f5377d0954686af8efdf74a467c75dfe6cbc2605994f3ddebc4b" ||
		stage07AuditedSourceDigest != "1421afe0a536ea09158e015b4acfb60d68c901586b9d52268b074d49b2bed042" ||
		stage07CandidateImplementation != "d1710c8250f660d56d824e59cb58cfcc6f2053c68a44e28c94be29613db790c4" ||
		stage07BaselineImplementation != "d1710c8250f660d56d824e59cb58cfcc6f2053c68a44e28c94be29613db790c4" {
		t.Fatal("reviewed source checkpoint identities changed")
	}
}

func TestStage07IdempotencyKeyMustBeFresh(t *testing.T) {
	prior1 := fmt.Sprintf("%x", sha256.Sum256([]byte("previous-key-one")))
	prior2 := fmt.Sprintf("%x", sha256.Sum256([]byte("previous-key-two")))
	prior3 := fmt.Sprintf("%x", sha256.Sum256([]byte("previous-key-three")))
	prior := [3]string{prior1, prior2, prior3}
	if stage07IdempotencyKeyNew("previous-key-one", prior) || stage07IdempotencyKeyNew("previous-key-two", prior) || stage07IdempotencyKeyNew("previous-key-three", prior) || !stage07IdempotencyKeyNew("different-key", prior) || stage07IdempotencyKeyNew("short", prior) {
		t.Fatal("prior or invalid attempt key accepted")
	}
}

func TestStage07PriorAttemptLineageIsPinned(t *testing.T) {
	if !reflect.DeepEqual(stage07PriorFailedExperiments(), []string{
		"2c1ab23be00a734b6f53224b7a65f79725e88dc63ed91a71a6fb6064a92dcc63",
		"4cceee430468b9515c60033e1ca5edac0ab46289a47761e9d9ece441918264b4",
	}) || !reflect.DeepEqual(stage07PriorRegisteredUnfinishedExperiments(), []string{"a8f62251ce99914384d5e5a0edd747dc1053b7a4ea9d379bae5d2cb15c404931"}) || stage07BoundaryAttemptKeySHA != "49e304cbd812e5df58d00c1a94fc6e3774c46aa77903f83498b5eb7a7dfbef48" || stage07AllocationAttemptKeySHA != "f64edf70ba81edd5672a4baa8e8e95c1ba707e55539e5b6033d6e512deb9c337" || stage07RegisteredUnfinishedAttemptKeySHA != "e482b724e294d0633a040f32c59a92dffeb0debc0199ff56e1912ff90f29e951" {
		t.Fatal("prior Stage 07 attempt lineage changed")
	}
}

func TestStage07PlanMetadataRequiresRegisteredUnfinishedLineage(t *testing.T) {
	old := stage07SpecFixture(t)
	plan := stage07Plan{SchemaVersion: stage07PlanVersion, SourceCodeRevision: stage07SourceSHA, DriverCodeRevision: strings.Repeat("9", 40), OldManifestSHA256: stage07OldSpecSHA, Creator: "researcher", IdempotencyKey: "new-key-123", PriorFailedExperimentIDs: stage07PriorFailedExperiments(), PriorRegisteredUnfinishedExperimentIDs: stage07PriorRegisteredUnfinishedExperiments(), SourceReferences: stage07RefsFixture(old)}
	opts := stage07Options{DriverSHA: plan.DriverCodeRevision}
	if err := validateStage07PlanMetadata(plan, opts); err != nil {
		t.Fatal(err)
	}
	plan.PriorRegisteredUnfinishedExperimentIDs = nil
	if err := validateStage07PlanMetadata(plan, opts); err == nil {
		t.Fatal("registered unfinished attempt missing from lineage")
	}
	plan.PriorRegisteredUnfinishedExperimentIDs = stage07PriorRegisteredUnfinishedExperiments()
	plan.IdempotencyKey = "short"
	if err := validateStage07PlanMetadata(plan, opts); err == nil {
		t.Fatal("invalid idempotency key accepted")
	}
}
