package validation

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"trading-go/internal/cutover"
	"trading-go/internal/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxEvidenceBytes = 2 << 20

type Repository struct{ DB *gorm.DB }

// Stage08ObservationContext is the immutable observation envelope shared by
// experiment registration and execution-job creation.  A rule-only candidate
// has no learned model; record that fact explicitly instead of dereferencing a
// nil optional model or silently omitting the version from the audit context.
func Stage08ObservationContext(flags cutover.Flags, manifest ExperimentManifest) string {
	modelVersion := "none"
	if manifest.Spec.Model != nil {
		modelVersion = manifest.Spec.Model.Version
	}
	return flags.ObservationContext("stage07_validation", map[string]string{
		"strategy": manifest.Spec.Candidate.ID + "@" + manifest.Spec.Candidate.Version,
		"model":    modelVersion,
		"policy":   manifest.Spec.Policies.Composite,
		"dataset":  manifest.Spec.DatasetManifestID,
		"universe": manifest.Spec.UniversePolicy,
	})
}

func (r Repository) CreateManifest(manifest ExperimentManifest, backtestJobID *uint, comparisonDigest *string) (ExperimentManifest, error) {
	return r.CreateManifestAuthenticated(manifest, backtestJobID, comparisonDigest, "system", "")
}

func (r Repository) CreateManifestAuthenticated(manifest ExperimentManifest, backtestJobID *uint, comparisonDigest *string, creator, idempotencyKey string) (ExperimentManifest, error) {
	if r.DB == nil {
		return ExperimentManifest{}, fmt.Errorf("validation repository database is required")
	}
	if err := manifest.Verify(); err != nil {
		return ExperimentManifest{}, err
	}
	content, err := json.Marshal(manifest.Spec)
	if err != nil {
		return ExperimentManifest{}, err
	}
	creator, idempotencyKey = strings.TrimSpace(creator), strings.TrimSpace(idempotencyKey)
	if creator == "" {
		return ExperimentManifest{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "creator"}
	}
	if idempotencyKey != "" && (len(idempotencyKey) < 8 || len(idempotencyKey) > 120) {
		return ExperimentManifest{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "idempotency_key"}
	}
	var key *string
	if idempotencyKey != "" {
		key = &idempotencyKey
	}
	stage08Context := "{}"
	if flags, active := cutover.Active(); active {
		stage08Context = Stage08ObservationContext(flags, manifest)
	}
	row := database.ValidationExperiment{ID: manifest.ID, ContentID: manifest.ContentID, SchemaVersion: manifest.Spec.SchemaVersion, ContentJSON: string(content), ContentDigest: manifest.ContentDigest, CreatedAt: manifest.CreatedAt, BacktestJobID: backtestJobID, ComparisonDigest: comparisonDigest, AuthorityPolicyDigest: manifest.Spec.AuthorityPolicy.Digest, Stage08ContextJSON: stage08Context, CreatedBy: creator, IdempotencyKey: key}
	err = r.DB.Transaction(func(tx *gorm.DB) error {
		if key != nil {
			if e := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", *key).Error; e != nil {
				return e
			}
			var existing database.ValidationExperiment
			if e := tx.Where("idempotency_key=?", *key).First(&existing).Error; e == nil {
				if existing.ContentID != manifest.ContentID {
					return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "idempotency_key", Details: "key reused for different semantic content"}
				}
				row = existing
				return nil
			} else if !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
		}
		created := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&row)
		if e := created.Error; e != nil {
			return e
		}
		if created.RowsAffected == 0 {
			return nil
		}
		return r.registerResearchAttempt(tx, manifest)
	})
	if err != nil {
		return ExperimentManifest{}, err
	}
	return r.LoadManifest(row.ID)
}

