package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func NewManifest(spec ManifestSpec, createdAt time.Time) (ExperimentManifest, error) {
	canonical, normalized, err := CanonicalManifestSpec(spec)
	if err != nil {
		return ExperimentManifest{}, err
	}
	createdAt = createdAt.UTC().Truncate(time.Microsecond)
	if createdAt.IsZero() {
		return ExperimentManifest{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "created_at", Details: "immutable creation time is required"}
	}
	contentID := digest(canonical)
	recordID := digest([]byte(contentID + "\n" + createdAt.Format(time.RFC3339Nano)))
	return ExperimentManifest{ID: recordID, ContentID: contentID, ContentDigest: contentID, CreatedAt: createdAt, Spec: normalized}, nil
}

func (m ExperimentManifest) Verify() error {
	canonical, _, err := CanonicalManifestSpec(m.Spec)
	if err != nil {
		return err
	}
	expected := digest(canonical)
	if expected != m.ContentID || expected != m.ContentDigest {
		return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "content_digest", Details: "stored semantic content differs from immutable identity"}
	}
	record := digest([]byte(expected + "\n" + m.CreatedAt.UTC().Format(time.RFC3339Nano)))
	if record != m.ID {
		return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "id", Details: "record identity or creation time was mutated"}
	}
	return nil
}

func CanonicalManifestSpec(spec ManifestSpec) ([]byte, ManifestSpec, error) {
	spec.SchemaVersion = strings.TrimSpace(spec.SchemaVersion)
	if spec.SchemaVersion == "" {
		spec.SchemaVersion = ManifestSchemaVersion
	}
	if spec.SchemaVersion != ManifestSchemaVersion || strings.TrimSpace(spec.CodeRevision) == "" || strings.TrimSpace(spec.Candidate.ID) == "" || strings.TrimSpace(spec.Candidate.Version) == "" || strings.TrimSpace(spec.Baseline.ID) == "" || strings.TrimSpace(spec.Baseline.Version) == "" || strings.TrimSpace(spec.DatasetManifestID) == "" || spec.DatasetManifestID != spec.DatasetManifestHash || strings.TrimSpace(spec.Policies.Composite) == "" || !spec.Interval.Valid() {
		return nil, ManifestSpec{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Details: "schema, revision, candidate, baseline, exact dataset digest, policy bundle, and interval are required"}
	}
	if spec.StudyType != "exploratory" && spec.StudyType != "confirmatory" {
		return nil, ManifestSpec{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "study_type", Details: "must be exploratory or confirmatory"}
	}
	if spec.Exploratory != (spec.StudyType == "exploratory") {
		return nil, ManifestSpec{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "exploratory", Details: "study label is inconsistent"}
	}
	if spec.FeatureHorizon < 0 || spec.LabelHorizon <= 0 || spec.Purge < 0 || spec.Embargo < 0 {
		return nil, ManifestSpec{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "horizons", Details: "label horizon must be positive and purge/embargo cannot be negative"}
	}
	if spec.BootstrapIterations <= 0 || spec.BootstrapIterations > MaxBootstrapIterations {
		return nil, ManifestSpec{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "bootstrap_iterations", Details: "iterations are outside bounded limits"}
	}
	if spec.StatisticalUnit != "chronological_test_window" && spec.StatisticalUnit != "declared_block" {
		return nil, ManifestSpec{}, &DiagnosticError{Code: DiagnosticUnsupportedUnit, Field: "statistical_unit", Details: "per-bar IID bootstrap is prohibited"}
	}
	if len(spec.Metrics) == 0 || len(spec.PromotionThresholds) == 0 || len(spec.RollbackThresholds) == 0 || spec.Reproduce.Command == "" {
		return nil, ManifestSpec{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Details: "metrics, gates, rollback thresholds, and reproduction invocation must be predeclared"}
	}
	if len(spec.Metrics) > 128 || len(spec.PromotionThresholds) > 128 || len(spec.RollbackThresholds) > 128 || len(spec.AllowedTuning) > 64 || len(spec.Reproduce.Args) > 64 || len(spec.ExecutionSemantics) > 128 || len(spec.RequiredElapsed) > 16 {
		return nil, ManifestSpec{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Details: "manifest collection exceeds bounded limits"}
	}
	for key, choices := range spec.AllowedTuning {
		if strings.TrimSpace(key) == "" || len(choices) == 0 || len(choices) > 64 {
			return nil, ManifestSpec{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "allowed_tuning", Details: "tuning keys and choices must be non-empty and bounded"}
		}
	}
	if err := ValidateFolds(spec.Folds, spec.Interval, spec.Samples.MinFolds); err != nil {
		return nil, ManifestSpec{}, err
	}
	if spec.Model != nil {
		if err := ValidateModelAuthority(*spec.Model, spec); err != nil {
			return nil, ManifestSpec{}, err
		}
	}
	if spec.ResearchOverride != nil {
		override := spec.ResearchOverride
		if strings.TrimSpace(override.Mode) == "" || strings.TrimSpace(override.Reason) == "" || !override.BoundedTo.Valid() || override.BoundedTo.Start.Before(spec.Interval.Start) || override.BoundedTo.End.After(spec.Interval.End) {
			return nil, ManifestSpec{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "research_override", Details: "override must declare a reason, mode, and bounded interval inside the experiment"}
		}
		override.BoundedTo.Start, override.BoundedTo.End = override.BoundedTo.Start.UTC(), override.BoundedTo.End.UTC()
	}
	spec.Interval.Start, spec.Interval.End = spec.Interval.Start.UTC(), spec.Interval.End.UTC()
	for i := range spec.Folds {
		spec.Folds[i].Train.Start, spec.Folds[i].Train.End = spec.Folds[i].Train.Start.UTC(), spec.Folds[i].Train.End.UTC()
		spec.Folds[i].Validation.Start, spec.Folds[i].Validation.End = spec.Folds[i].Validation.Start.UTC(), spec.Folds[i].Validation.End.UTC()
		spec.Folds[i].Test.Start, spec.Folds[i].Test.End = spec.Folds[i].Test.Start.UTC(), spec.Folds[i].Test.End.UTC()
	}
	sort.Strings(spec.Metrics)
	for key := range spec.AllowedTuning {
		sort.Strings(spec.AllowedTuning[key])
	}
	sort.Slice(spec.PromotionThresholds, func(i, j int) bool { return thresholdLess(spec.PromotionThresholds[i], spec.PromotionThresholds[j]) })
	sort.Slice(spec.RollbackThresholds, func(i, j int) bool { return thresholdLess(spec.RollbackThresholds[i], spec.RollbackThresholds[j]) })
	for _, threshold := range append(append([]Threshold(nil), spec.PromotionThresholds...), spec.RollbackThresholds...) {
		if math.IsNaN(threshold.Value) || math.IsInf(threshold.Value, 0) || (threshold.Op != ">" && threshold.Op != ">=" && threshold.Op != "<" && threshold.Op != "<=") {
			return nil, ManifestSpec{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "thresholds", Details: "threshold values and operators must be finite and supported"}
		}
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return nil, ManifestSpec{}, fmt.Errorf("canonical manifest: %w", err)
	}
	if len(encoded) > MaxManifestBytes {
		return nil, ManifestSpec{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Details: "manifest exceeds 1 MiB canonical limit"}
	}
	return encoded, spec, nil
}

