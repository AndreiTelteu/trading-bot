package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"time"

	"trading-go/internal/backtest"
	"trading-go/internal/database"
	"trading-go/internal/validation"

	"gorm.io/gorm"
)

const (
	stage07V4SourceSHA               = "cc39d9833aea1e1f33fa4afac437a69067bd387e"
	stage07V4FirstSourceJobID        = uint(103)
	stage07V4ComparisonDigest        = "396b2aedd19f117ef5692d45c783294ca0111b72c68b1e416d6e9a616d8a88cd"
	stage07V4SourceArtifactDigest    = "dce8690cadea93f4782acc189d9cb3aee7a70d0342a346110b21072092dd949d"
	stage07V4DatasetDigest           = "32fe7e3664ab9d1a14c7cff30c0db6dc673f37ca0eea0479ed9cd812011ec9bc"
	stage07V4CandidateImplementation = "3ad180b912257a9608256216889e9167efa913bfd07542f00cb9348302c24821"
	stage07V4CandidateConfig         = "94b8b16e96c882908c4a72385ef0cd68599e8d727fad40ad2ac3ced9830e62e6"
	stage07V4BaselineImplementation  = "739ab88cf6aac578d552958921569896bad0c77fa3a660691cba051d616ba2ed"
	stage07V4BaselineConfig          = "5ffa2ab1b36d0c13e01dc95d1f58b30e765ac04d4d66e7cbf740a53ba1b10792"
	stage07V4PlanVersion             = "researchlab-stage07-v4-plan-v1"
	stage07V4NoFillRule              = "selected_volume_cap_all_or_none_cancel_v1"
)

type stage07V4Plan struct {
	SchemaVersion    string                   `json:"schema_version"`
	SourceCodeSHA    string                   `json:"source_code_sha"`
	DriverCodeSHA    string                   `json:"driver_code_sha"`
	Creator          string                   `json:"creator"`
	IdempotencyKey   string                   `json:"idempotency_key"`
	SourceReferences []stage07SourceReference `json:"source_references"`
	ComparisonDigest string                   `json:"comparison_digest"`
	Spec             validation.ManifestSpec  `json:"spec"`
}

func validateStage07V4CodeLineage(driverSHA string) error {
	if err := exec.Command("git", "merge-base", "--is-ancestor", stage07V4SourceSHA, driverSHA).Run(); err != nil {
		return fmt.Errorf("reviewed v4 driver is not descended from its source revision: %w", err)
	}
	output, err := exec.Command("git", "diff", "--name-only", "--no-renames", "-z", stage07V4SourceSHA, driverSHA, "--").Output()
	if err != nil {
		return err
	}
	paths := make([]string, 0)
	for _, path := range bytes.Split(bytes.TrimSuffix(output, []byte{0}), []byte{0}) {
		if len(path) != 0 {
			paths = append(paths, string(path))
		}
	}
	return validateStage07V4DiffPaths(paths)
}

func validateStage07V4DiffPaths(paths []string) error {
	for _, path := range paths {
		if path == "internal/backtest/stage07_attribution.go" || path == "internal/backtest/stage07_attribution_test.go" || path == "internal/backtest/stage07_v4_test.go" {
			continue
		}
		if !strings.HasPrefix(path, "cmd/researchlab/") && !strings.HasPrefix(path, "docs/") {
			return fmt.Errorf("v4 source-to-driver diff changes forbidden path %q", path)
		}
	}
	return nil
}

func parseStage07V4SourceIDs(raw string) ([]uint, error) {
	ids, err := parseStage07SourceIDsAny(raw)
	if err != nil || !reflect.DeepEqual(ids, []uint{103, 104, 105}) {
		return nil, fmt.Errorf("v4 validation requires audited exact-repeat sources 103,104,105")
	}
	return ids, nil
}

