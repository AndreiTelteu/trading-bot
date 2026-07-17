package validation

import (
	"errors"
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

func TestStage07PostgresManifestEvidenceIntegrityIdempotencyAndFailure(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	repo := Repository{DB: db}
	manifest := manifestFixture(t)
	created, err := repo.CreateManifest(manifest, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	same, err := repo.CreateManifest(manifest, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if same.ID != created.ID {
		t.Fatal("manifest retry changed identity")
	}
	result := passingResult(t, created)
	evidence, err := repo.PersistEvidence(created.ID, &result, nil, created.CreatedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	retry, err := repo.PersistEvidence(created.ID, &result, nil, created.CreatedAt.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if retry.ID != evidence.ID {
		t.Fatal("evidence retry changed identity")
	}
	if err := db.Model(&database.ValidationEvidence{}).Where("id=?", evidence.ID).Update("status", "forged").Error; err == nil {
		t.Fatal("immutable evidence update succeeded")
	}
	loaded, err := repo.LoadEvidence(evidence.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != "passed" {
		t.Fatalf("status=%s", loaded.Status)
	}
	other := manifestFixture(t)
	other.Spec.Seed++
	other, err = NewManifest(other.Spec, other.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	other, err = repo.CreateManifest(other, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	failure, err := repo.PersistEvidence(other.ID, nil, &DiagnosticError{Code: DiagnosticMissingBenchmark, Details: "benchmark series absent"}, other.CreatedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if failure.Status != "failed" || failure.Failure == nil || failure.Failure.Code != DiagnosticMissingBenchmark {
		t.Fatalf("failure=%+v", failure)
	}
	if _, err := repo.PersistEvidence(other.ID, &result, nil, other.CreatedAt.Add(2*time.Hour)); err == nil {
		t.Fatal("failed experiment evidence was replaced")
	}
}

func TestStage07StoredDigestMutationFailsClosed(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	repo := Repository{DB: db}
	manifest := manifestFixture(t)
	manifest, err := repo.CreateManifest(manifest, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE validation_experiments DISABLE TRIGGER validation_experiments_immutable").Error; err != nil {
		t.Fatal(err)
	}
	corruptDigest := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := db.Model(&database.ValidationExperiment{}).Where("id=?", manifest.ID).Updates(map[string]any{"content_id": corruptDigest, "content_digest": corruptDigest}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE validation_experiments ENABLE TRIGGER validation_experiments_immutable").Error; err != nil {
		t.Fatal(err)
	}
	_, err = repo.LoadManifest(manifest.ID)
	var diagnostic *DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Code != DiagnosticManifestIntegrity {
		t.Fatalf("got %v", err)
	}
}

func passingResult(t *testing.T, manifest ExperimentManifest) WalkForwardResult {
	t.Helper()
	folds := make([]FoldResult, 0, len(manifest.Spec.Folds))
	for _, fold := range manifest.Spec.Folds {
		primitives := healthyPrimitives(fold, .01)
		metrics, err := DeriveFoldMetrics(primitives)
		if err != nil {
			t.Fatal(err)
		}
		folds = append(folds, FoldResult{Fold: fold, Frozen: FrozenDecision{FoldIndex: fold.Index, Choice: "20", Parameters: map[string]string{"lookback": "20"}, FitDigest: "fit", SelectionDigest: "select"}, Primitives: primitives, Metrics: metrics})
	}
	aggregate, err := Evaluate(folds, manifest.Spec)
	if err != nil {
		t.Fatal(err)
	}
	return WalkForwardResult{SchemaVersion: EvidenceSchemaVersion, ExperimentID: manifest.ID, Folds: folds, Aggregate: aggregate}
}