func (r Repository) LoadManifest(id string) (ExperimentManifest, error) {
	var row database.ValidationExperiment
	if err := r.DB.Where("id = ?", id).First(&row).Error; err != nil {
		return ExperimentManifest{}, err
	}
	var spec ManifestSpec
	if len(row.ContentJSON) > MaxEvidenceBytes {
		return ExperimentManifest{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "manifest exceeds bounded load size"}
	}
	if err := json.Unmarshal([]byte(row.ContentJSON), &spec); err != nil {
		return ExperimentManifest{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: err.Error()}
	}
	manifest := ExperimentManifest{ID: row.ID, ContentID: row.ContentID, ContentDigest: row.ContentDigest, CreatedAt: row.CreatedAt.UTC(), Spec: spec}
	if err := manifest.Verify(); err != nil {
		return ExperimentManifest{}, err
	}
	if row.AuthorityPolicyDigest != manifest.Spec.AuthorityPolicy.Digest {
		return ExperimentManifest{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "stored authority policy digest mismatch"}
	}
	return manifest, nil
}

type PersistedEvidence struct {
	ID           string             `json:"id"`
	ExperimentID string             `json:"experiment_id"`
	Status       string             `json:"status"`
	Result       *WalkForwardResult `json:"result,omitempty"`
	Failure      *DiagnosticError   `json:"failure,omitempty"`
	CreatedAt    time.Time          `json:"created_at"`
}

// prewriteResultValidationError identifies validation of a supplied result
// before any evidence transaction starts. Database, manifest-load, conflict,
// and post-commit errors must never be treated as failed research results.
type prewriteResultValidationError struct{ cause error }

func (e *prewriteResultValidationError) Error() string { return e.cause.Error() }
func (e *prewriteResultValidationError) Unwrap() error { return e.cause }

func resultValidationError(err error) error {
	return &prewriteResultValidationError{cause: err}
}

func validateResultForPersistence(manifest ExperimentManifest, result *WalkForwardResult) error {
	if result == nil {
		return resultValidationError(&DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "result is required"})
	}
	if result.ExperimentID != manifest.ID || result.SchemaVersion != EvidenceSchemaVersion {
		return resultValidationError(&DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "result is not bound to experiment"})
	}
	if len(result.Folds) != len(manifest.Spec.Folds) {
		return resultValidationError(&DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "fold evidence does not cover the immutable manifest"})
	}
	for i, fold := range result.Folds {
		if fold.Fold != manifest.Spec.Folds[i] || fold.Frozen.FoldIndex != fold.Fold.Index {
			return resultValidationError(&DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "fold identity or frozen decision mismatch"})
		}
		derived, err := DeriveFoldMetrics(fold.Primitives)
		if err != nil {
			return resultValidationError(err)
		}
		storedMetrics, err := json.Marshal(fold.Metrics)
		if err != nil {
			return resultValidationError(err)
		}
		derivedMetrics, err := json.Marshal(derived)
		if err != nil {
			return resultValidationError(err)
		}
		if string(storedMetrics) != string(derivedMetrics) {
			return resultValidationError(&DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "fold metrics do not reproduce from immutable primitives"})
		}
		if err := ValidateFoldMetrics(derived, manifest.Spec.Samples); err != nil {
			return resultValidationError(err)
		}
	}
	recomputed, err := Evaluate(result.Folds, manifest.Spec)
	if err != nil {
		return resultValidationError(err)
	}
	storedAggregate, err := json.Marshal(result.Aggregate)
	if err != nil {
		return resultValidationError(err)
	}
	recomputedAggregate, err := json.Marshal(recomputed)
	if err != nil {
		return resultValidationError(err)
	}
	if string(storedAggregate) != string(recomputedAggregate) {
		return resultValidationError(&DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "aggregate metrics do not reproduce from immutable folds"})
	}
	return nil
}