func ValidateFolds(folds []Fold, interval Interval, minimum int) error {
	if minimum < 2 {
		minimum = 2
	}
	if len(folds) < minimum || len(folds) > MaxFolds {
		return &DiagnosticError{Code: DiagnosticInsufficientWindows, Details: fmt.Sprintf("need %d..%d folds, got %d", minimum, MaxFolds, len(folds))}
	}
	var priorTestEnd time.Time
	for i, fold := range folds {
		if fold.Index != i || !fold.Train.Valid() || !fold.Validation.Valid() || !fold.Test.Valid() || fold.Train.Start.Before(interval.Start) || fold.Test.End.After(interval.End) || fold.Train.End.After(fold.Validation.Start) || fold.Validation.End.After(fold.Test.Start) {
			return &DiagnosticError{Code: DiagnosticInvalidWindowOrder, Field: fmt.Sprintf("folds[%d]", i), Details: "folds must be indexed, half-open, chronological train/validation/test intervals inside the manifest interval"}
		}
		if !priorTestEnd.IsZero() && fold.Test.Start.Before(priorTestEnd) {
			return &DiagnosticError{Code: DiagnosticInvalidWindowOrder, Field: fmt.Sprintf("folds[%d].test", i), Details: "untouched test windows overlap or are out of order"}
		}
		priorTestEnd = fold.Test.End
	}
	return nil
}

func ValidateModelAuthority(model ModelAuthority, spec ManifestSpec) error {
	if model.Version == "" || model.ModelDigest == "" || model.FeatureSpec == "" || len(model.Features) == 0 || model.LabelSpec == "" || model.LabelHorizon <= 0 || model.CodeRevision == "" || model.DatasetManifest == "" || model.TrainingManifest == "" || model.PolicyVersion == "" {
		return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "model", Details: "complete model training provenance and ordered feature schema are required"}
	}
	if model.CodeRevision != spec.CodeRevision || model.DatasetManifest != spec.DatasetManifestID || model.PolicyVersion != spec.Policies.Composite || model.LabelHorizon != spec.LabelHorizon {
		return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "model", Details: "model provenance is incompatible with experiment code, data, label horizon, or policy"}
	}
	seen := map[string]bool{}
	for _, feature := range model.Features {
		if feature.Name == "" || (feature.Type != "float64" && feature.Type != "int64" && feature.Type != "bool") || seen[feature.Name] {
			return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "model.features", Details: "ordered feature names/types must be unique and supported"}
		}
		seen[feature.Name] = true
	}
	switch model.Class {
	case ArtifactBootstrap, ArtifactContractFixture, ArtifactResearch, ArtifactShadowCandidate, ArtifactPromotableCandidate:
	default:
		return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "model.class", Details: "unknown artifact class"}
	}
	return nil
}

func thresholdLess(a, b Threshold) bool {
	if a.Metric != b.Metric {
		return a.Metric < b.Metric
	}
	if a.Op != b.Op {
		return a.Op < b.Op
	}
	return a.Value < b.Value
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
