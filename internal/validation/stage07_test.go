package validation

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"
)

func manifestFixture(t *testing.T) ExperimentManifest {
	t.Helper()
	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	authority, err := NewAuthorityPolicyEnvelope(map[string]string{"selection_top_k": "1", "selection_min_probability": "0.6", "selection_min_ev": "0.01", "fallback_mode": "stage05-baseline-v1", "strategy_parameters": "sha256:params", "risk_policy": "risk-v1", "turnover_policy": "turnover-v1", "cash_policy": "cash-v1", "universe_policy": "universe-v1", "execution_policy": "exec-v1", "cost_policy": "cost-v1", "model_version": "none", "feature_schema": "none", "rollout_state": "research"})
	if err != nil {
		t.Fatal(err)
	}
	spec := ManifestSpec{
		SchemaVersion: ManifestSchemaVersion, StudyType: "confirmatory", CodeRevision: "0657c08",
		Candidate: VersionRef{ID: "trend-momentum", Version: "1.0.0", Digest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}, Baseline: VersionRef{ID: "momentum", Version: "1.0.0", Digest: "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
		Policies:         PolicyBundle{Composite: "policy-v1", Execution: "exec-v1", Universe: "universe-v1", ModelSelection: "model-v1", EntrySelection: "entry-v1", PortfolioRisk: "risk-v1", Rollout: "rollout-v1", Cost: "cost-v1"},
		GovernancePolicy: GovernancePolicyVersion, AuthorityPolicy: authority,
		DatasetManifestID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", DatasetManifestHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", UniversePolicy: "universe-v1",
		Interval: Interval{base, base.Add(18 * 24 * time.Hour)}, DecisionClock: "4h-close", ExecutionClock: "next-1m-open", Seed: 42, ExecutionSemantics: map[string]string{"fee_bps": "10", "slippage_bps": "5", "timing": "next-open", "liquidity": "closed-bar"},
		Folds: []Fold{
			{Index: 0, Train: Interval{base, base.Add(3 * 24 * time.Hour)}, Validation: Interval{base.Add(3 * 24 * time.Hour), base.Add(5 * 24 * time.Hour)}, Test: Interval{base.Add(5 * 24 * time.Hour), base.Add(7 * 24 * time.Hour)}},
			{Index: 1, Train: Interval{base.Add(4 * 24 * time.Hour), base.Add(8 * 24 * time.Hour)}, Validation: Interval{base.Add(8 * 24 * time.Hour), base.Add(10 * 24 * time.Hour)}, Test: Interval{base.Add(10 * 24 * time.Hour), base.Add(12 * 24 * time.Hour)}},
			{Index: 2, Train: Interval{base.Add(9 * 24 * time.Hour), base.Add(13 * 24 * time.Hour)}, Validation: Interval{base.Add(13 * 24 * time.Hour), base.Add(15 * 24 * time.Hour)}, Test: Interval{base.Add(15 * 24 * time.Hour), base.Add(17 * 24 * time.Hour)}},
		},
		FoldSourceJobIDs: []uint{1, 2, 3},
		FeatureHorizon:   time.Hour, LabelHorizon: time.Hour, Purge: time.Hour, Embargo: time.Hour,
		AllowedTuning: map[string][]string{"lookback": {"20", "30"}}, Metrics: append([]string(nil), RequiredConfirmatoryMetrics...), StatisticalUnit: "chronological_test_window", BootstrapIterations: 200,
		Samples:             SampleRequirements{MinFolds: 3, MinIndependentUnits: 3, MinObservationsPerFold: 10, MinTradesPerFold: 1, MinRegimes: 2},
		PromotionThresholds: []Threshold{{Metric: "coverage", Op: ">=", Value: .9}, {Metric: "after_cost_return", Op: ">", Value: -1}}, RollbackThresholds: []Threshold{{Metric: "max_drawdown", Op: ">=", Value: .2}},
		RequiredElapsed: map[string]time.Duration{"paper": time.Hour, "limited_live": time.Hour, "full_live": time.Hour},
		Artifacts:       ArtifactLinks{Metrics: "metrics.json", Trades: "trades.parquet", Curves: "curves.parquet", Cohorts: "cohorts.json", Factors: "factors.json", Coverage: "coverage.json", Comparison: "comparison.json"},
		Reproduce:       ReproductionInvocation{Command: "trading-bot", Args: []string{"validate", "--manifest", "manifest.json"}},
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
	copy.ExecutionSemantics = map[string]string{"slippage_bps": "5", "fee_bps": "10", "timing": "next-open", "liquidity": "closed-bar"}
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

func TestManifestAuthorityComponentsCannotBeOmittedOrWeakened(t *testing.T) {
	m := manifestFixture(t)
	cases := map[string]func(*ManifestSpec){"execution policy": func(s *ManifestSpec) { s.Policies.Execution = "" }, "source jobs": func(s *ManifestSpec) { s.FoldSourceJobIDs = nil }, "execution semantics": func(s *ManifestSpec) { delete(s.ExecutionSemantics, "liquidity") }, "required metric": func(s *ManifestSpec) { s.Metrics = s.Metrics[1:] }, "duplicate metric": func(s *ManifestSpec) { s.Metrics = append(s.Metrics, s.Metrics[0]) }, "weaken samples": func(s *ManifestSpec) { s.Samples.MinIndependentUnits = 2 }, "elapsed": func(s *ManifestSpec) { delete(s.RequiredElapsed, "paper") }, "authority component": func(s *ManifestSpec) {
		payload := cloneStringMap(s.AuthorityPolicy.Payload)
		delete(payload, "risk_policy")
		s.AuthorityPolicy, _ = NewAuthorityPolicyEnvelope(payload)
	}}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			spec := m.Spec
			spec.ExecutionSemantics = cloneStringMap(m.Spec.ExecutionSemantics)
			spec.Metrics = append([]string(nil), m.Spec.Metrics...)
			spec.FoldSourceJobIDs = append([]uint(nil), m.Spec.FoldSourceJobIDs...)
			spec.RequiredElapsed = map[string]time.Duration{}
			for k, v := range m.Spec.RequiredElapsed {
				spec.RequiredElapsed[k] = v
			}
			mutate(&spec)
			if _, err := NewManifest(spec, m.CreatedAt); err == nil {
				t.Fatal("weakened manifest passed")
			}
		})
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
	selected    *[]string
	testReturns map[int]float64
}

func (r *leakRunner) FitAndSelect(f Fold, train, valid []Sample, allowed map[string][]string) (FoldFit, error) {
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
	*r.selected = append(*r.selected, choice)
	return FoldFit{Choice: choice, Parameters: map[string]string{"lookback": choice}, Artifact: []byte("artifact:" + choice)}, nil
}
func (r *leakRunner) Test(f Fold, artifact []byte, test []Sample) (FoldPrimitives, error) {
	v := r.testReturns[f.Index]
	return healthyPrimitives(f, v), nil
}

type leakRunnerFactory struct {
	selected    []string
	testReturns map[int]float64
}

func (f *leakRunnerFactory) NewFoldRunner(_ Fold) (FoldRunner, error) {
	return &leakRunner{selected: &f.selected, testReturns: f.testReturns}, nil
}

func TestFutureTestOutcomeCannotAffectFrozenSelection(t *testing.T) {
	m := manifestFixture(t)
	samples := samplesForManifest(m)
	a := &leakRunnerFactory{testReturns: map[int]float64{0: .01, 1: .02, 2: .03}}
	first, err := RunWalkForward(m, samples, a)
	if err != nil {
		t.Fatal(err)
	}
	b := &leakRunnerFactory{testReturns: map[int]float64{0: -.01, 1: -.02, 2: -.03}}
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

type reusedRunnerFactory struct{ runner *leakRunner }

func (f *reusedRunnerFactory) NewFoldRunner(Fold) (FoldRunner, error) { return f.runner, nil }

type invalidChoiceFactory struct{ selected []string }

func (f *invalidChoiceFactory) NewFoldRunner(Fold) (FoldRunner, error) {
	return &invalidChoiceRunner{}, nil
}

type invalidChoiceRunner struct{}

func (*invalidChoiceRunner) FitAndSelect(Fold, []Sample, []Sample, map[string][]string) (FoldFit, error) {
	return FoldFit{Choice: "forged", Parameters: map[string]string{"lookback": "999"}, Artifact: []byte("forged")}, nil
}
func (*invalidChoiceRunner) Test(Fold, []byte, []Sample) (FoldPrimitives, error) {
	return FoldPrimitives{}, nil
}

func TestFoldIsolationChoiceAndSampleCausalityFailures(t *testing.T) {
	m := manifestFixture(t)
	samples := samplesForManifest(m)
	selected := []string{}
	shared := &leakRunner{selected: &selected, testReturns: map[int]float64{}}
	_, err := RunWalkForward(m, samples, &reusedRunnerFactory{runner: shared})
	diagnosticCode(err, DiagnosticTestLeakage, t)
	_, err = RunWalkForward(m, samples, &invalidChoiceFactory{})
	diagnosticCode(err, DiagnosticInvalidManifest, t)
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	valid := Sample{ID: "x", ObservedAt: base, FeatureStart: base.Add(-time.Hour), FeatureEnd: base, LabelEnd: base.Add(time.Hour), Values: map[string]float64{"x": 1}}
	for name, mutate := range map[string]func(*[]Sample){"duplicate": func(v *[]Sample) { *v = append(*v, (*v)[0]) }, "future feature": func(v *[]Sample) { (*v)[0].FeatureEnd = base.Add(time.Nanosecond) }, "short label": func(v *[]Sample) { (*v)[0].LabelEnd = base.Add(time.Minute) }} {
		t.Run(name, func(t *testing.T) {
			values := []Sample{valid}
			mutate(&values)
			if err := validateSamples(values, time.Hour, time.Hour); err == nil {
				t.Fatal("malformed samples passed")
			}
		})
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

func TestPrimitiveReconciliationAndCapitalWeightedAggregation(t *testing.T) {
	m := manifestFixture(t)
	p := healthyPrimitives(m.Spec.Folds[0], .01)
	bad := p
	bad.StartingCapital = 0
	_, err := DeriveFoldMetrics(bad)
	diagnosticCode(err, DiagnosticInvalidManifest, t)
	bad = p
	bad.Trades = append([]TradePrimitive(nil), p.Trades...)
	bad.Trades[0].NetPnL++
	_, err = DeriveFoldMetrics(bad)
	diagnosticCode(err, DiagnosticManifestIntegrity, t)
	bad = p
	bad.Curve = append([]CurvePrimitive(nil), p.Curve...)
	bad.Curve[len(bad.Curve)-1].Equity++
	_, err = DeriveFoldMetrics(bad)
	diagnosticCode(err, DiagnosticManifestIntegrity, t)
	returns := []float64{.01, .02, .03}
	folds := make([]FoldResult, 3)
	for i := range folds {
		primitive := healthyPrimitives(m.Spec.Folds[i], returns[i])
		primitive.StartingCapital = []float64{100, 1000, 1000}[i]
		for tradeIndex := range primitive.Trades {
			primitive.Trades[tradeIndex].NetPnL = primitive.StartingCapital * returns[i] / 4
			primitive.Trades[tradeIndex].GrossPnL = primitive.Trades[tradeIndex].NetPnL + .1
		}
		primitive.Curve[0].Equity = primitive.StartingCapital
		primitive.Curve[0].Benchmark = primitive.StartingCapital
		primitive.Curve[1].Equity = primitive.StartingCapital * (1 + returns[i])
		primitive.Curve[1].Benchmark = primitive.StartingCapital
		metrics, e := DeriveFoldMetrics(primitive)
		if e != nil {
			t.Fatal(e)
		}
		folds[i] = FoldResult{Fold: m.Spec.Folds[i], Primitives: primitive, Metrics: metrics}
	}
	evaluation, err := Evaluate(folds, m.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(evaluation.Metrics.AfterCostReturn.Mean-(51.0/2100.0)) > 1e-12 {
		t.Fatalf("return was not capital weighted: %v", evaluation.Metrics.AfterCostReturn.Mean)
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
		values = append(values, MLOutcome{ID: string(rune('a' + i)), Window: i / 4, Symbol: []string{"A", "B"}[i%2], Probability: p, Positive: positive, AfterCostReturn: r, BaselineReturn: r - .01, CandidateSet: []string{"A", "B"}, BaselineSet: []string{"B", "A"}, GrossExposure: .5, BaselineExposure: .5, CandidateExposureByAsset: map[string]float64{"A": .25, "B": .25}, BaselineExposureByAsset: map[string]float64{"A": .25, "B": .25}})
	}
	got, err := EvaluateML(values, MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6, MinAUC: .6, MaxLogLoss: .8, MaxCalibrationError: .3})
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
	_, err = EvaluateML(one, MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6})
	diagnosticCode(err, DiagnosticOneClass, t)
	mismatch := append([]MLOutcome(nil), values...)
	mismatch[0].BaselineSet = []string{"A"}
	_, err = EvaluateML(mismatch, MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6})
	diagnosticCode(err, DiagnosticBaselineMismatch, t)
	invalid := append([]MLOutcome(nil), values...)
	invalid[0].Probability = math.NaN()
	_, err = EvaluateML(invalid, MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6})
	diagnosticCode(err, DiagnosticInvalidProbability, t)
	weak := append([]MLOutcome(nil), values...)
	for i := range weak {
		weak[i].Probability = .5
		weak[i].AfterCostReturn = -.01
	}
	_, err = EvaluateML(weak, MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 8, ClipEpsilon: 1e-6, MinAUC: .6, MaxLogLoss: .8, MaxCalibrationError: .3})
	diagnosticCode(err, DiagnosticInsufficientObservations, t)
	nonmonotonic := append([]MLOutcome(nil), values...)
	for i := range nonmonotonic {
		nonmonotonic[i].AfterCostReturn = float64(len(nonmonotonic)-i) * .001
	}
	ranking, err := EvaluateML(nonmonotonic, MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6, MinAUC: .6, MaxLogLoss: .8, MaxCalibrationError: .3})
	if err != nil {
		t.Fatal(err)
	}
	if ranking.RankMonotonic || ranking.Gates["rank_monotonic"] {
		t.Fatalf("non-monotonic ranking passed: %+v", ranking)
	}
	duplicate := append([]MLOutcome(nil), values...)
	duplicate[1].ID = duplicate[0].ID
	_, err = EvaluateML(duplicate, MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6, MinAUC: .6, MaxLogLoss: .8, MaxCalibrationError: .3})
	diagnosticCode(err, DiagnosticInvalidManifest, t)
	oneWindow := append([]MLOutcome(nil), values...)
	for i := range oneWindow {
		oneWindow[i].Window = 0
	}
	_, err = EvaluateML(oneWindow, MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6, MinAUC: .6, MaxLogLoss: .8, MaxCalibrationError: .3})
	diagnosticCode(err, DiagnosticInsufficientWindows, t)
	unequal := append([]MLOutcome(nil), values...)
	unequal[0].CandidateExposureByAsset = map[string]float64{"A": .4, "B": .1}
	_, err = EvaluateML(unequal, MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6, MinAUC: .6, MaxLogLoss: .8, MaxCalibrationError: .3})
	diagnosticCode(err, DiagnosticBaselineMismatch, t)
	_, err = EvaluateML(values, MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6, MinAUC: math.Inf(1), MaxLogLoss: .8, MaxCalibrationError: .3})
	diagnosticCode(err, DiagnosticInvalidManifest, t)
	notBetter := append([]MLOutcome(nil), values...)
	for i := range notBetter {
		notBetter[i].BaselineReturn = notBetter[i].AfterCostReturn
	}
	evaluation, err := EvaluateML(notBetter, MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6, MinAUC: .6, MaxLogLoss: .8, MaxCalibrationError: .3})
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.Passed || evaluation.Gates["beats_baseline"] {
		t.Fatal("candidate equal to baseline passed")
	}
}