// HasCompletedOutcome is a read-only guard against replaying a finished job.
// The unique immutable outcome constraint remains the concurrent-write guard.
func (r Repository) HasCompletedOutcome(manifestID string) (bool, error) {
	if r.DB == nil {
		return false, fmt.Errorf("validation repository database is required")
	}
	var outcome database.ResearchAttemptOutcome
	err := r.DB.Select("id").Where("experiment_id=?", manifestID).Take(&outcome).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (r Repository) PersistEvidence(manifestID string, result *WalkForwardResult, failure error, createdAt time.Time) (PersistedEvidence, error) {
	if (result == nil) == (failure == nil) {
		return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Details: "exactly one of result or failure is required"}
	}
	manifest, err := r.LoadManifest(manifestID)
	if err != nil {
		return PersistedEvidence{}, err
	}
	createdAt = createdAt.UTC()
	if createdAt.IsZero() {
		return PersistedEvidence{}, fmt.Errorf("evidence creation time is required")
	}
	prepared, err := prepareEvidenceForPersistence(manifest, result, failure)
	if err != nil {
		return PersistedEvidence{}, err
	}
	id, evidenceDigest, status := prepared.ID, prepared.RootDigest, prepared.Status
	err = r.DB.Transaction(func(tx *gorm.DB) error {
		if result != nil {
			for _, fold := range prepared.Folds {
				row := database.ValidationFoldEvidence{ExperimentID: manifest.ID, FoldIndex: fold.Ref.FoldIndex, SchemaVersion: EvidenceSchemaVersion, Status: status, FrozenDigest: fold.Ref.FrozenDigest, EvidenceJSON: string(fold.Bytes), EvidenceDigest: fold.Ref.EvidenceDigest, CreatedAt: createdAt}
				res := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "experiment_id"}, {Name: "fold_index"}}, DoNothing: true}).Create(&row)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					var existing database.ValidationFoldEvidence
					if e := tx.Where("experiment_id=? AND fold_index=?", manifest.ID, fold.Ref.FoldIndex).First(&existing).Error; e != nil {
						return e
					}
					if existing.EvidenceDigest != row.EvidenceDigest || existing.FrozenDigest != row.FrozenDigest || existing.SchemaVersion != row.SchemaVersion || existing.Status != row.Status {
						return &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "idempotent fold retry has different content"}
					}
				}
			}
		}
		row := database.ValidationEvidence{ID: id, ExperimentID: manifest.ID, SchemaVersion: CompactEvidenceRootSchemaVersion, Status: status, EvidenceJSON: string(prepared.RootBytes), EvidenceDigest: evidenceDigest, CreatedAt: createdAt}
		res := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "experiment_id"}}, DoNothing: true}).Create(&row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			var existing database.ValidationEvidence
			if e := tx.Where("experiment_id=?", manifest.ID).First(&existing).Error; e != nil {
				return e
			}
			if existing.ID != id || existing.EvidenceDigest != evidenceDigest || existing.SchemaVersion != row.SchemaVersion || existing.Status != row.Status {
				return &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "experiment already has different immutable evidence"}
			}
		}
		var attempt database.ResearchExperimentAttempt
		if e := tx.Where("experiment_id=?", manifest.ID).First(&attempt).Error; e != nil {
			return e
		}
		outcomePayload := struct {
			AttemptID  string `json:"attempt_id"`
			EvidenceID string `json:"evidence_id"`
			Status     string `json:"status"`
		}{attempt.ID, id, status}
		outcomeBytes, e := json.Marshal(outcomePayload)
		if e != nil {
			return e
		}
		outcome := database.ResearchAttemptOutcome{ID: digest([]byte(attempt.ID + "\n" + id)), AttemptID: attempt.ID, ExperimentID: manifest.ID, EvidenceID: id, Status: status, ContentDigest: digest(outcomeBytes), CreatedAt: createdAt}
		res = tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "attempt_id"}}, DoNothing: true}).Create(&outcome)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			var existing database.ResearchAttemptOutcome
			if e := tx.Where("attempt_id=?", attempt.ID).First(&existing).Error; e != nil {
				return e
			}
			if existing.EvidenceID != id || existing.Status != status {
				return &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "attempt outcome was replaced"}
			}
		}
		return nil
	})
	if err != nil {
		return PersistedEvidence{}, err
	}
	return r.LoadEvidence(id)
}

