package validation

import (
	"fmt"
	"math"
	"sort"
)

type MLOutcome struct {
	ID                       string             `json:"id"`
	Window                   int                `json:"window"`
	Symbol                   string             `json:"symbol"`
	Probability              float64            `json:"probability"`
	Positive                 bool               `json:"positive"`
	AfterCostReturn          float64            `json:"after_cost_return"`
	CandidateSet             []string           `json:"candidate_set"`
	GrossExposure            float64            `json:"gross_exposure"`
	BaselineSet              []string           `json:"baseline_set"`
	BaselineExposure         float64            `json:"baseline_exposure"`
	BaselineReturn           float64            `json:"baseline_after_cost_return"`
	CandidateExposureByAsset map[string]float64 `json:"candidate_exposure_by_asset"`
	BaselineExposureByAsset  map[string]float64 `json:"baseline_exposure_by_asset"`
}

type CalibrationBucket struct {
	Lower           float64 `json:"lower"`
	Upper           float64 `json:"upper"`
	Support         int     `json:"support"`
	MeanProbability float64 `json:"mean_probability"`
	ObservedRate    float64 `json:"observed_rate"`
	MeanReturn      float64 `json:"mean_return"`
}

type MLEvaluation struct {
	SchemaVersion                string              `json:"schema_version"`
	ClipEpsilon                  float64             `json:"probability_clip_epsilon"`
	ROC_AUC                      float64             `json:"roc_auc"`
	Brier                        float64             `json:"brier"`
	LogLoss                      float64             `json:"log_loss"`
	ProbabilityReturnCorrelation float64             `json:"probability_return_correlation"`
	Calibration                  []CalibrationBucket `json:"calibration"`
	RankMonotonic                bool                `json:"rank_monotonic"`
	AfterCostExpectancy          float64             `json:"after_cost_expectancy"`
	EqualComparison              bool                `json:"equal_candidate_exposure_comparison"`
	BaselineAfterCostExpectancy  float64             `json:"baseline_after_cost_expectancy"`
	CandidateMinusBaseline       float64             `json:"candidate_minus_baseline"`
	Gates                        map[string]bool     `json:"gates"`
	Passed                       bool                `json:"passed"`
}

type MLRequirements struct {
	MinLabels             int     `json:"min_labels"`
	MinIndependentWindows int     `json:"min_independent_windows"`
	Buckets               int     `json:"buckets"`
	MinBucketSupport      int     `json:"min_bucket_support"`
	ClipEpsilon           float64 `json:"clip_epsilon"`
	MinAUC                float64 `json:"min_auc"`
	MaxLogLoss            float64 `json:"max_log_loss"`
	MaxBrier              float64 `json:"max_brier"`
	MaxCalibrationError   float64 `json:"max_calibration_error"`
}

func EvaluateML(outcomes []MLOutcome, requirements MLRequirements) (MLEvaluation, error) {
	var err error
	requirements, err = NormalizeMLRequirements(requirements)
	if err != nil {
		return MLEvaluation{}, err
	}
	if requirements.MinLabels < 2 || len(outcomes) < requirements.MinLabels {
		return MLEvaluation{}, &DiagnosticError{Code: DiagnosticInsufficientObservations, Details: fmt.Sprintf("labels=%d minimum=%d", len(outcomes), requirements.MinLabels)}
	}
	return evaluateMLNormalized(outcomes, requirements)
}

func NormalizeMLRequirements(requirements MLRequirements) (MLRequirements, error) {
	if requirements.MinAUC == 0 {
		requirements.MinAUC = .55
	}
	if requirements.MaxLogLoss == 0 {
		requirements.MaxLogLoss = .8
	}
	if requirements.MaxBrier == 0 {
		requirements.MaxBrier = .25
	}
	if requirements.MaxCalibrationError == 0 {
		requirements.MaxCalibrationError = .3
	}
	if requirements.MinIndependentWindows < 2 || requirements.Buckets < 2 || requirements.Buckets > 20 || requirements.MinBucketSupport < 1 || requirements.ClipEpsilon <= 0 || requirements.ClipEpsilon >= .1 || !finite(requirements.MinAUC) || !finite(requirements.MaxLogLoss) || !finite(requirements.MaxBrier) || !finite(requirements.MaxCalibrationError) || requirements.MinAUC < .55 || requirements.MaxLogLoss > .8 || requirements.MaxBrier > .25 || requirements.MaxCalibrationError > .3 {
		return requirements, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "ml_requirements"}
	}
	return requirements, nil
}

