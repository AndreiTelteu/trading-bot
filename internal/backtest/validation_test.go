package backtest

import (
	"math"
	"math/rand"
	"testing"
	"time"
	"trading-go/internal/services"
)

func TestRunBootstrapConstantValues(t *testing.T) {
	values := []float64{1, 1, 1, 1}
	rng := rand.New(rand.NewSource(42))
	ci := runBootstrap(values, 100, rng)

	if math.Abs(ci.Mean-1) > 0.0001 {
		t.Errorf("Mean = %v, want 1", ci.Mean)
	}
	if math.Abs(ci.Lower-1) > 0.0001 {
		t.Errorf("Lower = %v, want 1", ci.Lower)
	}
	if math.Abs(ci.Upper-1) > 0.0001 {
		t.Errorf("Upper = %v, want 1", ci.Upper)
	}
}

func TestWalkForwardSplit(t *testing.T) {
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC)
	windows := walkForwardSplit(start, end, 6, 3)
	if len(windows) == 0 {
		t.Fatal("walkForwardSplit() expected windows")
	}
	for _, w := range windows {
		if !w.TrainEnd.After(w.TrainStart) {
			t.Errorf("Train window invalid: %v to %v", w.TrainStart, w.TrainEnd)
		}
		if !w.TestEnd.After(w.TestStart) {
			t.Errorf("Test window invalid: %v to %v", w.TestStart, w.TestEnd)
		}
		if !w.TestStart.After(w.TrainStart) {
			t.Errorf("TestStart should be after TrainStart")
		}
	}
}

func TestEvaluatePromotionReadinessRecommendsPaperWhenCoreGatesPass(t *testing.T) {
	config := BacktestConfig{
		BacktestMode: BacktestModeDynamicModel,
		UniverseMode: UniverseDynamicRecompute,
	}
	summary := validationCISet{
		AcceptedMetrics:       []string{"sharpe", "profit_factor"},
		ProfitFactorCandidate: MetricCI{Mean: 1.2},
	}
	ranking := &RankingDiagnostics{PositiveSpread: 0.5, MonotonicWinRate: true}
	regimes := []RegimeSliceMetric{{Regime: services.UniverseRegimeRiskOn, Trades: 3}, {Regime: services.UniverseRegimeNeutral, Trades: 2}}

	readiness := evaluatePromotionReadiness(config, summary, ranking, regimes)
	if !readiness.Passed {
		t.Fatalf("expected promotion readiness to pass, got %+v", readiness)
	}
	if readiness.RecommendedStage != services.ModelRolloutPaper {
		t.Fatalf("RecommendedStage = %s, want %s", readiness.RecommendedStage, services.ModelRolloutPaper)
	}
}
