package services

import (
	"encoding/json"
	"time"
	"trading-go/internal/database"
)

type modelObservation struct {
	Symbol            string
	FeatureSnapshotID *uint
	Prediction        ModelPrediction
	Rank              int
	Selected          bool
	DecisionResult    string
}

func persistFeatureSnapshot(row ModelFeatureRow, candidate UniverseCandidateMetrics, modelVersion string, selection *UniverseSelectionResult) (*uint, error) {
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

func persistPredictionLogs(observations []modelObservation, selection *UniverseSelectionResult, rolloutState string) error {
	if database.DB == nil || len(observations) == 0 {
		return nil
	}

	rows := make([]database.PredictionLog, 0, len(observations))
	for _, observation := range observations {
		rows = append(rows, database.PredictionLog{
			PredictionTime:       selectionTimestamp(selection),
			Symbol:               observation.Symbol,
			ModelVersion:         observation.Prediction.ModelVersion,
			FeatureSnapshotID:    observation.FeatureSnapshotID,
			UniverseSnapshotID:   universeSnapshotID(selection),
			PredictedProbability: observation.Prediction.Probability,
			PredictedEV:          observation.Prediction.ExpectedValue,
			RawScore:             observation.Prediction.RawScore,
			Rank:                 observation.Rank,
			Selected:             observation.Selected,
			DecisionResult:       observation.DecisionResult,
			RolloutState:         rolloutState,
		})
	}

	return database.DB.Create(&rows).Error
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
