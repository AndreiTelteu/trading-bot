package backtest

import (
	"fmt"
	"math/rand"
	"time"
	"trading-go/internal/services"
)

type MetricCI struct {
	Mean  float64 `json:"mean"`
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
}

type ValidationSummary struct {
	Windows                         int       `json:"windows"`
	TrainWindows                    int       `json:"train_windows"`
	TestWindows                     int       `json:"test_windows"`
	TrainingBaselineMetrics         []Metrics `json:"training_baseline_metrics"`
	TrainingVolSizingMetrics        []Metrics `json:"training_vol_sizing_metrics"`
	BaselineMetrics                 []Metrics `json:"baseline_metrics"`
	VolSizingMetrics                []Metrics `json:"vol_sizing_metrics"`
	TrainingSharpeBaselineCI        MetricCI  `json:"training_sharpe_baseline_ci"`
	TrainingSharpeVolSizingCI       MetricCI  `json:"training_sharpe_vol_sizing_ci"`
	SharpeBaselineCI                MetricCI  `json:"sharpe_baseline_ci"`
	SharpeVolSizingCI               MetricCI  `json:"sharpe_vol_sizing_ci"`
	TrainingMaxDrawdownBaselineCI   MetricCI  `json:"training_max_drawdown_baseline_ci"`
	TrainingMaxDrawdownVolSizingCI  MetricCI  `json:"training_max_drawdown_vol_sizing_ci"`
	MaxDrawdownBaselineCI           MetricCI  `json:"max_drawdown_baseline_ci"`
	MaxDrawdownVolSizingCI          MetricCI  `json:"max_drawdown_vol_sizing_ci"`
	TrainingProfitFactorBaselineCI  MetricCI  `json:"training_profit_factor_baseline_ci"`
	TrainingProfitFactorVolSizingCI MetricCI  `json:"training_profit_factor_vol_sizing_ci"`
	ProfitFactorBaselineCI          MetricCI  `json:"profit_factor_baseline_ci"`
	ProfitFactorVolSizingCI         MetricCI  `json:"profit_factor_vol_sizing_ci"`
	TrainingAcceptedMetrics         []string  `json:"training_accepted_metrics"`
	AcceptedMetrics                 []string  `json:"accepted_metrics"`
	TrainingPassed                  bool      `json:"training_passed"`
	Passed                          bool      `json:"passed"`
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

	var trainBaselineMetrics []Metrics
	var trainVolMetrics []Metrics
	var testBaselineMetrics []Metrics
	var testVolMetrics []Metrics

	for _, window := range windows {
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

		trainBaselineMetrics = append(trainBaselineMetrics, trainBaseline.Metrics)
		trainVolMetrics = append(trainVolMetrics, trainVol.Metrics)
		testBaselineMetrics = append(testBaselineMetrics, testBaseline.Metrics)
		testVolMetrics = append(testVolMetrics, testVol.Metrics)
	}

	if len(testBaselineMetrics) == 0 || len(testVolMetrics) == 0 {
		return ValidationSummary{}, fmt.Errorf("no successful validation windows")
	}

	trainingSummary := summarizeValidationMetrics(trainBaselineMetrics, trainVolMetrics, iterations)
	testSummary := summarizeValidationMetrics(testBaselineMetrics, testVolMetrics, iterations)
	trainingPassed := len(trainingSummary.AcceptedMetrics) >= 2
	testPassed := len(testSummary.AcceptedMetrics) >= 2

	return ValidationSummary{
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
		TrainingPassed:                  trainingPassed,
		Passed:                          trainingPassed && testPassed,
	}, nil
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
