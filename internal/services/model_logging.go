package services

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"trading-go/internal/database"

	"gorm.io/gorm"
)

type modelObservation struct {
	Symbol            string
	FeatureSnapshotID *uint
	Prediction        ModelPrediction
	Rank              int
	Selected          bool
	DecisionResult    string
	PredictionLogID   *uint
	DecisionTime      time.Time
	EntryPrice        float64
	FeatureSpec       string
}

func persistFeatureSnapshot(row ModelFeatureRow, candidate UniverseCandidateMetrics, modelVersion string, selection *UniverseSelectionResult, governance GovernanceContext) (*uint, error) {
	if database.DB == nil {
		return nil, nil
	}

	featuresJSON, err := json.Marshal(row.Values)
	if err != nil {
		return nil, err
	}
	qualityJSON, err := json.Marshal(row.QualityFlags)
	if err != nil {
		return nil, err
	}

	record := database.FeatureSnapshot{
		SnapshotTime:       row.Timestamp,
		Symbol:             row.Symbol,
		UniverseSnapshotID: universeSnapshotID(selection),
		ModelVersion:       modelVersion,
		PolicyVersion:      governance.PolicyVersions.CompositeVersion,
		UniverseMode:       governance.UniverseMode,
		ExperimentID:       stringPtr(governance.ExperimentID),
		FeatureSpecVersion: row.SpecVersion,
		LastPrice:          row.LastPrice,
		RegimeState:        selectionRegime(selection),
		BreadthRatio:       selectionBreadth(selection),
		RankScore:          candidate.RankScore,
		FeaturesJSON:       string(featuresJSON),
		QualityFlagsJSON:   string(qualityJSON),
	}

	if err := database.DB.Create(&record).Error; err != nil {
		return nil, err
	}
	return &record.ID, nil
}

