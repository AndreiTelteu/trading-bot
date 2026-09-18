package backtest

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/pointintime"
	"trading-go/internal/services"
	"trading-go/internal/validation"

	"gorm.io/gorm"
)

// Stage07ComparisonReference is the explicit, digest-verified adapter by which
// canonical Stage 05 comparisons and embedded Stage 06 candidate evidence are
// admitted as manifest provenance. It does not treat a single comparison as
// multi-window validation evidence.
type Stage07ComparisonReference struct {
	JobID          uint                          `json:"job_id"`
	ArtifactDigest string                        `json:"artifact_digest"`
	Candidate      string                        `json:"candidate"`
	DatasetDigest  validation.DatasetDigest      `json:"dataset_digest"`
	Strategies     map[string]Stage07StrategyRef `json:"strategies"`
}

type Stage07StrategyRef struct {
	ImplementationDigest validation.ImplementationDigest `json:"implementation_digest"`
	ConfigDigest         validation.ConfigDigest         `json:"config_digest"`
	RunManifestDigest    validation.RunManifestDigest    `json:"run_manifest_digest"`
}

type Stage07ExperimentSource struct{ DB *gorm.DB }

const stage07SourceArtifactSchemaVersion = "stage07-source-artifact-v2"

type stage07SourceArtifact struct {
	SchemaVersion, ComparisonDigest, DatasetManifestID string
	ReplaySettings                                     map[string]string                `json:"replay_settings"`
	ReplaySettingsDigest                               string                           `json:"replay_settings_digest"`
	Results                                            map[string]Stage05StrategyResult `json:"results"`
}
type stage07FoldArtifact struct {
	config              BacktestConfig
	series              map[string][]services.OHLCV
	candidate, baseline SelectedStrategy
	fixture             bool
	dataDigest          string
}
type stage07Factory struct{ folds map[int]stage07FoldArtifact }
type stage07Runner struct {
	fold   int
	source stage07FoldArtifact
}

