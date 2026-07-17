package validation

import (
	"errors"
	"math"
	"reflect"
	"testing"
	"time"
)

func manifestFixture(t *testing.T) ExperimentManifest {
	t.Helper()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	spec := ManifestSpec{
		SchemaVersion: ManifestSchemaVersion, StudyType: "confirmatory", CodeRevision: "0657c08",
		Candidate: VersionRef{ID: "trend-momentum", Version: "1.0.0"}, Baseline: VersionRef{ID: "momentum", Version: "1.0.0"},
		Policies:          PolicyBundle{Composite: "policy-v1", Execution: "exec-v1", Universe: "universe-v1", ModelSelection: "model-v1", EntrySelection: "entry-v1", PortfolioRisk: "risk-v1", Rollout: "rollout-v1", Cost: "cost-v1"},
		DatasetManifestID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", DatasetManifestHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", UniversePolicy: "universe-v1",
		Interval: Interval{base, base.Add(18 * 24 * time.Hour)}, DecisionClock: "4h-close", ExecutionClock: "next-1m-open", Seed: 42, ExecutionSemantics: map[string]string{"fee_bps": "10", "slippage_bps": "5"},
		Folds: []Fold{
			{Index: 0, Train: Interval{base, base.Add(3 * 24 * time.Hour)}, Validation: Interval{base.Add(3 * 24 * time.Hour), base.Add(5 * 24 * time.Hour)}, Test: Interval{base.Add(5 * 24 * time.Hour), base.Add(7 * 24 * time.Hour)}},
			{Index: 1, Train: Interval{base.Add(4 * 24 * time.Hour), base.Add(8 * 24 * time.Hour)}, Validation: Interval{base.Add(8 * 24 * time.Hour), base.Add(10 * 24 * time.Hour)}, Test: Interval{base.Add(10 * 24 * time.Hour), base.Add(12 * 24 * time.Hour)}},
			{Index: 2, Train: Interval{base.Add(9 * 24 * time.Hour), base.Add(13 * 24 * time.Hour)}, Validation: Interval{base.Add(13 * 24 * time.Hour), base.Add(15 * 24 * time.Hour)}, Test: Interval{base.Add(15 * 24 * time.Hour), base.Add(17 * 24 * time.Hour)}},
		},
		FeatureHorizon: time.Hour, LabelHorizon: time.Hour, Purge: time.Hour, Embargo: time.Hour,
		AllowedTuning: map[string][]string{"lookback": {"20", "30"}}, Metrics: []string{"after_cost_return", "coverage"}, StatisticalUnit: "chronological_test_window", BootstrapIterations: 200,
		Samples:             SampleRequirements{MinFolds: 3, MinIndependentUnits: 3, MinObservationsPerFold: 1, MinTradesPerFold: 1, MinRegimes: 2},
		PromotionThresholds: []Threshold{{Metric: "coverage", Op: ">=", Value: .9}, {Metric: "after_cost_return", Op: ">", Value: -1}}, RollbackThresholds: []Threshold{{Metric: "max_drawdown", Op: ">=", Value: .2}},
		Artifacts: ArtifactLinks{Metrics: "metrics.json", Trades: "trades.parquet", Curves: "curves.parquet", Cohorts: "cohorts.json", Factors: "factors.json", Coverage: "coverage.json", Comparison: "comparison.json"},
		Reproduce: ReproductionInvocation{Command: "trading-bot", Args: []string{"validate", "--manifest", "manifest.json"}},
	}
	manifest, err := NewManifest(spec, base.Add(20*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestManifestDeterminismMutationAndIntegrity(t *testing.T) {
	m := manifestFixture(t)
	same, err := NewManifest(m.Spec, m.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if same.ID != m.ID || same.ContentID != m.ContentID {
		t.Fatal("identical semantic content and creation time must reproduce both identities")
	}
	copy := m.Spec
	copy.ExecutionSemantics = map[string]string{"slippage_bps": "5", "fee_bps": "10"}
	reordered, err := NewManifest(copy, m.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if reordered.ContentID != m.ContentID {
		t.Fatal("map order changed identity")
	}
	mutated := m
	mutated.Spec.Seed++
	err = mutated.Verify()
	diagnosticCode(err, DiagnosticManifestIntegrity, t)
	changed, err := NewManifest(mutated.Spec, m.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if changed.ContentID == m.ContentID {
		t.Fatal("semantic mutation retained identity")
	}
	recordedLater, err := NewManifest(m.Spec, m.CreatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if recordedLater.ContentID != m.ContentID || recordedLater.ID == m.ID {
		t.Fatal("creation time must affect record identity only")
	}
}

func TestPurgeEmbargoExactBoundaries(t *testing.T) {
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	fold := Fold{Train: Interval{base, base.Add(10 * time.Hour)}, Validation: Interval{base.Add(10 * time.Hour), base.Add(20 * time.Hour)}, Test: Interval{base.Add(20 * time.Hour), base.Add(30 * time.Hour)}}
	samples := []Sample{
		{ID: "safe_equal", ObservedAt: base.Add(time.Hour), FeatureStart: base, FeatureEnd: base.Add(8 * time.Hour), LabelEnd: base.Add(9 * time.Hour)},
		{ID: "purged_one_ns", ObservedAt: base.Add(2 * time.Hour), FeatureStart: base, FeatureEnd: base.Add(8 * time.Hour), LabelEnd: base.Add(9*time.Hour + time.Nanosecond)},
		{ID: "embargo_before", ObservedAt: base.Add(10*time.Hour + 59*time.Minute + 59*time.Second), FeatureStart: base, FeatureEnd: base, LabelEnd: base.Add(10*time.Hour + 59*time.Minute + 59*time.Second)},
		{ID: "embargo_equal_safe", ObservedAt: base.Add(11 * time.Hour), FeatureStart: base, FeatureEnd: base, LabelEnd: base.Add(11 * time.Hour)},
		{ID: "test_embargo_before", ObservedAt: base.Add(20*time.Hour + 59*time.Minute + 59*time.Second), FeatureStart: base, FeatureEnd: base, LabelEnd: base.Add(20*time.Hour + 59*time.Minute + 59*time.Second)},
		{ID: "test_embargo_equal_safe", ObservedAt: base.Add(21 * time.Hour), FeatureStart: base, FeatureEnd: base, LabelEnd: base.Add(21 * time.Hour)},
	}
	split, err := SplitFold(samples, fold, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(split.Train); !reflect.DeepEqual(got, []string{"safe_equal"}) {
		t.Fatalf("train=%v", got)
	}
	if !reflect.DeepEqual(split.Purged, []string{"purged_one_ns"}) || !reflect.DeepEqual(split.Embargoed, []string{"embargo_before", "test_embargo_before"}) {
		t.Fatalf("purged=%v embargoed=%v", split.Purged, split.Embargoed)
	}
	if !reflect.DeepEqual(ids(split.Validation), []string{"embargo_equal_safe"}) || !reflect.DeepEqual(ids(split.Test), []string{"test_embargo_equal_safe"}) {
		t.Fatalf("validation/test boundary mismatch")
	}
}

type leakRunner struct {
	selected    []string
	testReturns map[int]float64
}

func (r *leakRunner) FitAndSelect(f Fold, train, valid []Sample, allowed map[string][]string) (FrozenDecision, error) {
	choice := ""
	best := math.Inf(-1)
	for _, c := range allowed["lookback"] {
		score := 0.0
		for _, s := range append(train, valid...) {
			score += s.Values[c]
		}
		if score > best {
			best, choice = score, c
		}
	}
	r.selected = append(r.selected, choice)
	return FrozenDecision{FoldIndex: f.Index, Choice: choice, Parameters: map[string]string{"lookback": choice}, FitDigest: "fit", SelectionDigest: "select"}, nil
}
func (r *leakRunner) Test(f Fold, frozen FrozenDecision, test []Sample) (FoldMetrics, error) {
	v := r.testReturns[f.Index]
	return healthyMetrics(v), nil
}

func TestFutureTestOutcomeCannotAffectFrozenSelection(t *testing.T) {
	m := manifestFixture(t)
	samples := samplesForManifest(m)
	a := &leakRunner{testReturns: map[int]float64{0: .01, 1: .02, 2: .03}}
	first, err := RunWalkForward(m, samples, a)
	if err != nil {
		t.Fatal(err)
	}
	b := &leakRunner{testReturns: map[int]float64{0: -.01, 1: -.02, 2: -.03}}
	second, err := RunWalkForward(m, samples, b)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a.selected, b.selected) {
		t.Fatalf("future outcome changed selection: %v %v", a.selected, b.selected)
	}
	if first.Aggregate.Metrics.AfterCostReturn.Mean == second.Aggregate.Metrics.AfterCostReturn.Mean {
		t.Fatal("changed test outcomes did not change test metrics")
	}
}

func TestTypedRefusalsAndDomination(t *testing.T) {
	m := manifestFixture(t)
	one := m.Spec
	one.Folds = one.Folds[:1]
	if _, _, err := CanonicalManifestSpec(one); err == nil {
		t.Fatal("one fold passed")
	} else {
		diagnosticCode(err, DiagnosticInsufficientWindows, t)
	}
	bad := healthyMetrics(.01)
	bad.Trades = 0
	diagnosticCode(ValidateFoldMetrics(bad, m.Spec.Samples), DiagnosticZeroTrades, t)
	bad = healthyMetrics(.01)
	bad.BenchmarkPresent = false
	diagnosticCode(ValidateFoldMetrics(bad, m.Spec.Samples), DiagnosticMissingBenchmark, t)
	bad = healthyMetrics(.01)
	bad.CoverageComplete = false
	diagnosticCode(ValidateFoldMetrics(bad, m.Spec.Samples), DiagnosticIncompleteCoverage, t)
	bad = healthyMetrics(.01)
	bad.Regimes = map[string]int{"only": 1}
	diagnosticCode(ValidateFoldMetrics(bad, m.Spec.Samples), DiagnosticInsufficientRegimes, t)
	bad = healthyMetrics(.01)
	bad.AfterCostReturn = math.NaN()
	diagnosticCode(ValidateFoldMetrics(bad, m.Spec.Samples), DiagnosticNonFinite, t)
	folds := []FoldResult{}
	for i, v := range []float64{.001, .001, .1} {
		metrics := healthyMetrics(v)
		metrics.SymbolContributions = map[string]float64{"BTC": v}
		metrics.TradeContributions = map[string]float64{"trade": v}
		folds = append(folds, FoldResult{Fold: Fold{Index: i}, Metrics: metrics})
	}
	_, err := Evaluate(folds, m.Spec)
	diagnosticCode(err, DiagnosticDominated, t)
	unsupported := m.Spec
	unsupported.StatisticalUnit = "raw_bar"
	_, err = Evaluate([]FoldResult{{Metrics: healthyMetrics(.01)}, {Metrics: healthyMetrics(.01)}, {Metrics: healthyMetrics(.01)}}, unsupported)
	diagnosticCode(err, DiagnosticUnsupportedUnit, t)
}

func TestDeterministicBootstrapAndWorstCohorts(t *testing.T) {
	m := manifestFixture(t)
	folds := []FoldResult{}
	for i, value := range []float64{.01, -.005, .008} {
		metrics := healthyMetrics(value)
		metrics.TradeContributions = map[string]float64{"a": .002, "b": .002, "c": .002, "d": .002}
		metrics.SymbolContributions = map[string]float64{"ALPHA": .002, "OMEGA": -.001 * float64(i+1)}
		metrics.RegimeContributions = map[string]float64{"risk_on": .002, "risk_off": -.001 * float64(i+1)}
		folds = append(folds, FoldResult{Fold: m.Spec.Folds[i], Metrics: metrics})
	}
	first, err := Evaluate(folds, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Evaluate(folds, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatal("bootstrap changed for identical seed and windows")
	}
	if first.WorstWindow != 1 || first.WorstRegime != "risk_off" || first.WorstSymbol != "OMEGA" {
		t.Fatalf("worst cohorts=%+v", first)
	}
}

func TestMLGoldenAndEdgeCases(t *testing.T) {
	values := []MLOutcome{}
	for i, p := range []float64{.1, .2, .3, .4, .6, .7, .8, .9} {
		positive := i >= 4
		r := -.02
		if positive {
			r = .03
		}
		values = append(values, MLOutcome{ID: string(rune('a' + i)), Probability: p, Positive: positive, AfterCostReturn: r, CandidateSet: []string{"A", "B"}, BaselineSet: []string{"B", "A"}, GrossExposure: .5, BaselineExposure: .5})
	}
	got, err := EvaluateML(values, MLRequirements{MinLabels: 8, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6, MinAUC: .6, MaxLogLoss: 1, MaxCalibrationError: .3})
	if err != nil {
		t.Fatal(err)
	}
	if got.ROC_AUC != 1 || !got.RankMonotonic || !got.Passed {
		t.Fatalf("golden=%+v", got)
	}
	one := append([]MLOutcome(nil), values...)
	for i := range one {
		one[i].Positive = true
	}
	_, err = EvaluateML(one, MLRequirements{MinLabels: 8, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6})
	diagnosticCode(err, DiagnosticOneClass, t)
	mismatch := append([]MLOutcome(nil), values...)
	mismatch[0].BaselineSet = []string{"A"}
	_, err = EvaluateML(mismatch, MLRequirements{MinLabels: 8, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6})
	diagnosticCode(err, DiagnosticBaselineMismatch, t)
	invalid := append([]MLOutcome(nil), values...)
	invalid[0].Probability = math.NaN()
	_, err = EvaluateML(invalid, MLRequirements{MinLabels: 8, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6})
	diagnosticCode(err, DiagnosticInvalidProbability, t)
	weak := append([]MLOutcome(nil), values...)
	for i := range weak {
		weak[i].Probability = .5
		weak[i].AfterCostReturn = -.01
	}
	weakEvaluation, err := EvaluateML(weak, MLRequirements{MinLabels: 8, Buckets: 2, MinBucketSupport: 8, ClipEpsilon: 1e-6, MinAUC: .6, MaxLogLoss: 1, MaxCalibrationError: 1})
	if err != nil {
		t.Fatal(err)
	}
	if weakEvaluation.Passed || weakEvaluation.Gates["discrimination"] || weakEvaluation.Gates["after_cost_expectancy"] {
		t.Fatalf("weak ML passed gates: %+v", weakEvaluation)
	}
	nonmonotonic := append([]MLOutcome(nil), values...)
	for i := range nonmonotonic {
		nonmonotonic[i].AfterCostReturn = float64(len(nonmonotonic)-i) * .001
	}
	ranking, err := EvaluateML(nonmonotonic, MLRequirements{MinLabels: 8, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6, MinAUC: .6, MaxLogLoss: 1, MaxCalibrationError: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ranking.RankMonotonic || ranking.Gates["rank_monotonic"] {
		t.Fatalf("non-monotonic ranking passed: %+v", ranking)
	}
}

func healthyMetrics(v float64) FoldMetrics {
	return FoldMetrics{Observations: 10, Trades: 4, BenchmarkPresent: true, CoverageComplete: true, Regimes: map[string]int{"risk_on": 2, "risk_off": 2}, RegimeContributions: map[string]float64{"risk_on": v * .75, "risk_off": v * .25}, AfterCostExpectancy: v / 4, AfterCostReturn: v, BenchmarkRelativeReturn: v / 2, MaxDrawdown: .05, Turnover: .2, GrossExposure: .5, NetExposure: .5, Coverage: 1, TradeContributions: map[string]float64{"a": v / 4, "b": v / 4, "c": v / 4, "d": v / 4}, SymbolContributions: map[string]float64{"A": v / 2, "B": v / 2}}
}
func samplesForManifest(m ExperimentManifest) []Sample {
	result := []Sample{}
	for _, fold := range m.Spec.Folds {
		for _, interval := range []Interval{fold.Train, fold.Validation, fold.Test} {
			at := interval.Start.Add(2 * time.Hour)
			if interval == fold.Validation {
				at = interval.Start.Add(2 * time.Hour)
			}
			result = append(result, Sample{ID: at.String(), ObservedAt: at, FeatureStart: at.Add(-time.Hour), FeatureEnd: at, LabelEnd: at.Add(time.Hour), BenchmarkSeen: true, CoverageOK: true, Regime: "risk_on", Values: map[string]float64{"20": 2, "30": 1}})
		}
	}
	return result
}
func ids(v []Sample) []string {
	r := make([]string, len(v))
	for i := range v {
		r[i] = v[i].ID
	}
	return r
}
func diagnosticCode(err error, want DiagnosticCode, t *testing.T) {
	t.Helper()
	if err == nil {
		t.Fatalf("wanted %s", want)
	}
	var d *DiagnosticError
	if !errors.As(err, &d) || d.Code != want {
		t.Fatalf("got %T %v, want %s", err, err, want)
	}
}
