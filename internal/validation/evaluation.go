package validation

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
)

type ConfidenceInterval struct {
	Available bool    `json:"available"`
	Reason    string  `json:"reason,omitempty"`
	Mean      float64 `json:"mean,omitempty"`
	Lower     float64 `json:"lower,omitempty"`
	Upper     float64 `json:"upper,omitempty"`
}

type MetricSummary struct {
	AfterCostExpectancy     ConfidenceInterval `json:"after_cost_expectancy"`
	AfterCostReturn         ConfidenceInterval `json:"after_cost_return"`
	BenchmarkRelativeReturn ConfidenceInterval `json:"benchmark_relative_return"`
	MaxDrawdown             ConfidenceInterval `json:"max_drawdown"`
	Turnover                ConfidenceInterval `json:"turnover"`
	GrossExposure           ConfidenceInterval `json:"gross_exposure"`
	NetExposure             ConfidenceInterval `json:"net_exposure"`
	Coverage                ConfidenceInterval `json:"coverage"`
}

type Domination struct {
	TradeFraction  float64 `json:"trade_fraction"`
	SymbolFraction float64 `json:"symbol_fraction"`
	WindowFraction float64 `json:"window_fraction"`
	Dominated      bool    `json:"dominated"`
}

type Evaluation struct {
	SchemaVersion    string        `json:"schema_version"`
	StudyType        string        `json:"study_type"`
	IndependentUnits int           `json:"independent_units"`
	Metrics          MetricSummary `json:"metrics"`
	WorstWindow      int           `json:"worst_window"`
	WorstRegime      string        `json:"worst_regime"`
	WorstSymbol      string        `json:"worst_symbol"`
	Domination       Domination    `json:"domination"`
	Gates            []GateResult  `json:"gates"`
	Passed           bool          `json:"passed"`
}

type GateResult struct {
	Metric    string  `json:"metric"`
	Op        string  `json:"op"`
	Threshold float64 `json:"threshold"`
	Observed  float64 `json:"observed"`
	Passed    bool    `json:"passed"`
}

func ValidateFoldMetrics(metrics FoldMetrics, requirements SampleRequirements) error {
	if metrics.Observations < requirements.MinObservationsPerFold {
		return &DiagnosticError{Code: DiagnosticInsufficientObservations, Details: fmt.Sprintf("got %d", metrics.Observations)}
	}
	if metrics.Trades == 0 {
		return &DiagnosticError{Code: DiagnosticZeroTrades, Details: "untouched test window has no trades"}
	}
	if metrics.Trades < requirements.MinTradesPerFold {
		return &DiagnosticError{Code: DiagnosticInsufficientTrades, Details: fmt.Sprintf("got %d", metrics.Trades)}
	}
	if !metrics.BenchmarkPresent {
		return &DiagnosticError{Code: DiagnosticMissingBenchmark}
	}
	if !metrics.CoverageComplete {
		return &DiagnosticError{Code: DiagnosticIncompleteCoverage}
	}
	if len(metrics.Regimes) < requirements.MinRegimes {
		return &DiagnosticError{Code: DiagnosticInsufficientRegimes, Details: fmt.Sprintf("got %d", len(metrics.Regimes))}
	}
	if len(metrics.RegimeContributions) < requirements.MinRegimes {
		return &DiagnosticError{Code: DiagnosticInsufficientRegimes, Details: "regime contribution evidence is incomplete"}
	}
	regimeTrades := 0
	for regime, count := range metrics.Regimes {
		if regime == "" || count <= 0 {
			return &DiagnosticError{Code: DiagnosticInsufficientRegimes, Details: "invalid regime cohort"}
		}
		if _, ok := metrics.RegimeContributions[regime]; !ok {
			return &DiagnosticError{Code: DiagnosticInsufficientRegimes, Details: "regime contribution cohort mismatch"}
		}
		regimeTrades += count
	}
	if regimeTrades != metrics.Trades || len(metrics.Regimes) != len(metrics.RegimeContributions) {
		return &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "regime counts do not reconcile to trades"}
	}
	if len(metrics.TradeContributions) != metrics.Trades || len(metrics.SymbolContributions) == 0 {
		return &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: "trade and symbol contributions must be complete"}
	}
	for label, values := range map[string]map[string]float64{"trade": metrics.TradeContributions, "symbol": metrics.SymbolContributions, "regime": metrics.RegimeContributions} {
		total := 0.0
		for _, value := range values {
			total += value
		}
		if math.Abs(total-metrics.AfterCostReturn) > tolerance(math.Max(1, math.Abs(metrics.AfterCostReturn))) {
			return &DiagnosticError{Code: DiagnosticManifestIntegrity, Details: label + " contributions do not reconcile to after-cost return"}
		}
	}
	values := []float64{metrics.AfterCostExpectancy, metrics.AfterCostReturn, metrics.BenchmarkRelativeReturn, metrics.MaxDrawdown, metrics.Turnover, metrics.GrossExposure, metrics.NetExposure, metrics.Coverage}
	for _, value := range values {
		if !finite(value) {
			return &DiagnosticError{Code: DiagnosticNonFinite}
		}
	}
	for _, values := range []map[string]float64{metrics.RegimeContributions, metrics.TradeContributions, metrics.SymbolContributions} {
		for _, value := range values {
			if !finite(value) {
				return &DiagnosticError{Code: DiagnosticNonFinite}
			}
		}
	}
	return nil
}

