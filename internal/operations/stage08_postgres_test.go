package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

func parityContext(symbol string, at time.Time) cutover.DecisionContext {
	return cutover.DecisionContext{SymbolID: symbol, VenueSymbol: strings.ToUpper(symbol) + "USDT", MarketAt: at, DecisionAt: at, DatasetVersion: "dataset-v1", UniverseVersion: "universe-v1"}
}

func genuineParity(t *testing.T, captured cutover.DecisionContext, policy cutover.ComparisonPolicy) cutover.Comparison {
	t.Helper()
	outcome := cutover.DecisionOutcome{Action: "skip", SymbolID: captured.SymbolID, VenueSymbol: captured.VenueSymbol, Quantity: "0", Notional: "0", DatasetVersion: captured.DatasetVersion, UniverseVersion: captured.UniverseVersion, SignalAt: captured.DecisionAt, DecisionAt: captured.DecisionAt}
	comparison, err := cutover.RunParity(context.Background(), captured, fixedParityAdapter{outcome}, fixedParityAdapter{outcome}, policy)
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

func enterResearchIngestion(t *testing.T, service Service) database.Stage08FlagSnapshot {
	t.Helper()
	flags := cutover.SafeFlags()
	flags.NewBacktest, flags.PointInTime = "research", "research"
	snapshot, err := service.DeclareFlagSnapshot(context.Background(), flags, "operator")
	if err != nil {
		t.Fatal(err)
	}
	request := TransitionRequest{IdempotencyKey: "enter-research-ingestion", ToStage: "research_ingestion", Principal: "operator", Reason: "approve manifest-backed research ingestion", FlagSnapshotID: snapshot.ID}
	transition, err := service.TransitionCutover(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if transition.ToAuthority != "legacy" {
		t.Fatalf("research ingestion changed capital authority: %+v", transition)
	}
	return snapshot
}

func TestInitializeCreatesExactBootstrapTransitionSentinel(t *testing.T) {
	service, flagID := stage08DB(t)
	var state database.CutoverState
	var snapshot database.Stage08FlagSnapshot
	var sentinel database.CutoverTransition
	if err := service.DB.First(&state, 1).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&snapshot, "id=?", flagID).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&sentinel, "id=?", strings.Repeat("0", 64)).Error; err != nil {
		t.Fatal(err)
	}
	if state.TransitionID != sentinel.ID || sentinel.IdempotencyKey != "stage08-bootstrap-sentinel-v1" || sentinel.FromStage != "schema_legacy" || sentinel.ToStage != state.Stage || sentinel.FromAuthority != "legacy" || sentinel.ToAuthority != state.Authority || sentinel.FlagSnapshotID != snapshot.ID || sentinel.FlagSnapshotDigest != snapshot.ContentDigest || sentinel.SourceStateVersion != 0 || sentinel.SourceEnvelopeDigest != state.AuthorityDigest || sentinel.TargetEnvelopeDigest != state.AuthorityDigest || !canonicalJSONEqual(sentinel.SourceEnvelopeJSON, state.AuthorityJSON) || !canonicalJSONEqual(sentinel.TargetEnvelopeJSON, state.AuthorityJSON) || sentinel.ContentDigest != sentinel.ID || sentinel.Principal != "system:stage08-bootstrap" || sentinel.Reason != "deterministic initial legacy authority bootstrap" || sentinel.PrerequisitesJSON != "[]" {
		t.Fatalf("bootstrap sentinel is not canonical: %+v", sentinel)
	}
	if _, err := service.Initialize(context.Background()); err != nil {
		t.Fatalf("repeat initialize failed: %v", err)
	}
	var count int64
	if err := service.DB.Model(&database.CutoverTransition{}).Where("id=?", sentinel.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("bootstrap sentinel was not idempotent: count=%d err=%v", count, err)
	}
	if err := service.DB.Exec("ALTER TABLE cutover_transitions DISABLE TRIGGER cutover_transitions_immutable").Error; err != nil {
		t.Fatal(err)
	}
	defer service.DB.Exec("ALTER TABLE cutover_transitions ENABLE TRIGGER cutover_transitions_immutable")
	if err := service.DB.Delete(&sentinel).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.InitializeFromPersistedAuthority(context.Background()); err == nil {
		t.Fatal("missing bootstrap sentinel was accepted")
	}
}

