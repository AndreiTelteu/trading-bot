package backtest

import (
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"time"
	"trading-go/internal/services"
)

type MetricCI struct {
	Mean  float64 `json:"mean"`
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

type ValidationSummary struct {
	BacktestMode                    BacktestMode              `json:"backtest_mode"`
	ModelVersion                    string                    `json:"model_version,omitempty"`
	UniverseMode                    UniverseMode              `json:"universe_mode"`
	PolicyVersion                   string                    `json:"policy_version,omitempty"`
	RolloutState                    string                    `json:"rollout_state,omitempty"`
	Windows                         int                       `json:"windows"`
	TrainWindows                    int                       `json:"train_windows"`
	TestWindows                     int                       `json:"test_windows"`
	TrainingBaselineMetrics         []Metrics                 `json:"training_baseline_metrics"`
	TrainingVolSizingMetrics        []Metrics                 `json:"training_vol_sizing_metrics"`
	BaselineMetrics                 []Metrics                 `json:"baseline_metrics"`
	VolSizingMetrics                []Metrics                 `json:"vol_sizing_metrics"`
	TrainingSharpeBaselineCI        MetricCI                  `json:"training_sharpe_baseline_ci"`
	TrainingSharpeVolSizingCI       MetricCI                  `json:"training_sharpe_vol_sizing_ci"`
	SharpeBaselineCI                MetricCI                  `json:"sharpe_baseline_ci"`
	SharpeVolSizingCI               MetricCI                  `json:"sharpe_vol_sizing_ci"`
	TrainingMaxDrawdownBaselineCI   MetricCI                  `json:"training_max_drawdown_baseline_ci"`
	TrainingMaxDrawdownVolSizingCI  MetricCI                  `json:"training_max_drawdown_vol_sizing_ci"`
	MaxDrawdownBaselineCI           MetricCI                  `json:"max_drawdown_baseline_ci"`
	MaxDrawdownVolSizingCI          MetricCI                  `json:"max_drawdown_vol_sizing_ci"`
	TrainingProfitFactorBaselineCI  MetricCI                  `json:"training_profit_factor_baseline_ci"`
	TrainingProfitFactorVolSizingCI MetricCI                  `json:"training_profit_factor_vol_sizing_ci"`
	ProfitFactorBaselineCI          MetricCI                  `json:"profit_factor_baseline_ci"`
	ProfitFactorVolSizingCI         MetricCI                  `json:"profit_factor_vol_sizing_ci"`
	TrainingAcceptedMetrics         []string                  `json:"training_accepted_metrics"`
	AcceptedMetrics                 []string                  `json:"accepted_metrics"`
	BaselineRankingDiagnostics      *RankingDiagnostics       `json:"baseline_ranking_diagnostics,omitempty"`
	VolSizingRankingDiagnostics     *RankingDiagnostics       `json:"vol_sizing_ranking_diagnostics,omitempty"`
	BaselineRegimeSlices            []RegimeSliceMetric       `json:"baseline_regime_slices,omitempty"`
	VolSizingRegimeSlices           []RegimeSliceMetric       `json:"vol_sizing_regime_slices,omitempty"`
	BaselineSymbolCohorts           []SymbolCohortMetric      `json:"baseline_symbol_cohorts,omitempty"`
	VolSizingSymbolCohorts          []SymbolCohortMetric      `json:"vol_sizing_symbol_cohorts,omitempty"`
	WindowSummaries                 []ValidationWindowSummary `json:"window_summaries,omitempty"`
	BaselineDecileMetrics           []DecileMetric            `json:"baseline_decile_metrics,omitempty"`
	VolSizingDecileMetrics          []DecileMetric            `json:"vol_sizing_decile_metrics,omitempty"`
	PromotionReadiness              PromotionReadiness        `json:"promotion_readiness"`
	TrainingPassed                  bool                      `json:"training_passed"`
	Passed                          bool                      `json:"passed"`
}

type ValidationWindowSummary struct {
	Window            WalkForwardWindow   `json:"window"`
	TrainingBaseline  Metrics             `json:"training_baseline"`
	TrainingVolSizing Metrics             `json:"training_vol_sizing"`
	Baseline          Metrics             `json:"baseline"`
	VolSizing         Metrics             `json:"vol_sizing"`
	BaselineRanking   *RankingDiagnostics `json:"baseline_ranking,omitempty"`
	VolSizingRanking  *RankingDiagnostics `json:"vol_sizing_ranking,omitempty"`
}

type PromotionGateResult struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details,omitempty"`
}

