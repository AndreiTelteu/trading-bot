package validation

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

type Sample struct {
	ID            string             `json:"id"`
	ObservedAt    time.Time          `json:"observed_at"`
	FeatureStart  time.Time          `json:"feature_start"`
	FeatureEnd    time.Time          `json:"feature_end"`
	LabelEnd      time.Time          `json:"label_end"`
	Symbol        string             `json:"symbol"`
	Regime        string             `json:"regime"`
	BenchmarkSeen bool               `json:"benchmark_seen"`
	CoverageOK    bool               `json:"coverage_ok"`
	Values        map[string]float64 `json:"values,omitempty"`
}

type Split struct {
	Train      []Sample `json:"train"`
	Validation []Sample `json:"validation"`
	Test       []Sample `json:"test"`
	Purged     []string `json:"purged_ids"`
	Embargoed  []string `json:"embargoed_ids"`
}

// SplitFold treats intervals as half-open. A sample is purged when its feature
// or outcome information interval reaches into the next evaluation boundary.
// Exact equality at the boundary is safe because LabelEnd is exclusive.
func SplitFold(samples []Sample, fold Fold, purge, embargo time.Duration) (Split, error) {
	ordered := append([]Sample(nil), samples...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].ObservedAt.Equal(ordered[j].ObservedAt) {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].ObservedAt.Before(ordered[j].ObservedAt)
	})
	result := Split{}
	for _, sample := range ordered {
		if sample.ID == "" || sample.ObservedAt.IsZero() || sample.FeatureStart.IsZero() || sample.FeatureEnd.Before(sample.FeatureStart) || sample.LabelEnd.Before(sample.ObservedAt) {
			return Split{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "sample", Details: "sample identity and causal feature/label horizon are invalid"}
		}
		switch {
		case contains(fold.Train, sample.ObservedAt):
			boundary := fold.Validation.Start.Add(-purge)
			if sample.FeatureEnd.After(boundary) || sample.LabelEnd.After(boundary) {
				result.Purged = append(result.Purged, sample.ID)
			} else {
				result.Train = append(result.Train, sample)
			}
		case contains(fold.Validation, sample.ObservedAt):
			if sample.ObservedAt.Before(fold.Train.End.Add(embargo)) {
				result.Embargoed = append(result.Embargoed, sample.ID)
			} else if sample.FeatureEnd.After(fold.Test.Start.Add(-purge)) || sample.LabelEnd.After(fold.Test.Start.Add(-purge)) {
				result.Purged = append(result.Purged, sample.ID)
			} else {
				result.Validation = append(result.Validation, sample)
			}
		case contains(fold.Test, sample.ObservedAt):
			if sample.ObservedAt.Before(fold.Validation.End.Add(embargo)) {
				result.Embargoed = append(result.Embargoed, sample.ID)
			} else {
				result.Test = append(result.Test, sample)
			}
		}
	}
	return result, nil
}

func contains(interval Interval, at time.Time) bool {
	return !at.Before(interval.Start) && at.Before(interval.End)
}

type FrozenDecision struct {
	FoldIndex       int               `json:"fold_index"`
	Choice          string            `json:"choice"`
	Parameters      map[string]string `json:"parameters"`
	FitDigest       string            `json:"fit_digest"`
	SelectionDigest string            `json:"selection_digest"`
}

func (f FrozenDecision) Digest() (string, error) {
	encoded, err := json.Marshal(f)
	if err != nil {
		return "", err
	}
	return digest(encoded), nil
}

type FoldMetrics struct {
	Observations            int                `json:"observations"`
	Trades                  int                `json:"trades"`
	BenchmarkPresent        bool               `json:"benchmark_present"`
	CoverageComplete        bool               `json:"coverage_complete"`
	Regimes                 map[string]int     `json:"regimes"`
	RegimeContributions     map[string]float64 `json:"regime_contributions"`
	AfterCostExpectancy     float64            `json:"after_cost_expectancy"`
	AfterCostReturn         float64            `json:"after_cost_return"`
	BenchmarkRelativeReturn float64            `json:"benchmark_relative_return"`
	MaxDrawdown             float64            `json:"max_drawdown"`
	Turnover                float64            `json:"turnover"`
	GrossExposure           float64            `json:"gross_exposure"`
	NetExposure             float64            `json:"net_exposure"`
	Coverage                float64            `json:"coverage"`
	TradeContributions      map[string]float64 `json:"trade_contributions,omitempty"`
	SymbolContributions     map[string]float64 `json:"symbol_contributions,omitempty"`
}

type FoldResult struct {
	Fold        Fold           `json:"fold"`
	Frozen      FrozenDecision `json:"frozen"`
	Metrics     FoldMetrics    `json:"metrics"`
	WorstRegime string         `json:"worst_regime,omitempty"`
	WorstSymbol string         `json:"worst_symbol,omitempty"`
}