func (r Repository) registerResearchAttempt(tx *gorm.DB, manifest ExperimentManifest) error {
	familyContent, err := json.Marshal(struct {
		Candidate      string               `json:"candidate"`
		Implementation ImplementationDigest `json:"implementation"`
		Dataset        DatasetDigest        `json:"dataset"`
		Policy         string               `json:"policy"`
	}{manifest.Spec.Candidate.ID, manifest.Spec.Candidate.ImplementationDigest, manifest.Spec.DatasetDigest, manifest.Spec.Policies.Composite})
	if err != nil {
		return err
	}
	familyDigest := digest(familyContent)
	family := database.ResearchExperimentFamily{ID: manifest.Spec.FamilyID, ContentJSON: string(familyContent), ContentDigest: familyDigest, CreatedAt: manifest.CreatedAt}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&family)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var existing database.ResearchExperimentFamily
		if err := tx.Where("id=?", family.ID).First(&existing).Error; err != nil {
			return err
		}
		if existing.ContentDigest != familyDigest {
			return &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "research family scope differs"}
		}
	}
	candidateBytes, _ := json.Marshal(manifest.Spec.Candidate)
	attempt := database.ResearchExperimentAttempt{ID: digest([]byte(manifest.Spec.FamilyID + "\n" + manifest.ID)), FamilyID: manifest.Spec.FamilyID, ExperimentID: manifest.ID, CandidateDigest: digest(candidateBytes), ContentDigest: manifest.ContentDigest, CreatedAt: manifest.CreatedAt}
	res = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&attempt)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var existing database.ResearchExperimentAttempt
		if err := tx.Where("experiment_id=?", manifest.ID).First(&existing).Error; err != nil {
			return err
		}
		if existing.ID != attempt.ID || existing.FamilyID != attempt.FamilyID || existing.ContentDigest != attempt.ContentDigest {
			return &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "attempt identity differs on retry"}
		}
	}
	if manifest.Spec.StudyType != "confirmatory" {
		return nil
	}
	h := manifest.Spec.ConfirmatoryHoldout
	holdout := database.ResearchConfirmatoryHoldout{ID: h.ID, FamilyID: manifest.Spec.FamilyID, DatasetDigest: string(h.DatasetDigest), StartAt: h.Interval.Start.UTC(), EndAt: h.Interval.End.UTC(), ContentDigest: h.ID, LockedAt: manifest.CreatedAt}
	res = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&holdout)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var existing database.ResearchConfirmatoryHoldout
		if err := tx.Where("family_id=?", holdout.FamilyID).First(&existing).Error; err != nil {
			return err
		}
		if existing.ID != holdout.ID || existing.DatasetDigest != holdout.DatasetDigest || !existing.StartAt.Equal(holdout.StartAt) || !existing.EndAt.Equal(holdout.EndAt) {
			return &DiagnosticError{Code: DiagnosticHoldoutReuse, Details: "family has a different locked confirmatory holdout"}
		}
	}
	use := database.ResearchConfirmatoryHoldoutUse{ID: digest([]byte(h.ID + "\n" + manifest.ID)), HoldoutID: h.ID, ExperimentID: manifest.ID, CreatedAt: manifest.CreatedAt}
	res = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&use)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var existing database.ResearchConfirmatoryHoldoutUse
		if err := tx.Where("holdout_id=?", h.ID).First(&existing).Error; err != nil {
			return err
		}
		if existing.ExperimentID != manifest.ID {
			return &DiagnosticError{Code: DiagnosticHoldoutReuse, Details: "locked confirmatory holdout has already been consumed"}
		}
	}
	return nil
}

func (r Repository) LoadEvidence(id string) (PersistedEvidence, error) {
	var rootSize struct{ EvidenceBytes int64 }
	if err := r.DB.Table("validation_evidences").Select("octet_length(evidence_json::text) AS evidence_bytes").Where("id=?", id).Take(&rootSize).Error; err != nil {
		return PersistedEvidence{}, err
	}
	if rootSize.EvidenceBytes < 0 || rootSize.EvidenceBytes > 2*MaxEvidenceBytes {
		return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "evidence exceeds bounded load size"}
	}
	var row database.ValidationEvidence
	if err := r.DB.Where("id=?", id).First(&row).Error; err != nil {
		return PersistedEvidence{}, err
	}
	if len(row.EvidenceJSON) > MaxEvidenceBytes {
		return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "evidence exceeds bounded load size"}
	}
	if row.SchemaVersion == CompactEvidenceRootSchemaVersion {
		return r.loadCompactEvidence(row)
	}
	var payload struct {
		SchemaVersion string             `json:"schema_version"`
		ExperimentID  string             `json:"experiment_id"`
		Status        string             `json:"status"`
		Result        *WalkForwardResult `json:"result,omitempty"`
		Failure       *DiagnosticError   `json:"failure,omitempty"`
	}
	if err := json.Unmarshal([]byte(row.EvidenceJSON), &payload); err != nil {
		return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: err.Error()}
	}
	canonical, marshalErr := json.Marshal(payload)
	if marshalErr != nil || digest(canonical) != row.EvidenceDigest || digest([]byte(row.ExperimentID+"\n"+row.EvidenceDigest)) != row.ID {
		return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "stored evidence digest mismatch"}
	}
	if payload.SchemaVersion != EvidenceSchemaVersion || payload.ExperimentID != row.ExperimentID || payload.Status != row.Status {
		return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "evidence envelope mismatch"}
	}
	return PersistedEvidence{ID: row.ID, ExperimentID: row.ExperimentID, Status: row.Status, Result: payload.Result, Failure: payload.Failure, CreatedAt: row.CreatedAt.UTC()}, nil
}

