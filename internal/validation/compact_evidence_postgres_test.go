package validation

import (
	"encoding/json"
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

// These integration regressions are intentionally run by the parent serially
// against the shared PostgreSQL schema, never by the compact-evidence worker.
func TestCompactEvidencePostgresAtomicHydrationAndRetry(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	repo := Repository{DB: db}
	manifest, err := repo.CreateManifest(manifestFixture(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := passingResult(t, manifest)
	created := manifest.CreatedAt.Add(time.Hour)
	got, err := repo.PersistEvidence(manifest.ID, &result, nil, created)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result == nil || len(got.Result.Folds) != len(result.Folds) {
		t.Fatal("complete folds not hydrated")
	}
	wantDigest, err := FoldResultsDigest(result.Folds)
	if err != nil {
		t.Fatal(err)
	}
	gotDigest, err := FoldResultsDigest(got.Result.Folds)
	if err != nil || gotDigest != wantDigest {
		t.Fatalf("consumer fold digest=%s want=%s err=%v", gotDigest, wantDigest, err)
	}
	var root database.ValidationEvidence
	if err := db.Where("id=?", got.ID).First(&root).Error; err != nil {
		t.Fatal(err)
	}
	if root.SchemaVersion != CompactEvidenceRootSchemaVersion || len(root.EvidenceJSON) > MaxEvidenceBytes {
		t.Fatal("root is not compact v3")
	}
	var count int64
	if err := db.Model(&database.ValidationFoldEvidence{}).Where("experiment_id=?", manifest.ID).Count(&count).Error; err != nil || count != int64(len(result.Folds)) {
		t.Fatalf("fold count=%d err=%v", count, err)
	}
	retry, err := repo.PersistEvidence(manifest.ID, &result, nil, created.Add(time.Hour))
	if err != nil || retry.ID != got.ID {
		t.Fatalf("retry=%+v err=%v", retry, err)
	}
	changed := passingResult(t, manifest)
	changed.Folds[0].Frozen.SelectionDigest = "different-selection"
	if _, err := repo.PersistEvidence(manifest.ID, &changed, nil, created.Add(2*time.Hour)); err == nil {
		t.Fatal("different immutable retry accepted")
	}
}

func TestCompactEvidencePostgresMissingFoldFailsClosed(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	repo := Repository{DB: db}
	manifest, err := repo.CreateManifest(manifestFixture(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := passingResult(t, manifest)
	evidence, err := repo.PersistEvidence(manifest.ID, &result, nil, manifest.CreatedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE validation_fold_evidences DISABLE TRIGGER validation_fold_evidence_immutable").Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Exec("ALTER TABLE validation_fold_evidences ENABLE TRIGGER validation_fold_evidence_immutable").Error
	})
	if err := db.Where("experiment_id=? AND fold_index=?", manifest.ID, result.Folds[0].Fold.Index).Delete(&database.ValidationFoldEvidence{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := repo.LoadEvidence(evidence.ID); err == nil {
		t.Fatal("missing fold accepted")
	}
}

func TestCompactEvidencePostgresExtraAndCorruptFoldsFailClosed(t *testing.T) {
	for _, kind := range []string{"extra", "corrupt"} {
		t.Run(kind, func(t *testing.T) {
			db := testutil.SetupPostgresDB(t)
			repo := Repository{DB: db}
			manifest, err := repo.CreateManifest(manifestFixture(t), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			result := passingResult(t, manifest)
			evidence, err := repo.PersistEvidence(manifest.ID, &result, nil, manifest.CreatedAt.Add(time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			var fold database.ValidationFoldEvidence
			if err := db.Where("experiment_id=?", manifest.ID).Order("fold_index").First(&fold).Error; err != nil {
				t.Fatal(err)
			}
			if kind == "extra" {
				fold.ID = 0
				fold.FoldIndex = 100
				if err := db.Create(&fold).Error; err != nil {
					t.Fatal(err)
				}
			} else {
				if err := db.Exec("ALTER TABLE validation_fold_evidences DISABLE TRIGGER validation_fold_evidence_immutable").Error; err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					_ = db.Exec("ALTER TABLE validation_fold_evidences ENABLE TRIGGER validation_fold_evidence_immutable").Error
				})
				if err := db.Model(&database.ValidationFoldEvidence{}).Where("id=?", fold.ID).Update("evidence_digest", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa").Error; err != nil {
					t.Fatal(err)
				}
			}
			if _, err := repo.LoadEvidence(evidence.ID); err == nil {
				t.Fatal("tampered fold set accepted")
			}
		})
	}
}

func TestCompactEvidencePostgresLegacyV2AndFailedZeroFolds(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	repo := Repository{DB: db}
	manifest, err := repo.CreateManifest(manifestFixture(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := passingResult(t, manifest)
	legacy, err := json.Marshal(struct {
		SchemaVersion string             `json:"schema_version"`
		ExperimentID  string             `json:"experiment_id"`
		Status        string             `json:"status"`
		Result        *WalkForwardResult `json:"result,omitempty"`
		Failure       *DiagnosticError   `json:"failure,omitempty"`
	}{EvidenceSchemaVersion, manifest.ID, "passed", &result, nil})
	if err != nil {
		t.Fatal(err)
	}
	rootDigest := digest(legacy)
	legacyID := digest([]byte(manifest.ID + "\n" + rootDigest))
	row := database.ValidationEvidence{ID: legacyID, ExperimentID: manifest.ID, SchemaVersion: EvidenceSchemaVersion, Status: "passed", EvidenceJSON: string(legacy), EvidenceDigest: rootDigest, CreatedAt: manifest.CreatedAt.Add(time.Hour)}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.LoadEvidence(legacyID)
	if err != nil || loaded.Result == nil || len(loaded.Result.Folds) != len(result.Folds) {
		t.Fatalf("legacy read=%+v err=%v", loaded, err)
	}
	other := manifestFixture(t)
	other.Spec.Seed++
	other.Spec.StudyType, other.Spec.Exploratory, other.Spec.ConfirmatoryHoldout = "exploratory", true, nil
	other, err = NewManifest(other.Spec, other.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	other, err = repo.CreateManifest(other, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := repo.PersistEvidence(other.ID, nil, &DiagnosticError{Code: DiagnosticMissingBenchmark}, other.CreatedAt.Add(time.Hour))
	if err != nil || failed.Failure == nil {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
	var count int64
	if err := db.Model(&database.ValidationFoldEvidence{}).Where("experiment_id=?", other.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("failed fold count=%d err=%v", count, err)
	}
}