func (s Stage07ExperimentSource) Load(manifest validation.ExperimentManifest) ([]validation.Sample, validation.FoldRunnerFactory, error) {
	if s.DB == nil {
		return nil, nil, fmt.Errorf("Stage 07 source database is required")
	}
	dataset, err := pointintime.LoadManifest(s.DB, manifest.Spec.DatasetManifestID)
	if err != nil {
		return nil, nil, err
	}
	if dataset.ID != manifest.Spec.DatasetManifestID || dataset.ContentHash != manifest.Spec.DatasetManifestHash {
		return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "Stage 04 manifest identity/content mismatch"}
	}
	var replaySettings map[string]string
	selections := make([]struct{ candidate, baseline SelectedStrategy }, len(manifest.Spec.Folds))
	for i, jobID := range manifest.Spec.FoldSourceJobIDs {
		ref, err := LoadStage07ComparisonReference(s.DB, jobID)
		if err != nil {
			return nil, nil, err
		}
		candidateRef, candidateOK := ref.Strategies[manifest.Spec.Candidate.ID]
		baselineRef, baselineOK := ref.Strategies[manifest.Spec.Baseline.ID]
		if !candidateOK || !baselineOK || string(ref.DatasetDigest) != manifest.Spec.DatasetManifestID || ref.Candidate != manifest.Spec.Candidate.ID+"@"+manifest.Spec.Candidate.Version || candidateRef.ImplementationDigest != manifest.Spec.Candidate.ImplementationDigest || candidateRef.ConfigDigest != manifest.Spec.Candidate.ConfigDigest || baselineRef.ImplementationDigest != manifest.Spec.Baseline.ImplementationDigest || baselineRef.ConfigDigest != manifest.Spec.Baseline.ConfigDigest {
			return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "Stage 05/06 source provenance mismatch"}
		}
		var job database.BacktestJob
		if err := s.DB.First(&job, jobID).Error; err != nil {
			return nil, nil, err
		}
		if job.ValidationArtifactJSON == nil || job.ValidationArtifactDigest == nil {
			return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticIncompleteCoverage, Details: "Stage 05 job lacks primitive validation artifact"}
		}
		raw := []byte(*job.ValidationArtifactJSON)
		sum := sha256.Sum256(raw)
		if fmt.Sprintf("%x", sum) != *job.ValidationArtifactDigest {
			return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "Stage 05 primitive artifact digest mismatch"}
		}
		var artifact stage07SourceArtifact
		if json.Unmarshal(raw, &artifact) != nil || artifact.SchemaVersion != stage07SourceArtifactSchemaVersion || artifact.ComparisonDigest != ref.ArtifactDigest || artifact.DatasetManifestID != manifest.Spec.DatasetManifestID || artifact.ReplaySettingsDigest == "" || artifact.ReplaySettingsDigest != stage07SettingsDigest(artifact.ReplaySettings) {
			return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "Stage 05 primitive artifact envelope mismatch"}
		}
		candidate, ok := artifact.Results[manifest.Spec.Candidate.ID]
		if !ok {
			return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticIncompleteCoverage, Details: "candidate result missing"}
		}
		baseline, ok := artifact.Results[manifest.Spec.Baseline.ID]
		if !ok {
			return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticMissingBenchmark, Details: "baseline result missing"}
		}
		if candidate.Manifest.DatasetManifestID != manifest.Spec.DatasetManifestID || baseline.Manifest.DatasetManifestID != manifest.Spec.DatasetManifestID {
			return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticInvalidWindowOrder, Details: "source job dataset differs from immutable manifest"}
		}
		if replaySettings == nil {
			replaySettings = cloneStringMap(artifact.ReplaySettings)
		} else if stage07SettingsDigest(replaySettings) != artifact.ReplaySettingsDigest {
			return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "fold source replay settings differ"}
		}
		selections[i] = struct{ candidate, baseline SelectedStrategy }{candidate.Manifest.Strategy, baseline.Manifest.Strategy}
	}
	if len(replaySettings) == 0 {
		return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "immutable replay settings are required"}
	}
	// Load the complete declared dataset once. Each fold runner then truncates
	// it at its causal boundary before fitting or testing.
	replaySettings["backtest_dataset_manifest_id"] = manifest.Spec.DatasetManifestID
	replaySettings["backtest_start"] = manifest.Spec.Interval.Start.UTC().Format(time.RFC3339Nano)
	replaySettings["backtest_end"] = manifest.Spec.Interval.End.UTC().Format(time.RFC3339Nano)
	replaySettings["backtest_execution_1m"] = "true"
	config, series, err := preparePointInTimeBacktestInputs(replaySettings)
	if err != nil {
		return nil, nil, err
	}
	if config.DatasetManifestID != manifest.Spec.DatasetManifestID || !config.DatasetManifestValidated || config.CodeRevision != manifest.Spec.CodeRevision {
		return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "replay configuration does not match immutable validation manifest"}
	}
	samples, err := stage07Samples(s.DB, dataset, manifest, config)
	if err != nil {
		return nil, nil, err
	}
	dataDigest, err := stage07DataDigest(config, series)
	if err != nil {
		return nil, nil, err
	}
	factory := &stage07Factory{folds: map[int]stage07FoldArtifact{}}
	for i, fold := range manifest.Spec.Folds {
		factory.folds[fold.Index] = stage07FoldArtifact{config: config, series: cloneOHLCVSeries(series), candidate: selections[i].candidate, baseline: selections[i].baseline, dataDigest: dataDigest}
	}
	return samples, factory, nil
}
func (f *stage07Factory) NewFoldRunner(fold validation.Fold) (validation.FoldRunner, error) {
	source, ok := f.folds[fold.Index]
	if !ok {
		return nil, fmt.Errorf("fold source unavailable")
	}
	return &stage07Runner{fold: fold.Index, source: source}, nil
}
func (r *stage07Runner) FitAndSelect(fold validation.Fold, train, valid []validation.Sample, allowed map[string][]string) (validation.FoldFit, error) {
	if fold.Index != r.fold {
		return validation.FoldFit{}, &validation.DiagnosticError{Code: validation.DiagnosticTestLeakage}
	}
	if err := r.validateSamples(train, fold.Train); err != nil {
		return validation.FoldFit{}, err
	}
	if err := r.validateSamples(valid, fold.Validation); err != nil {
		return validation.FoldFit{}, err
	}
	choices, err := stage07ParameterChoices(r.source.candidate.Parameters, allowed)
	if err != nil {
		return validation.FoldFit{}, err
	}
	type scored struct {
		params       map[string]string
		train, valid float64
	}
	scores := make([]scored, 0, len(choices))
	for _, params := range choices {
		trainResult, err := r.run(r.source.candidate, params, fold.Train, fold.Train.End)
		if err != nil {
			return validation.FoldFit{}, err
		}
		validResult, err := r.run(r.source.candidate, params, fold.Validation, fold.Validation.End)
		if err != nil {
			return validation.FoldFit{}, err
		}
		scores = append(scores, scored{params: params, train: stage07Return(trainResult), valid: stage07Return(validResult)})
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].valid != scores[j].valid {
			return scores[i].valid > scores[j].valid
		}
		return stage07ParameterKey(scores[i].params) < stage07ParameterKey(scores[j].params)
	})
	if len(scores) == 0 {
		return validation.FoldFit{}, &validation.DiagnosticError{Code: validation.DiagnosticInvalidManifest, Details: "no predeclared tuning candidates"}
	}
	trainDigest, _ := stage07SampleDigest(train)
	validDigest, _ := stage07SampleDigest(valid)
	artifact := stage07FrozenArtifact{SchemaVersion: "stage07-frozen-fold-v1", Fold: fold.Index, Parameters: scores[0].params, TrainDigest: trainDigest, ValidationDigest: validDigest, DataDigest: r.source.dataDigest, SelectionRationale: fmt.Sprintf("validation_after_cost_return=%+.12f;train_after_cost_return=%+.12f", scores[0].valid, scores[0].train)}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return validation.FoldFit{}, err
	}
	return validation.FoldFit{Choice: stage07ParameterKey(scores[0].params), Parameters: cloneStringMap(scores[0].params), Artifact: encoded, TrainDigest: trainDigest, ValidationDigest: validDigest, DataDigest: r.source.dataDigest, SelectionRationale: artifact.SelectionRationale}, nil
}
func (r *stage07Runner) Test(fold validation.Fold, artifact []byte, test []validation.Sample) (validation.FoldPrimitives, error) {
	if fold.Index != r.fold {
		return validation.FoldPrimitives{}, &validation.DiagnosticError{Code: validation.DiagnosticTestLeakage}
	}
	if err := r.validateSamples(test, fold.Test); err != nil {
		return validation.FoldPrimitives{}, err
	}
	var frozen stage07FrozenArtifact
	if json.Unmarshal(artifact, &frozen) != nil || frozen.SchemaVersion != "stage07-frozen-fold-v1" || frozen.Fold != fold.Index || frozen.DataDigest != r.source.dataDigest {
		return validation.FoldPrimitives{}, &validation.DiagnosticError{Code: validation.DiagnosticTestLeakage, Details: "frozen selection artifact is invalid"}
	}
	testDigest, _ := stage07SampleDigest(test)
	if frozen.Parameters == nil || testDigest == "" {
		return validation.FoldPrimitives{}, &validation.DiagnosticError{Code: validation.DiagnosticTestLeakage}
	}
	candidate, err := r.run(r.source.candidate, frozen.Parameters, fold.Test, fold.Test.End)
	if err != nil {
		return validation.FoldPrimitives{}, err
	}
	baseline, err := r.run(r.source.baseline, r.source.baseline.Parameters, fold.Test, fold.Test.End)
	if err != nil {
		return validation.FoldPrimitives{}, err
	}
	return stage07Primitives(candidate, baseline, fold.Index, len(test), r.source.series)
}
func stage07Primitives(candidate, baseline Stage05StrategyResult, fold, observations int, series map[string][]services.OHLCV) (validation.FoldPrimitives, error) {
	start, err := strconv.ParseFloat(candidate.Metrics.StartingCapital, 64)
	if err != nil || start <= 0 {
		return validation.FoldPrimitives{}, &validation.DiagnosticError{Code: validation.DiagnosticNonFinite, Details: "invalid starting capital"}
	}
	if len(candidate.Equity) != len(baseline.Equity) || len(candidate.Equity) < 2 {
		return validation.FoldPrimitives{}, &validation.DiagnosticError{Code: validation.DiagnosticMissingBenchmark, Details: "candidate/baseline curves are not aligned"}
	}
	curve := make([]validation.CurvePrimitive, len(candidate.Equity))
	gross, net := metricValue(candidate.Metrics.AverageGrossExposure), metricValue(candidate.Metrics.AverageNetExposure)
	for i := range curve {
		if !candidate.Equity[i].Time.Equal(baseline.Equity[i].Time) {
			return validation.FoldPrimitives{}, &validation.DiagnosticError{Code: validation.DiagnosticMissingBenchmark, Details: "curve clocks differ"}
		}
		curve[i] = validation.CurvePrimitive{At: candidate.Equity[i].Time.UTC(), Equity: candidate.Equity[i].Value, Benchmark: baseline.Equity[i].Value, GrossExposure: gross, NetExposure: net}
	}
	trades := make([]validation.TradePrimitive, len(candidate.Trades))
	for i, t := range candidate.Trades {
		regime := strings.TrimSpace(t.RegimeState)
		if regime == "" {
			regime = "unknown"
		}
		cost, err := stage07TradeCost(candidate.Artifacts.Fills, series, t)
		if err != nil {
			return validation.FoldPrimitives{}, err
		}
		trades[i] = validation.TradePrimitive{ID: fmt.Sprintf("%d:%d:%s:%s", fold, i, t.Symbol, t.EntryTime.UTC().Format(time.RFC3339Nano)), Symbol: t.Symbol, Regime: regime, OpenedAt: t.EntryTime.UTC(), ClosedAt: t.ExitTime.UTC(), Notional: math.Abs(t.EntryPrice * t.Size), GrossPnL: t.Pnl + cost, Cost: cost, NetPnL: t.Pnl}
	}
	return validation.FoldPrimitives{StartingCapital: start, ExpectedObservations: observations, ObservedObservations: observations, Trades: trades, Curve: curve}, nil
}
func metricValue(v OptionalMetric) float64 {
	if v.Available {
		return v.Value
	}
	return 0
}

