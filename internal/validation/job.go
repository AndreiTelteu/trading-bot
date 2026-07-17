package validation

import (
	"fmt"
	"time"
)

// ExperimentSource is the production boundary for Stage 04 samples and
// Stage 05/06 strategy/model runners. Implementations must load only the exact
// immutable dataset and versions named by the manifest.
type ExperimentSource interface {
	Load(manifest ExperimentManifest) ([]Sample, FoldRunner, error)
}

type JobService struct {
	Repository Repository
	Source     ExperimentSource
	Now        func() time.Time
}

func (s JobService) Run(manifestID string) (PersistedEvidence, error) {
	if s.Source == nil {
		return PersistedEvidence{}, fmt.Errorf("validation experiment source is required")
	}
	manifest, err := s.Repository.LoadManifest(manifestID)
	if err != nil {
		return PersistedEvidence{}, err
	}
	samples, runner, loadErr := s.Source.Load(manifest)
	createdAt := time.Now().UTC()
	if s.Now != nil {
		createdAt = s.Now().UTC()
	}
	if loadErr != nil {
		evidence, persistErr := s.Repository.PersistEvidence(manifest.ID, nil, loadErr, createdAt)
		if persistErr != nil {
			return PersistedEvidence{}, persistErr
		}
		return evidence, loadErr
	}
	result, runErr := RunWalkForward(manifest, samples, runner)
	if runErr != nil {
		evidence, persistErr := s.Repository.PersistEvidence(manifest.ID, nil, runErr, createdAt)
		if persistErr != nil {
			return PersistedEvidence{}, persistErr
		}
		return evidence, runErr
	}
	return s.Repository.PersistEvidence(manifest.ID, &result, nil, createdAt)
}