func parseStage07SourceIDsAny(raw string) ([]uint, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 3 {
		return nil, fmt.Errorf("exactly three source job IDs are required")
	}
	ids := make([]uint, 0, 3)
	seen := map[uint]bool{}
	for _, part := range parts {
		parsed, err := strconv.ParseUint(strings.TrimSpace(part), 10, 32)
		id := uint(parsed)
		if err != nil || id == 0 || seen[id] {
			return nil, fmt.Errorf("source job IDs must be distinct positive integers")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return ids, nil
}

func loadStage07V4References(ids []uint) ([]stage07SourceReference, string, map[string]string, error) {
	refs := make([]stage07SourceReference, 0, len(ids))
	comparisons := make([]backtest.Stage07ComparisonReference, 0, len(ids))
	var candidateParameters map[string]string
	for _, id := range ids {
		ref, err := backtest.LoadStage07ComparisonReference(database.DB, id)
		if err != nil {
			return nil, "", nil, err
		}
		candidate, cOK := ref.Strategies[backtest.StrategyTrendMomentumCandidate]
		baseline, bOK := ref.Strategies[backtest.StrategyMatchedMomentumID]
		if !cOK || !bOK || ref.Candidate != "trend_momentum_candidate@1.4.0" || string(ref.DatasetDigest) != stage07V4DatasetDigest || ref.ArtifactDigest != stage07V4ComparisonDigest || string(candidate.ImplementationDigest) != stage07V4CandidateImplementation || string(candidate.ConfigDigest) != stage07V4CandidateConfig || string(baseline.ImplementationDigest) != stage07V4BaselineImplementation || string(baseline.ConfigDigest) != stage07V4BaselineConfig {
			return nil, "", nil, fmt.Errorf("v4 source job %d differs from the audited candidate, baseline, dataset, or comparison identity", id)
		}
		var job database.BacktestJob
		if err := database.DB.First(&job, id).Error; err != nil {
			return nil, "", nil, err
		}
		if job.Status != "completed" || job.ValidationArtifactJSON == nil || job.ValidationArtifactDigest == nil || *job.ValidationArtifactDigest != stage07V4SourceArtifactDigest {
			return nil, "", nil, fmt.Errorf("v4 source job %d lacks the audited complete source artifact", id)
		}
		artifact, err := verifyStage07CanonicalSourceArtifact([]byte(*job.ValidationArtifactJSON), *job.ValidationArtifactDigest, ref.ArtifactDigest, stage07V4DatasetDigest)
		if err != nil {
			return nil, "", nil, err
		}
		c, cOK := artifact.Results[backtest.StrategyTrendMomentumCandidate]
		b, bOK := artifact.Results[backtest.StrategyMatchedMomentumID]
		if !cOK || !bOK || c.Manifest.CodeRevision != stage07V4SourceSHA || b.Manifest.CodeRevision != stage07V4SourceSHA || c.Manifest.ExecutionPolicy.Version != "backtest-execution-v4" || b.Manifest.ExecutionPolicy.Version != "backtest-execution-v4" || c.Manifest.ExecutionPolicy.NoFillRule != stage07V4NoFillRule || b.Manifest.ExecutionPolicy.NoFillRule != stage07V4NoFillRule {
			return nil, "", nil, fmt.Errorf("v4 source job %d code or execution semantics differ", id)
		}
		if candidateParameters == nil {
			candidateParameters = cloneStage07Strings(c.Manifest.Strategy.Parameters)
		} else if !reflect.DeepEqual(candidateParameters, c.Manifest.Strategy.Parameters) {
			return nil, "", nil, fmt.Errorf("v4 source candidate parameters differ")
		}
		refs = append(refs, stage07SourceReference{Comparison: ref, ValidationArtifactDigest: *job.ValidationArtifactDigest, SourceCodeRevision: stage07V4SourceSHA})
		comparisons = append(comparisons, ref)
	}
	if !reflect.DeepEqual(ids, []uint{103, 104, 105}) {
		return nil, "", nil, fmt.Errorf("v4 audited source set differs")
	}
	encoded, err := json.Marshal(comparisons)
	if err != nil {
		return nil, "", nil, err
	}
	return refs, fmt.Sprintf("%x", sha256.Sum256(encoded)), candidateParameters, nil
}

func buildStage07V4Spec(refs []stage07SourceReference, parameters map[string]string, driverSHA, comparisonDigest string) (validation.ManifestSpec, error) {
	if len(refs) != 3 || len(parameters) == 0 || driverSHA == "" || comparisonDigest == "" {
		return validation.ManifestSpec{}, fmt.Errorf("incomplete v4 validation identity")
	}
	start := time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	folds := []validation.Fold{
		{Index: 0, Train: validation.Interval{Start: start, End: time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)}, Validation: validation.Interval{Start: time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, Test: validation.Interval{Start: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}},
		{Index: 1, Train: validation.Interval{Start: start, End: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}, Validation: validation.Interval{Start: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)}, Test: validation.Interval{Start: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}},
		{Index: 2, Train: validation.Interval{Start: start, End: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)}, Validation: validation.Interval{Start: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}, Test: validation.Interval{Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), End: time.Date(2026, 8, 31, 20, 0, 0, 0, time.UTC)}},
	}
	allowed := make(map[string][]string, len(parameters))
	for key, value := range parameters {
		allowed[key] = []string{value}
	}
	authority, err := validation.NewAuthorityPolicyEnvelope(map[string]string{
		"selection_top_k": "3", "selection_min_probability": "0.70", "selection_min_ev": "0", "fallback_mode": "fail_closed",
		"strategy_parameters": stage07V4CandidateConfig, "risk_policy": "trend-momentum-risk-v1", "turnover_policy": "turnover-budget-0.10",
		"cash_policy": "cash-reserve-0.25", "universe_policy": "research-universe-v1", "execution_policy": "backtest-execution-v4",
		"cost_policy": "backtest-cost-v1", "model_version": "decider/decider-4b", "feature_schema": "decision-council-state-v2-compact-v3", "rollout_state": "research",
	})
	if err != nil {
		return validation.ManifestSpec{}, err
	}
	policy := validation.PolicyBundle{Execution: "backtest-execution-v4", Universe: "research-universe-v1", ModelSelection: "decision-council-active-v2-decider-4b", EntrySelection: "council-admit-bull-0.70-bear-below-0.50-entry-only", PortfolioRisk: "trend-momentum-risk-v1", Rollout: "research-only-v1", Cost: "backtest-cost-v1"}
	policyRaw, _ := json.Marshal(policy)
	policy.Composite = fmt.Sprintf("policy_bundle_v4_%x", sha256.Sum256(policyRaw))[:29]
	spec := validation.ManifestSpec{
		SchemaVersion: validation.ManifestSchemaVersion, StudyType: "exploratory", Exploratory: true, CodeRevision: driverSHA,
		Candidate: validation.VersionRef{ID: backtest.StrategyTrendMomentumCandidate, Version: "1.4.0", ImplementationDigest: validation.ImplementationDigest(stage07V4CandidateImplementation), ConfigDigest: validation.ConfigDigest(stage07V4CandidateConfig)},
		Baseline:  validation.VersionRef{ID: backtest.StrategyMatchedMomentumID, Version: "1.0.0", ImplementationDigest: validation.ImplementationDigest(stage07V4BaselineImplementation), ConfigDigest: validation.ConfigDigest(stage07V4BaselineConfig)},
		Policies:  policy, GovernancePolicy: validation.GovernancePolicyVersion, AuthorityPolicy: authority,
		DatasetManifestID: stage07V4DatasetDigest, DatasetManifestHash: stage07V4DatasetDigest, DatasetDigest: validation.DatasetDigest(stage07V4DatasetDigest), UniversePolicy: "research-universe-v1",
		Interval: validation.Interval{Start: start, End: end}, DecisionClock: "completed-4h-close", ExecutionClock: "selected-1m-close-after-volume", Seed: 0,
		ExecutionSemantics: map[string]string{"fee_bps": "10", "slippage_bps": "5", "timing": "selected_bar_close_after_volume", "liquidity": "volume_capped", "max_participation_bps": "1000", "no_fill_rule": stage07V4NoFillRule, "execution_policy_version": "backtest-execution-v4", "source_code_revision": stage07V4SourceSHA},
		CapacityStress:     validation.CapacityStressPolicy{MaxParticipation: .1, ImpactBpsAtMax: 10, StressMultiplier: 2}, BaselineComparability: &validation.BaselineComparabilityPolicy{MaxGrossExposureDifference: .02, MaxTurnoverRelativeDiff: .1},
		Folds: folds, FoldSourceJobIDs: []uint{103, 104, 105}, FeatureHorizon: 5 * 24 * time.Hour, LabelHorizon: 4 * time.Hour, Purge: 4 * time.Hour, Embargo: 4 * time.Hour,
		AllowedTuning: allowed, Metrics: append([]string(nil), validation.RequiredConfirmatoryMetrics...), StatisticalUnit: "chronological_test_window", BootstrapIterations: 500,
		Samples:             validation.SampleRequirements{MinFolds: 3, MinIndependentUnits: 3, MinObservationsPerFold: 10, MinTradesPerFold: 1, MinRegimes: 2},
		PromotionThresholds: []validation.Threshold{{Metric: "after_cost_return", Op: ">", Value: 0}, {Metric: "benchmark_relative_return", Op: ">", Value: 0}, {Metric: "coverage", Op: ">=", Value: 1}, {Metric: "max_drawdown", Op: "<=", Value: .2}, {Metric: "stressed_after_cost_return", Op: ">", Value: 0}},
		RollbackThresholds:  []validation.Threshold{{Metric: "max_drawdown", Op: ">=", Value: .2}}, RequiredElapsed: map[string]time.Duration{},
		Artifacts:        validation.ArtifactLinks{Metrics: "metrics.json", Trades: "trades.json", Curves: "curves.json", Cohorts: "cohorts.json", Factors: "factors.json", Coverage: "coverage.json", Comparison: "stage05-comparison-references-sha256:" + comparisonDigest},
		Reproduce:        validation.ReproductionInvocation{Command: "go", Args: []string{"run", "./cmd/researchlab", "-stage07-track", "v4", "-stage07-mode", "run", "-clone-marker-file", "<clone-marker-file>", "-ledger-file", "<private-attempt-ledger>", "-reviewed-code-sha", driverSHA, "-stage07-plan-file", "<reviewed-plan-file>", "-stage07-plan-sha256", "<reviewed-plan-sha256>"}, Env: map[string]string{"BACKTEST_CODE_REVISION": driverSHA, "DATABASE_URL_FILE": "<clone-runtime-dsn-file>", "STAGE08_NEW_BACKTEST": "research"}},
		ResearchOverride: &validation.ResearchOverride{Mode: "retrospective_exploratory_walk_forward", BoundedTo: validation.Interval{Start: start, End: end}, Reason: "entry threshold was selected on this dataset; this run measures chronological stability and cannot authorize promotion"},
	}
	manifest, err := validation.NewManifest(spec, time.Now().UTC())
	if err != nil {
		return spec, err
	}
	return manifest.Spec, nil
}

