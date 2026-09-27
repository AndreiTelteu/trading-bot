package validation

import (
	"errors"
	"strings"
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

type countingFailureSource struct{ loads int }

func (s *countingFailureSource) Load(ExperimentManifest) ([]Sample, FoldRunnerFactory, error) {
	s.loads++
	return nil, nil, &DiagnosticError{Code: DiagnosticMissingBenchmark, Details: "source unavailable"}
}

func TestStage07IntegrityFailureRecordsOneImmutableOutcome(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	repo := Repository{DB: db}
	manifest := manifestFixture(t)
	manifest, err := repo.CreateManifest(manifest, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result := passingResult(t, manifest)
	result.Aggregate.Passed = !result.Aggregate.Passed
	service := JobService{Repository: repo}
	createdAt := manifest.CreatedAt.Add(time.Hour)
	evidence, err := service.persistResult(manifest, result, createdAt)
	var diagnostic *DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Code != DiagnosticManifestIntegrity || !strings.Contains(err.Error(), "aggregate metrics") {
		t.Fatalf("original integrity failure lost: %v", err)
	}
	if evidence.Status != "failed" || evidence.Failure == nil || evidence.Failure.Code != DiagnosticManifestIntegrity {
		t.Fatalf("failure evidence=%+v", evidence)
	}
	for _, count := range []struct {
		model any
		want  int64
	}{
		{&database.ValidationFoldEvidence{}, 0},
		{&database.ValidationEvidence{}, 1},
		{&database.ResearchAttemptOutcome{}, 1},
	} {
		var got int64
		if err := db.Model(count.model).Where("experiment_id=?", manifest.ID).Count(&got).Error; err != nil || got != count.want {
			t.Fatalf("%T count=%d error=%v want=%d", count.model, got, err, count.want)
		}
	}
	retry, err := repo.PersistEvidence(manifest.ID, nil, diagnostic, createdAt.Add(time.Hour))
	if err != nil || retry.ID != evidence.ID {
		t.Fatalf("idempotent failed outcome retry=%+v error=%v", retry, err)
	}
	valid := passingResult(t, manifest)
	if _, err := repo.PersistEvidence(manifest.ID, &valid, nil, createdAt.Add(2*time.Hour)); err == nil {
		t.Fatal("immutable failed outcome was overwritten")
	}
	source := &countingFailureSource{}
	if _, err := (JobService{Repository: repo, Source: source}).Run(manifest.ID); err == nil || source.loads != 0 {
		t.Fatalf("completed job replayed source: loads=%d error=%v", source.loads, err)
	}
}

func TestStage07RunPreservesFailureWhenPersistenceFails(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	repo := Repository{DB: db}
	manifest := manifestFixture(t)
	manifest, err := repo.CreateManifest(manifest, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	original := &DiagnosticError{Code: DiagnosticMissingBenchmark, Details: "source unavailable"}
	source := &countingFailureSource{}
	// The repository read remains available, while the failed write must surface
	// alongside the source error. A trigger rejects only the evidence insert.
	if err := db.Exec(`CREATE FUNCTION reject_test_validation_evidence() RETURNS trigger AS $$
	BEGIN RAISE EXCEPTION 'test evidence write refused'; END; $$ LANGUAGE plpgsql`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Exec("DROP FUNCTION IF EXISTS reject_test_validation_evidence() CASCADE").Error })
	if err := db.Exec("CREATE TRIGGER reject_test_validation_evidence BEFORE INSERT ON validation_evidences FOR EACH ROW EXECUTE FUNCTION reject_test_validation_evidence()").Error; err != nil {
		t.Fatal(err)
	}
	_, err = (JobService{Repository: repo, Source: source}).Run(manifest.ID)
	if err == nil || !strings.Contains(err.Error(), original.Error()) || !strings.Contains(err.Error(), "test evidence write refused") || source.loads != 1 {
		t.Fatalf("source/persistence errors not preserved: loads=%d error=%v", source.loads, err)
	}
	result := passingResult(t, manifest)
	result.Aggregate.Passed = !result.Aggregate.Passed
	_, err = (JobService{Repository: repo}).persistResult(manifest, result, manifest.CreatedAt.Add(time.Hour))
	if err == nil || !strings.Contains(err.Error(), "aggregate metrics do not reproduce") || !strings.Contains(err.Error(), "test evidence write refused") {
		t.Fatalf("integrity/persistence errors not preserved: %v", err)
	}
	var outcomes int64
	if err := db.Model(&database.ResearchAttemptOutcome{}).Where("experiment_id=?", manifest.ID).Count(&outcomes).Error; err != nil || outcomes != 0 {
		t.Fatalf("unexpected outcome after rejected transaction: %d %v", outcomes, err)
	}
}
