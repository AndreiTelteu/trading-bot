package backtest

import (
	"fmt"
	"math/rand"
	"time"
	"trading-go/internal/services"
)

type MetricCI struct {
	Mean  float64
	Lower float64
	Upper float64
}

type ValidationSummary struct {
	Windows                int
	BaselineMetrics        []Metrics
	VolSizingMetrics       []Metrics
	SharpeBaselineCI       MetricCI
	SharpeVolSizingCI      MetricCI
	MaxDrawdownBaselineCI  MetricCI
	MaxDrawdownVolSizingCI MetricCI
	ProfitFactorBaselineCI MetricCI
	ProfitFactorVolSizingCI MetricCI
	Passed                 bool
}

type WalkForwardWindow struct {
	TrainStart time.Time
	TrainEnd   time.Time
	TestStart  time.Time
	TestEnd    time.Time
}

func RunValidation(config BacktestConfig, series map[string][]services.OHLCV, trainMonths int, testMonths int, iterations int) (ValidationSummary, error) {
	windows := walkForwardSplit(config.Start, config.End, trainMonths, testMonths)
	if len(windows) == 0 {
		return ValidationSummary{}, fmt.Errorf("no validation windows")
	}

	var baselineMetrics []Metrics
	var volMetrics []Metrics

	for _, window := range windows {
		windowSeries := filterSeriesByTime(series, window.TestStart, window.TestEnd)
		if len(windowSeries) == 0 {
			continue
		}
		baselineConfig := config
		baselineConfig.StrategyMode = StrategyBaseline
		baselineConfig.Start = window.TestStart
		baselineConfig.End = window.TestEnd
		resultBaseline, err := RunBacktest(baselineConfig, windowSeries)
		if err != nil {
			continue
		}

		volConfig := config
		volConfig.StrategyMode = StrategyVolSizing
		volConfig.Start = window.TestStart
		volConfig.End = window.TestEnd
		resultVol, err := RunBacktest(volConfig, windowSeries)
		if err != nil {
			continue
		}

		baselineMetrics = append(baselineMetrics, resultBaseline.Metrics)
		volMetrics = append(volMetrics, resultVol.Metrics)
	}

	seeded := rand.New(rand.NewSource(42))
	sharpeBaseline := collectMetric(baselineMetrics, func(m Metrics) float64 { return m.Sharpe })
	sharpeVol := collectMetric(volMetrics, func(m Metrics) float64 { return m.Sharpe })
	ddBaseline := collectMetric(baselineMetrics, func(m Metrics) float64 { return m.MaxDrawdown })
	ddVol := collectMetric(volMetrics, func(m Metrics) float64 { return m.MaxDrawdown })
	pfBaseline := collectMetric(baselineMetrics, func(m Metrics) float64 { return m.ProfitFactor })
	pfVol := collectMetric(volMetrics, func(m Metrics) float64 { return m.ProfitFactor })

	sharpeBaselineCI := runBootstrap(sharpeBaseline, iterations, seeded)
	sharpeVolCI := runBootstrap(sharpeVol, iterations, seeded)
	ddBaselineCI := runBootstrap(ddBaseline, iterations, seeded)
	ddVolCI := runBootstrap(ddVol, iterations, seeded)
	pfBaselineCI := runBootstrap(pfBaseline, iterations, seeded)
	pfVolCI := runBootstrap(pfVol, iterations, seeded)

	passCount := 0
	if sharpeVolCI.Lower > sharpeBaselineCI.Upper {
		passCount++
	}
	if pfVolCI.Lower > pfBaselineCI.Upper {
		passCount++
	}
	if ddVolCI.Upper < ddBaselineCI.Lower {
		passCount++
	}

	return ValidationSummary{
		Windows:                 len(windows),
		BaselineMetrics:         baselineMetrics,
		VolSizingMetrics:        volMetrics,
		SharpeBaselineCI:        sharpeBaselineCI,
		SharpeVolSizingCI:       sharpeVolCI,
		MaxDrawdownBaselineCI:   ddBaselineCI,
		MaxDrawdownVolSizingCI:  ddVolCI,
		ProfitFactorBaselineCI:  pfBaselineCI,
		ProfitFactorVolSizingCI: pfVolCI,
		Passed:                  passCount >= 2,
	}, nil
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
