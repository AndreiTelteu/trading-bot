package backtest

import (
	"fmt"
	"trading-go/internal/database"
	"trading-go/internal/validation"

	"gorm.io/gorm"
)

// Stage07ComparisonReference is the explicit, digest-verified adapter by which
// canonical Stage 05 comparisons and embedded Stage 06 candidate evidence are
// admitted as manifest provenance. It does not treat a single comparison as
// multi-window validation evidence.
type Stage07ComparisonReference struct {
	JobID          uint   `json:"job_id"`
	ArtifactDigest string `json:"artifact_digest"`
	Candidate      string `json:"candidate"`
	DatasetID      string `json:"dataset_manifest_id"`
}

func LoadStage07ComparisonReference(db *gorm.DB, jobID uint) (Stage07ComparisonReference, error) {
	if db == nil || jobID == 0 {
		return Stage07ComparisonReference{}, fmt.Errorf("Stage 05 job id and database are required")
	}
	var job database.BacktestJob
	if err := db.Where("id=? AND job_type=? AND status=?", jobID, "stage05_comparison", "completed").First(&job).Error; err != nil {
		return Stage07ComparisonReference{}, err
	}
	if job.SummaryJSON == nil || job.ArtifactDigest == nil {
		return Stage07ComparisonReference{}, fmt.Errorf("Stage 05 job lacks canonical artifact")
	}
	artifact, err := UnmarshalComparisonArtifact([]byte(*job.SummaryJSON))
	if err != nil {
		return Stage07ComparisonReference{}, err
	}
	if artifact.ArtifactDigest != *job.ArtifactDigest {
		return Stage07ComparisonReference{}, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "Stage 05 job digest differs from canonical artifact"}
	}
	if artifact.CandidateEvidence == nil && artifact.Candidate == StrategyTrendMomentumCandidate+"@1.0.0" {
		return Stage07ComparisonReference{}, fmt.Errorf("Stage 06 candidate evidence is missing")
	}
	return Stage07ComparisonReference{JobID: job.ID, ArtifactDigest: artifact.ArtifactDigest, Candidate: artifact.Candidate, DatasetID: artifact.ManifestID}, nil
}
