package validation

import (
	"fmt"
	"time"
)

// ExperimentSource is the production boundary for Stage 04 samples and
// Stage 05/06 strategy/model runners. Implementations must load only the exact
// immutable dataset and versions named by the manifest.
type ExperimentSource interface {
	Load(manifest ExperimentManifest) ([]Sample, FoldRunnerFactory, error)
}

type MLExperimentSource interface {
	LoadML(manifest ExperimentManifest, result WalkForwardResult) ([]MLOutcome, MLRequirements, MLProvenance, error)
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
	if manifest.Spec.Model != nil {
		mlSource, ok := s.Source.(MLExperimentSource)
		if !ok {
			runErr = &DiagnosticError{Code: DiagnosticIncompleteCoverage, Field: "ml_evidence", Details: "trusted production ML source is required"}
		} else {
			outcomes, requirements, provenance, mlErr := mlSource.LoadML(manifest, result)
			if mlErr != nil {
				runErr = mlErr
			} else {
				evidence, buildErr := NewImmutableMLEvidence(manifest, result, outcomes, requirements, provenance, createdAt)
				if buildErr != nil {
					runErr = buildErr
				} else if _, persistErr := s.Repository.PersistMLEvidence(evidence); persistErr != nil {
					return PersistedEvidence{}, persistErr
				}
			}
		}
		if runErr != nil {
			evidence, persistErr := s.Repository.PersistEvidence(manifest.ID, nil, runErr, createdAt)
			if persistErr != nil {
				return PersistedEvidence{}, persistErr
			}
			return evidence, runErr
		}
	}
	return s.Repository.PersistEvidence(manifest.ID, &result, nil, createdAt)
}
