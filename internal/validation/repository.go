package validation

import (
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
		stage08Context = flags.ObservationContext("stage07_validation", map[string]string{"strategy": manifest.Spec.Candidate.ID + "@" + manifest.Spec.Candidate.Version, "model": manifest.Spec.Model.Version, "policy": manifest.Spec.Policies.Composite, "dataset": manifest.Spec.DatasetManifestID, "universe": manifest.Spec.UniversePolicy})
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

func (r Repository) PersistEvidence(manifestID string, result *WalkForwardResult, failure error, createdAt time.Time) (PersistedEvidence, error) {
	manifest, err := r.LoadManifest(manifestID)
	if err != nil {
		return PersistedEvidence{}, err
	}
	createdAt = createdAt.UTC()
	if createdAt.IsZero() {
		return PersistedEvidence{}, fmt.Errorf("evidence creation time is required")
	}
	status := "passed"
	var diagnostic *DiagnosticError
	if failure != nil {
		status = "failed"
		if !errors.As(failure, &diagnostic) {
			diagnostic = &DiagnosticError{Code: DiagnosticInvalidManifest, Details: failure.Error()}
		}
		result = nil
	}
	if result != nil && (result.ExperimentID != manifest.ID || result.SchemaVersion != EvidenceSchemaVersion) {
		return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "result is not bound to experiment"}
	}
	if result != nil {
		if len(result.Folds) != len(manifest.Spec.Folds) {
			return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "fold evidence does not cover the immutable manifest"}
		}
		for i, fold := range result.Folds {
			if fold.Fold != manifest.Spec.Folds[i] || fold.Frozen.FoldIndex != fold.Fold.Index {
				return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "fold identity or frozen decision mismatch"}
			}
			derived, deriveErr := DeriveFoldMetrics(fold.Primitives)
			if deriveErr != nil {
				return PersistedEvidence{}, deriveErr
			}
			storedMetrics, _ := json.Marshal(fold.Metrics)
			derivedMetrics, _ := json.Marshal(derived)
			if string(storedMetrics) != string(derivedMetrics) {
				return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "fold metrics do not reproduce from immutable primitives"}
			}
			if err := ValidateFoldMetrics(derived, manifest.Spec.Samples); err != nil {
				return PersistedEvidence{}, err
			}
		}
		recomputed, evalErr := Evaluate(result.Folds, manifest.Spec)
		if evalErr != nil {
			return PersistedEvidence{}, evalErr
		}
		storedAggregate, _ := json.Marshal(result.Aggregate)
		recomputedAggregate, _ := json.Marshal(recomputed)
		if string(storedAggregate) != string(recomputedAggregate) {
			return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "aggregate metrics do not reproduce from immutable folds"}
		}
	}
	payload := struct {
		SchemaVersion string             `json:"schema_version"`
		ExperimentID  string             `json:"experiment_id"`
		Status        string             `json:"status"`
		Result        *WalkForwardResult `json:"result,omitempty"`
		Failure       *DiagnosticError   `json:"failure,omitempty"`
	}{EvidenceSchemaVersion, manifest.ID, status, result, diagnostic}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return PersistedEvidence{}, err
	}
	if len(encoded) > MaxEvidenceBytes {
		return PersistedEvidence{}, fmt.Errorf("validation evidence exceeds 2 MiB limit")
	}
	evidenceDigest := digest(encoded)
	id := digest([]byte(manifest.ID + "\n" + evidenceDigest))
	err = r.DB.Transaction(func(tx *gorm.DB) error {
		if result != nil {
			for _, fold := range result.Folds {
				foldBytes, e := json.Marshal(fold)
				if e != nil {
					return e
				}
				fd, e := fold.Frozen.Digest()
				if e != nil {
					return e
				}
				row := database.ValidationFoldEvidence{ExperimentID: manifest.ID, FoldIndex: fold.Fold.Index, SchemaVersion: EvidenceSchemaVersion, Status: status, FrozenDigest: fd, EvidenceJSON: string(foldBytes), EvidenceDigest: digest(foldBytes), CreatedAt: createdAt}
				res := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "experiment_id"}, {Name: "fold_index"}}, DoNothing: true}).Create(&row)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					var existing database.ValidationFoldEvidence
					if e := tx.Where("experiment_id=? AND fold_index=?", manifest.ID, fold.Fold.Index).First(&existing).Error; e != nil {
						return e
					}
					if existing.EvidenceDigest != row.EvidenceDigest {
						return &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "idempotent fold retry has different content"}
					}
				}
			}
		}
		row := database.ValidationEvidence{ID: id, ExperimentID: manifest.ID, SchemaVersion: EvidenceSchemaVersion, Status: status, EvidenceJSON: string(encoded), EvidenceDigest: evidenceDigest, CreatedAt: createdAt}
		res := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "experiment_id"}}, DoNothing: true}).Create(&row)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			var existing database.ValidationEvidence
			if e := tx.Where("experiment_id=?", manifest.ID).First(&existing).Error; e != nil {
				return e
			}
			if existing.ID != id || existing.EvidenceDigest != evidenceDigest {
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
	var row database.ValidationEvidence
	if err := r.DB.Where("id=?", id).First(&row).Error; err != nil {
		return PersistedEvidence{}, err
	}
	if len(row.EvidenceJSON) > MaxEvidenceBytes {
		return PersistedEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "evidence exceeds bounded load size"}
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

func IsNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