type PromotionReadiness struct {
	RecommendedStage string                `json:"recommended_stage"`
	Passed           bool                  `json:"passed"`
	Gates            []PromotionGateResult `json:"gates"`
}

type WalkForwardWindow struct {
	TrainStart time.Time `json:"train_start"`
	TrainEnd   time.Time `json:"train_end"`
	TestStart  time.Time `json:"test_start"`
	TestEnd    time.Time `json:"test_end"`
}

type validationCISet struct {
	SharpeBaseline        MetricCI
	SharpeCandidate       MetricCI
	MaxDrawdownBaseline   MetricCI
	MaxDrawdownCandidate  MetricCI
	ProfitFactorBaseline  MetricCI
	ProfitFactorCandidate MetricCI
	AcceptedMetrics       []string
}

func RunValidation(config BacktestConfig, series map[string][]services.OHLCV, trainMonths int, testMonths int, iterations int) (ValidationSummary, error) {
	windows := walkForwardSplit(config.Start, config.End, trainMonths, testMonths)
	if len(windows) == 0 {
		return ValidationSummary{}, fmt.Errorf("no validation windows")
	}

	type windowResult struct {
		index           int
		trainBaseline   BacktestResult
		trainVol        BacktestResult
		testBaseline    BacktestResult
		testVol         BacktestResult
		window          WalkForwardWindow
		ok              bool
	}

	results := make([]windowResult, len(windows))
	workers := runtime.NumCPU()
	if workers > len(windows) {
		workers = len(windows)
	}
	if workers < 1 {
		workers = 1
	}

	windowCh := make(chan int, len(windows))
	for i := range windows {
		windowCh <- i
	}
	close(windowCh)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range windowCh {
				window := windows[idx]
				trainSeries := filterSeriesByTime(series, window.TrainStart, window.TrainEnd)
				testSeries := filterSeriesByTime(series, window.TestStart, window.TestEnd)
				if len(trainSeries) == 0 || len(testSeries) == 0 {
					continue
				}
				trainBaseline, trainVol, err := runValidationPair(config, trainSeries, window.TrainStart, window.TrainEnd)
				if err != nil {
					continue
				}
				testBaseline, testVol, err := runValidationPair(config, testSeries, window.TestStart, window.TestEnd)
				if err != nil {
					continue
				}
				results[idx] = windowResult{
					index:         idx,
					trainBaseline: trainBaseline,
					trainVol:      trainVol,
					testBaseline:  testBaseline,
					testVol:       testVol,
					window:        window,
					ok:            true,
				}
			}
		}()
	}
	wg.Wait()

	var trainBaselineMetrics []Metrics
	var trainVolMetrics []Metrics
	var testBaselineMetrics []Metrics
	var testVolMetrics []Metrics
	var baselineTrades []Trade
	var volTrades []Trade
	windowSummaries := make([]ValidationWindowSummary, 0, len(windows))

	for _, res := range results {
		if !res.ok {
			continue
		}
		trainBaselineMetrics = append(trainBaselineMetrics, res.trainBaseline.Metrics)
		trainVolMetrics = append(trainVolMetrics, res.trainVol.Metrics)
		testBaselineMetrics = append(testBaselineMetrics, res.testBaseline.Metrics)
		testVolMetrics = append(testVolMetrics, res.testVol.Metrics)
		baselineTrades = append(baselineTrades, res.testBaseline.Trades...)
		volTrades = append(volTrades, res.testVol.Trades...)
		windowSummaries = append(windowSummaries, ValidationWindowSummary{
			Window:            res.window,
			TrainingBaseline:  res.trainBaseline.Metrics,
			TrainingVolSizing: res.trainVol.Metrics,
			Baseline:          res.testBaseline.Metrics,
			VolSizing:         res.testVol.Metrics,
			BaselineRanking:   rankingDiagnostics(res.testBaseline.RankingMetrics),
			VolSizingRanking:  rankingDiagnostics(res.testVol.RankingMetrics),
		})
	}

	if len(testBaselineMetrics) == 0 || len(testVolMetrics) == 0 {
		return ValidationSummary{}, fmt.Errorf("no successful validation windows")
	}

	trainingSummary := summarizeValidationMetrics(trainBaselineMetrics, trainVolMetrics, iterations)
	testSummary := summarizeValidationMetrics(testBaselineMetrics, testVolMetrics, iterations)
	trainingPassed := len(trainingSummary.AcceptedMetrics) >= 2
	testPassed := len(testSummary.AcceptedMetrics) >= 2
	baselineRanking := buildRankingDiagnosticsFromTrades(baselineTrades, config)
	volRanking := buildRankingDiagnosticsFromTrades(volTrades, config)
	baselineRegime := buildRegimeSliceMetrics(baselineTrades)
	volRegime := buildRegimeSliceMetrics(volTrades)
	baselineSymbols := buildSymbolCohortMetrics(baselineTrades)
	volSymbols := buildSymbolCohortMetrics(volTrades)
	baselineDeciles := buildDecileMetrics(baselineTrades)
	volDeciles := buildDecileMetrics(volTrades)
	readiness := evaluatePromotionReadiness(config, testSummary, volRanking, volRegime, volDeciles)

	return ValidationSummary{
		BacktestMode:                    config.BacktestMode,
		ModelVersion:                    config.Governance.ModelVersion,
		UniverseMode:                    config.UniverseMode,
		PolicyVersion:                   config.Governance.PolicyVersions.CompositeVersion,
		RolloutState:                    config.Governance.RolloutState,
		Windows:                         len(windows),
		TrainWindows:                    len(trainBaselineMetrics),
		TestWindows:                     len(testBaselineMetrics),
		TrainingBaselineMetrics:         trainBaselineMetrics,
		TrainingVolSizingMetrics:        trainVolMetrics,
		BaselineMetrics:                 testBaselineMetrics,
		VolSizingMetrics:                testVolMetrics,
		TrainingSharpeBaselineCI:        trainingSummary.SharpeBaseline,
		TrainingSharpeVolSizingCI:       trainingSummary.SharpeCandidate,
		SharpeBaselineCI:                testSummary.SharpeBaseline,
		SharpeVolSizingCI:               testSummary.SharpeCandidate,
		TrainingMaxDrawdownBaselineCI:   trainingSummary.MaxDrawdownBaseline,
		TrainingMaxDrawdownVolSizingCI:  trainingSummary.MaxDrawdownCandidate,
		MaxDrawdownBaselineCI:           testSummary.MaxDrawdownBaseline,
		MaxDrawdownVolSizingCI:          testSummary.MaxDrawdownCandidate,
		TrainingProfitFactorBaselineCI:  trainingSummary.ProfitFactorBaseline,
		TrainingProfitFactorVolSizingCI: trainingSummary.ProfitFactorCandidate,
		ProfitFactorBaselineCI:          testSummary.ProfitFactorBaseline,
		ProfitFactorVolSizingCI:         testSummary.ProfitFactorCandidate,
		TrainingAcceptedMetrics:         trainingSummary.AcceptedMetrics,
		AcceptedMetrics:                 testSummary.AcceptedMetrics,
		BaselineRankingDiagnostics:      baselineRanking,
		VolSizingRankingDiagnostics:     volRanking,
		BaselineRegimeSlices:            baselineRegime,
		VolSizingRegimeSlices:           volRegime,
		BaselineSymbolCohorts:           baselineSymbols,
		VolSizingSymbolCohorts:          volSymbols,
		WindowSummaries:                 windowSummaries,
		BaselineDecileMetrics:           baselineDeciles,
		VolSizingDecileMetrics:          volDeciles,
		PromotionReadiness:              readiness,
		TrainingPassed:                  trainingPassed,
		Passed:                          trainingPassed && testPassed,
	}, nil
}

