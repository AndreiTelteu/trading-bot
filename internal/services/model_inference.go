package services

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

const (
	DefaultActiveModelVersion = "logistic_baseline_v1"
	ModelRolloutShadow        = "shadow"
	ModelRolloutPaper         = "paper"
	ModelRolloutLive          = "live"
)

type ModelArtifactFeature struct {
	Name        string  `json:"name"`
	Mean        float64 `json:"mean"`
	Std         float64 `json:"std"`
	Coefficient float64 `json:"coefficient"`
}

type ModelCalibration struct {
	A float64 `json:"a"`
	B float64 `json:"b"`
}

type LogisticModelArtifact struct {
	Version            string                 `json:"version"`
	ModelFamily        string                 `json:"model_family"`
	FeatureSpecVersion string                 `json:"feature_spec_version"`
	LabelSpecVersion   string                 `json:"label_spec_version"`
	CalibrationMethod  string                 `json:"calibration_method"`
	TrainingWindow     string                 `json:"training_window"`
	ValidationWindow   string                 `json:"validation_window"`
	TestWindow         string                 `json:"test_window"`
	Metrics            map[string]float64     `json:"metrics"`
	Metadata           map[string]interface{} `json:"metadata"`
	AvgGain            float64                `json:"avg_gain"`
	AvgLoss            float64                `json:"avg_loss"`
	Intercept          float64                `json:"intercept"`
	Calibration        ModelCalibration       `json:"calibration"`
	Features           []ModelArtifactFeature `json:"features"`
}

type ModelPrediction struct {
	ModelVersion       string  `json:"model_version"`
	Probability        float64 `json:"probability"`
	ExpectedValue      float64 `json:"expected_value"`
	RawScore           float64 `json:"raw_score"`
	CalibratedLogit    float64 `json:"calibrated_logit"`
	FeatureSpecVersion string  `json:"feature_spec_version"`
}

type ModelSelectionPolicy struct {
	ActiveModelVersion string  `json:"active_model_version"`
	RolloutState       string  `json:"rollout_state"`
	TopK               int     `json:"top_k"`
	MinProbability     float64 `json:"min_probability"`
	MinExpectedValue   float64 `json:"min_expected_value"`
}

type ModelRankedCandidate struct {
	Symbol          string  `json:"symbol"`
	Probability     float64 `json:"probability"`
	ExpectedValue   float64 `json:"expected_value"`
	RawScore        float64 `json:"raw_score"`
	Rank            int     `json:"rank"`
	Selected        bool    `json:"selected"`
	SelectionReason string  `json:"selection_reason,omitempty"`
}

func GetModelSelectionPolicy(settings map[string]string) ModelSelectionPolicy {
	policy := ModelSelectionPolicy{
		ActiveModelVersion: strings.TrimSpace(getSettingString(settings, "active_model_version", DefaultActiveModelVersion)),
		RolloutState:       strings.ToLower(strings.TrimSpace(getSettingString(settings, "model_rollout_state", ModelRolloutShadow))),
		TopK:               getSettingInt(settings, "selection_policy_top_k", 3),
		MinProbability:     getSettingFloat(settings, "selection_policy_min_prob", 0.53),
		MinExpectedValue:   getSettingFloat(settings, "selection_policy_min_ev", 0.001),
	}

	if policy.TopK <= 0 {
		policy.TopK = 3
	}
	if policy.RolloutState == "" {
		policy.RolloutState = ModelRolloutShadow
	}
	return policy
}

func (policy ModelSelectionPolicy) Enabled() bool {
	return strings.TrimSpace(policy.ActiveModelVersion) != ""
}

func (policy ModelSelectionPolicy) UseForLiveEntries() bool {
	if !policy.Enabled() {
		return false
	}
	switch policy.RolloutState {
	case ModelRolloutPaper, ModelRolloutLive:
		return true
	default:
		return false
	}
}

func (policy ModelSelectionPolicy) rolloutLabel() string {
	if policy.RolloutState == "" {
		return ModelRolloutShadow
	}
	return policy.RolloutState
}