func Evaluate(folds []FoldResult, spec ManifestSpec) (Evaluation, error) {
	minimum := spec.Samples.MinIndependentUnits
	if minimum < 2 {
		minimum = 2
	}
	if len(folds) < minimum {
		return Evaluation{}, &DiagnosticError{Code: DiagnosticInsufficientWindows, Details: fmt.Sprintf("need %d independent units, got %d", minimum, len(folds))}
	}
	if spec.StatisticalUnit != "chronological_test_window" && spec.StatisticalUnit != "declared_block" {
		return Evaluation{}, &DiagnosticError{Code: DiagnosticUnsupportedUnit}
	}
	metric := func(extract func(FoldMetrics) float64, weight func(FoldResult) float64) (ConfidenceInterval, error) {
		values := make([]float64, len(folds))
		weights := make([]float64, len(folds))
		for i := range folds {
			values[i] = extract(folds[i].Metrics)
			weights[i] = weight(folds[i])
			if !finite(weights[i]) || weights[i] <= 0 {
				weights[i] = 1
			}
		}
		return bootstrapWeighted(values, weights, spec.Seed, spec.BootstrapIterations)
	}
	capitalWeight := func(v FoldResult) float64 { return v.Primitives.StartingCapital }
	observationWeight := func(v FoldResult) float64 { return float64(v.Metrics.Observations) }
	tradeWeight := func(v FoldResult) float64 { return float64(v.Metrics.Trades) }
	windowWeight := func(FoldResult) float64 { return 1 }
	expectancy, err := metric(func(v FoldMetrics) float64 { return v.AfterCostExpectancy }, tradeWeight)
	if err != nil {
		return Evaluation{}, err
	}
	returns, err := metric(func(v FoldMetrics) float64 { return v.AfterCostReturn }, capitalWeight)
	if err != nil {
		return Evaluation{}, err
	}
	relative, err := metric(func(v FoldMetrics) float64 { return v.BenchmarkRelativeReturn }, capitalWeight)
	if err != nil {
		return Evaluation{}, err
	}
	drawdown, err := metric(func(v FoldMetrics) float64 { return v.MaxDrawdown }, windowWeight)
	if err != nil {
		return Evaluation{}, err
	}
	drawdown.Mean = chronologicalAggregateDrawdown(folds)
	turnover, err := metric(func(v FoldMetrics) float64 { return v.Turnover }, capitalWeight)
	if err != nil {
		return Evaluation{}, err
	}
	gross, err := metric(func(v FoldMetrics) float64 { return v.GrossExposure }, observationWeight)
	if err != nil {
		return Evaluation{}, err
	}
	net, err := metric(func(v FoldMetrics) float64 { return v.NetExposure }, observationWeight)
	if err != nil {
		return Evaluation{}, err
	}
	coverage, err := metric(func(v FoldMetrics) float64 { return v.Coverage }, observationWeight)
	if err != nil {
		return Evaluation{}, err
	}
	worstWindow := 0
	for i := 1; i < len(folds); i++ {
		if folds[i].Metrics.AfterCostReturn < folds[worstWindow].Metrics.AfterCostReturn {
			worstWindow = i
		}
	}
	regimeTotals, symbolTotals, symbolAbsTotals, tradeMax, totalAbs := map[string]float64{}, map[string]float64{}, map[string]float64{}, 0.0, 0.0
	windowAbs := make([]float64, len(folds))
	for i, fold := range folds {
		windowAbs[i] = math.Abs(fold.Metrics.AfterCostReturn)
		totalAbs += windowAbs[i]
		for key, value := range fold.Metrics.SymbolContributions {
			symbolTotals[key] += value
			symbolAbsTotals[key] += math.Abs(value)
		}
		for key, value := range fold.Metrics.RegimeContributions {
			regimeTotals[key] += value
		}
		for _, value := range fold.Metrics.TradeContributions {
			if math.Abs(value) > tradeMax {
				tradeMax = math.Abs(value)
			}
		}
	}
	tradeTotal := 0.0
	for _, fold := range folds {
		tradeTotal += absMapSum(fold.Metrics.TradeContributions)
	}
	domination := Domination{TradeFraction: safeFraction(tradeMax, tradeTotal), SymbolFraction: largestAbsFraction(symbolAbsTotals), WindowFraction: largestFraction(windowAbs)}
	domination.Dominated = domination.TradeFraction > .5 || domination.SymbolFraction > .6 || domination.WindowFraction > .6
	if domination.Dominated {
		return Evaluation{}, &DiagnosticError{Code: DiagnosticDominated, Details: fmt.Sprintf("trade=%.4f symbol=%.4f window=%.4f", domination.TradeFraction, domination.SymbolFraction, domination.WindowFraction)}
	}
	summary := MetricSummary{expectancy, returns, relative, drawdown, turnover, gross, net, coverage}
	gates := evaluateThresholds(spec.PromotionThresholds, summary)
	passed := true
	for _, gate := range gates {
		if !gate.Passed {
			passed = false
		}
	}
	return Evaluation{SchemaVersion: EvidenceSchemaVersion, StudyType: spec.StudyType, IndependentUnits: len(folds), Metrics: summary, WorstWindow: folds[worstWindow].Fold.Index, WorstRegime: minimumFloatKey(regimeTotals), WorstSymbol: minimumFloatKey(symbolTotals), Domination: domination, Gates: gates, Passed: passed}, nil
}