func buildRankingDiagnosticsFromTrades(trades []Trade, config BacktestConfig) *RankingDiagnostics {
	ranking := buildRankingMetrics(trades, config)
	if ranking == nil {
		return nil
	}
	return ranking.Diagnostics
}

func evaluatePromotionReadiness(config BacktestConfig, summary validationCISet, ranking *RankingDiagnostics, regimeSlices []RegimeSliceMetric, deciles []DecileMetric) PromotionReadiness {
	gates := []PromotionGateResult{
		{
			Name:    "walk_forward_complete",
			Passed:  len(summary.AcceptedMetrics) >= 2,
			Details: fmt.Sprintf("accepted metrics: %s", strings.Join(summary.AcceptedMetrics, ",")),
		},
		{
			Name:    "dynamic_universe_included",
			Passed:  config.UniverseMode == UniverseDynamicRecompute || config.UniverseMode == UniverseDynamicReplay,
			Details: fmt.Sprintf("universe mode: %s", config.UniverseMode),
		},
		{
			Name:    "profit_factor_after_costs",
			Passed:  summary.ProfitFactorCandidate.Mean > 1,
			Details: fmt.Sprintf("mean profit factor: %.3f", summary.ProfitFactorCandidate.Mean),
		},
		{
			Name:    "ranking_spread_positive",
			Passed:  ranking != nil && ranking.PositiveSpread > 0,
			Details: formatRankingReadiness(ranking),
		},
		{
			Name:    "regime_slice_coverage",
			Passed:  len(regimeSlices) >= 2,
			Details: fmt.Sprintf("regime slices: %d", len(regimeSlices)),
		},
		evaluateCalibrationGate(deciles),
		evaluateOutperformsBaselineGate(summary),
	}
	passed := true
	passedCount := 0
	for _, gate := range gates {
		if gate.Passed {
			passedCount++
		} else {
			passed = false
		}
	}
	recommendedStage := services.ModelRolloutResearchOnly
	switch {
	case passed:
		recommendedStage = services.ModelRolloutPaper
	case passedCount >= 3:
		recommendedStage = services.ModelRolloutShadow
	}
	return PromotionReadiness{
		RecommendedStage: recommendedStage,
		Passed:           passed,
		Gates:            gates,
	}
}

