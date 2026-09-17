package backtest

import (
	"math"
	"math/rand"
	"strings"
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

func TestValidateValidationWindowRejectsShortIntervalBeforeExecution(t *testing.T) {
	config := BacktestConfig{
		Start:                         time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		End:                           time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC),
		ValidationTrainMonths:         12,
		ValidationTestMonths:          3,
		ValidationBootstrapIterations: 500,
	}
	err := ValidateValidationWindow(config)
	if err == nil {
		t.Fatal("expected short interval to fail validation preflight")
	}
	want := "cannot fit validation_train_months=12 plus validation_test_months=3; end must be at or after 2026-04-01T00:00:00Z"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want substring %q", err, want)
	}
}

func TestValidateValidationWindowAcceptsExactMinimumInterval(t *testing.T) {
	config := BacktestConfig{
		Start:                         time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
		End:                           time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		ValidationTrainMonths:         12,
		ValidationTestMonths:          3,
		ValidationBootstrapIterations: 500,
	}
	if err := ValidateValidationWindow(config); err != nil {
		t.Fatalf("exact minimum interval rejected: %v", err)
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
		SharpeCandidate:       MetricCI{Mean: 1.5, Lower: 1.0, Upper: 2.0},
		SharpeBaseline:        MetricCI{Mean: 0.5, Lower: 0.1, Upper: 0.9},
	}
	ranking := &RankingDiagnostics{PositiveSpread: 0.5, MonotonicWinRate: true}
	regimes := []RegimeSliceMetric{{Regime: services.UniverseRegimeRiskOn, Trades: 3}, {Regime: services.UniverseRegimeNeutral, Trades: 2}}

	deciles := []DecileMetric{
		{Decile: 1, Trades: 5, WinRate: 0.3, AvgPnl: -1.0},
		{Decile: 2, Trades: 5, WinRate: 0.35, AvgPnl: -0.5},
		{Decile: 3, Trades: 5, WinRate: 0.4, AvgPnl: 0.0},
		{Decile: 4, Trades: 5, WinRate: 0.45, AvgPnl: 0.5},
		{Decile: 5, Trades: 5, WinRate: 0.5, AvgPnl: 1.0},
		{Decile: 6, Trades: 5, WinRate: 0.55, AvgPnl: 1.5},
		{Decile: 7, Trades: 5, WinRate: 0.6, AvgPnl: 2.0},
		{Decile: 8, Trades: 5, WinRate: 0.65, AvgPnl: 2.5},
		{Decile: 9, Trades: 5, WinRate: 0.7, AvgPnl: 3.0},
		{Decile: 10, Trades: 5, WinRate: 0.75, AvgPnl: 3.5},
	}
	readiness := evaluatePromotionReadiness(config, summary, ranking, regimes, deciles)
	if !readiness.Passed {
		t.Fatalf("expected promotion readiness to pass, got %+v", readiness)
	}
	if readiness.RecommendedStage != services.ModelRolloutPaper {
		t.Fatalf("RecommendedStage = %s, want %s", readiness.RecommendedStage, services.ModelRolloutPaper)
	}
}
