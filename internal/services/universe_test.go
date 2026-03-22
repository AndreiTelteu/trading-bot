package services

import "testing"

func TestUniverseHardFilterReason(t *testing.T) {
	policy := UniversePolicy{
		MinListingDays:         30,
		MinDailyQuoteVolume:    1000,
		MinIntradayQuoteVolume: 100,
		MaxGapRatio:            0.1,
		VolRatioMin:            0.01,
		VolRatioMax:            0.20,
		Max24hMove:             25,
	}

	candidate := UniverseCandidateMetrics{
		ListingAgeDays:            10,
		MedianDailyQuoteVolume:    5000,
		MedianIntradayQuoteVolume: 500,
		GapRatio:                  0.01,
		VolatilityRatio:           0.05,
		Change24h:                 5,
	}
	if reason := UniverseHardFilterReason(candidate, policy); reason != "listing_age" {
		t.Fatalf("expected listing_age rejection, got %s", reason)
	}

	candidate.ListingAgeDays = 40
	candidate.MedianDailyQuoteVolume = 100
	if reason := UniverseHardFilterReason(candidate, policy); reason != "daily_quote_volume" {
		t.Fatalf("expected daily_quote_volume rejection, got %s", reason)
	}

	candidate.MedianDailyQuoteVolume = 5000
	candidate.GapRatio = 0.2
	if reason := UniverseHardFilterReason(candidate, policy); reason != "missing_bar_ratio" {
		t.Fatalf("expected missing_bar_ratio rejection, got %s", reason)
	}
}

func TestRankUniverseCandidatesPrefersHigherRelativeStrength(t *testing.T) {
	policy := UniversePolicy{VolRatioMin: 0.01, VolRatioMax: 0.10}
	candidates := []UniverseCandidateMetrics{
		{
			Symbol:                    "AAAUSDT",
			Return1D:                  2,
			Return3D:                  4,
			Return7D:                  9,
			RelativeStrength:          7,
			MedianIntradayQuoteVolume: 900_000,
			TrendQuality:              3,
			BreakoutProximity:         0.99,
			VolumeAcceleration:        1.5,
			VolatilityRatio:           0.04,
			OverextensionPenalty:      0.2,
			QuoteVolume24h:            15_000_000,
		},
		{
			Symbol:                    "BBBUSDT",
			Return1D:                  1,
			Return3D:                  2,
			Return7D:                  3,
			RelativeStrength:          1,
			MedianIntradayQuoteVolume: 850_000,
			TrendQuality:              1,
			BreakoutProximity:         0.95,
			VolumeAcceleration:        1.0,
			VolatilityRatio:           0.04,
			OverextensionPenalty:      0.3,
			QuoteVolume24h:            14_000_000,
		},
	}

	ranked := RankUniverseCandidates(candidates, policy)
	if len(ranked) != 2 {
		t.Fatalf("expected 2 ranked candidates, got %d", len(ranked))
	}
	if ranked[0].Symbol != "AAAUSDT" {
		t.Fatalf("expected AAAUSDT to rank first, got %s", ranked[0].Symbol)
	}
	if ranked[0].RankScore <= ranked[1].RankScore {
		t.Fatalf("expected descending rank scores, got %f <= %f", ranked[0].RankScore, ranked[1].RankScore)
	}
	if len(ranked[0].RankComponents) == 0 {
		t.Fatal("expected rank components to be populated")
	}
}

func TestSelectUniverseCandidatesAppliesRiskOffTightening(t *testing.T) {
	ranked := []UniverseCandidateMetrics{{Symbol: "A"}, {Symbol: "B"}, {Symbol: "C"}, {Symbol: "D"}}
	policy := UniversePolicy{TopK: 4, AnalyzeTopN: 3}
	active, shortlist := SelectUniverseCandidates(ranked, policy, UniverseRegimeRiskOff)
	if len(active) != 3 {
		t.Fatalf("expected risk-off active universe size 3, got %d", len(active))
	}
	if len(shortlist) != 2 {
		t.Fatalf("expected risk-off shortlist size 2, got %d", len(shortlist))
	}
}
