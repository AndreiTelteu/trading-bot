package validation

import (
	"encoding/json"
	"fmt"
	"reflect"
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
	ArtifactDigest  string            `json:"artifact_digest"`
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
	Primitives  FoldPrimitives `json:"primitives"`
	Metrics     FoldMetrics    `json:"metrics"`
	WorstRegime string         `json:"worst_regime,omitempty"`
	WorstSymbol string         `json:"worst_symbol,omitempty"`
}

const MaxFoldArtifactBytes = 4 << 20

type FoldFit struct {
	Choice     string            `json:"choice"`
	Parameters map[string]string `json:"parameters"`
	Artifact   []byte            `json:"artifact"`
}

type TradePrimitive struct {
	ID       string    `json:"id"`
	Symbol   string    `json:"symbol"`
	Regime   string    `json:"regime"`
	OpenedAt time.Time `json:"opened_at"`
	ClosedAt time.Time `json:"closed_at"`
	Notional float64   `json:"notional"`
	GrossPnL float64   `json:"gross_pnl"`
	Cost     float64   `json:"cost"`
	NetPnL   float64   `json:"net_pnl"`
}

type CurvePrimitive struct {
	At            time.Time `json:"at"`
	Equity        float64   `json:"equity"`
	Benchmark     float64   `json:"benchmark"`
	GrossExposure float64   `json:"gross_exposure"`
	NetExposure   float64   `json:"net_exposure"`
}

type FoldPrimitives struct {
	StartingCapital      float64          `json:"starting_capital"`
	ExpectedObservations int              `json:"expected_observations"`
	ObservedObservations int              `json:"observed_observations"`
	Trades               []TradePrimitive `json:"trades"`
	Curve                []CurvePrimitive `json:"curve"`
}

// FoldRunner is one fresh, isolated fold instance. Fit returns complete immutable
// artifact bytes; Test must use only those bytes and the untouched test samples.
type FoldRunner interface {
	FitAndSelect(fold Fold, train, validation []Sample, allowed map[string][]string) (FoldFit, error)
	Test(fold Fold, artifact []byte, test []Sample) (FoldPrimitives, error)
}

type FoldRunnerFactory interface {
	NewFoldRunner(fold Fold) (FoldRunner, error)
}

type WalkForwardResult struct {
	SchemaVersion string       `json:"schema_version"`
	ExperimentID  string       `json:"experiment_id"`
	Folds         []FoldResult `json:"folds"`
	Aggregate     Evaluation   `json:"aggregate"`
}

func RunWalkForward(manifest ExperimentManifest, samples []Sample, factory FoldRunnerFactory) (WalkForwardResult, error) {
	if err := manifest.Verify(); err != nil {
		return WalkForwardResult{}, err
	}
	if factory == nil {
		return WalkForwardResult{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Details: "fold runner factory is required"}
	}
	if err := validateSamples(samples, manifest.Spec.FeatureHorizon, manifest.Spec.LabelHorizon); err != nil {
		return WalkForwardResult{}, err
	}
	decisions := make([]FrozenDecision, 0, len(manifest.Spec.Folds))
	splits := make([]Split, 0, len(manifest.Spec.Folds))
	runners := make([]FoldRunner, 0, len(manifest.Spec.Folds))
	runnerPointers := map[uintptr]struct{}{}
	artifacts := make([][]byte, 0, len(manifest.Spec.Folds))
	// First freeze every fold. No test slice is exposed during fit/selection.
	for _, fold := range manifest.Spec.Folds {
		split, err := SplitFold(samples, fold, manifest.Spec.Purge, manifest.Spec.Embargo)
		if err != nil {
			return WalkForwardResult{}, err
		}
		if err := validateSplit(split, manifest.Spec.Samples); err != nil {
			return WalkForwardResult{}, err
		}
		runner, err := factory.NewFoldRunner(fold)
		if err != nil || runner == nil {
			return WalkForwardResult{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: fmt.Sprintf("folds[%d].runner", fold.Index), Details: "fresh fold runner is required"}
		}
		value := reflect.ValueOf(runner)
		if value.Kind() == reflect.Pointer {
			pointer := value.Pointer()
			if _, reused := runnerPointers[pointer]; reused {
				return WalkForwardResult{}, &DiagnosticError{Code: DiagnosticTestLeakage, Field: "fold_runner", Details: "runner instance reused across folds"}
			}
			runnerPointers[pointer] = struct{}{}
		}
		fit, err := runner.FitAndSelect(fold, cloneSamples(split.Train), cloneSamples(split.Validation), cloneChoices(manifest.Spec.AllowedTuning))
		if err != nil {
			return WalkForwardResult{}, err
		}
		if err := validateFit(fit, manifest.Spec.AllowedTuning); err != nil {
			return WalkForwardResult{}, err
		}
		trainDigest, err := sampleDigest(split.Train)
		if err != nil {
			return WalkForwardResult{}, err
		}
		validationDigest, err := sampleDigest(split.Validation)
		if err != nil {
			return WalkForwardResult{}, err
		}
		artifactDigest := digest(fit.Artifact)
		fitDigest, _ := canonicalDigest(struct {
			Fold                    int `json:"fold"`
			Train, Policy, Artifact string
			Seed                    int64
		}{fold.Index, trainDigest, manifest.Spec.AuthorityPolicy.Digest, artifactDigest, manifest.Spec.Seed})
		selectionDigest, _ := canonicalDigest(struct {
			Fold               int `json:"fold"`
			Validation, Choice string
			Parameters         map[string]string
			Allowed            map[string][]string
		}{fold.Index, validationDigest, fit.Choice, fit.Parameters, cloneChoices(manifest.Spec.AllowedTuning)})
		frozen := FrozenDecision{FoldIndex: fold.Index, Choice: fit.Choice, Parameters: cloneStringMap(fit.Parameters), FitDigest: fitDigest, SelectionDigest: selectionDigest, ArtifactDigest: artifactDigest}
		if frozen.Choice == "" || frozen.FitDigest == "" || frozen.SelectionDigest == "" {
			return WalkForwardResult{}, &DiagnosticError{Code: DiagnosticTestLeakage, Field: fmt.Sprintf("folds[%d].frozen", fold.Index), Details: "trusted frozen decision is incomplete"}
		}
		decisions, splits, runners, artifacts = append(decisions, frozen), append(splits, split), append(runners, runner), append(artifacts, append([]byte(nil), fit.Artifact...))
	}
	results := make([]FoldResult, 0, len(decisions))
	for i, frozen := range decisions {
		primitive, err := runners[i].Test(manifest.Spec.Folds[i], append([]byte(nil), artifacts[i]...), cloneSamples(splits[i].Test))
		if err != nil {
			return WalkForwardResult{}, err
		}
		metrics, err := DeriveFoldMetrics(primitive)
		if err != nil {
			return WalkForwardResult{}, err
		}
		if err := ValidateFoldMetrics(metrics, manifest.Spec.Samples); err != nil {
			return WalkForwardResult{}, err
		}
		worstRegime := minimumFloatKey(metrics.RegimeContributions)
		worstSymbol := minimumFloatKey(metrics.SymbolContributions)
		results = append(results, FoldResult{Fold: manifest.Spec.Folds[i], Frozen: frozen, Primitives: primitive, Metrics: metrics, WorstRegime: worstRegime, WorstSymbol: worstSymbol})
	}
	evaluation, err := Evaluate(results, manifest.Spec)
	if err != nil {
		return WalkForwardResult{}, err
	}
	return WalkForwardResult{SchemaVersion: EvidenceSchemaVersion, ExperimentID: manifest.ID, Folds: results, Aggregate: evaluation}, nil
}