// FoldRunner is intentionally generic: Stage 05 baselines, Stage 06 candidates,
// and future model trainers can implement the same fit/freeze/test contract.
// Test receives only the already frozen decision and untouched samples.
type FoldRunner interface {
	FitAndSelect(fold Fold, train, validation []Sample, allowed map[string][]string) (FrozenDecision, error)
	Test(fold Fold, frozen FrozenDecision, test []Sample) (FoldMetrics, error)
}

type WalkForwardResult struct {
	SchemaVersion string       `json:"schema_version"`
	ExperimentID  string       `json:"experiment_id"`
	Folds         []FoldResult `json:"folds"`
	Aggregate     Evaluation   `json:"aggregate"`
}

func RunWalkForward(manifest ExperimentManifest, samples []Sample, runner FoldRunner) (WalkForwardResult, error) {
	if err := manifest.Verify(); err != nil {
		return WalkForwardResult{}, err
	}
	if runner == nil {
		return WalkForwardResult{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Details: "fold runner is required"}
	}
	decisions := make([]FrozenDecision, 0, len(manifest.Spec.Folds))
	splits := make([]Split, 0, len(manifest.Spec.Folds))
	// First freeze every fold. No test slice is exposed during fit/selection.
	for _, fold := range manifest.Spec.Folds {
		split, err := SplitFold(samples, fold, manifest.Spec.Purge, manifest.Spec.Embargo)
		if err != nil {
			return WalkForwardResult{}, err
		}
		if err := validateSplit(split, manifest.Spec.Samples); err != nil {
			return WalkForwardResult{}, err
		}
		frozen, err := runner.FitAndSelect(fold, cloneSamples(split.Train), cloneSamples(split.Validation), cloneChoices(manifest.Spec.AllowedTuning))
		if err != nil {
			return WalkForwardResult{}, err
		}
		if frozen.FoldIndex != fold.Index || frozen.Choice == "" || frozen.FitDigest == "" || frozen.SelectionDigest == "" {
			return WalkForwardResult{}, &DiagnosticError{Code: DiagnosticTestLeakage, Field: fmt.Sprintf("folds[%d].frozen", fold.Index), Details: "runner did not return a complete frozen decision"}
		}
		decisions, splits = append(decisions, frozen), append(splits, split)
	}
	results := make([]FoldResult, 0, len(decisions))
	for i, frozen := range decisions {
		metrics, err := runner.Test(manifest.Spec.Folds[i], frozen, cloneSamples(splits[i].Test))
		if err != nil {
			return WalkForwardResult{}, err
		}
		if err := ValidateFoldMetrics(metrics, manifest.Spec.Samples); err != nil {
			return WalkForwardResult{}, err
		}
		worstRegime := minimumFloatKey(metrics.RegimeContributions)
		worstSymbol := minimumFloatKey(metrics.SymbolContributions)
		results = append(results, FoldResult{Fold: manifest.Spec.Folds[i], Frozen: frozen, Metrics: metrics, WorstRegime: worstRegime, WorstSymbol: worstSymbol})
	}
	evaluation, err := Evaluate(results, manifest.Spec)
	if err != nil {
		return WalkForwardResult{}, err
	}
	return WalkForwardResult{SchemaVersion: EvidenceSchemaVersion, ExperimentID: manifest.ID, Folds: results, Aggregate: evaluation}, nil
}

func validateSplit(split Split, requirements SampleRequirements) error {
	if len(split.Train) < requirements.MinObservationsPerFold || len(split.Validation) < requirements.MinObservationsPerFold || len(split.Test) < requirements.MinObservationsPerFold {
		return &DiagnosticError{Code: DiagnosticInsufficientObservations, Details: fmt.Sprintf("train=%d validation=%d test=%d minimum=%d", len(split.Train), len(split.Validation), len(split.Test), requirements.MinObservationsPerFold)}
	}
	for _, sample := range append(append(append([]Sample{}, split.Train...), split.Validation...), split.Test...) {
		if !sample.BenchmarkSeen {
			return &DiagnosticError{Code: DiagnosticMissingBenchmark, Details: sample.ID}
		}
		if !sample.CoverageOK {
			return &DiagnosticError{Code: DiagnosticIncompleteCoverage, Details: sample.ID}
		}
	}
	return nil
}

func cloneSamples(values []Sample) []Sample {
	result := append([]Sample(nil), values...)
	for i := range result {
		result[i].Values = cloneFloatMap(result[i].Values)
	}
	return result
}

func cloneChoices(values map[string][]string) map[string][]string {
	result := make(map[string][]string, len(values))
	for key, choices := range values {
		result[key] = append([]string(nil), choices...)
	}
	return result
}
