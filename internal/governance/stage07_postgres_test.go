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
	manifest, evidence := persistPassingEvidence(t, db, clock.at, nil)
	contextKey := "strategy:" + manifest.Spec.Candidate.ID + "@" + manifest.Spec.Candidate.Version
	research := TransitionRequest{IdempotencyKey: "research", ContextKey: contextKey, ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: StateResearch, ArtifactVersion: manifest.Spec.Candidate.Version, PolicyVersion: manifest.Spec.Policies.Composite, FallbackVersion: "baseline-v1", Reason: "begin governed research"}
	if _, err := service.Transition(research); err != nil {
		t.Fatal(err)
	}
	if replay, err := service.Transition(research); err != nil || replay.IdempotencyKey != research.IdempotencyKey {
		t.Fatalf("idempotent research replay=%+v err=%v", replay, err)
	}
	forgedReplay := research
	forgedReplay.Reason = "different"
	if _, err := service.Transition(forgedReplay); err == nil {
		t.Fatal("mismatched idempotency replay passed")
	} else {
		code(err, CodeIntegrity, t)
	}
	illegal := research
	illegal.IdempotencyKey = "illegal-skip"
	illegal.TargetState = StatePaper
	if _, err := service.Transition(illegal); err == nil {
		t.Fatal("research skipped directly to paper")
	} else {
		code(err, CodeIllegalTransition, t)
	}
	shadow := research
	shadow.IdempotencyKey = "shadow-no-approval"
	shadow.TargetState = StateShadow
	_, err := service.Transition(shadow)
	code(err, CodeApprovalRequired, t)
	stale := database.GovernanceApproval{IdempotencyKey: "stale", ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: string(StateShadow), ArtifactVersion: manifest.Spec.Candidate.Version, PolicyVersion: manifest.Spec.Policies.Composite, Approver: "human", Reason: "stale", ApprovedAt: evidence.CreatedAt.Add(-time.Second)}
	stale.ContentDigest = approvalDigest(stale)
	stale.ID = hash([]byte(stale.ContentDigest))
	if err := db.Create(&stale).Error; err != nil {
		t.Fatal(err)
	}
	shadow.ApprovalID = stale.ID
	shadow.IdempotencyKey = "shadow-stale"
	if _, err := service.Transition(shadow); err == nil {
		t.Fatal("stale approval passed")
	} else {
		code(err, CodeApprovalMismatch, t)
	}
	approval, err := service.Approve(ApprovalRequest{IdempotencyKey: "approve-shadow", ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: StateShadow, ArtifactVersion: manifest.Spec.Candidate.Version, PolicyVersion: manifest.Spec.Policies.Composite, Approver: "human@example.test", Reason: "reviewed immutable evidence"})
	if err != nil {
		t.Fatal(err)
	}
	shadow.IdempotencyKey = "shadow"
	shadow.ApprovalID = approval.ID
	if _, err := service.Transition(shadow); err != nil {
		t.Fatal(err)
	}
	// Two callers cannot both advance the locked deployment.
	paperApproval, err := service.Approve(ApprovalRequest{IdempotencyKey: "approve-paper", ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: StatePaper, ArtifactVersion: manifest.Spec.Candidate.Version, PolicyVersion: manifest.Spec.Policies.Composite, Approver: "human@example.test", Reason: "paper approval"})
	if err != nil {
		t.Fatal(err)
	}
	paper := shadow
	paper.TargetState = StatePaper
	paper.ApprovalID = paperApproval.ID
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, key := range []string{"paper-a", "paper-b"} {
		wg.Add(1)
		go func(k string) {
			defer wg.Done()
			request := paper
			request.IdempotencyKey = k
			_, e := service.Transition(request)
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
	rollback, err := service.Rollback(RollbackRequest{IdempotencyKey: "rollback", ContextKey: contextKey, ExperimentID: manifest.ID, EvidenceID: evidence.ID, FallbackVersion: "baseline-v1", Reason: "drawdown threshold crossed", Observed: map[string]float64{"max_drawdown": .25}})
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
	var history int64
	if err := db.Model(&database.GovernanceTransition{}).Where("context_key=?", contextKey).Count(&history).Error; err != nil {
		t.Fatal(err)
	}
	if history != 4 {
		t.Fatalf("audit history=%d", history)
	}
	if _, err := service.Rollback(RollbackRequest{IdempotencyKey: "rollback-no-gate", ContextKey: contextKey, ExperimentID: manifest.ID, EvidenceID: evidence.ID, FallbackVersion: "baseline-v1", Reason: "not crossed", Observed: map[string]float64{"max_drawdown": .1}}); err == nil {
		t.Fatal("rollback without crossed threshold passed")
	}
}

func TestBootstrapArtifactCannotEnterPaper(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	clock := &fixedClock{at: time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC)}
	model := &validation.ModelAuthority{Version: "fixture-v1", Class: validation.ArtifactContractFixture, ModelDigest: "digest", FeatureSpec: "features-v1", Features: []validation.FeatureField{{Name: "x", Type: "float64"}}, LabelSpec: "label-v1", LabelHorizon: time.Hour, CodeRevision: "rev", DatasetManifest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", TrainingManifest: "train-digest", PolicyVersion: "policy-v1", Seed: 7}
	manifest, evidence := persistPassingEvidence(t, db, clock.at, model)
	service := NewService(db)
	service.Clock = clock
	contextKey := "model:fixture-v1"
	research := TransitionRequest{IdempotencyKey: "r", ContextKey: contextKey, ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: StateResearch, ArtifactVersion: "fixture-v1", PolicyVersion: "policy-v1", FallbackVersion: "rule-v1", Reason: "research"}
	if _, err := service.Transition(research); err != nil {
		t.Fatal(err)
	}
	shadowApproval, _ := service.Approve(ApprovalRequest{IdempotencyKey: "as", ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: StateShadow, ArtifactVersion: "fixture-v1", PolicyVersion: "policy-v1", Approver: "human", Reason: "shadow"})
	shadow := research
	shadow.IdempotencyKey = "s"
	shadow.TargetState = StateShadow
	shadow.ApprovalID = shadowApproval.ID
	if _, err := service.Transition(shadow); err != nil {
		t.Fatal(err)
	}
	paperApproval, _ := service.Approve(ApprovalRequest{IdempotencyKey: "ap", ExperimentID: manifest.ID, EvidenceID: evidence.ID, TargetState: StatePaper, ArtifactVersion: "fixture-v1", PolicyVersion: "policy-v1", Approver: "human", Reason: "paper"})
	paper := shadow
	paper.IdempotencyKey = "p"
	paper.TargetState = StatePaper
	paper.ApprovalID = paperApproval.ID
	if _, err := service.Transition(paper); err == nil {
		t.Fatal("fixture entered paper")
	} else {
		code(err, CodeArtifactQuarantined, t)
	}
}

func persistPassingEvidence(t *testing.T, gormDB *gorm.DB, created time.Time, model *validation.ModelAuthority) (validation.ExperimentManifest, validation.PersistedEvidence) {
	t.Helper()
	base := created.Add(-1000 * time.Hour)
	spec := validation.ManifestSpec{SchemaVersion: validation.ManifestSchemaVersion, StudyType: "confirmatory", CodeRevision: "rev", Candidate: validation.VersionRef{ID: "candidate", Version: "candidate-v1"}, Baseline: validation.VersionRef{ID: "baseline", Version: "baseline-v1"}, Model: model, Policies: validation.PolicyBundle{Composite: "policy-v1", Execution: "e", Universe: "u", ModelSelection: "m", EntrySelection: "i", PortfolioRisk: "r", Rollout: "o", Cost: "c"}, DatasetManifestID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", DatasetManifestHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", UniversePolicy: "u", Interval: validation.Interval{Start: base, End: base.Add(90 * time.Hour)}, DecisionClock: "4h", ExecutionClock: "next", Seed: 2, ExecutionSemantics: map[string]string{"fees": "1"}, FeatureHorizon: time.Hour, LabelHorizon: time.Hour, Purge: time.Hour, Embargo: time.Hour, AllowedTuning: map[string][]string{"x": {"1"}}, Metrics: []string{"after_cost_return"}, StatisticalUnit: "chronological_test_window", BootstrapIterations: 100, Samples: validation.SampleRequirements{MinFolds: 3, MinIndependentUnits: 3, MinObservationsPerFold: 2, MinTradesPerFold: 2, MinRegimes: 2}, PromotionThresholds: []validation.Threshold{{Metric: "after_cost_return", Op: ">", Value: -1}}, RollbackThresholds: []validation.Threshold{{Metric: "max_drawdown", Op: ">=", Value: .2}}, Artifacts: validation.ArtifactLinks{Metrics: "m", Trades: "t", Curves: "c", Cohorts: "h", Factors: "f", Coverage: "v", Comparison: "x"}, Reproduce: validation.ReproductionInvocation{Command: "validate", Args: []string{"run"}}}
	for i := 0; i < 3; i++ {
		start := base.Add(time.Duration(i*30) * time.Hour)
		spec.Folds = append(spec.Folds, validation.Fold{Index: i, Train: validation.Interval{Start: start, End: start.Add(10 * time.Hour)}, Validation: validation.Interval{Start: start.Add(10 * time.Hour), End: start.Add(15 * time.Hour)}, Test: validation.Interval{Start: start.Add(15 * time.Hour), End: start.Add(20 * time.Hour)}})
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
		metrics := validation.FoldMetrics{Observations: 10, Trades: 4, BenchmarkPresent: true, CoverageComplete: true, Regimes: map[string]int{"on": 2, "off": 2}, RegimeContributions: map[string]float64{"on": .0075, "off": .0025}, AfterCostExpectancy: .002, AfterCostReturn: .01, BenchmarkRelativeReturn: .005, MaxDrawdown: .05, Turnover: .2, GrossExposure: .5, NetExposure: .5, Coverage: 1, TradeContributions: map[string]float64{"a": .0025, "b": .0025, "c": .0025, "d": .0025}, SymbolContributions: map[string]float64{"A": .005, "B": .005}}
		folds = append(folds, validation.FoldResult{Fold: fold, Frozen: validation.FrozenDecision{FoldIndex: fold.Index, Choice: "1", Parameters: map[string]string{"x": "1"}, FitDigest: "fit", SelectionDigest: "select"}, Metrics: metrics})
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