func (artifact LogisticModelArtifact) Validate() error {
	if strings.TrimSpace(artifact.Version) == "" {
		return fmt.Errorf("model artifact version is required")
	}
	if len(artifact.Features) == 0 {
		return fmt.Errorf("model artifact %s has no features", artifact.Version)
	}
	for _, feature := range artifact.Features {
		if strings.TrimSpace(feature.Name) == "" {
			return fmt.Errorf("model artifact %s contains unnamed feature", artifact.Version)
		}
	}
	return nil
}

func (artifact LogisticModelArtifact) PredictRow(row ModelFeatureRow) (ModelPrediction, error) {
	if !row.Valid {
		return ModelPrediction{}, fmt.Errorf("feature row for %s is invalid", row.Symbol)
	}
	if row.SpecVersion != artifact.FeatureSpecVersion {
		return ModelPrediction{}, fmt.Errorf("feature spec mismatch: row=%s artifact=%s", row.SpecVersion, artifact.FeatureSpecVersion)
	}
	return artifact.PredictValues(row.Values)
}

func (artifact LogisticModelArtifact) PredictValues(values map[string]float64) (ModelPrediction, error) {
	if err := artifact.Validate(); err != nil {
		return ModelPrediction{}, err
	}

	rawScore := artifact.Intercept
	for _, feature := range artifact.Features {
		value, ok := values[feature.Name]
		if !ok {
			return ModelPrediction{}, fmt.Errorf("missing feature %s for model %s", feature.Name, artifact.Version)
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return ModelPrediction{}, fmt.Errorf("invalid feature %s for model %s", feature.Name, artifact.Version)
		}
		std := feature.Std
		if std == 0 {
			std = 1
		}
		normalized := (value - feature.Mean) / std
		rawScore += normalized * feature.Coefficient
	}

	calibratedLogit := rawScore
	if artifact.Calibration.A != 0 || artifact.Calibration.B != 0 {
		calibratedLogit = artifact.Calibration.A*rawScore + artifact.Calibration.B
	}
	probability := sigmoid(calibratedLogit)
	expectedValue := probability*artifact.AvgGain - (1-probability)*artifact.AvgLoss

	return ModelPrediction{
		ModelVersion:       artifact.Version,
		Probability:        probability,
		ExpectedValue:      expectedValue,
		RawScore:           rawScore,
		CalibratedLogit:    calibratedLogit,
		FeatureSpecVersion: artifact.FeatureSpecVersion,
	}, nil
}

func RankModelPredictions(candidates []ModelRankedCandidate, policy ModelSelectionPolicy) []ModelRankedCandidate {
	if len(candidates) == 0 {
		return nil
	}

	ranked := append([]ModelRankedCandidate(nil), candidates...)
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].ExpectedValue == ranked[j].ExpectedValue {
			if ranked[i].Probability == ranked[j].Probability {
				return ranked[i].Symbol < ranked[j].Symbol
			}
			return ranked[i].Probability > ranked[j].Probability
		}
		return ranked[i].ExpectedValue > ranked[j].ExpectedValue
	})

	topK := policy.TopK
	if topK <= 0 {
		topK = len(ranked)
	}
	selectedCount := 0
	for i := range ranked {
		ranked[i].Rank = i + 1
		switch {
		case ranked[i].Probability < policy.MinProbability:
			ranked[i].SelectionReason = "below_probability_floor"
		case ranked[i].ExpectedValue < policy.MinExpectedValue:
			ranked[i].SelectionReason = "below_ev_floor"
		case selectedCount >= topK:
			ranked[i].SelectionReason = "outside_top_k"
		default:
			ranked[i].Selected = true
			ranked[i].SelectionReason = "selected"
			selectedCount++
		}
	}

	return ranked
}

func sigmoid(value float64) float64 {
	if value >= 0 {
		return 1 / (1 + math.Exp(-value))
	}
	expValue := math.Exp(value)
	return expValue / (1 + expValue)
}
