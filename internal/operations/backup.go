package operations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"trading-go/internal/database"

	"gorm.io/gorm"
)

type BackupVerificationManifest struct {
	SchemaVersion, SourceBefore, SourceAfter, TargetFingerprint, DumpChecksum, ManifestChecksum, TargetIdentityToken string
	ToolVersions                                                                                                     map[string]string
	VerifiedAt                                                                                                       time.Time
}

func (s Service) RecordBackupVerification(ctx context.Context, manifest BackupVerificationManifest, principal string) (database.BackupVerification, error) {
	var out database.BackupVerification
	if principal == "" || manifest.SchemaVersion != "stage08-backup-verification-v2" || len(manifest.SourceBefore) != 64 || manifest.SourceBefore != manifest.SourceAfter || manifest.SourceBefore != manifest.TargetFingerprint || len(manifest.DumpChecksum) != 64 || len(manifest.ManifestChecksum) != 64 || len(manifest.TargetIdentityToken) < 32 || manifest.VerifiedAt.IsZero() {
		return out, fmt.Errorf("complete successful isolated backup verification manifest required")
	}
	manifestMaterial := strings.Join([]string{manifest.SourceBefore, manifest.DumpChecksum, manifest.TargetIdentityToken, manifest.VerifiedAt.UTC().Format(time.RFC3339)}, "|")
	sum := sha256.Sum256([]byte(manifestMaterial))
	if manifest.ManifestChecksum != hex.EncodeToString(sum[:]) {
		return out, fmt.Errorf("backup verification manifest checksum mismatch")
	}
	var state database.CutoverState
	if err := s.DB.WithContext(ctx).First(&state, 1).Error; err != nil {
		return out, err
	}
	tools, _ := json.Marshal(manifest.ToolVersions)
	fixture, _ := json.Marshal(map[string]string{"target_identity_token": manifest.TargetIdentityToken, "recorded_by": principal})
	idPayload := struct {
		Manifest                    BackupVerificationManifest
		Principal, Flag, Transition string
	}{manifest, principal, state.FlagSnapshotID, state.TransitionID}
	payload, _ := json.Marshal(idPayload)
	idSum := sha256.Sum256(payload)
	id := hex.EncodeToString(idSum[:])
	out = database.BackupVerification{ID: id, SourceFingerprint: manifest.SourceBefore, DumpChecksum: manifest.DumpChecksum, FixtureMetadataJSON: string(fixture), TargetFingerprint: manifest.TargetFingerprint, CanonicalDigest: manifest.SourceBefore, Status: "verified", VerifiedAt: manifest.VerifiedAt.UTC(), ManifestChecksum: manifest.ManifestChecksum, ToolVersionsJSON: string(tools), FlagSnapshotID: state.FlagSnapshotID, CutoverTransitionID: state.TransitionID}
	if err := s.DB.WithContext(ctx).Create(&out).Error; err != nil {
		return out, err
	}
	return out, nil
}

var canonicalBackupTables = []string{
	"schema_migrations", "wallets", "positions", "orders", "fills", "ledger_events", "ledger_batches", "ledger_migration_states", "broker_outcome_ingestions", "settings",
	"stage08_flag_snapshots", "parity_observations", "parity_populations", "parity_acceptance_policies",
	"operational_incidents", "operational_incident_audits", "cutover_states", "cutover_transitions",
	"cutover_prerequisite_evidences", "reconciliation_evidences", "broker_conflict_counters", "backfill_plans", "backup_verifications",
	"assets", "exchange_symbols", "tradability_intervals", "symbol_constraint_versions", "historical_bars", "dataset_manifests", "universe_snapshots", "universe_members", "backtest_jobs", "trend_analysis_histories", "validation_experiments", "validation_fold_evidences", "validation_evidences", "validation_ml_evidences",
	"governance_approvals", "governance_transitions", "governance_deployments", "governance_monitoring_evidences",
	"model_artifacts", "policy_configs", "experiment_runs", "rollout_events", "feature_snapshots", "prediction_logs", "trade_labels", "monitoring_snapshots",
}

type CanonicalDatabaseFingerprint struct {
	SchemaVersion string                               `json:"schema_version"`
	Tables        map[string]CanonicalTableFingerprint `json:"tables"`
	Digest        string                               `json:"digest"`
}
type CanonicalTableFingerprint struct {
	Count      int      `json:"count"`
	RowDigests []string `json:"row_digests"`
	Digest     string   `json:"digest"`
}

func CanonicalRowsDigest(rows map[string][]json.RawMessage) (CanonicalDatabaseFingerprint, error) {
	out := CanonicalDatabaseFingerprint{SchemaVersion: "stage08-canonical-database-v2", Tables: map[string]CanonicalTableFingerprint{}}
	tables := make([]string, 0, len(rows))
	for table := range rows {
		tables = append(tables, table)
	}
	sort.Strings(tables)
	for _, table := range tables {
		digests := make([]string, 0, len(rows[table]))
		for _, raw := range rows[table] {
			var value any
			if err := json.Unmarshal(raw, &value); err != nil {
				return out, fmt.Errorf("%s: %w", table, err)
			}
			canonical, err := json.Marshal(value)
			if err != nil {
				return out, err
			}
			sum := sha256.Sum256(canonical)
			digests = append(digests, hex.EncodeToString(sum[:]))
		}
		sort.Strings(digests)
		sum := sha256.Sum256([]byte(strings.Join(digests, "\n")))
		out.Tables[table] = CanonicalTableFingerprint{Count: len(digests), RowDigests: digests, Digest: hex.EncodeToString(sum[:])}
	}
	copyOut := out
	copyOut.Digest = ""
	payload, _ := json.Marshal(copyOut)
	sum := sha256.Sum256(payload)
	out.Digest = hex.EncodeToString(sum[:])
	return out, nil
}

func FingerprintDatabase(ctx context.Context, db *gorm.DB) (CanonicalDatabaseFingerprint, error) {
	rows := map[string][]json.RawMessage{}
	for _, table := range canonicalBackupTables {
		if !db.Migrator().HasTable(table) {
			continue
		}
		var values []string
		query := fmt.Sprintf(`SELECT to_jsonb(t)::text FROM %s t ORDER BY to_jsonb(t)::text`, table)
		if err := db.WithContext(ctx).Raw(query).Scan(&values).Error; err != nil {
			return CanonicalDatabaseFingerprint{}, err
		}
		for _, value := range values {
			rows[table] = append(rows[table], json.RawMessage(value))
		}
		if rows[table] == nil {
			rows[table] = []json.RawMessage{}
		}
	}
	return CanonicalRowsDigest(rows)
}