func chronologicalAggregateDrawdown(folds []FoldResult) float64 {
	equity, peak, worst := 1.0, 1.0, 0.0
	for _, fold := range folds {
		if len(fold.Primitives.Curve) >= 2 {
			for i := 1; i < len(fold.Primitives.Curve); i++ {
				previous, current := fold.Primitives.Curve[i-1].Equity, fold.Primitives.Curve[i].Equity
				if previous <= 0 || current <= 0 {
					continue
				}
				equity *= current / previous
				if equity > peak {
					peak = equity
				}
				if value := (peak - equity) / peak; value > worst {
					worst = value
				}
			}
		} else {
			equity *= 1 + fold.Metrics.AfterCostReturn
			if equity > peak {
				peak = equity
			}
			if value := (peak - equity) / peak; value > worst {
				worst = value
			}
		}
	}
	return worst
}

func bootstrap(values []float64, seed int64, iterations int) (ConfidenceInterval, error) {
	weights := make([]float64, len(values))
	for i := range weights {
		weights[i] = 1
	}
	return bootstrapWeighted(values, weights, seed, iterations)
}

func bootstrapWeighted(values, weights []float64, seed int64, iterations int) (ConfidenceInterval, error) {
	if len(values) < 2 {
		return ConfidenceInterval{Reason: "insufficient independent units"}, &DiagnosticError{Code: DiagnosticInsufficientWindows}
	}
	if iterations <= 0 || iterations > MaxBootstrapIterations {
		return ConfidenceInterval{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "bootstrap_iterations"}
	}
	if len(weights) != len(values) {
		return ConfidenceInterval{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "bootstrap_weights"}
	}
	for i, v := range values {
		if !finite(v) || !finite(weights[i]) || weights[i] <= 0 {
			return ConfidenceInterval{}, &DiagnosticError{Code: DiagnosticNonFinite}
		}
	}
	rng := rand.New(rand.NewSource(seed))
	distribution := make([]float64, iterations)
	for i := range distribution {
		sum, totalWeight := 0.0, 0.0
		for range values {
			index := rng.Intn(len(values))
			sum += values[index] * weights[index]
			totalWeight += weights[index]
		}
		distribution[i] = sum / totalWeight
	}
	sort.Float64s(distribution)
	return ConfidenceInterval{Available: true, Mean: weightedMean(values, weights), Lower: percentile(distribution, .025), Upper: percentile(distribution, .975)}, nil
}