func persistedNonDefaultAuthority(t *testing.T) (Service, database.Stage08FlagSnapshot, database.CutoverState) {
	t.Helper()
	service, _ := stage08DB(t)
	flags := cutover.SafeFlags()
	flags.LedgerAuthority, flags.SharedEngine, flags.DualRun = "compare", "shadow", "observe"
	snapshot, err := service.DeclareFlagSnapshot(context.Background(), flags, "operator")
	if err != nil {
		t.Fatal(err)
	}
	state := database.CutoverState{}
	if err := service.DB.First(&state, 1).Error; err != nil {
		t.Fatal(err)
	}
	envelope, envelopeDigest := authorityEnvelope("shared_shadow", "legacy", snapshot.ID, snapshot.ContentDigest, "", "")
	transitionID := strings.Repeat("d", 64)
	transition := database.CutoverTransition{ID: transitionID, IdempotencyKey: "persisted-authority-fixture", FromStage: "ledger_compare", ToStage: "shared_shadow", FromAuthority: "legacy", ToAuthority: "legacy", FlagSnapshotID: snapshot.ID, FlagSnapshotDigest: snapshot.ContentDigest, SourceStateVersion: state.Version, SourceEnvelopeJSON: state.AuthorityJSON, SourceEnvelopeDigest: state.AuthorityDigest, TargetEnvelopeJSON: envelope, TargetEnvelopeDigest: envelopeDigest, RequestDigest: strings.Repeat("a", 64), Principal: "operator", Reason: "fixture", PrerequisitesJSON: "[]", ContentDigest: transitionID, CreatedAt: time.Now().UTC()}
	if err := service.DB.Create(&transition).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Model(&database.CutoverState{}).Where("id=1").Updates(map[string]any{"stage": "shared_shadow", "authority": "legacy", "flag_snapshot_id": snapshot.ID, "flag_digest": snapshot.ContentDigest, "authority_json": envelope, "authority_digest": envelopeDigest, "transition_id": transitionID, "version": state.Version + 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DB.First(&state, 1).Error; err != nil {
		t.Fatal(err)
	}
	return service, snapshot, state
}

func backupManifest(now time.Time) BackupVerificationManifest {
	digest, dumpChecksum, token := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 32)
	sum := sha256.Sum256([]byte(strings.Join([]string{digest, dumpChecksum, token, now.UTC().Format(time.RFC3339)}, "|")))
	return BackupVerificationManifest{SchemaVersion: "stage08-backup-verification-v2", SourceBefore: digest, SourceAfter: digest, TargetFingerprint: digest, DumpChecksum: dumpChecksum, ManifestChecksum: hex.EncodeToString(sum[:]), TargetIdentityToken: token, ToolVersions: map[string]string{"pg_dump": "test"}, VerifiedAt: now}
}

func TestPersistedAuthorityInitializationAndBackupEvidenceIgnoreLocalFlags(t *testing.T) {
	service, snapshot, state := persistedNonDefaultAuthority(t)
	conflicting := New(service.DB, cutover.SafeFlags())
	if _, err := conflicting.Initialize(context.Background()); err == nil {
		t.Fatal("normal initialization accepted a local/persisted flag mismatch")
	}
	loaded, err := conflicting.InitializeFromPersistedAuthority(context.Background())
	if err != nil || loaded.ID != snapshot.ID {
		t.Fatalf("persisted authority initialization failed: snapshot=%+v err=%v", loaded, err)
	}
	if active, id, _, ok := cutover.ActiveEvidence(); !ok || id != snapshot.ID || active != mustFlags(t, snapshot) {
		t.Fatalf("persisted authority was not activated exactly: flags=%+v id=%s active=%t", active, id, ok)
	}
	row, err := conflicting.RecordBackupVerification(context.Background(), backupManifest(time.Now().UTC().Truncate(time.Second)), "restore-operator")
	if err != nil || row.FlagSnapshotID != snapshot.ID || row.CutoverTransitionID != state.TransitionID {
		t.Fatalf("backup evidence was not bound to persisted authority: %+v %v", row, err)
	}
	if row.ID == "" || row.SourceFingerprint != row.TargetFingerprint || row.CanonicalDigest != row.SourceFingerprint || row.DumpChecksum == "" || row.FixtureMetadataJSON == "" || row.Status != "verified" || row.VerifiedAt.IsZero() || row.ManifestChecksum == "" || row.ToolVersionsJSON == "" {
		t.Fatalf("backup evidence function did not map every returned field: %+v", row)
	}
	var resultType string
	if err := service.DB.Raw(`SELECT pg_get_function_result('public.record_verified_backup_evidence(text,text,text,text,text,text,jsonb,timestamp with time zone,text,text,text)'::regprocedure)`).Scan(&resultType).Error; err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"id text", "source_fingerprint text", "fixture_metadata_json jsonb", "verified_at timestamp with time zone", "tool_versions_json jsonb", "cutover_transition_id text"} {
		if !strings.Contains(resultType, field) {
			t.Fatalf("backup evidence function result type omits exact %q: %s", field, resultType)
		}
	}
}

