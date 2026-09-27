package validation

import (
	"errors"
	"testing"
)

func TestPrewriteResultValidationClassification(t *testing.T) {
	manifest := manifestFixture(t)
	result := passingResult(t, manifest)
	result.Aggregate.Passed = !result.Aggregate.Passed
	err := validateResultForPersistence(manifest, &result)
	var prewrite *prewriteResultValidationError
	var diagnostic *DiagnosticError
	if !errors.As(err, &prewrite) || !errors.As(err, &diagnostic) || diagnostic.Code != DiagnosticManifestIntegrity {
		t.Fatalf("aggregate mismatch must retain a typed prewrite error and diagnostic: %v", err)
	}
	if !isPrewriteResultValidationError(err) || isPrewriteResultValidationError(&DiagnosticError{Code: DiagnosticManifestIntegrity}) || isPrewriteResultValidationError(errors.New("database failed")) {
		t.Fatal("only typed result-validation errors may trigger failed-outcome fallback")
	}
	if err := validateResultForPersistence(manifest, nil); err == nil {
		t.Fatal("nil result passed result validation")
	}
	result = passingResult(t, manifest)
	if err := validateResultForPersistence(manifest, &result); err != nil {
		t.Fatalf("valid result rejected: %v", err)
	}
}

func TestPersistEvidenceRejectsAmbiguousOutcomeInputs(t *testing.T) {
	var repo Repository
	if _, err := repo.PersistEvidence("unused", nil, nil, manifestFixture(t).CreatedAt); err == nil {
		t.Fatal("nil result and nil failure would record a false pass")
	}
	result := WalkForwardResult{}
	if _, err := repo.PersistEvidence("unused", &result, errors.New("failure"), manifestFixture(t).CreatedAt); err == nil {
		t.Fatal("result and failure cannot both be supplied")
	}
}