func weightedMean(values, weights []float64) float64 {
	sum, total := 0.0, 0.0
	for i := range values {
		sum += values[i] * weights[i]
		total += weights[i]
	}
	return sum / total
}

func evaluateThresholds(thresholds []Threshold, metrics MetricSummary) []GateResult {
	lookup := map[string]float64{"after_cost_expectancy": metrics.AfterCostExpectancy.Lower, "after_cost_return": metrics.AfterCostReturn.Lower, "benchmark_relative_return": metrics.BenchmarkRelativeReturn.Lower, "max_drawdown": metrics.MaxDrawdown.Upper, "turnover": metrics.Turnover.Upper, "gross_exposure": metrics.GrossExposure.Upper, "net_exposure": metrics.NetExposure.Upper, "coverage": metrics.Coverage.Lower}
	result := make([]GateResult, 0, len(thresholds))
	for _, threshold := range thresholds {
		observed, ok := lookup[threshold.Metric]
		passed := ok && compare(observed, threshold.Op, threshold.Value)
		result = append(result, GateResult{threshold.Metric, threshold.Op, threshold.Value, observed, passed})
	}
	return result
}

func compare(value float64, op string, threshold float64) bool {
	switch op {
	case ">":
		return value > threshold
	case ">=":
		return value >= threshold
	case "<":
		return value < threshold
	case "<=":
		return value <= threshold
	}
	return false
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func mean(v []float64) float64 {
	total := 0.0
	for _, x := range v {
		total += x
	}
	return total / float64(len(v))
}
func percentile(v []float64, p float64) float64 {
	if len(v) == 1 {
		return v[0]
	}
	x := p * float64(len(v)-1)
	lo := int(math.Floor(x))
	hi := int(math.Ceil(x))
	if lo == hi {
		return v[lo]
	}
	return v[lo] + (v[hi]-v[lo])*(x-float64(lo))
}
func safeFraction(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return math.Abs(a) / math.Abs(b)
}
func largestFraction(values []float64) float64 {
	sum, max := 0.0, 0.0
	for _, v := range values {
		v = math.Abs(v)
		sum += v
		if v > max {
			max = v
		}
	}
	return safeFraction(max, sum)
}
func absMapSum(values map[string]float64) float64 {
	total := 0.0
	for _, v := range values {
		total += math.Abs(v)
	}
	return total
}
func largestAbsFraction(values map[string]float64) float64 {
	max := 0.0
	for _, v := range values {
		if math.Abs(v) > max {
			max = math.Abs(v)
		}
	}
	return safeFraction(max, absMapSum(values))
}
func minimumKey(values map[string]int) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	best := keys[0]
	for _, k := range keys[1:] {
		if values[k] < values[best] {
			best = k
		}
	}
	return best
}
func minimumFloatKey(values map[string]float64) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return ""
	}
	best := keys[0]
	for _, k := range keys[1:] {
		if values[k] < values[best] {
			best = k
		}
	}
	return best
}
func cloneFloatMap(v map[string]float64) map[string]float64 {
	r := make(map[string]float64, len(v))
	for k, x := range v {
		r[k] = x
	}
	return r
}
