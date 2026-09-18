package validation

import (
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

func TestResearchFamilyRetainsFailedAttemptsAndLocksConfirmatoryHoldout(t *testing.T) {
	db := testutil.SetupPostgresDB(t)
	repo := Repository{DB: db}
	manifest := manifestFixture(t)
	manifest.Spec.StudyType, manifest.Spec.Exploratory = "exploratory", true
	manifest.Spec.ConfirmatoryHoldout = nil
	manifest, err := NewManifest(manifest.Spec, manifest.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	first, err := repo.CreateManifest(manifest, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.PersistEvidence(first.ID, nil, &DiagnosticError{Code: DiagnosticMissingBenchmark}, first.CreatedAt.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	confirmatory := manifestFixture(t)
	confirmatory.Spec.FamilyID = first.Spec.FamilyID
	confirmatory, err = NewManifest(confirmatory.Spec, confirmatory.CreatedAt.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateManifest(confirmatory, nil, nil); err != nil {
		t.Fatal(err)
	}

	var attempts int64
	if err := db.Model(&database.ResearchExperimentAttempt{}).Where("family_id=?", first.Spec.FamilyID).Count(&attempts).Error; err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d; failed attempt must remain in denominator", attempts)
	}
	var outcomes int64
	if err := db.Model(&database.ResearchAttemptOutcome{}).Count(&outcomes).Error; err != nil {
		t.Fatal(err)
	}
	if outcomes != 1 {
		t.Fatalf("outcomes=%d", outcomes)
	}

	reused := confirmatory
	reused.CreatedAt = confirmatory.CreatedAt.Add(time.Hour)
	reused.ID = ""
	second, err := NewManifest(reused.Spec, reused.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateManifest(second, nil, nil); err == nil {
		t.Fatal("confirmatory holdout was reused")
	}
}

func TestResearchMetricsRejectUnmatchedLiquidityAndReportTailRisk(t *testing.T) {
	m := manifestFixture(t)
	p := healthyPrimitives(m.Spec.Folds[0], .01)
	p.BaselineGrossExposure = .25
	if _, err := DeriveFoldMetrics(p); err == nil {
		t.Fatal("unmatched baseline exposure accepted")
	}
	p = healthyPrimitives(m.Spec.Folds[0], .01)
	metrics, err := DeriveFoldMetrics(p)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.DownsideDeviation < 0 || metrics.ExpectedShortfall95 < 0 || metrics.MaxLiquidityParticipation <= 0 {
		t.Fatalf("metrics=%+v", metrics)
	}
}