func evaluateMLNormalized(outcomes []MLOutcome, requirements MLRequirements) (MLEvaluation, error) {
	positives, negatives := 0, 0
	ids, windows := map[string]struct{}{}, map[int]struct{}{}
	for _, o := range outcomes {
		if o.ID == "" || o.Symbol == "" {
			return MLEvaluation{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "ml_outcome", Details: "outcome identity and symbol are required"}
		}
		if _, duplicate := ids[o.ID]; duplicate {
			return MLEvaluation{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "ml_outcome.id", Details: "duplicate outcome: " + o.ID}
		}
		ids[o.ID], windows[o.Window] = struct{}{}, struct{}{}
		if !finite(o.Probability) || o.Probability < 0 || o.Probability > 1 || !finite(o.AfterCostReturn) || !finite(o.BaselineReturn) || !finite(o.GrossExposure) || !finite(o.BaselineExposure) {
			return MLEvaluation{}, &DiagnosticError{Code: DiagnosticInvalidProbability, Details: o.ID}
		}
		if o.Positive {
			positives++
		} else {
			negatives++
		}
		if !uniqueSet(o.CandidateSet) || !uniqueSet(o.BaselineSet) || !sameSet(o.CandidateSet, o.BaselineSet) || math.Abs(o.GrossExposure-o.BaselineExposure) > 1e-12 || !equalExposure(o.CandidateExposureByAsset, o.BaselineExposureByAsset) || !exposureReconciles(o.CandidateExposureByAsset, o.GrossExposure) || !exposureReconciles(o.BaselineExposureByAsset, o.BaselineExposure) {
			return MLEvaluation{}, &DiagnosticError{Code: DiagnosticBaselineMismatch, Details: o.ID}
		}
	}
	if len(windows) < requirements.MinIndependentWindows {
		return MLEvaluation{}, &DiagnosticError{Code: DiagnosticInsufficientWindows, Details: fmt.Sprintf("windows=%d minimum=%d", len(windows), requirements.MinIndependentWindows)}
	}
	if positives == 0 || negatives == 0 {
		return MLEvaluation{}, &DiagnosticError{Code: DiagnosticOneClass}
	}
	auc := rocAUC(outcomes, positives, negatives)
	brier, logLoss, returns, baselineReturns, probs := 0.0, 0.0, make([]float64, len(outcomes)), make([]float64, len(outcomes)), make([]float64, len(outcomes))
	for i, o := range outcomes {
		y := 0.0
		if o.Positive {
			y = 1
		}
		d := o.Probability - y
		brier += d * d
		p := math.Max(requirements.ClipEpsilon, math.Min(1-requirements.ClipEpsilon, o.Probability))
		logLoss -= y*math.Log(p) + (1-y)*math.Log(1-p)
		returns[i], baselineReturns[i], probs[i] = o.AfterCostReturn, o.BaselineReturn, o.Probability
	}
	brier /= float64(len(outcomes))
	logLoss /= float64(len(outcomes))
	buckets := calibration(outcomes, requirements.Buckets)
	monotonic, maxError := true, 0.0
	previous := math.Inf(-1)
	for _, bucket := range buckets {
		if bucket.Support < requirements.MinBucketSupport {
			return MLEvaluation{}, &DiagnosticError{Code: DiagnosticInsufficientObservations, Details: "calibration bucket support is insufficient"}
		}
		if bucket.MeanReturn+1e-15 < previous {
			monotonic = false
		}
		previous = bucket.MeanReturn
		if e := math.Abs(bucket.MeanProbability - bucket.ObservedRate); e > maxError {
			maxError = e
		}
	}
	correlation, ok := pearson(probs, returns)
	if !ok {
		return MLEvaluation{}, &DiagnosticError{Code: DiagnosticInsufficientObservations, Field: "probability_return_correlation", Details: "degenerate probability or return variance"}
	}
	expectancy, baselineExpectancy := mean(returns), mean(baselineReturns)
	delta := expectancy - baselineExpectancy
	gates := map[string]bool{"discrimination": auc >= requirements.MinAUC, "brier": brier <= requirements.MaxBrier, "log_loss": logLoss <= requirements.MaxLogLoss, "calibration": maxError <= requirements.MaxCalibrationError, "rank_monotonic": monotonic, "after_cost_expectancy": expectancy > 0, "equal_candidate_exposure": true, "beats_baseline": delta > 0}
	passed := true
	for _, ok := range gates {
		if !ok {
			passed = false
		}
	}
	return MLEvaluation{SchemaVersion: MLSchemaVersion, ClipEpsilon: requirements.ClipEpsilon, ROC_AUC: auc, Brier: brier, LogLoss: logLoss, ProbabilityReturnCorrelation: correlation, Calibration: buckets, RankMonotonic: monotonic, AfterCostExpectancy: expectancy, EqualComparison: true, BaselineAfterCostExpectancy: baselineExpectancy, CandidateMinusBaseline: delta, Gates: gates, Passed: passed}, nil
}