// stage07ReplaySettings deliberately stores only non-secret replay controls.
// It is an immutable input snapshot, never a route to reuse a live setting.
func stage07ReplaySettings(values map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range values {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "password") || strings.Contains(lower, "secret") || strings.Contains(lower, "token") || strings.Contains(lower, "api_key") {
			continue
		}
		if strings.HasPrefix(key, "backtest_") || strings.HasPrefix(key, "universe_") || strings.HasPrefix(key, "selection_policy_") || strings.HasPrefix(key, "active_model_") || strings.HasPrefix(key, "model_rollout_") || strings.HasPrefix(key, "model_fallback_") || strings.HasPrefix(key, "model_rollback_") || strings.HasPrefix(key, "entry_") || strings.HasPrefix(key, "risk_") || strings.HasPrefix(key, "max_") || strings.HasPrefix(key, "stop_") || strings.HasPrefix(key, "take_") || strings.HasPrefix(key, "atr_") || strings.HasPrefix(key, "buy_") || strings.HasPrefix(key, "min_") || strings.HasPrefix(key, "sell_") || strings.HasPrefix(key, "allow_") || strings.HasPrefix(key, "trailing_") || strings.HasPrefix(key, "rsi_") || strings.HasPrefix(key, "macd_") || strings.HasPrefix(key, "bb_") || strings.HasPrefix(key, "volume_") || strings.HasPrefix(key, "momentum_") {
			result[key] = value
		}
	}
	return result
}

