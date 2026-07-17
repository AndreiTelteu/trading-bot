package validation

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"trading-go/internal/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MaxEvidenceBytes = 2 << 20

type Repository struct{ DB *gorm.DB }

func (r Repository) CreateManifest(manifest ExperimentManifest, backtestJobID *uint, comparisonDigest *string) (ExperimentManifest, error) {
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
	row := database.ValidationExperiment{ID: manifest.ID, ContentID: manifest.ContentID, SchemaVersion: manifest.Spec.SchemaVersion, ContentJSON: string(content), ContentDigest: manifest.ContentDigest, CreatedAt: manifest.CreatedAt, BacktestJobID: backtestJobID, ComparisonDigest: comparisonDigest}
	err = r.DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoNothing: true}).Create(&row).Error
	if err != nil {
		return ExperimentManifest{}, err
	}
	return r.LoadManifest(manifest.ID)
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
			if err := ValidateFoldMetrics(fold.Metrics, manifest.Spec.Samples); err != nil {
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
		return nil
	})
	if err != nil {
		return PersistedEvidence{}, err
	}
	return r.LoadEvidence(id)
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
