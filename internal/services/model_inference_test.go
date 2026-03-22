package services

import "testing"

func TestLoadModelArtifactAndPredict(t *testing.T) {
	artifact, err := LoadModelArtifact(DefaultActiveModelVersion)
	if err != nil {
		t.Fatalf("LoadModelArtifact() error = %v", err)
	}
	if artifact == nil {
		t.Fatal("LoadModelArtifact() returned nil artifact")
	}

	strong := featureValuesAtMean(artifact)
	strong["ret_15m_16"] = 6
	strong["relative_strength_7d"] = 8
	strong["universe_rank_pct"] = 0.95
	strong["regime_score"] = 1
	strong["breadth_ratio"] = 0.65
	strong["exposure_ratio"] = 0.15

	weak := featureValuesAtMean(artifact)
	weak["ret_15m_16"] = -4
	weak["relative_strength_7d"] = -6
	weak["universe_rank_pct"] = 0.15
	weak["regime_score"] = -1
	weak["breadth_ratio"] = 0.32
	weak["exposure_ratio"] = 0.75

	strongPrediction, err := artifact.PredictValues(strong)
	if err != nil {
		t.Fatalf("PredictValues(strong) error = %v", err)
	}
	weakPrediction, err := artifact.PredictValues(weak)
	if err != nil {
		t.Fatalf("PredictValues(weak) error = %v", err)
	}

	if strongPrediction.Probability <= weakPrediction.Probability {
		t.Fatalf("expected strong probability > weak probability, got %f <= %f", strongPrediction.Probability, weakPrediction.Probability)
	}
	if strongPrediction.ExpectedValue <= weakPrediction.ExpectedValue {
		t.Fatalf("expected strong EV > weak EV, got %f <= %f", strongPrediction.ExpectedValue, weakPrediction.ExpectedValue)
	}
}

func TestRankModelPredictions(t *testing.T) {
	policy := ModelSelectionPolicy{TopK: 2, MinProbability: 0.55, MinExpectedValue: 0.001}
	ranked := RankModelPredictions([]ModelRankedCandidate{
		{Symbol: "AAAUSDT", Probability: 0.66, ExpectedValue: 0.010, RawScore: 0.5},
		{Symbol: "BBBUSDT", Probability: 0.61, ExpectedValue: 0.006, RawScore: 0.4},
		{Symbol: "CCCUSDT", Probability: 0.52, ExpectedValue: 0.002, RawScore: 0.3},
		{Symbol: "DDDUSDT", Probability: 0.70, ExpectedValue: -0.001, RawScore: 0.6},
	}, policy)

	if len(ranked) != 4 {
		t.Fatalf("expected 4 ranked candidates, got %d", len(ranked))
	}
	if !ranked[0].Selected || !ranked[1].Selected {
		t.Fatalf("expected first two candidates selected, got %+v", ranked)
	}
	if ranked[2].Selected || ranked[2].SelectionReason != "below_probability_floor" {
		t.Fatalf("expected third candidate rejected by probability floor, got %+v", ranked[2])
	}
	if ranked[3].Selected || ranked[3].SelectionReason != "below_ev_floor" {
		t.Fatalf("expected fourth candidate rejected by EV floor, got %+v", ranked[3])
	}
}

func featureValuesAtMean(artifact *LogisticModelArtifact) map[string]float64 {
	values := make(map[string]float64, len(artifact.Features))
	for _, feature := range artifact.Features {
		values[feature.Name] = feature.Mean
	}
	return values
}