func stage07SettingsDigest(values map[string]string) string {
	encoded, _ := json.Marshal(values)
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum)
}

type stage07FrozenArtifact struct {
	SchemaVersion, TrainDigest, ValidationDigest, DataDigest, SelectionRationale string
	Fold                                                                         int
	Parameters                                                                   map[string]string
}

func (r *stage07Runner) run(selected SelectedStrategy, parameters map[string]string, interval validation.Interval, availableThrough time.Time) (Stage05StrategyResult, error) {
	if !interval.Valid() || availableThrough.Before(interval.End) {
		return Stage05StrategyResult{}, &validation.DiagnosticError{Code: validation.DiagnosticInvalidWindowOrder}
	}
	config := r.source.config
	config.Start, config.End = interval.Start.UTC(), interval.End.UTC()
	series := stage07TruncateSeries(r.source.series, availableThrough)
	config.ExecutionSeries = stage07TruncateSeries(config.ExecutionSeries, availableThrough)
	config.BenchmarkSeries = stage07TruncateBars(config.BenchmarkSeries, availableThrough)
	params := cloneStringMap(selected.Parameters)
	for key, value := range parameters {
		params[key] = value
	}
	resolved, strategy, planner, err := DefaultStrategyRegistry.ResolveExecutable(selected.Descriptor.ID, selected.Descriptor.Version, params)
	if err != nil {
		return Stage05StrategyResult{}, err
	}
	return runStage05StrategyWithPlanner(config, series, resolved, strategy, planner, r.source.fixture)
}

