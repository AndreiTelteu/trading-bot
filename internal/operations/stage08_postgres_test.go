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

	"gorm.io/gorm"
)

type failingAlert struct{}

func (failingAlert) Dispatch(context.Context, database.OperationalIncident) error {
	return errors.New("channel unavailable")
}

type fixedParityAdapter struct{ outcome cutover.DecisionOutcome }

func (a fixedParityAdapter) Decide(context.Context, cutover.NonCapitalMode, cutover.DecisionContext, cutover.SubmitDenyBroker) (cutover.DecisionOutcome, error) {
	return a.outcome, nil
}

func genuineParity(t *testing.T, contextID string, at time.Time, policy cutover.ComparisonPolicy) cutover.Comparison {
	t.Helper()
	outcome := cutover.DecisionOutcome{Action: "skip", SymbolID: "asset", VenueSymbol: "AAAUSDT", Quantity: "0", Notional: "0", SignalAt: at, DecisionAt: at}
	comparison, err := cutover.RunParity(context.Background(), cutover.DecisionContext{ContextID: contextID, SymbolID: "asset", VenueSymbol: "AAAUSDT", MarketAt: at, DecisionAt: at}, fixedParityAdapter{outcome}, fixedParityAdapter{outcome}, policy)
	if err != nil {
		t.Fatal(err)
	}
	return comparison
}

func stage08DB(t *testing.T) (Service, string) {
	t.Helper()
	cutover.ResetForTest()
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
	if err == nil {
		t.Fatal("dispatcher failure was not returned after durable incident persistence")
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
	r := TransitionRequest{IdempotencyKey: "cutover-1", ToStage: "ledger_compare", Principal: "operator", Reason: "approved", FlagSnapshotID: flagID}
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
	policy, err := service.DeclareParityPolicy(context.Background(), DeclareParityPolicyRequest{Name: "predeclared", MinimumSamples: 2, MinimumCoverageBPS: 10000, QuantityToleranceBPS: 1, NotionalToleranceBPS: 1}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	ctx1, ctx2 := strings.Repeat("1", 64), strings.Repeat("2", 64)
	population, comparisonPolicy, err := service.BeginParityPopulation(context.Background(), "legacy:new", policy.ID, flagID, []string{ctx1, ctx2}, time.Now().Add(-time.Minute), time.Now(), "dataset-v1", "universe-v1")
	if err != nil {
		t.Fatal(err)
	}
	base := genuineParity(t, ctx1, time.Now().UTC(), comparisonPolicy)
	if _, err := service.PersistParityBound(context.Background(), ParityBinding{PopulationID: population.ID}, base, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PersistParityBound(context.Background(), ParityBinding{PopulationID: population.ID}, base, time.Now()); err != nil {
		t.Fatal(err)
	}
	aggregate, err := service.EvaluateParity(population.ID)
	if err != nil || aggregate.Accepted || aggregate.Failure != "insufficient_samples" {
		t.Fatalf("insufficient samples passed: %+v %v", aggregate, err)
	}
	base = genuineParity(t, ctx2, time.Now().UTC(), comparisonPolicy)
	if _, err := service.PersistParityBound(context.Background(), ParityBinding{PopulationID: population.ID}, base, time.Now()); err != nil {
		t.Fatal(err)
	}
	aggregate, err = service.EvaluateParity(population.ID)
	if err != nil || !aggregate.Accepted {
		t.Fatalf("matching threshold failed: %+v %v", aggregate, err)
	}
}

func TestParityPopulationRejectsForgedPolicyDigest(t *testing.T) {
	service, flagID := stage08DB(t)
	forged := database.ParityAcceptancePolicy{ID: strings.Repeat("f", 64), SchemaVersion: cutover.ParitySchemaVersion, Name: "forged", MinimumSamples: 1, MinimumCoverageBPS: 1, ExpectedReasonsJSON: "[]", ContentDigest: strings.Repeat("f", 64), DeclaredBy: "attacker", DeclaredAt: time.Now().UTC()}
	if err := service.DB.Create(&forged).Error; err != nil {
		t.Fatal(err)
	}
	contextID := strings.Repeat("1", 64)
	if _, _, err := service.BeginParityPopulation(context.Background(), "legacy:new", forged.ID, flagID, []string{contextID}, time.Now().Add(-time.Minute), time.Now(), "dataset", "universe"); err == nil {
		t.Fatal("caller-forged parity policy was accepted")
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
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL trading_bot.ledger_write='on'").Error; err != nil {
			return err
		}
		return tx.Create(&wallet).Error
	}); err != nil {
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

func TestStage08JSONBNormalizationPreservesContentAddressedEvidence(t *testing.T) {
	service, flagID := stage08DB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	row, err := service.DeclarePrerequisiteEvidence(context.Background(), PrerequisiteEvidenceRequest{
		EvidenceType:   "schema_deployed",
		TargetStage:    "ledger_compare",
		FlagSnapshotID: flagID,
		WindowStart:    now.Add(-2 * time.Minute),
		WindowEnd:      now.Add(-time.Minute),
		Payload: map[string]any{
			"context":  map[string]any{"versions": map[string]any{"strategy": "v1", "policy": "p1"}, "active_path": "legacy"},
			"coverage": []any{map[string]any{"symbol": "BTCUSDT", "complete": true}},
		},
	}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	var loaded database.CutoverPrerequisiteEvidence
	if err := service.DB.First(&loaded, "id=?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyPrerequisiteEvidenceIntegrity(service.DB, loaded); err != nil {
		t.Fatalf("jsonb-normalized evidence failed digest verification: %v payload=%s", err, loaded.PayloadJSON)
	}
	if loaded.ContentDigest != row.ContentDigest {
		t.Fatalf("content digest changed across jsonb persistence: %s != %s", loaded.ContentDigest, row.ContentDigest)
	}
}