func validateStage07V4Plan(plan stage07V4Plan, opts stage07Options) error {
	if plan.SchemaVersion != stage07V4PlanVersion || plan.SourceCodeSHA != stage07V4SourceSHA || plan.DriverCodeSHA != opts.DriverSHA || strings.TrimSpace(plan.Creator) == "" || len(plan.IdempotencyKey) < 8 || len(plan.IdempotencyKey) > 120 {
		return fmt.Errorf("v4 Stage 07 plan identity is invalid")
	}
	ids := make([]uint, len(plan.SourceReferences))
	for i := range plan.SourceReferences {
		ids[i] = plan.SourceReferences[i].Comparison.JobID
	}
	refs, digest, params, err := loadStage07V4References(ids)
	if err != nil {
		return err
	}
	want, err := buildStage07V4Spec(refs, params, opts.DriverSHA, digest)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(refs, plan.SourceReferences) || digest != plan.ComparisonDigest || !reflect.DeepEqual(want, plan.Spec) {
		return fmt.Errorf("reviewed v4 Stage 07 plan differs from live audited sources or pinned derivation")
	}
	return nil
}

func runStage07V4Mode(opts stage07Options) error {
	if opts.Mode != "prepare" && opts.Mode != "run" {
		return fmt.Errorf("Stage 07 v4 mode must be prepare or run")
	}
	ledger, err := openLedger(opts.LedgerFile)
	if err != nil {
		return err
	}
	defer ledger.Close()
	if opts.Mode == "prepare" {
		if opts.OldManifestFile != "" || opts.SourceJobIDs == "" || opts.Creator == "" || len(opts.IdempotencyKey) < 8 || len(opts.IdempotencyKey) > 120 || opts.PlanSHA256 != "" {
			return fmt.Errorf("v4 prepare requires sources, creator, fresh key, no prior manifest, and no plan SHA")
		}
		ids, err := parseStage07V4SourceIDs(opts.SourceJobIDs)
		if err != nil {
			return err
		}
		refs, digest, params, err := loadStage07V4References(ids)
		if err != nil {
			return err
		}
		spec, err := buildStage07V4Spec(refs, params, opts.DriverSHA, digest)
		if err != nil {
			return err
		}
		plan := stage07V4Plan{SchemaVersion: stage07V4PlanVersion, SourceCodeSHA: stage07V4SourceSHA, DriverCodeSHA: opts.DriverSHA, Creator: opts.Creator, IdempotencyKey: opts.IdempotencyKey, SourceReferences: refs, ComparisonDigest: digest, Spec: spec}
		if err := validateStage07V4Plan(plan, opts); err != nil {
			return err
		}
		manifest, err := validation.NewManifest(spec, time.Now().UTC())
		if err != nil {
			return err
		}
		if _, _, err := (backtest.Stage07ExperimentSource{DB: database.DB}).Load(manifest); err != nil {
			return fmt.Errorf("Stage 07 v4 end-to-end source preflight: %w", err)
		}
		raw, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return err
		}
		raw = append(raw, '\n')
		file, err := os.OpenFile(opts.PlanFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, writeErr := file.Write(raw)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		if closeErr := file.Close(); writeErr == nil {
			writeErr = closeErr
		}
		if writeErr != nil {
			return writeErr
		}
		planSHA := fmt.Sprintf("%x", sha256.Sum256(raw))
		if err := appendRecord(ledger, map[string]any{"at": time.Now().UTC(), "status": "stage07_v4_plan_prepared", "plan_sha256": planSHA, "source_job_ids": ids, "comparison_digest": digest, "family_id": spec.FamilyID, "source_code_sha": stage07V4SourceSHA, "driver_code_sha": opts.DriverSHA, "study_type": "exploratory", "promotion_authorized": false}); err != nil {
			return err
		}
		fmt.Printf("{\"status\":\"stage07_v4_plan_prepared\",\"plan_sha256\":%q,\"comparison_digest\":%q}\n", planSHA, digest)
		return nil
	}
	if opts.OldManifestFile != "" || opts.SourceJobIDs != "" || opts.Creator != "" || opts.IdempotencyKey != "" {
		return fmt.Errorf("v4 run accepts only the exact reviewed plan")
	}
	raw, err := readStage07PrivateFile(opts.PlanFile)
	if err != nil {
		return err
	}
	if err := verifyStage07PlanSHA(raw, opts.PlanSHA256); err != nil {
		return err
	}
	var plan stage07V4Plan
	if err := decodeStage07JSON(raw, &plan); err != nil {
		return err
	}
	if err := validateStage07V4Plan(plan, opts); err != nil {
		return err
	}
	manifest, err := validation.NewManifest(plan.Spec, time.Now().UTC())
	if err != nil {
		return err
	}
	if _, _, err := (backtest.Stage07ExperimentSource{DB: database.DB}).Load(manifest); err != nil {
		return fmt.Errorf("Stage 07 v4 source preflight: %w", err)
	}
	if err := appendRecord(ledger, map[string]any{"at": time.Now().UTC(), "status": "stage07_v4_execution_intent", "plan_sha256": opts.PlanSHA256, "content_id": manifest.ContentID, "source_job_ids": plan.Spec.FoldSourceJobIDs, "family_id": plan.Spec.FamilyID, "promotion_authorized": false}); err != nil {
		return err
	}
	repo := validation.Repository{DB: database.DB}
	var existing database.ValidationExperiment
	if err := database.DB.Where("idempotency_key=?", plan.IdempotencyKey).First(&existing).Error; err == nil {
		return fmt.Errorf("Stage 07 v4 idempotency key already registered")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	first := plan.Spec.FoldSourceJobIDs[0]
	created, err := repo.CreateManifestAuthenticated(manifest, &first, &plan.ComparisonDigest, plan.Creator, plan.IdempotencyKey)
	if err != nil {
		return err
	}
	evidence, runErr := (validation.JobService{Repository: repo, Source: backtest.Stage07ExperimentSource{DB: database.DB}}).Run(created.ID)
	status := "stage07_v4_completed"
	if runErr != nil {
		status = "stage07_v4_failed"
	}
	if err := appendRecord(ledger, map[string]any{"at": time.Now().UTC(), "status": status, "experiment_id": created.ID, "evidence_id": evidence.ID, "evidence_status": evidence.Status, "failure": evidence.Failure, "promotion_authorized": false}); err != nil {
		return err
	}
	if runErr != nil {
		return runErr
	}
	fmt.Printf("{\"experiment_id\":%q,\"evidence_id\":%q,\"status\":%q,\"promotion_authorized\":false}\n", created.ID, evidence.ID, evidence.Status)
	return nil
}

func cloneStage07Strings(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