func (r *stage07Runner) validateSamples(samples []validation.Sample, interval validation.Interval) error {
	if len(samples) == 0 {
		return &validation.DiagnosticError{Code: validation.DiagnosticInsufficientObservations}
	}
	for _, sample := range samples {
		if sample.ObservedAt.Before(interval.Start) || !sample.ObservedAt.Before(interval.End) || !sample.CoverageOK || !sample.BenchmarkSeen {
			return &validation.DiagnosticError{Code: validation.DiagnosticIncompleteCoverage, Details: sample.ID}
		}
		bar, ok := stage07BarAt(r.source.series[sample.Symbol], time.UnixMilli(int64(sample.Values["bar_open_ms"])))
		if !ok || bar.Close != sample.Values["close"] {
			return &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "sample does not match fold dataset: " + sample.ID}
		}
	}
	return nil
}

func stage07ParameterChoices(base map[string]string, allowed map[string][]string) ([]map[string]string, error) {
	keys := make([]string, 0, len(allowed))
	for key, values := range allowed {
		if len(values) == 0 {
			return nil, &validation.DiagnosticError{Code: validation.DiagnosticInvalidManifest, Details: "empty tuning set: " + key}
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := []map[string]string{}
	var visit func(int, map[string]string)
	visit = func(index int, params map[string]string) {
		if index == len(keys) {
			result = append(result, cloneStringMap(params))
			return
		}
		for _, value := range allowed[keys[index]] {
			next := cloneStringMap(params)
			next[keys[index]] = value
			visit(index+1, next)
		}
	}
	visit(0, cloneStringMap(base))
	return result, nil
}
func stage07ParameterKey(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+params[key])
	}
	return strings.Join(parts, ";")
}
func stage07Return(result Stage05StrategyResult) float64 {
	if result.Metrics.TotalReturn.Available {
		return result.Metrics.TotalReturn.Value
	}
	return math.Inf(-1)
}
func stage07SampleDigest(samples []validation.Sample) (string, error) {
	encoded, err := json.Marshal(samples)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum), nil
}
func stage07DataDigest(config BacktestConfig, series map[string][]services.OHLCV) (string, error) {
	encoded, err := json.Marshal(struct {
		Manifest string
		Series   map[string][]services.OHLCV
	}{config.DatasetManifestID, series})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum), nil
}
func stage07TruncateSeries(values map[string][]services.OHLCV, end time.Time) map[string][]services.OHLCV {
	result := map[string][]services.OHLCV{}
	for symbol, bars := range values {
		result[symbol] = stage07TruncateBars(bars, end)
	}
	return result
}
func stage07TruncateBars(values []services.OHLCV, end time.Time) []services.OHLCV {
	result := make([]services.OHLCV, 0, len(values))
	for _, bar := range values {
		if !time.UnixMilli(bar.OpenTime).UTC().After(end) {
			result = append(result, bar)
		}
	}
	return result
}
func stage07BarAt(values []services.OHLCV, at time.Time) (services.OHLCV, bool) {
	for _, bar := range values {
		if time.UnixMilli(bar.OpenTime).UTC().Equal(at.UTC()) {
			return bar, true
		}
	}
	return services.OHLCV{}, false
}

func stage07TradeCost(fills []FillArtifact, series map[string][]services.OHLCV, trade Trade) (float64, error) {
	cost := 0.0
	for _, fill := range fills {
		at, err := time.Parse(time.RFC3339Nano, fill.FillAt)
		if err != nil {
			return 0, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "invalid fill time"}
		}
		if fill.Symbol != trade.Symbol || (!at.Equal(trade.EntryTime) && !at.Equal(trade.ExitTime)) {
			continue
		}
		fee, feeErr := strconv.ParseFloat(fill.Fee, 64)
		price, priceErr := strconv.ParseFloat(fill.Price, 64)
		quantity, qtyErr := strconv.ParseFloat(fill.Quantity, 64)
		bar, found := stage07BarAt(series[fill.Symbol], at)
		if feeErr != nil || priceErr != nil || qtyErr != nil || !found {
			return 0, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "actual fill cost cannot be attributed"}
		}
		cost += fee + math.Abs(price-bar.Open)*math.Abs(quantity)
	}
	if cost < 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return 0, &validation.DiagnosticError{Code: validation.DiagnosticNonFinite, Details: "invalid actual fill cost"}
	}
	return cost, nil
}