func mustFlags(t *testing.T, snapshot database.Stage08FlagSnapshot) cutover.Flags {
	t.Helper()
	flags, err := verifyFlagSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return flags
}

func TestPersistedAuthorityRejectsTampering(t *testing.T) {
	for name, tamper := range map[string]func(t *testing.T, db *gorm.DB, state database.CutoverState) error{
		"missing_snapshot": func(t *testing.T, db *gorm.DB, state database.CutoverState) error {
			return db.Model(&database.CutoverState{}).Where("id=1").Update("flag_snapshot_id", strings.Repeat("e", 64)).Error
		},
		"snapshot_content": func(t *testing.T, db *gorm.DB, state database.CutoverState) error {
			if err := db.Exec("ALTER TABLE stage08_flag_snapshots DISABLE TRIGGER stage08_flag_snapshots_immutable").Error; err != nil {
				return err
			}
			defer db.Exec("ALTER TABLE stage08_flag_snapshots ENABLE TRIGGER stage08_flag_snapshots_immutable")
			return db.Model(&database.Stage08FlagSnapshot{}).Where("id=?", state.FlagSnapshotID).Update("content_json", `{}`).Error
		},
		"digest": func(t *testing.T, db *gorm.DB, state database.CutoverState) error {
			return db.Model(&database.CutoverState{}).Where("id=1").Update("flag_digest", strings.Repeat("e", 64)).Error
		},
		"envelope": func(t *testing.T, db *gorm.DB, state database.CutoverState) error {
			return db.Model(&database.CutoverState{}).Where("id=1").Update("authority_digest", strings.Repeat("e", 64)).Error
		},
		"transition": func(t *testing.T, db *gorm.DB, state database.CutoverState) error {
			if err := db.Exec("ALTER TABLE cutover_transitions DISABLE TRIGGER cutover_transitions_immutable").Error; err != nil {
				return err
			}
			defer db.Exec("ALTER TABLE cutover_transitions ENABLE TRIGGER cutover_transitions_immutable")
			return db.Model(&database.CutoverTransition{}).Where("id=?", state.TransitionID).Update("flag_snapshot_digest", strings.Repeat("e", 64)).Error
		},
	} {
		t.Run(name, func(t *testing.T) {
			service, _, state := persistedNonDefaultAuthority(t)
			if err := tamper(t, service.DB, state); err != nil {
				t.Fatal(err)
			}
			if _, err := New(service.DB, cutover.SafeFlags()).InitializeFromPersistedAuthority(context.Background()); err == nil {
				t.Fatal("tampered persisted authority was accepted")
			}
		})
	}
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
	research := enterResearchIngestion(t, service)
	if _, err := New(service.DB, mustFlags(t, research)).Initialize(context.Background()); err != nil {
		t.Fatalf("research authority did not initialize: %v", err)
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
	now := time.Now().UTC()
	ctx1, ctx2 := parityContext("asset-a", now.Add(-2*time.Second)), parityContext("asset-b", now.Add(-time.Second))
	population, comparisonPolicy, err := service.BeginParityPopulation(context.Background(), "legacy:new", policy.ID, flagID, []cutover.DecisionContext{ctx1, ctx2}, now.Add(-time.Minute), now, "dataset-v1", "universe-v1")
	if err != nil {
		t.Fatal(err)
	}
	base := genuineParity(t, ctx1, comparisonPolicy)
	if _, err := service.PersistParityBound(context.Background(), ParityBinding{PopulationID: population.ID}, base, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.PersistParityBound(context.Background(), ParityBinding{PopulationID: population.ID}, base, now); err != nil {
		t.Fatal(err)
	}
	aggregate, err := service.EvaluateParity(population.ID)
	if err != nil || aggregate.Accepted || aggregate.Failure != "insufficient_samples" {
		t.Fatalf("insufficient samples passed: %+v %v", aggregate, err)
	}
	base = genuineParity(t, ctx2, comparisonPolicy)
	if _, err := service.PersistParityBound(context.Background(), ParityBinding{PopulationID: population.ID}, base, now); err != nil {
		t.Fatal(err)
	}
	aggregate, err = service.EvaluateParity(population.ID)
	if err != nil || !aggregate.Accepted {
		t.Fatalf("matching threshold failed: %+v %v", aggregate, err)
	}
	enterResearchIngestion(t, service)
	if _, err := service.TransitionCutover(context.Background(), TransitionRequest{IdempotencyKey: "new-attempt", ToStage: "ledger_compare", Principal: "operator", Reason: "start new attempt", FlagSnapshotID: flagID}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.EvaluateParity(population.ID); err == nil {
		t.Fatal("population from an earlier cutover attempt was reused")
	}
}

func TestParityPopulationRejectsForgedPolicyDigest(t *testing.T) {
	service, flagID := stage08DB(t)
	forged := database.ParityAcceptancePolicy{ID: strings.Repeat("f", 64), SchemaVersion: cutover.ParitySchemaVersion, Name: "forged", MinimumSamples: 1, MinimumCoverageBPS: 1, ExpectedReasonsJSON: "[]", ContentDigest: strings.Repeat("f", 64), DeclaredBy: "attacker", DeclaredAt: time.Now().UTC()}
	if err := service.DB.Create(&forged).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	contextValue := cutover.DecisionContext{SymbolID: "asset", VenueSymbol: "AAAUSDT", MarketAt: now, DecisionAt: now, DatasetVersion: "dataset", UniverseVersion: "universe"}
	if _, _, err := service.BeginParityPopulation(context.Background(), "legacy:new", forged.ID, flagID, []cutover.DecisionContext{contextValue}, now.Add(-time.Minute), now, "dataset", "universe"); err == nil {
		t.Fatal("caller-forged parity policy was accepted")
	}
}

func TestParityRejectsForgedObservationAndMixedBindings(t *testing.T) {
	service, flagID := stage08DB(t)
	policy, err := service.DeclareParityPolicy(context.Background(), DeclareParityPolicyRequest{Name: "strict", MinimumSamples: 1, MinimumCoverageBPS: 10000}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	first := parityContext("asset-a", now.Add(-time.Second))
	mixed := parityContext("asset-b", now.Add(-time.Second))
	mixed.DatasetVersion = "other-dataset"
	if _, _, err := service.BeginParityPopulation(context.Background(), "legacy:new", policy.ID, flagID, []cutover.DecisionContext{first, mixed}, now.Add(-time.Minute), now, "dataset-v1", "universe-v1"); err == nil {
		t.Fatal("mixed dataset population accepted")
	}
	population, _, err := service.BeginParityPopulation(context.Background(), "legacy:new", policy.ID, flagID, []cutover.DecisionContext{first}, now.Add(-time.Minute), now, "dataset-v1", "universe-v1")
	if err != nil {
		t.Fatal(err)
	}
	contextID, _ := cutover.CanonicalContextID(first)
	forged := database.ParityObservation{ID: strings.Repeat("a", 64), ContextID: contextID, PairKey: population.PairKey, SchemaVersion: cutover.ParitySchemaVersion, FlagSnapshotID: population.FlagSnapshotID, FlagSnapshotDigest: population.FlagSnapshotDigest, PolicyID: population.PolicyID, PolicyDigest: population.PolicyDigest, PopulationID: population.ID, CutoverAttemptID: population.CutoverAttemptID, LegacyDigest: strings.Repeat("b", 64), CandidateDigest: strings.Repeat("b", 64), ComparisonDigest: strings.Repeat("c", 64), ComparisonJSON: `{}`, Classification: "match", DivergenceCodesJSON: `[]`, ExpectedPolicyReasons: `[]`, CompactSampleJSON: `{}`, ContentDigest: strings.Repeat("a", 64), ObservedAt: now}
	if err := service.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_runtime").Error; err != nil {
			return err
		}
		return tx.Create(&forged).Error
	}); err == nil {
		t.Fatal("runtime database role inserted parity evidence")
	}
	if err := service.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_ledger_writer").Error; err != nil {
			return err
		}
		return tx.Create(&forged).Error
	}); err == nil {
		t.Fatal("ledger writer bypassed the dedicated parity writer boundary")
	}
	if err := service.DB.Create(&forged).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.EvaluateParity(population.ID); err == nil {
		t.Fatal("forged direct parity observation was trusted")
	}
}

