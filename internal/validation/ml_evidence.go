package validation

import (
	"encoding/json"
	"time"
	"trading-go/internal/database"

	"gorm.io/gorm/clause"
)

const MLEvidenceSchemaVersion = "ml-promotion-evidence-v1"

type MLProvenance struct {
	ArtifactDigest         string     `json:"artifact_digest"`
	TrainingManifestDigest string     `json:"training_manifest_digest"`
	BaselineStrategy       VersionRef `json:"baseline_strategy"`
	BaselinePolicyDigest   string     `json:"baseline_policy_digest"`
	DatasetManifestDigest  string     `json:"dataset_manifest_digest"`
}

type ImmutableMLEvidence struct {
	ID                 string       `json:"id"`
	SchemaVersion      string       `json:"schema_version"`
	ExperimentID       string       `json:"experiment_id"`
	FoldEvidenceDigest string       `json:"fold_evidence_digest"`
	Provenance         MLProvenance `json:"provenance"`
	OutcomesDigest     string       `json:"outcomes_digest"`
	Evaluation         MLEvaluation `json:"evaluation"`
	ContentDigest      string       `json:"content_digest"`
	CreatedAt          time.Time    `json:"created_at"`
}

func NewImmutableMLEvidence(manifest ExperimentManifest, result WalkForwardResult, outcomes []MLOutcome, requirements MLRequirements, provenance MLProvenance, createdAt time.Time) (ImmutableMLEvidence, error) {
	if manifest.Spec.Model == nil {
		return ImmutableMLEvidence{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "model", Details: "model manifest is required"}
	}
	if provenance.ArtifactDigest != manifest.Spec.Model.ModelDigest || provenance.TrainingManifestDigest != manifest.Spec.Model.TrainingManifest || provenance.BaselineStrategy != manifest.Spec.Baseline || provenance.BaselinePolicyDigest != manifest.Spec.Policies.Composite || provenance.DatasetManifestDigest != manifest.Spec.DatasetManifestHash {
		return ImmutableMLEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "ML provenance does not match immutable experiment"}
	}
	evaluation, err := EvaluateML(outcomes, requirements)
	if err != nil {
		return ImmutableMLEvidence{}, err
	}
	foldDigest, err := canonicalDigest(result.Folds)
	if err != nil {
		return ImmutableMLEvidence{}, err
	}
	outcomesDigest, err := canonicalDigest(outcomes)
	if err != nil {
		return ImmutableMLEvidence{}, err
	}
	createdAt = createdAt.UTC()
	payload := struct {
		SchemaVersion, ExperimentID, FoldEvidenceDigest string
		Provenance                                      MLProvenance
		OutcomesDigest                                  string
		Evaluation                                      MLEvaluation
		CreatedAt                                       time.Time
	}{MLEvidenceSchemaVersion, manifest.ID, foldDigest, provenance, outcomesDigest, evaluation, createdAt}
	encoded, _ := json.Marshal(payload)
	contentDigest := digest(encoded)
	return ImmutableMLEvidence{ID: digest([]byte(manifest.ID + "\n" + contentDigest)), SchemaVersion: MLEvidenceSchemaVersion, ExperimentID: manifest.ID, FoldEvidenceDigest: foldDigest, Provenance: provenance, OutcomesDigest: outcomesDigest, Evaluation: evaluation, ContentDigest: contentDigest, CreatedAt: createdAt}, nil
}

func (r Repository) PersistMLEvidence(e ImmutableMLEvidence) (ImmutableMLEvidence, error) {
	manifest, err := r.LoadManifest(e.ExperimentID)
	if err != nil {
		return ImmutableMLEvidence{}, err
	}
	if manifest.Spec.Model == nil {
		return ImmutableMLEvidence{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "model"}
	}
	encoded, _ := json.Marshal(struct {
		SchemaVersion, ExperimentID, FoldEvidenceDigest string
		Provenance                                      MLProvenance
		OutcomesDigest                                  string
		Evaluation                                      MLEvaluation
		CreatedAt                                       time.Time
	}{e.SchemaVersion, e.ExperimentID, e.FoldEvidenceDigest, e.Provenance, e.OutcomesDigest, e.Evaluation, e.CreatedAt.UTC()})
	expected := digest(encoded)
	if e.SchemaVersion != MLEvidenceSchemaVersion || e.ContentDigest != expected || e.ID != digest([]byte(e.ExperimentID+"\n"+expected)) || !e.Evaluation.Passed {
		return ImmutableMLEvidence{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "ML evidence integrity or gates failed"}
	}
	bytes, _ := json.Marshal(e)
	row := database.ValidationMLEvidence{ID: e.ID, ExperimentID: e.ExperimentID, SchemaVersion: e.SchemaVersion, EvidenceJSON: string(bytes), EvidenceDigest: e.ContentDigest, Passed: e.Evaluation.Passed, CreatedAt: e.CreatedAt.UTC()}
	res := r.DB.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "experiment_id"}}, DoNothing: true}).Create(&row)
	if res.Error != nil {
		return ImmutableMLEvidence{}, res.Error
	}
	return r.LoadMLEvidenceForExperiment(e.ExperimentID)
}

func (r Repository) LoadMLEvidenceForExperiment(experimentID string) (ImmutableMLEvidence, error) {
	var row database.ValidationMLEvidence
	if err := r.DB.Where("experiment_id=?", experimentID).First(&row).Error; err != nil {
		return ImmutableMLEvidence{}, err
	}
	var evidence ImmutableMLEvidence
	if len(row.EvidenceJSON) > MaxEvidenceBytes || json.Unmarshal([]byte(row.EvidenceJSON), &evidence) != nil {
		return evidence, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "malformed ML evidence"}
	}
	encoded, _ := json.Marshal(struct {
		SchemaVersion, ExperimentID, FoldEvidenceDigest string
		Provenance                                      MLProvenance
		OutcomesDigest                                  string
		Evaluation                                      MLEvaluation
		CreatedAt                                       time.Time
	}{evidence.SchemaVersion, evidence.ExperimentID, evidence.FoldEvidenceDigest, evidence.Provenance, evidence.OutcomesDigest, evidence.Evaluation, evidence.CreatedAt.UTC()})
	if digest(encoded) != row.EvidenceDigest || evidence.ContentDigest != row.EvidenceDigest || evidence.ID != row.ID || !row.Passed || !evidence.Evaluation.Passed {
		return evidence, &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "stored ML evidence digest or gates failed"}
	}
	return evidence, nil
}