// evaluateCalibrationGate checks if top probability buckets have higher win rates than bottom ones.
func evaluateCalibrationGate(deciles []DecileMetric) PromotionGateResult {
	if len(deciles) < 4 {
		return PromotionGateResult{
			Name:    "calibration_acceptable",
			Passed:  false,
			Details: fmt.Sprintf("insufficient deciles: %d", len(deciles)),
		}
	}
	// Compare top 3 deciles avg win rate vs bottom 3 deciles avg win rate
	topWinRate := 0.0
	topCount := 0
	bottomWinRate := 0.0
	bottomCount := 0
	for i := 0; i < 3 && i < len(deciles); i++ {
		if deciles[i].Trades > 0 {
			bottomWinRate += deciles[i].WinRate
			bottomCount++
		}
	}
	for i := len(deciles) - 3; i < len(deciles); i++ {
		if i >= 0 && deciles[i].Trades > 0 {
			topWinRate += deciles[i].WinRate
			topCount++
		}
	}
	if topCount > 0 {
		topWinRate /= float64(topCount)
	}
	if bottomCount > 0 {
		bottomWinRate /= float64(bottomCount)
	}
	passed := topCount > 0 && bottomCount > 0 && topWinRate > bottomWinRate
	return PromotionGateResult{
		Name:    "calibration_acceptable",
		Passed:  passed,
		Details: fmt.Sprintf("top_decile_win_rate=%.3f bottom_decile_win_rate=%.3f", topWinRate, bottomWinRate),
	}
}

// evaluateOutperformsBaselineGate checks if vol_sizing sharpe CI lower bound > baseline sharpe CI upper bound.
func evaluateOutperformsBaselineGate(summary validationCISet) PromotionGateResult {
	passed := summary.SharpeCandidate.Lower > summary.SharpeBaseline.Upper
	return PromotionGateResult{
		Name:    "outperforms_baseline",
		Passed:  passed,
		Details: fmt.Sprintf("vol_sizing_sharpe_ci_lower=%.4f baseline_sharpe_ci_upper=%.4f", summary.SharpeCandidate.Lower, summary.SharpeBaseline.Upper),
	}
}

func formatRankingReadiness(ranking *RankingDiagnostics) string {
	if ranking == nil {
		return "no ranking diagnostics"
	}
	return fmt.Sprintf("spread %.4f, monotonic_win_rate=%t", ranking.PositiveSpread, ranking.MonotonicWinRate)
}

func runValidationPair(config BacktestConfig, series map[string][]services.OHLCV, start time.Time, end time.Time) (BacktestResult, BacktestResult, error) {
	baselineConfig := config
	baselineConfig.StrategyMode = StrategyBaseline
	baselineConfig.Start = start
	baselineConfig.End = end
	resultBaseline, err := RunBacktest(baselineConfig, series)
	if err != nil {
		return BacktestResult{}, BacktestResult{}, err
	}

	volConfig := config
	volConfig.StrategyMode = StrategyVolSizing
	volConfig.Start = start
	volConfig.End = end
	resultVol, err := RunBacktest(volConfig, series)
	if err != nil {
		return BacktestResult{}, BacktestResult{}, err
	}

	return resultBaseline, resultVol, nil
}

