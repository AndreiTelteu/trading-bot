package governance

import (
	"errors"
	"sync"
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
	"trading-go/internal/validation"

	"gorm.io/gorm"
)

type fixedClock struct{ at time.Time }

func (c *fixedClock) Now() time.Time { return c.at }

func TestStage07GovernanceProgressionApprovalRollbackAndConcurrency(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	clock := &fixedClock{at: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}
	service := NewService(db)
	service.Clock = clock
	admin := NewTrustedPrincipal("admin@example.test", CapabilityResearch, CapabilityApprove, CapabilityTransition, CapabilityRollback)
	manifest, evidence := persistPassingEvidence(t, db, clock.at, nil)
	contextKey := "strategy:" + manifest.Spec.Candidate.ID + "@" + manifest.Spec.Candidate.Version
	research := TransitionRequest{IdempotencyKey: "research", ContextKey: contextKey, ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: StateResearch, ArtifactVersion: manifest.Spec.Candidate.Version, PolicyVersion: manifest.Spec.Policies.Composite, FallbackVersion: "baseline-v1", Reason: "begin governed research"}
	if _, err := service.Transition(admin, research); err != nil {
		t.Fatal(err)
	}
	if replay, err := service.Transition(admin, research); err != nil || replay.IdempotencyKey != research.IdempotencyKey {
		t.Fatalf("idempotent research replay=%+v err=%v", replay, err)
	}
	forgedReplay := research
	forgedReplay.Reason = "different"
	if _, err := service.Transition(admin, forgedReplay); err == nil {
		t.Fatal("mismatched idempotency replay passed")
	} else {
		code(err, CodeIntegrity, t)
	}
	illegal := research
	illegal.IdempotencyKey = "illegal-skip"
	illegal.TargetState = StatePaper
	if _, err := service.Transition(admin, illegal); err == nil {
		t.Fatal("research skipped directly to paper")
	} else {
		code(err, CodeIllegalTransition, t)
	}
	shadow := research
	shadow.IdempotencyKey = "shadow-no-approval"
	shadow.TargetState = StateShadow
	_, err := service.Transition(admin, shadow)
	code(err, CodeApprovalRequired, t)
	stale := database.GovernanceApproval{IdempotencyKey: "stale", ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: string(StateShadow), ArtifactVersion: manifest.Spec.Candidate.Version, PolicyVersion: manifest.Spec.Policies.Composite, Approver: "human", Reason: "stale", ApprovedAt: evidence.CreatedAt.Add(-time.Second)}
	stale.ContentDigest = approvalDigest(stale)
	stale.ID = hash([]byte(stale.ContentDigest))
	if err := db.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	shadow.ApprovalID = stale.ID
	shadow.IdempotencyKey = "shadow-stale"
	if _, err := service.Transition(admin, shadow); err == nil {
		t.Fatal("stale approval passed")
	} else {
		code(err, CodeApprovalMismatch, t)
	}
	approval, err := service.Approve(admin, ApprovalRequest{IdempotencyKey: "approve-shadow", ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: StateShadow, ArtifactVersion: manifest.Spec.Candidate.Version, PolicyVersion: manifest.Spec.Policies.Composite, Reason: "reviewed immutable evidence", ExpiresAt: clock.at.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	shadow.IdempotencyKey = "shadow"
	shadow.ApprovalID = approval.ID
	if _, err := service.Transition(admin, shadow); err != nil {
		t.Fatal(err)
	}
	// Two callers cannot both advance the locked deployment.
	clock.at = clock.at.Add(2 * time.Hour)
	elapsed, err := service.RecordMonitoringEvidence(admin, MonitoringEvidenceRequest{ContextKey: contextKey, ExperimentID: manifest.ID, WindowStart: clock.at.Add(-2 * time.Hour), WindowEnd: clock.at, ExpectedObservations: 10, ObservedObservations: 10, Metrics: map[string]float64{"max_drawdown": .05}})
	if err != nil {
		t.Fatal(err)
	}
	paperApproval, err := service.Approve(admin, ApprovalRequest{IdempotencyKey: "approve-paper", ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: StatePaper, ArtifactVersion: manifest.Spec.Candidate.Version, PolicyVersion: manifest.Spec.Policies.Composite, Reason: "paper approval", ExpiresAt: clock.at.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	paper := shadow
	paper.TargetState = StatePaper
	paper.ApprovalID = paperApproval.ID
	paper.ElapsedEvidenceID = elapsed.ID
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"paper-a", "paper-b"} {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			request := paper
			request.IdempotencyKey = k
			_, e := service.Transition(admin, request)
			errs <- e
		}(key)
	}
	wg.Wait()
	close(errs)
	success, failed := 0, 0
	for e := range errs {
		if e == nil {
			success++
		} else {
			failed++
		}
	}
	if success != 1 || failed != 1 {
		t.Fatalf("concurrent promotions success=%d failed=%d", success, failed)
	}
	clock.at = clock.at.Add(time.Hour)
	monitor, err := service.RecordMonitoringEvidence(admin, MonitoringEvidenceRequest{ContextKey: contextKey, ExperimentID: manifest.ID, WindowStart: clock.at.Add(-time.Hour), WindowEnd: clock.at, ExpectedObservations: 10, ObservedObservations: 10, Metrics: map[string]float64{"max_drawdown": .25}})
	if err != nil {
		t.Fatal(err)
	}
	rollback, err := service.Rollback(admin, RollbackRequest{IdempotencyKey: "rollback", ContextKey: contextKey, ExperimentID: manifest.ID, EvidenceID: evidence.ID, FallbackVersion: "baseline-v1", Reason: "drawdown threshold crossed", MonitoringEvidenceID: monitor.ID})
	if err != nil {
		t.Fatal(err)
	}
	if rollback.ToState != string(StateRollback) {
		t.Fatalf("rollback=%+v", rollback)
	}
	var deployment database.GovernanceDeployment
	if err := db.Where("context_key=?", contextKey).First(&deployment).Error; err != nil {
		t.Fatal(err)
	}
	if deployment.ArtifactVersion != "baseline-v1" || deployment.State != string(StateRollback) {
		t.Fatalf("deployment=%+v", deployment)
	}
	if err := db.Model(&database.GovernanceDeployment{}).Where("context_key=?", contextKey).Update("state", string(StateFullLive)).Error; err == nil {
		t.Fatal("direct deployment update bypassed transition guard")
	}
	if err := db.Model(&database.GovernanceTransition{}).Where("id=?", rollback.ID).Update("reason", "tampered").Error; err == nil {
		t.Fatal("immutable transition was mutated")
	}
	var history int64
	if err := db.Model(&database.GovernanceTransition{}).Where("context_key=?", contextKey).Count(&history).Error; err != nil {
		t.Fatal(err)
	}
	if history != 4 {
		t.Fatalf("audit history=%d", history)
	}
	if _, err := service.Rollback(admin, RollbackRequest{IdempotencyKey: "rollback-no-gate", ContextKey: contextKey, ExperimentID: manifest.ID, EvidenceID: evidence.ID, FallbackVersion: "baseline-v1", Reason: "not crossed", MonitoringEvidenceID: monitor.ID}); err == nil {
		t.Fatal("rollback without crossed threshold passed")
	}
}

func TestBootstrapArtifactCannotEnterPaper(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	clock := &fixedClock{at: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)}
	model := &validation.ModelAuthority{Version: "fixture-v1", Class: validation.ArtifactContractFixture, ModelDigest: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", FeatureSpec: "features-v1", Features: []validation.FeatureField{{Name: "x", Type: "float64"}}, LabelSpec: "label-v1", LabelHorizon: time.Hour, CodeRevision: "rev", DatasetManifest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TrainingManifest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", PolicyVersion: "policy-v1", Seed: 7}
	manifest, evidence := persistPassingEvidence(t, db, clock.at, model)
	service := NewService(db)
	service.Clock = clock
	admin := NewTrustedPrincipal("admin", CapabilityApprove, CapabilityTransition, CapabilityRollback)
	contextKey := "model:fixture-v1"
	research := TransitionRequest{IdempotencyKey: "r", ContextKey: contextKey, ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: StateResearch, ArtifactVersion: "fixture-v1", PolicyVersion: "policy-v1", FallbackVersion: "rule-v1", Reason: "research"}
	if _, err := service.Transition(admin, research); err != nil {
		t.Fatal(err)
	}
	_, approvalErr := service.Approve(admin, ApprovalRequest{IdempotencyKey: "as", ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: StateShadow, ArtifactVersion: "fixture-v1", PolicyVersion: "policy-v1", Reason: "shadow", ExpiresAt: clock.at.Add(time.Hour)})
	code(approvalErr, CodeEvidenceFailed, t)
	shadow := research
	shadow.IdempotencyKey = "s"
	shadow.TargetState = StateShadow
	if _, err := service.Transition(admin, shadow); err == nil {
		t.Fatal("fixture entered paper")
	} else {
		code(err, CodeEvidenceFailed, t)
	}
}

func TestConcurrentFirstGovernanceContextCreatesExactlyOneInitialTransition(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	clock := &fixedClock{at: time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)}
	service := NewService(db)
	service.Clock = clock
	manifest, evidence := persistPassingEvidence(t, db, clock.at, nil)
	principal := NewTrustedPrincipal("operator", CapabilityTransition)
	base := TransitionRequest{ContextKey: "strategy:" + manifest.Spec.Candidate.ID + "@" + manifest.Spec.Candidate.Version, ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: StateResearch, ArtifactVersion: manifest.Spec.Candidate.Version, PolicyVersion: manifest.Spec.Policies.Composite, FallbackVersion: "baseline-v1", Reason: "initial"}
	errorsChannel := make(chan error, 2)
	var wait sync.WaitGroup
	for _, key := range []string{"initial-a", "initial-b"} {
		wait.Add(1)
		go func(key string) {
			defer wait.Done()
			request := base
			request.IdempotencyKey = key
			_, err := service.Transition(principal, request)
			errorsChannel <- err
		}(key)
	}
	wait.Wait()
	close(errorsChannel)
	success := 0
	for err := range errorsChannel {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("initial transitions succeeded=%d", success)
	}
	var count int64
	db.Model(&database.GovernanceTransition{}).Where("context_key=?", base.ContextKey).Count(&count)
	if count != 1 {
		t.Fatalf("initial audit rows=%d", count)
	}
}