func validateSamples(samples []Sample, featureHorizon, labelHorizon time.Duration) error {
	seen := make(map[string]struct{}, len(samples))
	for _, sample := range samples {
		if _, ok := seen[sample.ID]; ok {
			return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "sample.id", Details: "duplicate sample: " + sample.ID}
		}
		seen[sample.ID] = struct{}{}
		if sample.ID == "" || sample.ObservedAt.IsZero() || sample.FeatureStart.IsZero() || sample.FeatureEnd.IsZero() || sample.LabelEnd.IsZero() {
			return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "sample", Details: "complete sample timestamps are required"}
		}
		if sample.FeatureEnd.After(sample.ObservedAt) {
			return &DiagnosticError{Code: DiagnosticTestLeakage, Field: "sample.feature_end", Details: sample.ID + " contains future features"}
		}
		if !sample.FeatureEnd.Equal(sample.ObservedAt) || !sample.FeatureStart.Equal(sample.ObservedAt.Add(-featureHorizon)) {
			return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "sample.feature_horizon", Details: sample.ID}
		}
		if !sample.LabelEnd.Equal(sample.ObservedAt.Add(labelHorizon)) {
			return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "sample.label_horizon", Details: sample.ID}
		}
		for key, value := range sample.Values {
			if key == "" || !finite(value) {
				return &DiagnosticError{Code: DiagnosticNonFinite, Field: "sample.values", Details: sample.ID}
			}
		}
	}
	return nil
}

func validateFit(fit FoldFit, allowed map[string][]string) error {
	if fit.Choice == "" || len(fit.Artifact) == 0 || len(fit.Artifact) > MaxFoldArtifactBytes || len(fit.Parameters) == 0 {
		return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "fold_fit", Details: "choice, bounded artifact, and parameters are required"}
	}
	if len(fit.Parameters) != len(allowed) {
		return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "fold_fit.parameters", Details: "complete predeclared parameter set is required"}
	}
	choiceAllowed := false
	for key, value := range fit.Parameters {
		choices, ok := allowed[key]
		if !ok {
			return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "fold_fit.parameters", Details: "undeclared parameter: " + key}
		}
		found := false
		for _, candidate := range choices {
			if value == candidate {
				found = true
			}
			if fit.Choice == candidate {
				choiceAllowed = true
			}
		}
		if !found {
			return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "fold_fit.parameters", Details: "out-of-list value: " + key + "=" + value}
		}
	}
	if !choiceAllowed {
		return &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "fold_fit.choice", Details: "choice was not predeclared"}
	}
	return nil
}

func sampleDigest(samples []Sample) (string, error)        { return canonicalDigest(cloneSamples(samples)) }
func FoldResultsDigest(folds []FoldResult) (string, error) { return canonicalDigest(folds) }
func canonicalDigest(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return digest(encoded), nil
}
func cloneStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for k, v := range values {
		result[k] = v
	}
	return result
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
		result[i].ObservedAt = result[i].ObservedAt.UTC()
		result[i].FeatureStart = result[i].FeatureStart.UTC()
		result[i].FeatureEnd = result[i].FeatureEnd.UTC()
		result[i].LabelEnd = result[i].LabelEnd.UTC()
		result[i].Values = cloneFloatMap(result[i].Values)
		for key, value := range result[i].Values {
			if value == 0 {
				result[i].Values[key] = 0
			}
		}
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