func TestParityWriterHasEvidenceAuthorityWithoutLedgerAuthority(t *testing.T) {
	service, _ := stage08DB(t)
	if err := service.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_parity_writer").Error; err != nil {
			return err
		}
		var privileges struct {
			CanInsertObservations bool
			CanUpdateObservations bool
			CanInsertPopulations  bool
			CanUpdatePopulations  bool
			CanUpdateAggregates   bool
			CanInsertLedger       bool
			CanUpdatePositions    bool
			CanUpdateBackfill     bool
			CanUseAnySequence     bool
		}
		if err := tx.Raw(`SELECT
			has_table_privilege(current_user,'parity_observations','INSERT') AS can_insert_observations,
			has_table_privilege(current_user,'parity_observations','UPDATE') AS can_update_observations,
			has_table_privilege(current_user,'parity_populations','INSERT') AS can_insert_populations,
			has_table_privilege(current_user,'parity_populations','UPDATE') AS can_update_populations,
			has_table_privilege(current_user,'parity_aggregates','UPDATE') AS can_update_aggregates,
			has_table_privilege(current_user,'ledger_events','INSERT') AS can_insert_ledger,
			has_table_privilege(current_user,'positions','UPDATE') AS can_update_positions,
			has_table_privilege(current_user,'backfill_plans','UPDATE') AS can_update_backfill,
			EXISTS (
			 SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
			 WHERE n.nspname='public' AND c.relkind='S'
			   AND (has_sequence_privilege(current_user,c.oid,'USAGE') OR has_sequence_privilege(current_user,c.oid,'UPDATE'))
			) AS can_use_any_sequence`).Scan(&privileges).Error; err != nil {
			return err
		}
		if !privileges.CanInsertObservations || privileges.CanUpdateObservations || !privileges.CanInsertPopulations || privileges.CanUpdatePopulations || !privileges.CanUpdateAggregates || privileges.CanInsertLedger || privileges.CanUpdatePositions || privileges.CanUpdateBackfill || privileges.CanUseAnySequence {
			return errors.New("parity writer privilege boundary is incorrect")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeCanOnlyRecordBackupVerificationEvidenceViaFunction(t *testing.T) {
	service, _ := stage08DB(t)
	now := time.Now().UTC().Truncate(time.Second)
	digest := strings.Repeat("a", 64)
	dumpChecksum := strings.Repeat("b", 64)
	token := strings.Repeat("c", 32)
	material := strings.Join([]string{digest, dumpChecksum, token, now.Format(time.RFC3339)}, "|")
	sum := sha256.Sum256([]byte(material))
	manifest := BackupVerificationManifest{
		SchemaVersion:       "stage08-backup-verification-v2",
		SourceBefore:        digest,
		SourceAfter:         digest,
		TargetFingerprint:   digest,
		DumpChecksum:        dumpChecksum,
		ManifestChecksum:    hex.EncodeToString(sum[:]),
		TargetIdentityToken: token,
		ToolVersions:        map[string]string{"pg_dump": "test"},
		VerifiedAt:          now,
	}
	// Permission denials abort a PostgreSQL transaction. Keep the forged direct
	// write in its own transaction, prove it failed, then use a fresh runtime
	// transaction for the SECURITY DEFINER function path.
	err := service.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_runtime").Error; err != nil {
			return err
		}
		var privileges struct {
			CanInsert           bool
			CanSelect           bool
			CanUpdate           bool
			CanDelete           bool
			CanReadCutoverState bool
		}
		if err := tx.Raw(`SELECT
			has_table_privilege(current_user,'backup_verifications','INSERT') AS can_insert,
			has_table_privilege(current_user,'backup_verifications','SELECT') AS can_select,
			has_table_privilege(current_user,'backup_verifications','UPDATE') AS can_update,
			has_table_privilege(current_user,'backup_verifications','DELETE') AS can_delete,
			has_table_privilege(current_user,'cutover_states','SELECT') AS can_read_cutover_state`).Scan(&privileges).Error; err != nil {
			return err
		}
		if privileges.CanInsert || privileges.CanSelect || privileges.CanUpdate || privileges.CanDelete || !privileges.CanReadCutoverState {
			return errors.New("runtime backup verification privilege boundary is incorrect")
		}
		return tx.Exec(`INSERT INTO backup_verifications (id,source_fingerprint,dump_checksum,fixture_metadata_json,target_fingerprint,canonical_digest,status,verified_at,manifest_checksum,tool_versions_json,flag_snapshot_id,cutover_transition_id) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, strings.Repeat("f", 64), digest, dumpChecksum, `{}`, digest, digest, "verified", now, hex.EncodeToString(sum[:]), `{}`, strings.Repeat("0", 64), strings.Repeat("0", 64)).Error
	})
	if err == nil {
		t.Fatal("runtime direct forged backup evidence insert unexpectedly succeeded")
	}
	if err := service.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_runtime").Error; err != nil {
			return err
		}
		_, err := (Service{DB: tx, Flags: service.Flags}).RecordBackupVerification(context.Background(), manifest, "restore-operator")
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProductionParityWriterCreatesPopulationAndObservations(t *testing.T) {
	service, flagID := stage08DB(t)
	policy, err := service.DeclareParityPolicy(context.Background(), DeclareParityPolicyRequest{Name: "production-writer", MinimumSamples: 1, MinimumCoverageBPS: 10000}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	captured := parityContext("production-asset", now.Add(-time.Second))
	comparison := genuineParity(t, captured, cutover.ComparisonPolicy{})
	err = service.DB.Connection(func(conn *gorm.DB) error {
		if err := conn.Exec("SET ROLE trading_bot_parity_writer").Error; err != nil {
			return err
		}
		defer conn.Exec("RESET ROLE")
		production := New(conn, service.Flags)
		population, _, err := production.BeginParityPopulation(context.Background(), "legacy:production", policy.ID, flagID, []cutover.DecisionContext{captured}, now.Add(-time.Minute), now, "dataset-v1", "universe-v1")
		if err != nil {
			return err
		}
		_, err = production.PersistParityBound(context.Background(), ParityBinding{PopulationID: population.ID}, comparison, now)
		return err
	})
	if err != nil {
		t.Fatalf("production parity writer path failed: %v", err)
	}
	var populations, observations int64
	if err := service.DB.Model(&database.ParityPopulation{}).Where("pair_key=?", "legacy:production").Count(&populations).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Model(&database.ParityObservation{}).Where("pair_key=?", "legacy:production").Count(&observations).Error; err != nil {
		t.Fatal(err)
	}
	if populations != 1 || observations != 1 {
		t.Fatalf("production parity records populations=%d observations=%d", populations, observations)
	}
}

func TestPrerequisiteEvidenceTimestampIdentitySurvivesPostgresRoundTrip(t *testing.T) {
	service, flagID := stage08DB(t)
	now := time.Now().UTC().Add(-time.Second).Add(789 * time.Nanosecond)
	row, err := service.DeclarePrerequisiteEvidence(context.Background(), PrerequisiteEvidenceRequest{EvidenceType: "paper_round_trip", TargetStage: "new_paper", ContextKey: "ctx", FlagSnapshotID: flagID, WindowStart: now.Add(-time.Minute), WindowEnd: now, Payload: map[string]any{"ok": true}}, "operator")
	if err != nil {
		t.Fatal(err)
	}
	var persisted database.CutoverPrerequisiteEvidence
	if err := service.DB.First(&persisted, "id=?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := verifyPrerequisiteEvidenceIntegrity(service.DB, persisted); err != nil {
		t.Fatalf("microsecond-normalized evidence failed round trip: %v", err)
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
	testutil.WithLedgerProjectionWrites(t, db, func(tx *gorm.DB) error {
		return tx.Create(&wallet).Error
	})
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
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SET LOCAL ROLE trading_bot_ledger_writer").Error; err != nil {
			return err
		}
		return tx.Exec("SELECT finalize_applied_backfill_plan(?,?,?)", plan.ID, *approved.ApprovalDigest, time.Now().UTC()).Error
	}); err == nil {
		t.Fatal("ledger writer finalized an approved plan without same-transaction immutable backfill evidence")
	}
	applied, err := service.ApplyBackfill(context.Background(), plan.ID, *approved.ApprovalDigest)
	if err != nil || applied.Status != "applied" {
		t.Fatalf("apply failed: %+v %v", applied, err)
	}
	var state database.LedgerMigrationState
	if err := db.First(&state, "account_id=?", plan.AccountID).Error; err != nil || state.OpeningEventID == nil {
		t.Fatalf("applied backfill state lacks opening evidence: %+v %v", state, err)
	}
	var event database.LedgerEvent
	if err := db.First(&event, "id=?", *state.OpeningEventID).Error; err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"backfill_plan_id": plan.ID, "report_digest": plan.ReportDigest, "approval_digest": *approved.ApprovalDigest,
	} {
		var got string
		if err := db.Raw("SELECT metadata_json->>? FROM ledger_events WHERE id=?", key, event.ID).Scan(&got).Error; err != nil || got != want {
			t.Fatalf("backfill event %s=%q want %q err=%v", key, got, want, err)
		}
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