func rocAUC(values []MLOutcome, positives, negatives int) float64 {
	ordered := append([]MLOutcome(nil), values...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].Probability == ordered[j].Probability {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].Probability < ordered[j].Probability
	})
	rankSum := 0.0
	for i := 0; i < len(ordered); {
		j := i + 1
		for j < len(ordered) && ordered[j].Probability == ordered[i].Probability {
			j++
		}
		avgRank := (float64(i+1) + float64(j)) / 2
		for k := i; k < j; k++ {
			if ordered[k].Positive {
				rankSum += avgRank
			}
		}
		i = j
	}
	return (rankSum - float64(positives*(positives+1))/2) / float64(positives*negatives)
}

func calibration(values []MLOutcome, count int) []CalibrationBucket {
	result := make([]CalibrationBucket, count)
	for i := range result {
		result[i].Lower = float64(i) / float64(count)
		result[i].Upper = float64(i+1) / float64(count)
	}
	positive := make([]int, count)
	for _, v := range values {
		index := int(v.Probability * float64(count))
		if index == count {
			index--
		}
		b := &result[index]
		b.Support++
		b.MeanProbability += v.Probability
		b.MeanReturn += v.AfterCostReturn
		if v.Positive {
			positive[index]++
		}
	}
	filtered := make([]CalibrationBucket, 0, count)
	for i, b := range result {
		if b.Support == 0 {
			filtered = append(filtered, b)
			continue
		}
		b.MeanProbability /= float64(b.Support)
		b.MeanReturn /= float64(b.Support)
		b.ObservedRate = float64(positive[i]) / float64(b.Support)
		filtered = append(filtered, b)
	}
	return filtered
}

func pearson(a, b []float64) (float64, bool) {
	ma, mb := mean(a), mean(b)
	num, da, db := 0.0, 0.0, 0.0
	for i := range a {
		x, y := a[i]-ma, b[i]-mb
		num += x * y
		da += x * x
		db += y * y
	}
	if da == 0 || db == 0 {
		return 0, false
	}
	return num / math.Sqrt(da*db), true
}
func uniqueSet(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return false
		}
		if _, ok := seen[value]; ok {
			return false
		}
		seen[value] = struct{}{}
	}
	return len(values) > 0
}
func equalExposure(a, b map[string]float64) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for key, av := range a {
		bv, ok := b[key]
		if !ok || !finite(av) || !finite(bv) || math.Abs(av-bv) > 1e-12 {
			return false
		}
	}
	return true
}
func exposureReconciles(values map[string]float64, gross float64) bool {
	total := 0.0
	for _, value := range values {
		if !finite(value) {
			return false
		}
		total += math.Abs(value)
	}
	return math.Abs(total-gross) <= 1e-12
}
func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	x, y := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(x)
	sort.Strings(y)
	for i := range x {
		if x[i] != y[i] {
			return false
		}
	}
	return true
}