func TestImmutableMLPromotionEvidenceGoldenBinding(t *testing.T) {
	base := manifestFixture(t)
	spec := base.Spec
	model := &ModelAuthority{Version: "model-v1", Class: ArtifactPromotableCandidate, ModelDigest: "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", FeatureSpec: "features-v1", Features: []FeatureField{{Name: "x", Type: "float64"}}, LabelSpec: "label-v1", LabelHorizon: spec.LabelHorizon, CodeRevision: spec.CodeRevision, DatasetManifest: spec.DatasetManifestID, TrainingManifest: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", PolicyVersion: spec.Policies.Composite, Seed: spec.Seed}
	spec.Model = model
	authorityPayload := cloneStringMap(spec.AuthorityPolicy.Payload)
	authorityPayload["model_version"] = model.Version
	authorityPayload["feature_schema"] = model.FeatureSpec
	spec.AuthorityPolicy, _ = NewAuthorityPolicyEnvelope(authorityPayload)
	requirements := MLRequirements{MinLabels: 8, MinIndependentWindows: 2, Buckets: 2, MinBucketSupport: 4, ClipEpsilon: 1e-6, MinAUC: .6, MaxLogLoss: .8, MaxBrier: .25, MaxCalibrationError: .3}
	spec.MLRequirements = &requirements
	manifest, err := NewManifest(spec, base.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	folds := []FoldResult{}
	for _, fold := range manifest.Spec.Folds {
		primitive := healthyPrimitives(fold, .01)
		metrics, _ := DeriveFoldMetrics(primitive)
		folds = append(folds, FoldResult{Fold: fold, Primitives: primitive, Metrics: metrics})
	}
	aggregate, err := Evaluate(folds, manifest.Spec)
	if err != nil {
		t.Fatal(err)
	}
	result := WalkForwardResult{SchemaVersion: EvidenceSchemaVersion, ExperimentID: manifest.ID, Folds: folds, Aggregate: aggregate}
	outcomes := []MLOutcome{}
	for i, p := range []float64{.1, .2, .3, .4, .6, .7, .8, .9} {
		positive := i >= 4
		ret := -.02
		if positive {
			ret = .03
		}
		outcomes = append(outcomes, MLOutcome{ID: fmt.Sprintf("o%d", i), Window: i / 4, Symbol: []string{"A", "B"}[i%2], Probability: p, Positive: positive, AfterCostReturn: ret, BaselineReturn: ret - .01, CandidateSet: []string{"A", "B"}, BaselineSet: []string{"B", "A"}, GrossExposure: .5, BaselineExposure: .5, CandidateExposureByAsset: map[string]float64{"A": .25, "B": .25}, BaselineExposureByAsset: map[string]float64{"A": .25, "B": .25}})
	}
	evidence, err := NewImmutableMLEvidence(manifest, result, outcomes, requirements, MLProvenance{ArtifactDigest: model.ModelDigest, TrainingManifestDigest: model.TrainingManifest, BaselineStrategy: manifest.Spec.Baseline, BaselinePolicyDigest: manifest.Spec.Policies.Composite, DatasetManifestDigest: manifest.Spec.DatasetManifestHash}, manifest.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if !evidence.Evaluation.Passed || evidence.ContentDigest == "" || evidence.FoldEvidenceDigest == "" {
		t.Fatalf("ML evidence not promotable: %+v", evidence)
	}
}

func healthyMetrics(v float64) FoldMetrics {
	return FoldMetrics{Observations: 10, Trades: 4, BenchmarkPresent: true, CoverageComplete: true, Regimes: map[string]int{"risk_on": 2, "risk_off": 2}, RegimeContributions: map[string]float64{"risk_on": v * .75, "risk_off": v * .25}, AfterCostExpectancy: v / 4, AfterCostReturn: v, BenchmarkRelativeReturn: v / 2, MaxDrawdown: .05, Turnover: .2, GrossExposure: .5, NetExposure: .5, Coverage: 1, TradeContributions: map[string]float64{"a": v / 4, "b": v / 4, "c": v / 4, "d": v / 4}, SymbolContributions: map[string]float64{"A": v / 2, "B": v / 2}}
}
func healthyPrimitives(f Fold, value float64) FoldPrimitives {
	start := 1000.0
	pnl := start * value
	at := f.Test.Start.Add(time.Hour)
	return FoldPrimitives{StartingCapital: start, ExpectedObservations: 10, ObservedObservations: 10,
		Trades: []TradePrimitive{{ID: "a", Symbol: "A", Regime: "risk_on", OpenedAt: at, ClosedAt: at.Add(time.Minute), Notional: 50, GrossPnL: pnl/4 + .1, Cost: .1, NetPnL: pnl / 4}, {ID: "b", Symbol: "A", Regime: "risk_off", OpenedAt: at.Add(2 * time.Minute), ClosedAt: at.Add(3 * time.Minute), Notional: 50, GrossPnL: pnl/4 + .1, Cost: .1, NetPnL: pnl / 4}, {ID: "c", Symbol: "B", Regime: "risk_on", OpenedAt: at.Add(4 * time.Minute), ClosedAt: at.Add(5 * time.Minute), Notional: 50, GrossPnL: pnl/4 + .1, Cost: .1, NetPnL: pnl / 4}, {ID: "d", Symbol: "B", Regime: "risk_off", OpenedAt: at.Add(6 * time.Minute), ClosedAt: at.Add(7 * time.Minute), Notional: 50, GrossPnL: pnl/4 + .1, Cost: .1, NetPnL: pnl / 4}},
		Curve:  []CurvePrimitive{{At: at, Equity: start, Benchmark: start, GrossExposure: .5, NetExposure: .5}, {At: at.Add(time.Hour), Equity: start + pnl, Benchmark: start + pnl/2, GrossExposure: .5, NetExposure: .5}}}
}
func samplesForManifest(m ExperimentManifest) []Sample {
	result := []Sample{}
	for foldIndex, fold := range m.Spec.Folds {
		for _, interval := range []Interval{fold.Train, fold.Validation, fold.Test} {
			for i := 0; i < 10; i++ {
				at := interval.Start.Add(time.Duration(i+2) * time.Hour)
				result = append(result, Sample{ID: fmt.Sprintf("%d-%d-%s", foldIndex, i, interval.Start), ObservedAt: at, FeatureStart: at.Add(-time.Hour), FeatureEnd: at, LabelEnd: at.Add(time.Hour), BenchmarkSeen: true, CoverageOK: true, Regime: "risk_on", Values: map[string]float64{"20": 2, "30": 1}})
			}
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
