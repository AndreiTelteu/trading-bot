package operations

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"trading-go/internal/cutover"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

type failingAlert struct{}

func (failingAlert) Dispatch(context.Context, database.OperationalIncident) error {
	return errors.New("channel unavailable")
}

func stage08DB(t *testing.T) (Service, string) {
	t.Helper()
	db := testutil.OpenPostgresDB(t)
	testutil.ResetPublicSchema(t, db)
	if err := database.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	database.DB = db
	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	flags := cutover.SafeFlags()
	service := New(db, flags)
	snapshot, err := service.Initialize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return service, snapshot.ID
}

func TestStage08MigrationIncidentAndCutoverAudit(t *testing.T) {
	service, flagID := stage08DB(t)
	db := service.DB
	for _, model := range []any{&database.Stage08FlagSnapshot{}, &database.ParityObservation{}, &database.ParityAcceptancePolicy{}, &database.OperationalIncident{}, &database.CutoverState{}, &database.CutoverTransition{}, &database.BackfillPlan{}, &database.BackupVerification{}} {
		if !db.Migrator().HasTable(model) {
			t.Fatalf("missing Stage 08 table %T", model)
		}
	}
	first, err := service.RaiseIncident(context.Background(), IncidentInput{DedupeKey: "ledger:test", Type: "reconciliation_break", Severity: "critical", Summary: "broken", Details: map[string]any{"bounded": "detail"}})
	if err != nil {
		t.Fatal(err)
	}
	alertService := service
	alertService.Alerts = failingAlert{}
	delivered, err := alertService.RaiseIncident(context.Background(), IncidentInput{DedupeKey: "alert:test", Type: "missing_benchmark_or_universe", Severity: "warning", Summary: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.First(&delivered, "id=?", delivered.ID).Error; err != nil {
		t.Fatal(err)
	}
	if delivered.LastDeliveryState != "failed" || delivered.LastDeliveryError == "" {
		t.Fatalf("delivery failure was hidden: %+v", delivered)
	}
	second, err := service.RaiseIncident(context.Background(), IncidentInput{DedupeKey: "ledger:test", Type: "reconciliation_break", Severity: "critical", Summary: "still broken"})
	if err != nil || second.ID != first.ID || second.Occurrences != 2 {
		t.Fatalf("incident dedupe failed: %+v %v", second, err)
	}
	if _, err := service.TransitionIncident(context.Background(), first.ID, "acknowledged", "operator", "investigating"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.TransitionIncident(context.Background(), first.ID, "resolved", "operator", "reconciled"); err != nil {
		t.Fatal(err)
	}
	var audits int64
	db.Model(&database.OperationalIncidentAudit{}).Count(&audits)
	if audits != 2 {
		t.Fatalf("incident audits=%d", audits)
	}
	r := TransitionRequest{IdempotencyKey: "cutover-1", ToStage: "ledger_compare", Principal: "operator", Reason: "approved", FlagSnapshotID: flagID, Prerequisites: map[string]bool{"schema_deployed": true}}
	transition, err := service.TransitionCutover(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.TransitionCutover(context.Background(), r)
	if err != nil || again.ID != transition.ID {
		t.Fatalf("idempotent transition failed: %+v %v", again, err)
	}
	r.Reason = "different"
	if _, err := service.TransitionCutover(context.Background(), r); err == nil {
		t.Fatal("same key/different payload accepted")
	}
	rollback, err := service.TransitionCutover(context.Background(), TransitionRequest{IdempotencyKey: "rollback-1", ToStage: "schema_legacy", Principal: "operator", Reason: "rollback", FlagSnapshotID: flagID, Rollback: true})
	if err != nil || rollback.RollbackOf == nil {
		t.Fatalf("rollback failed: %+v %v", rollback, err)
	}
	status := service.Status(context.Background())
	if status.Status != "degraded" || status.SchemaVersion != StatusSchemaVersion || status.Flags != service.Flags {
		t.Fatalf("unexpected fail-closed status: %+v", status)
	}
}

func TestParityPersistenceThresholdsAndBounds(t *testing.T) {
	service, flagID := stage08DB(t)
	policy := database.ParityAcceptancePolicy{ID: strings.Repeat("a", 64), SchemaVersion: cutover.ParitySchemaVersion, Name: "predeclared", MinimumSamples: 2, MinimumCoverageBPS: 10000, MaxActionRateBPS: 0, MaxQuantityRateBPS: 0, MaxReasonRateBPS: 0, MaxVersionRateBPS: 0, QuantityToleranceBPS: 1, NotionalToleranceBPS: 1, ExpectedReasonsJSON: "[]", ContentDigest: strings.Repeat("b", 64), DeclaredBy: "operator", DeclaredAt: time.Now().UTC()}
	if err := service.DB.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	base := cutover.Comparison{ContextID: "ctx-1", LegacyDigest: strings.Repeat("1", 64), CandidateDigest: strings.Repeat("1", 64), ContentDigest: strings.Repeat("2", 64), Classification: "match"}
	if _, err := service.PersistParity(context.Background(), "legacy:new", flagID, base, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PersistParity(context.Background(), "legacy:new", flagID, base, time.Now()); err != nil {
		t.Fatal(err)
	}
	aggregate, err := service.EvaluateParity(policy.ID, 2)
	if err != nil || aggregate.Accepted || aggregate.Failure != "insufficient_samples" {
		t.Fatalf("insufficient samples passed: %+v %v", aggregate, err)
	}
	base.ContextID = "ctx-2"
	base.ContentDigest = strings.Repeat("3", 64)
	if _, err := service.PersistParity(context.Background(), "legacy:new", flagID, base, time.Now()); err != nil {
		t.Fatal(err)
	}
	aggregate, err = service.EvaluateParity(policy.ID, 2)
	if err != nil || !aggregate.Accepted {
		t.Fatalf("matching threshold failed: %+v %v", aggregate, err)
	}
}

func TestBackfillPlanApprovalDigestAndRetry(t *testing.T) {
	db := testutil.OpenPostgresDB(t)
	testutil.ResetPublicSchema(t, db)
	if err := database.RunMigrations(db); err != nil {
		t.Fatal(err)
	}
	database.DB = db
	now := time.Now().UTC()
	wallet := database.Wallet{AccountID: "primary", Balance: 50, Currency: "USDT", CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&wallet).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.LedgerMigrationState{AccountID: "primary", Status: "pending_approval", UnresolvedJSON: "[]", CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	service := New(db, cutover.SafeFlags())
	plan, err := service.PlanBackfill(context.Background(), "primary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ApproveBackfill(context.Background(), plan.ID, "wrong", "operator"); err == nil {
		t.Fatal("wrong digest approved")
	}
	approved, err := service.ApproveBackfill(context.Background(), plan.ID, plan.ReportDigest, "operator")
	if err != nil {
		t.Fatal(err)
	}
	applied, err := service.ApplyBackfill(context.Background(), plan.ID, *approved.ApprovalDigest)
	if err != nil || applied.Status != "applied" {
		t.Fatalf("apply failed: %+v %v", applied, err)
	}
	retry, err := service.ApplyBackfill(context.Background(), plan.ID, *approved.ApprovalDigest)
	if err != nil || retry.Status != "applied" {
		t.Fatalf("retry failed: %+v %v", retry, err)
	}
	var events int64
	db.Model(&database.LedgerEvent{}).Count(&events)
	if events != 1 {
		t.Fatalf("events duplicated: %d", events)
	}
}