func summarizeValidationMetrics(baselineMetrics []Metrics, candidateMetrics []Metrics, iterations int) validationCISet {
	seeded := rand.New(rand.NewSource(42))
	sharpeBaseline := collectMetric(baselineMetrics, func(m Metrics) float64 { return m.Sharpe })
	sharpeCandidate := collectMetric(candidateMetrics, func(m Metrics) float64 { return m.Sharpe })
	ddBaseline := collectMetric(baselineMetrics, func(m Metrics) float64 { return m.MaxDrawdown })
	ddCandidate := collectMetric(candidateMetrics, func(m Metrics) float64 { return m.MaxDrawdown })
	pfBaseline := collectMetric(baselineMetrics, func(m Metrics) float64 { return m.ProfitFactor })
	pfCandidate := collectMetric(candidateMetrics, func(m Metrics) float64 { return m.ProfitFactor })

	set := validationCISet{
		SharpeBaseline:        runBootstrap(sharpeBaseline, iterations, seeded),
		SharpeCandidate:       runBootstrap(sharpeCandidate, iterations, seeded),
		MaxDrawdownBaseline:   runBootstrap(ddBaseline, iterations, seeded),
		MaxDrawdownCandidate:  runBootstrap(ddCandidate, iterations, seeded),
		ProfitFactorBaseline:  runBootstrap(pfBaseline, iterations, seeded),
		ProfitFactorCandidate: runBootstrap(pfCandidate, iterations, seeded),
	}
	set.AcceptedMetrics = compareCI(set)
	return set
}

func compareCI(set validationCISet) []string {
	accepted := make([]string, 0, 3)
	if candidateExcludesBaseline(set.SharpeBaseline, set.SharpeCandidate, true) {
		accepted = append(accepted, "sharpe")
	}
	if candidateExcludesBaseline(set.ProfitFactorBaseline, set.ProfitFactorCandidate, true) {
		accepted = append(accepted, "profit_factor")
	}
	if candidateExcludesBaseline(set.MaxDrawdownBaseline, set.MaxDrawdownCandidate, false) {
		accepted = append(accepted, "max_drawdown")
	}
	return accepted
}

func candidateExcludesBaseline(baseline MetricCI, candidate MetricCI, higherIsBetter bool) bool {
	if higherIsBetter {
		return candidate.Lower > baseline.Upper
	}
	return candidate.Upper < baseline.Lower
}

func walkForwardSplit(start time.Time, end time.Time, trainMonths int, testMonths int) []WalkForwardWindow {
	if trainMonths <= 0 || testMonths <= 0 {
		return nil
	}
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return nil
	}

	var windows []WalkForwardWindow
	cursor := start
	for {
		trainStart := cursor
		trainEnd := addMonths(trainStart, trainMonths)
		testStart := trainEnd
		testEnd := addMonths(testStart, testMonths)
		if testEnd.After(end) {
			break
		}
		windows = append(windows, WalkForwardWindow{
			TrainStart: trainStart,
			TrainEnd:   trainEnd,
			TestStart:  testStart,
			TestEnd:    testEnd,
		})
		cursor = testStart
	}
	return windows
}

func runBootstrap(values []float64, iterations int, rng *rand.Rand) MetricCI {
	if len(values) == 0 {
		return MetricCI{}
	}
	if iterations <= 0 {
		iterations = 500
	}
	samples := make([]float64, iterations)
	for i := 0; i < iterations; i++ {
		total := 0.0
		for j := 0; j < len(values); j++ {
			idx := rng.Intn(len(values))
			total += values[idx]
		}
		samples[i] = total / float64(len(values))
	}
	return MetricCI{
		Mean:  meanValue(values),
		Lower: percentile(samples, 0.025),
		Upper: percentile(samples, 0.975),
	}
}

func collectMetric(metrics []Metrics, selector func(Metrics) float64) []float64 {
	values := make([]float64, 0, len(metrics))
	for _, m := range metrics {
		values = append(values, selector(m))
	}
	return values
}

func filterSeriesByTime(series map[string][]services.OHLCV, start time.Time, end time.Time) map[string][]services.OHLCV {
	result := map[string][]services.OHLCV{}
	startMs := start.UnixMilli()
	endMs := end.UnixMilli()
	for symbol, candles := range series {
		var filtered []services.OHLCV
		for _, c := range candles {
			if c.OpenTime >= startMs && c.OpenTime <= endMs {
				filtered = append(filtered, c)
			}
		}
		if len(filtered) > 0 {
			result[symbol] = filtered
		}
	}
	return result
}

func addMonths(t time.Time, months int) time.Time {
	return t.AddDate(0, months, 0)
}
