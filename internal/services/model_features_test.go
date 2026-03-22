package services

import (
	"math"
	"testing"
	"time"
)

func TestBuildModelFeatureRow(t *testing.T) {
	input := ModelFeatureInput{
		Timestamp:         time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC),
		Symbol:            "AAAUSDT",
		Candles15m:        buildModelTestCandles(180, 100, 0.35, 1_200),
		BTCCandles15m:     buildModelTestCandles(180, 40_000, 40, 5_000),
		OpenPositionCount: 2,
		ExposureRatio:     0.35,
		AlreadyOpen:       false,
		RegimeState:       UniverseRegimeRiskOn,
		BreadthRatio:      0.62,
		Candidate: UniverseCandidateMetrics{
			Symbol:                    "AAAUSDT",
			LastPrice:                 162.65,
			QuoteVolume24h:            8_500_000,
			MedianIntradayQuoteVolume: 320_000,
			RelativeStrength:          5.1,
			VolumeAcceleration:        1.4,
			OverextensionPenalty:      0.3,
			TrendQuality:              2.7,
			BreakoutProximity:         0.99,
			GapRatio:                  0.01,
			VolatilityRatio:           0.03,
			RankScore:                 1.8,
		},
		ActiveUniverse: []UniverseCandidateMetrics{
			{Symbol: "AAAUSDT", RankScore: 1.8, MedianIntradayQuoteVolume: 320_000, VolatilityRatio: 0.03},
			{Symbol: "BBBUSDT", RankScore: 0.8, MedianIntradayQuoteVolume: 210_000, VolatilityRatio: 0.05},
			{Symbol: "CCCUSDT", RankScore: -0.2, MedianIntradayQuoteVolume: 120_000, VolatilityRatio: 0.02},
		},
	}

	row := BuildModelFeatureRow(input)
	if !row.Valid {
		t.Fatalf("BuildModelFeatureRow() expected valid row, got flags=%v", row.QualityFlags)
	}
	if row.SpecVersion != ModelFeatureSpecVersion {
		t.Fatalf("SpecVersion = %s, want %s", row.SpecVersion, ModelFeatureSpecVersion)
	}
	if row.Values["regime_score"] != 1 {
		t.Fatalf("regime_score = %v, want 1", row.Values["regime_score"])
	}
	if row.Values["universe_rank_pct"] <= row.Values["liquidity_rank_pct"]-0.6 {
		t.Fatalf("unexpected percentile ranks: %+v", row.Values)
	}
	for key, value := range row.Values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("feature %s is invalid: %v", key, value)
		}
	}
}

func TestBuildModelFeatureRowRequiresHistory(t *testing.T) {
	row := BuildModelFeatureRow(ModelFeatureInput{
		Timestamp:      time.Now().UTC(),
		Symbol:         "AAAUSDT",
		Candles15m:     buildModelTestCandles(30, 100, 0.2, 1_000),
		ActiveUniverse: []UniverseCandidateMetrics{{Symbol: "AAAUSDT"}},
	})
	if row.Valid {
		t.Fatal("expected invalid row for insufficient history")
	}
	if len(row.QualityFlags) == 0 {
		t.Fatal("expected quality flags for insufficient history")
	}
}

func buildModelTestCandles(count int, base float64, slope float64, volume float64) []Candle {
	result := make([]Candle, 0, count)
	for i := 0; i < count; i++ {
		price := base + slope*float64(i)
		if price < 1 {
			price = 1
		}
		result = append(result, Candle{
			Close:  price,
			High:   price * 1.01,
			Low:    price * 0.99,
			Volume: volume + float64(i*5),
		})
	}
	return result
}