func (r Repository) loadCompactEvidence(row database.ValidationEvidence) (PersistedEvidence, error) {
	var loaded PersistedEvidence
	err := r.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		loaded, err = (Repository{DB: tx}).loadCompactEvidenceSnapshot(row)
		return err
	}, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	return loaded, err
}

func (r Repository) loadCompactEvidenceSnapshot(row database.ValidationEvidence) (PersistedEvidence, error) {
	bad := func(detail string) (PersistedEvidence, error) {
		return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: detail}
	}
	var root compactEvidenceRoot
	if err := json.Unmarshal([]byte(row.EvidenceJSON), &root); err != nil {
		return bad(err.Error())
	}
	canonical, err := json.Marshal(root)
	if err != nil || digest(canonical) != row.EvidenceDigest || digest([]byte(row.ExperimentID+"\n"+row.EvidenceDigest)) != row.ID || root.ExperimentID != row.ExperimentID || root.Status != row.Status || root.SchemaVersion != row.SchemaVersion {
		return bad("stored compact evidence digest or identity mismatch")
	}
	manifest, err := r.LoadManifest(row.ExperimentID)
	if err != nil {
		return PersistedEvidence{}, err
	}
	// Fetch only bounded metadata first. jsonb::text can add whitespace, so its
	// SQL read guard is twice the strict canonical 16 MiB fold cap.
	type foldSize struct {
		FoldIndex     int
		EvidenceBytes int64
	}
	var sizes []foldSize
	if err := r.DB.Table("validation_fold_evidences").Select("fold_index, octet_length(evidence_json::text) AS evidence_bytes").Where("experiment_id=?", row.ExperimentID).Order("fold_index").Limit(len(manifest.Spec.Folds) + 1).Scan(&sizes).Error; err != nil {
		return PersistedEvidence{}, err
	}
	if len(sizes) > len(manifest.Spec.Folds) {
		return bad("extra compact fold rows")
	}
	var textTotal int64
	for _, size := range sizes {
		if size.EvidenceBytes < 0 || size.EvidenceBytes > 2*MaxFoldEvidenceBytes || textTotal > 2*MaxTotalFoldEvidenceBytes-size.EvidenceBytes {
			return bad("compact fold row exceeds bounded load size")
		}
		textTotal += size.EvidenceBytes
	}
	if root.Result != nil && len(sizes) != len(root.Result.Folds) {
		return bad("missing compact fold rows")
	}
	if root.Result == nil && len(sizes) != 0 {
		return bad("failed compact evidence has fold rows")
	}
	rows := make([]database.ValidationFoldEvidence, 0, len(sizes))
	for _, size := range sizes {
		var fold database.ValidationFoldEvidence
		if err := r.DB.Where("experiment_id=? AND fold_index=? AND octet_length(evidence_json::text) <= ?", row.ExperimentID, size.FoldIndex, 2*MaxFoldEvidenceBytes).First(&fold).Error; err != nil {
			return PersistedEvidence{}, err
		}
		if !fold.CreatedAt.UTC().Equal(row.CreatedAt.UTC()) {
			return bad("compact fold creation time mismatch")
		}
		rows = append(rows, fold)
	}
	evidence, err := hydratePreparedEvidence(manifest, []byte(row.EvidenceJSON), rows)
	if err != nil {
		return PersistedEvidence{}, err
	}
	evidence.ID, evidence.CreatedAt = row.ID, row.CreatedAt.UTC()
	return evidence, nil
}

func IsNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