// stage07Samples derives every fold input from the manifest-pinned records:
// completed/available feature bars, a completed independent benchmark, a
// completed universe regime, and an available forward label.  It intentionally
// records incomplete rows so SplitFold/validation can fail closed instead of
// silently treating a missing dependency as a favourable observation.
func stage07Samples(db *gorm.DB, dataset pointintime.Manifest, manifest validation.ExperimentManifest, config BacktestConfig) ([]validation.Sample, error) {
	if db == nil {
		return nil, fmt.Errorf("Stage 07 source database is required")
	}
	ids := make([]string, 0, len(config.SymbolIdentities))
	byID := map[string]string{}
	for ticker, id := range config.SymbolIdentities {
		ids = append(ids, id)
		byID[id] = ticker
	}
	var decision, benchmark []database.HistoricalBar
	if err := db.Where("dataset_version=? AND role=? AND timeframe=? AND exchange_symbol_id IN ? AND open_time>=? AND open_time<?", dataset.DatasetVersion, pointintime.RoleDecision, "15m", ids, manifest.Spec.Interval.Start.Add(-manifest.Spec.FeatureHorizon), manifest.Spec.Interval.End).Order("open_time ASC, exchange_symbol_id ASC").Find(&decision).Error; err != nil {
		return nil, err
	}
	benchmarkID := config.SymbolIdentities[config.BenchmarkSymbol]
	if err := db.Where("dataset_version=? AND role=? AND timeframe=? AND exchange_symbol_id=? AND open_time>=? AND open_time<?", dataset.DatasetVersion, pointintime.RoleBenchmark, "15m", benchmarkID, manifest.Spec.Interval.Start, manifest.Spec.Interval.End).Order("open_time ASC").Find(&benchmark).Error; err != nil {
		return nil, err
	}
	benchAt := map[time.Time]database.HistoricalBar{}
	for _, bar := range benchmark {
		benchAt[bar.OpenTime.UTC()] = bar
	}
	bySymbolTime := map[string]map[time.Time]database.HistoricalBar{}
	for _, bar := range decision {
		if bySymbolTime[bar.ExchangeSymbolID] == nil {
			bySymbolTime[bar.ExchangeSymbolID] = map[time.Time]database.HistoricalBar{}
		}
		bySymbolTime[bar.ExchangeSymbolID][bar.OpenTime.UTC()] = bar
	}
	var snapshots []database.UniverseSnapshot
	if err := db.Where("dataset_manifest_id=? AND coverage_state='complete' AND snapshot_time<?", manifest.Spec.DatasetManifestID, manifest.Spec.Interval.End).Order("snapshot_time ASC").Find(&snapshots).Error; err != nil {
		return nil, err
	}
	samples := []validation.Sample{}
	for _, bar := range decision {
		ticker := byID[bar.ExchangeSymbolID]
		if ticker == "" {
			continue
		}
		observed := bar.AvailableAt.UTC()
		if observed.Before(manifest.Spec.Interval.Start) || !observed.Before(manifest.Spec.Interval.End) {
			continue
		}
		closeValue, err := strconv.ParseFloat(bar.Close, 64)
		if err != nil || !finiteStage07(closeValue) {
			return nil, &validation.DiagnosticError{Code: validation.DiagnosticNonFinite, Details: "invalid Stage 04 close"}
		}
		benchmarkBar, benchmarkOK := benchAt[bar.OpenTime.UTC()]
		regime, regimeOK := stage07RegimeAt(snapshots, observed)
		featureOK := bar.QualityStatus == "valid" && !bar.AvailableAt.After(observed)
		for at := observed.Add(-manifest.Spec.FeatureHorizon); at.Before(observed); at = at.Add(15 * time.Minute) {
			candidate, ok := bySymbolTime[bar.ExchangeSymbolID][at.Truncate(15*time.Minute)]
			featureOK = featureOK && ok && candidate.QualityStatus == "valid" && !candidate.AvailableAt.After(observed)
		}
		labelAt := bar.OpenTime.UTC().Add(manifest.Spec.LabelHorizon)
		label, labelOK := bySymbolTime[bar.ExchangeSymbolID][labelAt]
		labelValue := 0.0
		if labelOK {
			labelValue, err = strconv.ParseFloat(label.Close, 64)
			labelOK = err == nil && finiteStage07(labelValue)
		}
		benchmarkSeen := benchmarkOK && benchmarkBar.QualityStatus == "valid" && !benchmarkBar.AvailableAt.After(observed)
		samples = append(samples, validation.Sample{ID: fmt.Sprintf("%s:15m:%s", bar.ExchangeSymbolID, observed.Format(time.RFC3339Nano)), ObservedAt: observed, FeatureStart: observed.Add(-manifest.Spec.FeatureHorizon), FeatureEnd: observed, LabelEnd: observed.Add(manifest.Spec.LabelHorizon), Symbol: ticker, Regime: regime, BenchmarkSeen: benchmarkSeen, CoverageOK: featureOK && labelOK && regimeOK, Values: map[string]float64{"close": closeValue, "label_close": labelValue, "bar_open_ms": float64(bar.OpenTime.UnixMilli()), "quality_ok": boolFloat(featureOK)}})
	}
	return samples, nil
}
func stage07RegimeAt(values []database.UniverseSnapshot, at time.Time) (string, bool) {
	regime := ""
	found := false
	for _, value := range values {
		if value.SnapshotTime.After(at) {
			break
		}
		regime, found = value.RegimeState, value.RegimeState != ""
	}
	return regime, found
}
func finiteStage07(value float64) bool { return !math.IsNaN(value) && !math.IsInf(value, 0) }
func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func (s Stage07ExperimentSource) LoadML(manifest validation.ExperimentManifest, _ validation.WalkForwardResult) ([]validation.MLOutcome, validation.MLRequirements, validation.MLProvenance, error) {
	if manifest.Spec.Model == nil || manifest.Spec.MLRequirements == nil {
		return nil, validation.MLRequirements{}, validation.MLProvenance{}, &validation.DiagnosticError{Code: validation.DiagnosticInvalidManifest, Field: "ml_requirements"}
	}
	outcomes := []validation.MLOutcome{}
	for foldIndex, jobID := range manifest.Spec.FoldSourceJobIDs {
		var job database.BacktestJob
		if err := s.DB.First(&job, jobID).Error; err != nil {
			return nil, *manifest.Spec.MLRequirements, validation.MLProvenance{}, err
		}
		if job.ValidationArtifactJSON == nil {
			return nil, *manifest.Spec.MLRequirements, validation.MLProvenance{}, &validation.DiagnosticError{Code: validation.DiagnosticIncompleteCoverage, Details: "ML source artifact missing"}
		}
		var artifact stage07SourceArtifact
		if json.Unmarshal([]byte(*job.ValidationArtifactJSON), &artifact) != nil {
			return nil, *manifest.Spec.MLRequirements, validation.MLProvenance{}, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "ML source artifact malformed"}
		}
		candidate, ok := artifact.Results[manifest.Spec.Candidate.ID]
		if !ok {
			return nil, *manifest.Spec.MLRequirements, validation.MLProvenance{}, &validation.DiagnosticError{Code: validation.DiagnosticIncompleteCoverage, Details: "ML candidate missing"}
		}
		baseline, ok := artifact.Results[manifest.Spec.Baseline.ID]
		if !ok {
			return nil, *manifest.Spec.MLRequirements, validation.MLProvenance{}, &validation.DiagnosticError{Code: validation.DiagnosticMissingBenchmark}
		}
		candidateSet, candidateExposure := tradeSetExposure(candidate)
		baselineSet, baselineExposure := tradeSetExposure(baseline)
		candidateGross, baselineGross := mapAbsSum(candidateExposure), mapAbsSum(baselineExposure)
		baselineReturns := map[string]float64{}
		baselineStart, _ := strconv.ParseFloat(baseline.Metrics.StartingCapital, 64)
		for _, trade := range baseline.Trades {
			baselineReturns[trade.Symbol] += trade.Pnl / baselineStart
		}
		candidateStart, _ := strconv.ParseFloat(candidate.Metrics.StartingCapital, 64)
		for index, trade := range candidate.Trades {
			if trade.PredictedProbability == nil {
				return nil, *manifest.Spec.MLRequirements, validation.MLProvenance{}, &validation.DiagnosticError{Code: validation.DiagnosticIncompleteCoverage, Details: "candidate trade lacks immutable prediction"}
			}
			outcomes = append(outcomes, validation.MLOutcome{ID: fmt.Sprintf("%d:%d:%s:%s", foldIndex, index, trade.Symbol, trade.EntryTime.UTC().Format(time.RFC3339Nano)), Window: foldIndex, Symbol: trade.Symbol, Probability: *trade.PredictedProbability, Positive: trade.Pnl > 0, AfterCostReturn: trade.Pnl / candidateStart, BaselineReturn: baselineReturns[trade.Symbol], CandidateSet: append([]string(nil), candidateSet...), BaselineSet: append([]string(nil), baselineSet...), GrossExposure: candidateGross, BaselineExposure: baselineGross, CandidateExposureByAsset: cloneExposure(candidateExposure), BaselineExposureByAsset: cloneExposure(baselineExposure)})
		}
	}
	provenance := validation.MLProvenance{ArtifactDigest: manifest.Spec.Model.ModelDigest, TrainingManifestDigest: manifest.Spec.Model.TrainingManifest, BaselineStrategy: manifest.Spec.Baseline, BaselinePolicyDigest: manifest.Spec.Policies.Composite, DatasetManifestDigest: manifest.Spec.DatasetManifestHash}
	return outcomes, *manifest.Spec.MLRequirements, provenance, nil
}
func tradeSetExposure(result Stage05StrategyResult) ([]string, map[string]float64) {
	start, _ := strconv.ParseFloat(result.Metrics.StartingCapital, 64)
	exposure := map[string]float64{}
	for _, trade := range result.Trades {
		exposure[trade.Symbol] += math.Abs(trade.EntryPrice*trade.Size) / start
	}
	set := make([]string, 0, len(exposure))
	for symbol := range exposure {
		set = append(set, symbol)
	}
	sort.Strings(set)
	return set, exposure
}
func mapAbsSum(values map[string]float64) float64 {
	total := 0.0
	for _, value := range values {
		total += math.Abs(value)
	}
	return total
}
func cloneExposure(values map[string]float64) map[string]float64 {
	result := make(map[string]float64, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func LoadStage07ComparisonReference(db *gorm.DB, jobID uint) (Stage07ComparisonReference, error) {
	if db == nil || jobID == 0 {
		return Stage07ComparisonReference{}, fmt.Errorf("Stage 05 job id and database are required")
	}
	var job database.BacktestJob
	if err := db.Where("id=? AND job_type=? AND status=?", jobID, "stage05_comparison", "completed").First(&job).Error; err != nil {
		return Stage07ComparisonReference{}, err
	}
	if job.SummaryJSON == nil || job.ArtifactDigest == nil {
		return Stage07ComparisonReference{}, fmt.Errorf("Stage 05 job lacks canonical artifact")
	}
	artifact, err := UnmarshalComparisonArtifact([]byte(*job.SummaryJSON))
	if err != nil {
		return Stage07ComparisonReference{}, err
	}
	if artifact.ArtifactDigest != *job.ArtifactDigest {
		return Stage07ComparisonReference{}, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "Stage 05 job digest differs from canonical artifact"}
	}
	if artifact.CandidateEvidence == nil && artifact.Candidate == StrategyTrendMomentumCandidate+"@1.0.0" {
		return Stage07ComparisonReference{}, fmt.Errorf("Stage 06 candidate evidence is missing")
	}
	strategies := map[string]Stage07StrategyRef{}
	for _, row := range artifact.Rows {
		if row.ImplementationDigest == "" || row.ConfigDigest == "" || row.RunManifestDigest == "" || row.DatasetDigest != artifact.ManifestID {
			return Stage07ComparisonReference{}, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Details: "Stage 05 row has incomplete typed identities"}
		}
		strategies[row.StrategyID] = Stage07StrategyRef{ImplementationDigest: validation.ImplementationDigest(row.ImplementationDigest), ConfigDigest: validation.ConfigDigest(row.ConfigDigest), RunManifestDigest: validation.RunManifestDigest(row.RunManifestDigest)}
	}
	return Stage07ComparisonReference{JobID: job.ID, ArtifactDigest: artifact.ArtifactDigest, Candidate: artifact.Candidate, DatasetDigest: validation.DatasetDigest(artifact.ManifestID), Strategies: strategies}, nil
}