func persistPredictionLogs(observations []modelObservation, selection *UniverseSelectionResult, governance GovernanceContext, labelPolicy decisionLabelPolicy) (map[string]uint, error) {
	if database.DB == nil || len(observations) == 0 {
		return nil, nil
	}

	artifactDigest, err := modelArtifactDigest(observations[0].Prediction.ModelVersion)
	if err != nil {
		return nil, err
	}
	if artifactDigest == "" {
		return nil, fmt.Errorf("model observations require a durable artifact identity")
	}
	policyContextJSON := governancePolicyContextJSON(governance)
	idsBySymbol := make(map[string]uint, len(observations))
	err = database.DB.Transaction(func(tx *gorm.DB) error {
		for _, observation := range observations {
			if observation.Prediction.ModelVersion != observations[0].Prediction.ModelVersion {
				return fmt.Errorf("mixed model artifacts cannot share a decision cohort batch")
			}
			decisionTime := observation.DecisionTime.UTC()
			if decisionTime.IsZero() {
				decisionTime = selectionTimestamp(selection)
			}
			if observation.EntryPrice <= 0 || observation.FeatureSpec == "" {
				return fmt.Errorf("model observation %s is missing point-in-time feature evidence", observation.Symbol)
			}
			decisionID := decisionCohortIdentity(decisionTime, observation.Symbol, labelPolicy.Horizon, governance.PolicyVersions.CompositeVersion, artifactDigest, labelPolicy.CostModelVersion)
			var existing database.DecisionCohort
			if err := tx.Select("prediction_log_id").First(&existing, "decision_id = ?", decisionID).Error; err == nil {
				if existing.PredictionLogID == nil {
					return fmt.Errorf("decision cohort %s has no prediction log", decisionID)
				}
				idsBySymbol[observation.Symbol] = *existing.PredictionLogID
				continue
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			row := database.PredictionLog{
				PredictionTime:       decisionTime,
				Symbol:               observation.Symbol,
				ModelVersion:         observation.Prediction.ModelVersion,
				PolicyVersion:        governance.PolicyVersions.CompositeVersion,
				UniverseMode:         governance.UniverseMode,
				ExperimentID:         stringPtr(governance.ExperimentID),
				FeatureSnapshotID:    observation.FeatureSnapshotID,
				UniverseSnapshotID:   universeSnapshotID(selection),
				PredictedProbability: observation.Prediction.Probability,
				PredictedEV:          observation.Prediction.ExpectedValue,
				RawScore:             observation.Prediction.RawScore,
				Rank:                 observation.Rank,
				RankBucket:           rankBucket(observation.Rank),
				ProbabilityBucket:    probabilityBucket(observation.Prediction.Probability),
				Selected:             observation.Selected,
				DecisionResult:       observation.DecisionResult,
				RolloutState:         governance.RolloutState,
				PolicyContextJSON:    policyContextJSON,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			cohort := database.DecisionCohort{
				DecisionID:           decisionID,
				PredictionLogID:      &row.ID,
				FeatureSnapshotID:    observation.FeatureSnapshotID,
				UniverseSnapshotID:   universeSnapshotID(selection),
				DecisionTime:         decisionTime,
				Symbol:               observation.Symbol,
				HorizonSeconds:       int64(labelPolicy.Horizon / time.Second),
				MaturityTime:         decisionTime.Add(labelPolicy.Horizon),
				ModelVersion:         observation.Prediction.ModelVersion,
				ModelArtifactDigest:  artifactDigest,
				PolicyVersion:        governance.PolicyVersions.CompositeVersion,
				CostModelVersion:     labelPolicy.CostModelVersion,
				FeatureSpecVersion:   observation.FeatureSpec,
				EntryReferencePrice:  observation.EntryPrice,
				RoundTripCostBPS:     labelPolicy.RoundTripCostBPS,
				PredictedProbability: observation.Prediction.Probability,
				PredictedEV:          observation.Prediction.ExpectedValue,
				RawScore:             observation.Prediction.RawScore,
				Rank:                 observation.Rank,
				RankBucket:           rankBucket(observation.Rank),
				ProbabilityBucket:    probabilityBucket(observation.Prediction.Probability),
				Accepted:             observation.Selected,
				DecisionResult:       observation.DecisionResult,
				RolloutState:         governance.RolloutState,
				UniverseMode:         governance.UniverseMode,
				ExperimentID:         stringPtr(governance.ExperimentID),
				PolicyContextJSON:    policyContextJSON,
				OutcomeStatus:        DecisionOutcomePending,
			}
			if err := tx.Create(&cohort).Error; err != nil {
				return err
			}
			idsBySymbol[observation.Symbol] = row.ID
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return idsBySymbol, nil
}

func universeSnapshotID(selection *UniverseSelectionResult) *uint {
	if selection == nil || selection.SnapshotID == 0 {
		return nil
	}
	id := selection.SnapshotID
	return &id
}

func selectionTimestamp(selection *UniverseSelectionResult) time.Time {
	if selection == nil {
		return time.Now().UTC()
	}
	if parsed, err := time.Parse(time.RFC3339, selection.Timestamp); err == nil {
		return parsed.UTC()
	}
	return time.Now().UTC()
}

func selectionRegime(selection *UniverseSelectionResult) string {
	if selection == nil {
		return ""
	}
	return selection.RegimeState
}

func selectionBreadth(selection *UniverseSelectionResult) float64 {
	if selection == nil {
		return 0
	}
	return selection.BreadthRatio
}

func governancePolicyContextJSON(governance GovernanceContext) string {
	payload, err := json.Marshal(governance)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func probabilityBucket(probability float64) string {
	switch {
	case probability < 0.4:
		return "lt_0_40"
	case probability < 0.5:
		return "0_40_to_0_49"
	case probability < 0.6:
		return "0_50_to_0_59"
	case probability < 0.7:
		return "0_60_to_0_69"
	default:
		return "gte_0_70"
	}
}

func rankBucket(rank int) string {
	switch {
	case rank <= 0:
		return "unranked"
	case rank == 1:
		return "rank_1"
	case rank <= 3:
		return "rank_2_3"
	case rank <= 5:
		return "rank_4_5"
	default:
		return "rank_6_plus"
	}
}
