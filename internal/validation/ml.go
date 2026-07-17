package validation

import (
	"fmt"
	"math"
	"sort"
)

type MLOutcome struct {
	ID               string   `json:"id"`
	Window           int      `json:"window"`
	Symbol           string   `json:"symbol"`
	Probability      float64  `json:"probability"`
	Positive         bool     `json:"positive"`
	AfterCostReturn  float64  `json:"after_cost_return"`
	CandidateSet     []string `json:"candidate_set"`
	GrossExposure    float64  `json:"gross_exposure"`
	BaselineSet      []string `json:"baseline_set"`
	BaselineExposure float64  `json:"baseline_exposure"`
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
	Gates                        map[string]bool     `json:"gates"`
	Passed                       bool                `json:"passed"`
}

type MLRequirements struct {
	MinLabels           int     `json:"min_labels"`
	Buckets             int     `json:"buckets"`
	MinBucketSupport    int     `json:"min_bucket_support"`
	ClipEpsilon         float64 `json:"clip_epsilon"`
	MinAUC              float64 `json:"min_auc"`
	MaxLogLoss          float64 `json:"max_log_loss"`
	MaxCalibrationError float64 `json:"max_calibration_error"`
}

func EvaluateML(outcomes []MLOutcome, requirements MLRequirements) (MLEvaluation, error) {
	if requirements.MinLabels < 2 || len(outcomes) < requirements.MinLabels {
		return MLEvaluation{}, &DiagnosticError{Code: DiagnosticInsufficientObservations, Details: fmt.Sprintf("labels=%d minimum=%d", len(outcomes), requirements.MinLabels)}
	}
	if requirements.Buckets < 2 || requirements.Buckets > 20 || requirements.MinBucketSupport < 1 || requirements.ClipEpsilon <= 0 || requirements.ClipEpsilon >= .1 {
		return MLEvaluation{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "ml_requirements"}
	}
	positives, negatives := 0, 0
	for _, o := range outcomes {
		if !finite(o.Probability) || o.Probability < 0 || o.Probability > 1 || !finite(o.AfterCostReturn) || !finite(o.GrossExposure) || !finite(o.BaselineExposure) {
			return MLEvaluation{}, &DiagnosticError{Code: DiagnosticInvalidProbability, Details: o.ID}
		}
		if o.Positive {
			positives++
		} else {
			negatives++
		}
		if !sameSet(o.CandidateSet, o.BaselineSet) || math.Abs(o.GrossExposure-o.BaselineExposure) > 1e-12 {
			return MLEvaluation{}, &DiagnosticError{Code: DiagnosticBaselineMismatch, Details: o.ID}
		}
	}
	if positives == 0 || negatives == 0 {
		return MLEvaluation{}, &DiagnosticError{Code: DiagnosticOneClass}
	}
	auc := rocAUC(outcomes, positives, negatives)
	brier, logLoss, returns, probs := 0.0, 0.0, make([]float64, len(outcomes)), make([]float64, len(outcomes))
	for i, o := range outcomes {
		y := 0.0
		if o.Positive {
			y = 1
		}
		d := o.Probability - y
		brier += d * d
		p := math.Max(requirements.ClipEpsilon, math.Min(1-requirements.ClipEpsilon, o.Probability))
		logLoss -= y*math.Log(p) + (1-y)*math.Log(1-p)
		returns[i], probs[i] = o.AfterCostReturn, o.Probability
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
	expectancy := mean(returns)
	gates := map[string]bool{"discrimination": auc >= requirements.MinAUC, "log_loss": logLoss <= requirements.MaxLogLoss, "calibration": maxError <= requirements.MaxCalibrationError, "rank_monotonic": monotonic, "after_cost_expectancy": expectancy > 0, "equal_candidate_exposure": true}
	passed := true
	for _, ok := range gates {
		if !ok {
			passed = false
		}
	}
	return MLEvaluation{SchemaVersion: MLSchemaVersion, ClipEpsilon: requirements.ClipEpsilon, ROC_AUC: auc, Brier: brier, LogLoss: logLoss, ProbabilityReturnCorrelation: pearson(probs, returns), Calibration: buckets, RankMonotonic: monotonic, AfterCostExpectancy: expectancy, EqualComparison: true, Gates: gates, Passed: passed}, nil
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
			continue
		}
		b.MeanProbability /= float64(b.Support)
		b.MeanReturn /= float64(b.Support)
		b.ObservedRate = float64(positive[i]) / float64(b.Support)
		filtered = append(filtered, b)
	}
	return filtered
}

func pearson(a, b []float64) float64 {
	ma, mb := mean(a), mean(b)
	num, da, db := 0.0, 0.0, 0.0
	for i := range a {
		x, y := a[i]-ma, b[i]-mb
		num += x * y
		da += x * x
		db += y * y
	}
	if da == 0 || db == 0 {
		return 0
	}
	return num / math.Sqrt(da*db)
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