func persistPassingEvidence(t *testing.T, gormDB *gorm.DB, created time.Time, model *validation.ModelAuthority) (validation.ExperimentManifest, validation.PersistedEvidence) {
	t.Helper()
	base := created.Add(-1000 * time.Hour)
	modelVersion, featureSchema := "none", "none"
	if model != nil {
		modelVersion, featureSchema = model.Version, model.FeatureSpec
	}
	authority, err := validation.NewAuthorityPolicyEnvelope(map[string]string{"selection_top_k": "1", "selection_min_probability": ".6", "selection_min_ev": ".01", "fallback_mode": "baseline-v1", "strategy_parameters": "params", "risk_policy": "r", "turnover_policy": "t", "cash_policy": "cash", "universe_policy": "u", "execution_policy": "e", "cost_policy": "c", "model_version": modelVersion, "feature_schema": featureSchema, "rollout_state": "research"})
	if err != nil {
		t.Fatal(err)
	}
	spec := validation.ManifestSpec{SchemaVersion: validation.ManifestSchemaVersion, StudyType: "confirmatory", CodeRevision: "rev", Candidate: validation.VersionRef{ID: "candidate", Version: "candidate-v1", Digest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, Baseline: validation.VersionRef{ID: "baseline", Version: "baseline-v1", Digest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"}, Model: model, Policies: validation.PolicyBundle{Composite: "policy-v1", Execution: "e", Universe: "u", ModelSelection: "m", EntrySelection: "i", PortfolioRisk: "r", Rollout: "o", Cost: "c"}, GovernancePolicy: validation.GovernancePolicyVersion, AuthorityPolicy: authority, DatasetManifestID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", DatasetManifestHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", UniversePolicy: "u", Interval: validation.Interval{Start: base, End: base.Add(90 * time.Hour)}, DecisionClock: "4h", ExecutionClock: "next", Seed: 2, ExecutionSemantics: map[string]string{"fee_bps": "1", "slippage_bps": "1", "timing": "next", "liquidity": "bars"}, FeatureHorizon: time.Hour, LabelHorizon: time.Hour, Purge: time.Hour, Embargo: time.Hour, AllowedTuning: map[string][]string{"x": {"1"}}, Metrics: append([]string(nil), validation.RequiredConfirmatoryMetrics...), StatisticalUnit: "chronological_test_window", BootstrapIterations: 100, Samples: validation.SampleRequirements{MinFolds: 3, MinIndependentUnits: 3, MinObservationsPerFold: 10, MinTradesPerFold: 2, MinRegimes: 2}, PromotionThresholds: []validation.Threshold{{Metric: "after_cost_return", Op: ">", Value: -1}}, RollbackThresholds: []validation.Threshold{{Metric: "max_drawdown", Op: ">=", Value: .2}}, RequiredElapsed: map[string]time.Duration{"paper": time.Hour, "limited_live": time.Hour, "full_live": time.Hour}, Artifacts: validation.ArtifactLinks{Metrics: "m", Trades: "t", Curves: "c", Cohorts: "h", Factors: "f", Coverage: "v", Comparison: "x"}, Reproduce: validation.ReproductionInvocation{Command: "validate", Args: []string{"run"}}}
	if model != nil {
		requirements := validation.MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6, MinAUC: .6, MaxLogLoss: .8, MaxBrier: .25, MaxCalibrationError: .3}
		spec.MLRequirements = &requirements
	}
	for i := 0; i < 3; i++ {
		start := base.Add(time.Duration(i*30) * time.Hour)
		spec.Folds = append(spec.Folds, validation.Fold{Index: i, Train: validation.Interval{Start: start, End: start.Add(10 * time.Hour)}, Validation: validation.Interval{Start: start.Add(10 * time.Hour), End: start.Add(15 * time.Hour)}, Test: validation.Interval{Start: start.Add(15 * time.Hour), End: start.Add(20 * time.Hour)}})
		spec.FoldSourceJobIDs = append(spec.FoldSourceJobIDs, uint(i+1))
	}
	manifest, err := validation.NewManifest(spec, created)
	if err != nil {
		t.Fatal(err)
	}
	repo := validation.Repository{DB: gormDB}
	manifest, err = repo.CreateManifest(manifest, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	folds := []validation.FoldResult{}
	for _, fold := range manifest.Spec.Folds {
		at := fold.Test.Start.Add(time.Hour)
		primitives := validation.FoldPrimitives{StartingCapital: 1000, ExpectedObservations: 10, ObservedObservations: 10, Trades: []validation.TradePrimitive{{ID: "a", Symbol: "A", Regime: "on", OpenedAt: at, ClosedAt: at.Add(time.Minute), Notional: 50, GrossPnL: 2.6, Cost: .1, NetPnL: 2.5}, {ID: "b", Symbol: "A", Regime: "off", OpenedAt: at.Add(2 * time.Minute), ClosedAt: at.Add(3 * time.Minute), Notional: 50, GrossPnL: 2.6, Cost: .1, NetPnL: 2.5}, {ID: "c", Symbol: "B", Regime: "on", OpenedAt: at.Add(4 * time.Minute), ClosedAt: at.Add(5 * time.Minute), Notional: 50, GrossPnL: 2.6, Cost: .1, NetPnL: 2.5}, {ID: "d", Symbol: "B", Regime: "off", OpenedAt: at.Add(6 * time.Minute), ClosedAt: at.Add(7 * time.Minute), Notional: 50, GrossPnL: 2.6, Cost: .1, NetPnL: 2.5}}, Curve: []validation.CurvePrimitive{{At: at, Equity: 1000, Benchmark: 1000, GrossExposure: .5, NetExposure: .5}, {At: at.Add(time.Hour), Equity: 1010, Benchmark: 1005, GrossExposure: .5, NetExposure: .5}}}
		metrics, deriveErr := validation.DeriveFoldMetrics(primitives)
		if deriveErr != nil {
			t.Fatal(deriveErr)
		}
		folds = append(folds, validation.FoldResult{Fold: fold, Frozen: validation.FrozenDecision{FoldIndex: fold.Index, Choice: "1", Parameters: map[string]string{"x": "1"}, FitDigest: "fit", SelectionDigest: "select", ArtifactDigest: "artifact"}, Primitives: primitives, Metrics: metrics})
	}
	aggregate, err := validation.Evaluate(folds, manifest.Spec)
	if err != nil {
		t.Fatal(err)
	}
	result := &validation.WalkForwardResult{SchemaVersion: validation.EvidenceSchemaVersion, ExperimentID: manifest.ID, Folds: folds, Aggregate: aggregate}
	evidence, err := repo.PersistEvidence(manifest.ID, result, nil, created)
	if err != nil {
		t.Fatal(err)
	}
	return manifest, evidence
}

func code(err error, want Code, t *testing.T) {
	t.Helper()
	var target *Error
	if !errors.As(err, &target) || target.Code != want {
		t.Fatalf("got %T %v want %s", err, err, want)
	}
}
