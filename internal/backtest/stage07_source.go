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
	"trading-go/internal/validation"

	"gorm.io/gorm"
)

// Stage07ComparisonReference is the explicit, digest-verified adapter by which
// canonical Stage 05 comparisons and embedded Stage 06 candidate evidence are
// admitted as manifest provenance. It does not treat a single comparison as
// multi-window validation evidence.
type Stage07ComparisonReference struct {
	JobID           uint              `json:"job_id"`
	ArtifactDigest  string            `json:"artifact_digest"`
	Candidate       string            `json:"candidate"`
	DatasetID       string            `json:"dataset_manifest_id"`
	StrategyDigests map[string]string `json:"strategy_digests"`
}

type Stage07ExperimentSource struct{ DB *gorm.DB }
type stage07SourceArtifact struct {
	SchemaVersion, ComparisonDigest, DatasetManifestID string
	Results                                            map[string]Stage05StrategyResult `json:"results"`
}
type stage07FoldArtifact struct {
	bytes               []byte
	candidate, baseline Stage05StrategyResult
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
	factory := &stage07Factory{folds: map[int]stage07FoldArtifact{}}
	for i, jobID := range manifest.Spec.FoldSourceJobIDs {
		ref, err := LoadStage07ComparisonReference(s.DB, jobID)
		if err != nil {
			return nil, nil, err
		}
		if ref.DatasetID != manifest.Spec.DatasetManifestID || ref.Candidate != manifest.Spec.Candidate.ID+"@"+manifest.Spec.Candidate.Version || ref.StrategyDigests[manifest.Spec.Candidate.ID] != manifest.Spec.Candidate.Digest || ref.StrategyDigests[manifest.Spec.Baseline.ID] != manifest.Spec.Baseline.Digest {
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
		if json.Unmarshal(raw, &artifact) != nil || artifact.SchemaVersion != "stage07-source-artifact-v1" || artifact.ComparisonDigest != ref.ArtifactDigest || artifact.DatasetManifestID != manifest.Spec.DatasetManifestID {
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
		if !runCoversFold(candidate.Manifest, manifest.Spec.Folds[i].Test) || !runCoversFold(baseline.Manifest, manifest.Spec.Folds[i].Test) {
			return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticInvalidWindowOrder, Details: "source job clock differs from immutable test fold"}
		}
		factory.folds[i] = stage07FoldArtifact{bytes: append([]byte(nil), raw...), candidate: candidate, baseline: baseline}
	}
	var bars []database.HistoricalBar
	if err := s.DB.Where("dataset_version=? AND open_time>=? AND open_time<? AND role=?", dataset.DatasetVersion, manifest.Spec.Interval.Start, manifest.Spec.Interval.End, "decision").Order("open_time ASC, exchange_symbol_id ASC").Find(&bars).Error; err != nil {
		return nil, nil, err
	}
	samples := make([]validation.Sample, 0, len(bars))
	for _, bar := range bars {
		value, parseErr := strconv.ParseFloat(bar.Close, 64)
		if parseErr != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, nil, &validation.DiagnosticError{Code: validation.DiagnosticNonFinite, Details: "invalid Stage 04 close"}
		}
		observed := bar.OpenTime.UTC()
		samples = append(samples, validation.Sample{ID: fmt.Sprintf("%s:%s:%s", bar.ExchangeSymbolID, bar.Timeframe, observed.Format(time.RFC3339Nano)), ObservedAt: observed, FeatureStart: observed.Add(-manifest.Spec.FeatureHorizon), FeatureEnd: observed, LabelEnd: observed.Add(manifest.Spec.LabelHorizon), Symbol: bar.ExchangeSymbolID, Regime: "source", BenchmarkSeen: true, CoverageOK: bar.QualityStatus == "valid", Values: map[string]float64{"close": value}})
	}
	return samples, factory, nil
}
func runCoversFold(m RunManifest, fold validation.Interval) bool {
	start, e1 := time.Parse(time.RFC3339Nano, m.Start)
	end, e2 := time.Parse(time.RFC3339Nano, m.End)
	return e1 == nil && e2 == nil && start.Equal(fold.Start) && end.Equal(fold.End) && m.DatasetManifestID != "" && m.Coverage.Passed
}
func (f *stage07Factory) NewFoldRunner(fold validation.Fold) (validation.FoldRunner, error) {
	source, ok := f.folds[fold.Index]
	if !ok {
		return nil, fmt.Errorf("fold source unavailable")
	}
	return &stage07Runner{fold: fold.Index, source: source}, nil
}
func (r *stage07Runner) FitAndSelect(_ validation.Fold, _, _ []validation.Sample, allowed map[string][]string) (validation.FoldFit, error) {
	params := map[string]string{}
	keys := make([]string, 0, len(allowed))
	for key := range allowed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	choice := ""
	for _, key := range keys {
		actual := r.source.candidate.Manifest.Strategy.Parameters[key]
		found := false
		for _, value := range allowed[key] {
			if value == actual {
				found = true
			}
		}
		if !found {
			return validation.FoldFit{}, &validation.DiagnosticError{Code: validation.DiagnosticInvalidManifest, Details: "source parameter was not predeclared: " + key}
		}
		params[key] = actual
		if choice == "" {
			choice = actual
		}
	}
	return validation.FoldFit{Choice: choice, Parameters: params, Artifact: append([]byte(nil), r.source.bytes...)}, nil
}
func (r *stage07Runner) Test(fold validation.Fold, _ []byte, test []validation.Sample) (validation.FoldPrimitives, error) {
	if fold.Index != r.fold {
		return validation.FoldPrimitives{}, &validation.DiagnosticError{Code: validation.DiagnosticTestLeakage}
	}
	return stage07Primitives(r.source.candidate, r.source.baseline, fold.Index, len(test))
}
func stage07Primitives(candidate, baseline Stage05StrategyResult, fold, observations int) (validation.FoldPrimitives, error) {
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
	totalCost, _ := strconv.ParseFloat(candidate.Metrics.TotalCosts, 64)
	perCost := 0.0
	if len(candidate.Trades) > 0 {
		perCost = totalCost / float64(len(candidate.Trades))
	}
	trades := make([]validation.TradePrimitive, len(candidate.Trades))
	for i, t := range candidate.Trades {
		regime := strings.TrimSpace(t.RegimeState)
		if regime == "" {
			regime = "unknown"
		}
		trades[i] = validation.TradePrimitive{ID: fmt.Sprintf("%d:%d:%s:%s", fold, i, t.Symbol, t.EntryTime.UTC().Format(time.RFC3339Nano)), Symbol: t.Symbol, Regime: regime, OpenedAt: t.EntryTime.UTC(), ClosedAt: t.ExitTime.UTC(), Notional: math.Abs(t.EntryPrice * t.Size), GrossPnL: t.Pnl + perCost, Cost: perCost, NetPnL: t.Pnl}
	}
	return validation.FoldPrimitives{StartingCapital: start, ExpectedObservations: observations, ObservedObservations: observations, Trades: trades, Curve: curve}, nil
}
func metricValue(v OptionalMetric) float64 {
	if v.Available {
		return v.Value
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
	digests := map[string]string{}
	for _, row := range artifact.Rows {
		digests[row.StrategyID] = row.ManifestIdentity
	}
	return Stage07ComparisonReference{JobID: job.ID, ArtifactDigest: artifact.ArtifactDigest, Candidate: artifact.Candidate, DatasetID: artifact.ManifestID, StrategyDigests: digests}, nil
}
