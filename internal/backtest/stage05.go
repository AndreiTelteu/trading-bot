package backtest

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/tradingcore"
)

const (
	ComparisonSchemaVersion = "strategy-comparison-v1"
	EvaluationSchemaVersion = "strategy-evaluation-v1"
	GovernanceSchemaVersion = "baseline-governance-gate-v1"
)

type Stage05RunRequest struct {
	StrategyID           string            `json:"strategy_id"`
	StrategyVersion      string            `json:"strategy_version,omitempty"`
	Parameters           map[string]string `json:"parameters,omitempty"`
	TargetGrossExposure  string            `json:"target_gross_exposure"`
	MaxNetExposure       string            `json:"max_net_exposure"`
	FinalPolicy          string            `json:"final_policy"`
	AllowInMemoryFixture bool              `json:"-"`
}

type OptionalMetric struct {
	Available bool    `json:"available"`
	Value     float64 `json:"value,omitempty"`
	Reason    string  `json:"reason,omitempty"`
}

type ConcentrationMetric struct {
	Key      string  `json:"key"`
	Fraction float64 `json:"fraction"`
}

type ComparableMetrics struct {
	SchemaVersion        string                `json:"schema_version"`
	StartingCapital      string                `json:"starting_capital"`
	EndingEquity         string                `json:"ending_equity"`
	TotalReturn          OptionalMetric        `json:"total_return"`
	MaxDrawdown          OptionalMetric        `json:"max_drawdown"`
	Sharpe               OptionalMetric        `json:"sharpe"`
	Sortino              OptionalMetric        `json:"sortino"`
	ProfitFactor         OptionalMetric        `json:"profit_factor"`
	Expectancy           OptionalMetric        `json:"expectancy"`
	Turnover             string                `json:"turnover"`
	TurnoverRatio        OptionalMetric        `json:"turnover_ratio"`
	TotalCosts           string                `json:"total_costs"`
	AverageGrossExposure OptionalMetric        `json:"average_gross_exposure"`
	MaximumGrossExposure OptionalMetric        `json:"maximum_gross_exposure"`
	AverageNetExposure   OptionalMetric        `json:"average_net_exposure"`
	ExposureTime         OptionalMetric        `json:"exposure_time"`
	TradeCount           int                   `json:"trade_count"`
	FillCount            int                   `json:"fill_count"`
	Reconciled           bool                  `json:"reconciled"`
	BySymbol             []ConcentrationMetric `json:"concentration_by_symbol,omitempty"`
	ByPeriod             []ConcentrationMetric `json:"concentration_by_period,omitempty"`
	ByRegime             []ConcentrationMetric `json:"concentration_by_regime,omitempty"`
}

type Stage05StrategyResult struct {
	Manifest  RunManifest       `json:"manifest"`
	Metrics   ComparableMetrics `json:"metrics"`
	Artifacts BacktestArtifacts `json:"artifacts"`
	Equity    []EquityPoint     `json:"equity"`
	Trades    []Trade           `json:"trades"`
	Rankings  []RankingArtifact `json:"rankings,omitempty"`
}

type RankingArtifact struct {
	DecisionAt string  `json:"decision_at"`
	Symbol     string  `json:"symbol"`
	Rank       int     `json:"rank"`
	Score      float64 `json:"score"`
	Selected   bool    `json:"selected"`
}

type NormalizedAssumptions struct {
	StartingCapital   string          `json:"starting_capital"`
	MaxGrossExposure  string          `json:"max_gross_exposure"`
	MaxNetExposure    string          `json:"max_net_exposure"`
	DatasetManifestID string          `json:"dataset_manifest_id"`
	UniverseMode      UniverseMode    `json:"universe_mode"`
	DecisionCadence   string          `json:"decision_cadence"`
	ExecutionPolicy   ExecutionPolicy `json:"execution_policy"`
	FeeBPS            float64         `json:"fee_bps"`
	SlippageBPS       float64         `json:"slippage_bps"`
	FinalPolicy       string          `json:"final_policy"`
}

type ComparisonRow struct {
	StrategyID       string             `json:"strategy_id"`
	StrategyVersion  string             `json:"strategy_version"`
	Descriptor       StrategyDescriptor `json:"descriptor"`
	Parameters       map[string]string  `json:"parameters"`
	ManifestIdentity string             `json:"manifest_identity"`
	Baseline         bool               `json:"baseline"`
	Metrics          ComparableMetrics  `json:"metrics"`
	ExcessVsCash     OptionalMetric     `json:"excess_return_vs_cash"`
	ExcessVsMarket   OptionalMetric     `json:"excess_return_vs_market"`
	MetricDeltas     map[string]float64 `json:"metric_deltas,omitempty"`
	Pass             bool               `json:"pass"`
	Reasons          []string           `json:"reasons"`
}

type GovernanceGate struct {
	SchemaVersion       string   `json:"schema_version"`
	OptimizationAllowed bool     `json:"optimization_allowed"`
	PromotionAllowed    bool     `json:"promotion_allowed"`
	Reasons             []string `json:"reasons"`
}

type ComparisonArtifact struct {
	SchemaVersion string                           `json:"schema_version"`
	ManifestID    string                           `json:"manifest_id"`
	Candidate     string                           `json:"candidate"`
	Assumptions   NormalizedAssumptions            `json:"normalized_assumptions"`
	Rows          []ComparisonRow                  `json:"rows"`
	Governance    GovernanceGate                   `json:"governance"`
	Results       map[string]Stage05StrategyResult `json:"-"`
	Limitations   []string                         `json:"limitations"`
}

func RunStage05Comparison(config BacktestConfig, series map[string][]services.OHLCV, request Stage05RunRequest) (ComparisonArtifact, error) {
	if request.StrategyID == "" {
		return ComparisonArtifact{}, &StrategyDiagnosticError{Code: DiagnosticUnknownStrategy, Details: "candidate strategy id is required"}
	}
	if request.TargetGrossExposure == "" {
		request.TargetGrossExposure = "1"
	}
	if request.MaxNetExposure == "" {
		request.MaxNetExposure = request.TargetGrossExposure
	}
	if request.FinalPolicy == "" {
		request.FinalPolicy = "liquidate"
	}
	if err := validateComparableInputs(config, series, request); err != nil {
		return ComparisonArtifact{}, err
	}
	candidateParameters := cloneStringMap(request.Parameters)
	candidateParameters["target_gross"] = request.TargetGrossExposure
	if _, ok := candidateParameters["final_policy"]; ok || request.StrategyID != StrategyCashID {
		candidateParameters["final_policy"] = request.FinalPolicy
	}
	candidate, _, err := DefaultStrategyRegistry.Resolve(request.StrategyID, request.StrategyVersion, candidateParameters)
	if err != nil {
		return ComparisonArtifact{}, err
	}
	ids := []string{StrategyCashID, StrategyBenchmarkHoldID, StrategyBenchmarkTrendID}
	if strategyNeedsUniverse(candidate.Descriptor.ID) {
		ids = append(ids, StrategyEqualWeightID, StrategyMomentumID)
	}
	if !containsString(ids, candidate.Descriptor.ID) {
		ids = append(ids, candidate.Descriptor.ID)
	}
	results := map[string]Stage05StrategyResult{}
	for _, id := range ids {
		parameters := map[string]string{}
		version := ""
		if id == candidate.Descriptor.ID {
			parameters = candidateParameters
			version = candidate.Descriptor.Version
		} else if id != StrategyCashID {
			parameters["target_gross"] = request.TargetGrossExposure
			parameters["final_policy"] = request.FinalPolicy
		}
		selected, strategy, resolveErr := DefaultStrategyRegistry.Resolve(id, version, parameters)
		if resolveErr != nil {
			return ComparisonArtifact{}, resolveErr
		}
		result, runErr := runStage05Strategy(config, series, selected, strategy, request.AllowInMemoryFixture)
		if runErr != nil {
			return ComparisonArtifact{}, runErr
		}
		results[id] = result
	}
	comparison := buildStage05Comparison(config, request, candidate, results)
	return comparison, nil
}

func validateComparableInputs(config BacktestConfig, series map[string][]services.OHLCV, request Stage05RunRequest) error {
	if config.InitialBalance <= 0 || config.Start.IsZero() || !config.End.After(config.Start) {
		return &StrategyDiagnosticError{Code: DiagnosticInvalidCombination, Details: "positive starting capital and half-open [start,end) interval are required"}
	}
	if request.FinalPolicy != "liquidate" && request.FinalPolicy != "mark_to_market" {
		return invalidParameter(request.StrategyID, "final_policy", "must be liquidate or mark_to_market")
	}
	gross, err := tradingcore.ParseDecimal(request.TargetGrossExposure)
	if err != nil || gross.Sign() <= 0 || gross.Float64() > 1 {
		return invalidParameter(request.StrategyID, "target_gross_exposure", "must be an exact decimal in (0,1]")
	}
	net, err := tradingcore.ParseDecimal(request.MaxNetExposure)
	if err != nil || net.Sign() <= 0 || net.Float64() > gross.Float64() {
		return invalidParameter(request.StrategyID, "max_net_exposure", "must be an exact decimal in (0,target gross]")
	}
	if math.Abs(net.Float64()-gross.Float64()) > 1e-12 {
		return &StrategyDiagnosticError{Code: DiagnosticInvalidCombination, Strategy: request.StrategyID, Details: "long-only Stage 05 strategies require max net exposure to equal target gross exposure"}
	}
	if math.Trunc(config.FeeBps) != config.FeeBps || math.Trunc(config.SlippageBps) != config.SlippageBps || config.FeeBps < 0 || config.SlippageBps < 0 {
		return &StrategyDiagnosticError{Code: DiagnosticInvalidCombination, Details: "shared broker fee/slippage bps must be non-negative integers"}
	}
	if !request.AllowInMemoryFixture {
		if !config.DatasetManifestRequired || !config.DatasetManifestValidated || strings.TrimSpace(config.DatasetManifestID) == "" {
			return &StrategyDiagnosticError{Code: DiagnosticManifestRequired, Strategy: request.StrategyID, Details: "production comparisons require an exact validated Stage 04 manifest"}
		}
		if config.UniverseMode != UniverseDynamicReplay {
			return &StrategyDiagnosticError{Code: DiagnosticUniverseRequired, Strategy: request.StrategyID, Details: "production comparisons require persisted point-in-time universe replay"}
		}
		if !config.ConstraintsAvailable || config.ConstraintResolver == nil {
			return &StrategyDiagnosticError{Code: DiagnosticConstraintRequired, Strategy: request.StrategyID, Details: "manifest-pinned historical constraints are required"}
		}
	}
	if len(series) == 0 {
		return &StrategyDiagnosticError{Code: DiagnosticManifestIncompatible, Strategy: request.StrategyID, Details: "decision series are empty"}
	}
	if len(config.BenchmarkSeries) == 0 || strings.TrimSpace(config.BenchmarkSymbol) == "" {
		return &StrategyDiagnosticError{Code: DiagnosticBenchmarkRequired, Strategy: request.StrategyID, Details: "independent benchmark role series is required"}
	}
	return nil
}

func strategyNeedsUniverse(id string) bool {
	return id == StrategyEqualWeightID || id == StrategyMomentumID || id == StrategyLegacyCompatibility
}

type stage05Replay struct {
	at       time.Time
	regime   string
	complete bool
	members  []ReplayMember
}

func runStage05Strategy(config BacktestConfig, series map[string][]services.OHLCV, selected SelectedStrategy, strategy tradingcore.Strategy, fixture bool) (Stage05StrategyResult, error) {
	if err := validateStrategyDataRequirements(config, selected, fixture); err != nil {
		return Stage05StrategyResult{}, err
	}
	parameters := selected.Parameters
	warmup := selected.Descriptor.WarmupBars
	if raw := parameters["warmup_bars"]; raw != "" {
		warmup, _ = strconv.Atoi(raw)
	}
	if raw := parameters["lookback_bars"]; raw != "" {
		lookback, _ := strconv.Atoi(raw)
		warmup = lookback + 1
		if selected.Descriptor.ID == StrategyBenchmarkTrendID {
			sample, _ := strconv.Atoi(parameters["sample_bars"])
			warmup = lookback*sample + 1
		}
	}
	reference := stage05ReferenceSeries(config, series, selected.Descriptor.ID)
	if len(reference) <= warmup {
		return Stage05StrategyResult{}, &StrategyDiagnosticError{Code: DiagnosticInsufficientWarmup, Strategy: selected.Descriptor.ID, Details: fmt.Sprintf("need more than %d completed bars, have %d", warmup, len(reference))}
	}
	replays, err := loadStage05Replay(config, fixture)
	if err != nil {
		return Stage05StrategyResult{}, err
	}
	if strategyNeedsUniverse(selected.Descriptor.ID) && len(replays) == 0 {
		return Stage05StrategyResult{}, &StrategyDiagnosticError{Code: DiagnosticUniverseRequired, Strategy: selected.Descriptor.ID, Details: "no effective persisted universe snapshot"}
	}
	runConfig := config
	runConfig.StrategyID = selected.Descriptor.ID
	runConfig.StrategyVersion = selected.Descriptor.Version
	runConfig.StrategyParameters = cloneStringMap(parameters)
	ledger := newBacktestMemoryLedger(runConfig)
	equity := []EquityPoint{{Time: config.Start.UTC(), Value: config.InitialBalance}}
	rankings := []RankingArtifact{}
	lastTargets := []string{}
	lastRebalance := time.Time{}
	allSeries := cloneOHLCVSeries(series)
	if _, ok := allSeries[config.BenchmarkSymbol]; !ok {
		allSeries[config.BenchmarkSymbol] = append([]services.OHLCV(nil), config.BenchmarkSeries...)
	}
	finalPolicy := parameters["final_policy"]
	if finalPolicy == "" {
		finalPolicy = "mark_to_market"
	}
	lastExecutableAt := lastExecutionTimestamp(config, allSeries, reference)
	for i, bar := range reference {
		signalAt := time.UnixMilli(bar.CloseTime).UTC()
		if signalAt.Before(config.Start) || !signalAt.Before(config.End) {
			continue
		}
		marks := marksAsOf(allSeries, signalAt)
		if selected.Descriptor.ID == StrategyCashID {
			if err := recordStage05NoAction(ledger, runConfig, strategy, config.BenchmarkSymbol, bar.Close, signalAt, "cash_preserved", marks); err != nil {
				return Stage05StrategyResult{}, err
			}
			equity = appendEquity(equity, signalAt, portfolioEquity(ledger, marks))
			continue
		}
		if i+1 < warmup {
			equity = appendEquity(equity, signalAt, portfolioEquity(ledger, marks))
			continue
		}
		targets, ranked, decide, decisionErr := stage05Targets(selected, reference[:i+1], series, config, replays, signalAt, lastRebalance, lastTargets)
		if decisionErr != nil {
			return Stage05StrategyResult{}, decisionErr
		}
		rankings = append(rankings, ranked...)
		if !decide {
			equity = appendEquity(equity, signalAt, portfolioEquity(ledger, marks))
			continue
		}
		fillAt, fillPrices, ok := nextFillPrices(config, allSeries, targetsWithHeld(targets, ledger.positions), signalAt)
		if !ok || (finalPolicy == "liquidate" && fillAt.Equal(lastExecutableAt)) {
			break
		}
		regime := "unknown"
		if snapshot, found := replayAsOf(replays, signalAt); found && snapshot.regime != "" {
			regime = snapshot.regime
		}
		if err := rebalanceStage05(ledger, runConfig, strategy, targets, marks, fillPrices, signalAt, fillAt, parameters, regime); err != nil {
			return Stage05StrategyResult{}, err
		}
		lastTargets = append([]string(nil), targets...)
		lastRebalance = signalAt
		equity = appendEquity(equity, fillAt, portfolioEquity(ledger, marksAsOf(allSeries, fillAt)))
	}
	if finalPolicy == "liquidate" && len(ledger.positions) > 0 {
		signalAt, fillAt, fillPrices, ok := finalLiquidationPrices(config, allSeries, ledger.positions)
		if !ok {
			return Stage05StrategyResult{}, &StrategyDiagnosticError{Code: DiagnosticManifestIncompatible, Strategy: selected.Descriptor.ID, Details: "final liquidation has no next-executable evidence inside [start,end)"}
		}
		marks := marksAsOf(allSeries, signalAt)
		regime := "unknown"
		if snapshot, found := replayAsOf(replays, signalAt); found && snapshot.regime != "" {
			regime = snapshot.regime
		}
		if err := liquidateStage05(ledger, runConfig, strategy, marks, fillPrices, signalAt, fillAt, regime); err != nil {
			return Stage05StrategyResult{}, err
		}
		equity = appendEquity(equity, fillAt, ledger.cash)
	}
	endMarks := marksAsOf(allSeries, config.End.Add(-time.Nanosecond))
	endEquity := portfolioEquity(ledger, endMarks)
	equity = appendEquity(equity, config.End.UTC(), endEquity)
	states := stage05SymbolStates(allSeries, config.End)
	artifacts := buildBacktestArtifacts(ledger, ledger.positions, states, nil)
	coverage := CoverageReport{SchemaVersion: CoverageSchemaVersion, PolicyVersion: "stage05-exact-manifest-v1", Passed: true, Diagnostics: []CoverageDiagnostic{{Dataset: "strategy_requirements", Status: "passed"}}}
	classification := RunStrategyZeroTrades
	if len(ledger.events) > 0 {
		classification = RunSuccessfulExecution
	}
	manifest := buildManifest(runConfig, coverage, classification, config.DatasetManifestID)
	manifest.Strategy = selected
	manifest.StrategyVersion = selected.Descriptor.Version
	manifest.Artifacts.Comparison = "comparison.json"
	metrics := computeComparableMetrics(runConfig, ledger, equity, endMarks, allSeries)
	if !metrics.Reconciled {
		return Stage05StrategyResult{}, fmt.Errorf("stage05 ledger/equity reconciliation failed for %s", selected.Descriptor.ID)
	}
	return Stage05StrategyResult{Manifest: manifest, Metrics: metrics, Artifacts: artifacts, Equity: equity, Trades: append([]Trade(nil), ledger.trades...), Rankings: rankings}, nil
}

func validateStrategyDataRequirements(config BacktestConfig, selected SelectedStrategy, fixture bool) error {
	if fixture {
		return nil
	}
	for _, requirement := range selected.Descriptor.RequiredData {
		matched := map[string]bool{}
		for _, covered := range config.DatasetSeries {
			if covered.Role == requirement.Role && covered.Timeframe == requirement.Timeframe && covered.Rows > 0 && covered.SeriesHash != "" {
				matched[strings.ToUpper(covered.Ticker)] = true
			}
		}
		if requirement.Role == "benchmark" {
			if !matched[strings.ToUpper(config.BenchmarkSymbol)] {
				return &StrategyDiagnosticError{Code: DiagnosticManifestIncompatible, Strategy: selected.Descriptor.ID, Field: requirement.Role + ":" + requirement.Timeframe, Details: "exact benchmark series identity is absent from the validated manifest"}
			}
			continue
		}
		for _, symbol := range config.Symbols {
			if !matched[strings.ToUpper(symbol)] {
				return &StrategyDiagnosticError{Code: DiagnosticManifestIncompatible, Strategy: selected.Descriptor.ID, Field: requirement.Role + ":" + requirement.Timeframe, Details: "required exact series is absent for " + strings.ToUpper(symbol)}
			}
		}
	}
	return nil
}

func stage05ReferenceSeries(config BacktestConfig, series map[string][]services.OHLCV, id string) []services.OHLCV {
	// The independently sourced benchmark is the common decision/valuation
	// clock for every lane. Candidate bars are queried only as-of those times.
	return append([]services.OHLCV(nil), config.BenchmarkSeries...)
}

func loadStage05Replay(config BacktestConfig, fixture bool) ([]stage05Replay, error) {
	var entries []replaySnapshotEntry
	var err error
	if fixture {
		entries = fixtureReplaySnapshots(config.ReplaySnapshots)
	} else {
		entries, err = loadReplaySnapshotsForManifest(config.Start, config.End, config.DatasetManifestID)
	}
	if err != nil {
		return nil, err
	}
	public := canonicalReplaySnapshots(entries)
	result := make([]stage05Replay, 0, len(public))
	for _, snapshot := range public {
		result = append(result, stage05Replay{at: snapshot.Timestamp, regime: snapshot.RegimeState, complete: snapshot.ObservedComplete, members: snapshot.Members})
	}
	return result, nil
}

func stage05Targets(selected SelectedStrategy, reference []services.OHLCV, series map[string][]services.OHLCV, config BacktestConfig, replays []stage05Replay, at, lastRebalance time.Time, lastTargets []string) ([]string, []RankingArtifact, bool, error) {
	switch selected.Descriptor.ID {
	case StrategyBenchmarkHoldID:
		if len(lastTargets) > 0 {
			return lastTargets, nil, false, nil
		}
		return []string{config.BenchmarkSymbol}, nil, true, nil
	case StrategyBenchmarkTrendID:
		lookback, _ := strconv.Atoi(selected.Parameters["lookback_bars"])
		sample, _ := strconv.Atoi(selected.Parameters["sample_bars"])
		if len(reference) < lookback*sample {
			return nil, nil, false, &StrategyDiagnosticError{Code: DiagnosticInsufficientWarmup, Strategy: selected.Descriptor.ID, Details: "trend lookback unavailable"}
		}
		if len(reference)%sample != 0 {
			return lastTargets, nil, false, nil
		}
		window := make([]services.OHLCV, 0, lookback)
		for index := len(reference) - lookback*sample + sample - 1; index < len(reference); index += sample {
			window = append(window, reference[index])
		}
		sum := 0.0
		for _, bar := range window {
			sum += bar.Close
		}
		inMarket := window[len(window)-1].Close > sum/float64(lookback)
		targets := []string{}
		if inMarket {
			targets = []string{config.BenchmarkSymbol}
		}
		return targets, nil, !sameStrings(targets, lastTargets), nil
	case StrategyEqualWeightID, StrategyMomentumID:
		rebalance, _ := time.ParseDuration(selected.Parameters["rebalance"])
		if !lastRebalance.IsZero() && at.Sub(lastRebalance) < rebalance {
			return lastTargets, nil, false, nil
		}
		snapshot, ok := replayAsOf(replays, at)
		if !ok || !snapshot.complete || at.Sub(snapshot.at) > rebalance {
			return nil, nil, false, &StrategyDiagnosticError{Code: DiagnosticUniverseCoverage, Strategy: selected.Descriptor.ID, Details: "complete snapshot is missing or stale at rebalance"}
		}
		eligible := eligibleReplaySymbols(snapshot.members)
		if selected.Descriptor.ID == StrategyEqualWeightID {
			return eligible, nil, true, nil
		}
		lookback, _ := strconv.Atoi(selected.Parameters["lookback_bars"])
		topN, _ := strconv.Atoi(selected.Parameters["top_n"])
		type score struct {
			symbol string
			value  float64
		}
		scores := []score{}
		for _, symbol := range eligible {
			bars := barsAvailableAsOf(series[symbol], at)
			if len(bars) <= lookback {
				return nil, nil, false, &StrategyDiagnosticError{Code: DiagnosticInsufficientWarmup, Strategy: selected.Descriptor.ID, Field: symbol, Details: "momentum lookback unavailable for eligible member"}
			}
			from, to := bars[len(bars)-1-lookback].Close, bars[len(bars)-1].Close
			if from <= 0 {
				return nil, nil, false, &StrategyDiagnosticError{Code: DiagnosticManifestIncompatible, Strategy: selected.Descriptor.ID, Field: symbol, Details: "non-positive lookback close"}
			}
			scores = append(scores, score{symbol, to/from - 1})
		}
		sort.Slice(scores, func(i, j int) bool {
			if scores[i].value != scores[j].value {
				return scores[i].value > scores[j].value
			}
			return scores[i].symbol < scores[j].symbol
		})
		targets := []string{}
		ranked := make([]RankingArtifact, 0, len(scores))
		for i, item := range scores {
			selectedAsset := item.value > 0 && len(targets) < topN
			if selectedAsset {
				targets = append(targets, item.symbol)
			}
			ranked = append(ranked, RankingArtifact{DecisionAt: canonicalTime(at), Symbol: item.symbol, Rank: i + 1, Score: item.value, Selected: selectedAsset})
		}
		return targets, ranked, true, nil
	default:
		return nil, nil, false, &StrategyDiagnosticError{Code: DiagnosticUnknownStrategy, Strategy: selected.Descriptor.ID, Details: "Stage 05 planner is not registered"}
	}
}

func replayAsOf(values []stage05Replay, at time.Time) (stage05Replay, bool) {
	idx := sort.Search(len(values), func(i int) bool { return values[i].at.After(at) }) - 1
	if idx < 0 {
		return stage05Replay{}, false
	}
	return values[idx], true
}

func eligibleReplaySymbols(members []ReplayMember) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, member := range members {
		stage := strings.ToLower(member.Stage)
		eligibleStage := stage == "" || stage == "accepted" || stage == "eligible" || stage == "ranked" || member.Shortlisted
		if member.RejectionReason != "" || !eligibleStage {
			continue
		}
		symbol := strings.ToUpper(member.Symbol)
		if symbol != "" && !seen[symbol] {
			seen[symbol] = true
			result = append(result, symbol)
		}
	}
	sort.Strings(result)
	return result
}

func rebalanceStage05(ledger *backtestMemoryLedger, config BacktestConfig, strategy tradingcore.Strategy, targets []string, marks, fills map[string]float64, signalAt, fillAt time.Time, parameters map[string]string, regime string) error {
	if len(ledger.positions) > 0 {
		if err := liquidateStage05(ledger, config, strategy, marks, fills, signalAt, fillAt, regime); err != nil {
			return err
		}
	}
	if len(targets) == 0 {
		return nil
	}
	gross, _ := strconv.ParseFloat(parameters["target_gross"], 64)
	equity := portfolioEquity(ledger, marks)
	weight := gross / float64(len(targets))
	for rank, symbol := range targets {
		mark := marks[symbol]
		fill := fills[symbol]
		if mark <= 0 || fill <= 0 {
			return &StrategyDiagnosticError{Code: DiagnosticManifestIncompatible, Strategy: config.StrategyID, Field: symbol, Details: "signal or next executable price unavailable"}
		}
		quantity := equity * weight / mark
		if err := runStage05Target(ledger, config, strategy, symbol, tradingcore.Buy, quantity, mark, fill, signalAt, fillAt, rank+1, weight, "rebalance_entry", regime, marks); err != nil {
			return err
		}
	}
	return nil
}

func liquidateStage05(ledger *backtestMemoryLedger, config BacktestConfig, strategy tradingcore.Strategy, marks, fills map[string]float64, signalAt, fillAt time.Time, regime string) error {
	symbols := make([]string, 0, len(ledger.positions))
	for symbol := range ledger.positions {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	for i, symbol := range symbols {
		position := ledger.positions[symbol]
		mark, fill := marks[symbol], fills[symbol]
		if mark <= 0 || fill <= 0 {
			return &StrategyDiagnosticError{Code: DiagnosticManifestIncompatible, Strategy: config.StrategyID, Field: symbol, Details: "exit signal or next executable price unavailable"}
		}
		if err := runStage05Target(ledger, config, strategy, symbol, tradingcore.Sell, position.Size, mark, fill, signalAt, fillAt, i, 0, "rebalance_exit", regime, marks); err != nil {
			return err
		}
	}
	return nil
}

func runStage05Target(ledger *backtestMemoryLedger, config BacktestConfig, strategy tradingcore.Strategy, symbol string, side tradingcore.OrderSide, quantity, signalPrice, executionPrice float64, signalAt, fillAt time.Time, priority int, weight float64, reason, regime string, marks map[string]float64) error {
	instrument, err := backtestInstrument(config, symbol)
	if err != nil {
		return err
	}
	// Cash-capped quantity is an execution-adapter fill policy, not strategy
	// information: selection and target weight were fixed at signalAt. Capping
	// against the next executable price plus deterministic costs prevents an
	// opening gap from manufacturing negative cash.
	if side == tradingcore.Buy {
		costMultiplier := 1 + (config.FeeBps+config.SlippageBps)/10000
		cashCap := ledger.cash / (executionPrice * costMultiplier)
		if cashCap < quantity {
			quantity = cashCap
		}
		if quantity <= 0 {
			return &StrategyDiagnosticError{Code: DiagnosticInvalidCombination, Strategy: config.StrategyID, Field: symbol, Details: "cash-capped executable quantity is zero"}
		}
	}
	quantity = stage05EconomicFloat(quantity)
	accountName := config.AccountID
	if accountName == "" {
		accountName = "backtest"
	}
	account, _ := tradingcore.NewAccountID(accountName)
	portfolioPositions := make([]tradingcore.Position, 0, len(ledger.positions))
	for heldSymbol, held := range ledger.positions {
		heldInstrument, conversionErr := backtestInstrument(config, heldSymbol)
		if conversionErr != nil {
			return conversionErr
		}
		mark := marks[heldSymbol]
		if mark <= 0 {
			mark = held.EntryPrice
		}
		portfolioPositions = append(portfolioPositions, tradingcore.Position{ID: mustPositionID(heldSymbol), Instrument: heldInstrument, Quantity: mustQuantity(held.Size), AveragePrice: mustPrice(held.EntryPrice), MarkPrice: mustPrice(mark), OpenedAt: held.EntryTime, RealizedPnL: mustAmount(0)})
	}
	sort.Slice(portfolioPositions, func(i, j int) bool {
		return portfolioPositions[i].Instrument.ID.String() < portfolioPositions[j].Instrument.ID.String()
	})
	portfolio, err := tradingcore.NewPortfolioSnapshot(signalAt, account, tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{instrument.QuoteAsset: mustAmount(stage05EconomicFloat(ledger.cash))}, portfolioPositions, nil, stage05RiskState(ledger, marks))
	if err != nil {
		return err
	}
	price := mustPrice(signalPrice)
	quote := tradingcore.Quote{Instrument: instrument, Bid: price, Ask: price, Last: price, ObservedAt: signalAt}
	score, _ := tradingcore.ParseDecimal(decimalString(weight))
	universe, err := tradingcore.NewUniverseSnapshot(signalAt, "stage05-point-in-time-v1", string(config.UniverseMode), []tradingcore.UniverseCandidate{{Instrument: instrument, Rank: priority + 1, Score: score, Eligible: true}})
	if err != nil {
		return err
	}
	action := "buy"
	if side == tradingcore.Sell {
		action = "sell"
	}
	settings := map[string]string{"target_action." + instrument.ID.String(): action, "target_quantity." + instrument.ID.String(): decimalString(quantity), "target_priority." + instrument.ID.String(): strconv.Itoa(priority), "target_weight." + instrument.ID.String(): decimalString(weight), "target_reason." + instrument.ID.String(): reason, "target_regime." + instrument.ID.String(): regime, "strategy_horizon": config.Timeframe}
	versions := tradingcore.VersionContext{Strategy: config.StrategyID + "@" + config.StrategyVersion, Settings: config.ConfigVersion, Policy: backtestPolicyVersion(config), Dataset: config.DatasetManifestID}
	snapshot, err := tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{MarketObservedAt: signalAt, SignalAt: signalAt, DecisionAt: signalAt, Quotes: map[tradingcore.InstrumentID]tradingcore.Quote{instrument.ID: quote}, Universe: universe, Portfolio: portfolio, Settings: settings, Versions: versions})
	if err != nil {
		return err
	}
	portfolioValue := stage05EconomicFloat(portfolioEquity(ledger, marks))
	targetGross := 1.0
	if configured, parseErr := strconv.ParseFloat(config.StrategyParameters["target_gross"], 64); parseErr == nil && configured > 0 {
		targetGross = configured
	}
	maxGrossValue := stage05EconomicFloat(portfolioValue * targetGross)
	maxGross := mustAmount(maxGrossValue)
	maxPositionValue := maxGrossValue
	if config.MaxPositionValue > 0 && config.MaxPositionValue < maxPositionValue {
		maxPositionValue = config.MaxPositionValue
	}
	maxPosition := mustAmount(stage05EconomicFloat(maxPositionValue))
	lot, tick, minQuantity, minNotional, err := constraintValues(config, symbol, fillAt)
	if err != nil {
		return &StrategyDiagnosticError{Code: DiagnosticConstraintRequired, Strategy: config.StrategyID, Field: symbol, Details: err.Error()}
	}
	maxPositions := config.MaxPositions
	if maxPositions <= 0 {
		maxPositions = 1
	}
	policy := tradingcore.RiskPolicy{Version: backtestPolicyVersion(config), MaxPositions: maxPositions, MaxGrossExposure: maxGross, MaxPositionValue: maxPosition, MaxTurnover: mustAmount(0), CashReserve: mustAmount(0), MaxConcurrentOrders: maxPositions, LotSize: mustQuantity(lot), ExecutionCosts: tradingcore.ExecutionCostPolicy{Version: config.ExecutionPolicy.CostVersion, FeeBPS: int64(config.FeeBps), AdverseSlippageBPS: int64(config.SlippageBps)}}
	broker := tradingcore.NewBacktestBroker(tradingcore.NewFixedClock(fillAt), tradingcore.NewSequenceIDGenerator("stage05-"+symbol+"-"+strconv.FormatInt(fillAt.UnixNano(), 10), uint64(len(ledger.events)+1)), tradingcore.CostModel{FeeBPS: int64(config.FeeBps), SlippageBPS: int64(config.SlippageBps), Version: config.ExecutionPolicy.CostVersion, ExecutionPrice: tradingcore.SomePrice(mustPrice(executionPrice)), PriceTick: tick, MinQuantity: minQuantity, MinNotional: minNotional})
	runner := tradingcore.Orchestrator{Source: backtestDecisionSource{snapshot: snapshot, policy: policy}, Strategy: strategy, Risk: tradingcore.PortfolioRiskEngine{}, Broker: broker, Ledger: ledger, Observer: ledger}
	result, err := runner.Run(context.Background())
	if err != nil {
		return err
	}
	ledger.recordRun(result, signalAt, signalAt)
	return nil
}

func recordStage05NoAction(ledger *backtestMemoryLedger, config BacktestConfig, strategy tradingcore.Strategy, symbol string, price float64, at time.Time, code string, marks map[string]float64) error {
	instrument, err := backtestInstrument(config, symbol)
	if err != nil {
		return err
	}
	account, _ := tradingcore.NewAccountID("backtest")
	portfolio, _ := tradingcore.NewPortfolioSnapshot(at, account, tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{instrument.QuoteAsset: mustAmount(stage05EconomicFloat(ledger.cash))}, nil, nil, stage05RiskState(ledger, marks))
	universe, _ := tradingcore.NewUniverseSnapshot(at, "stage05-cash-v1", string(config.UniverseMode), []tradingcore.UniverseCandidate{{Instrument: instrument, Rank: 1, Score: tradingcore.MustDecimal(0, 0), Eligible: true}})
	p := mustPrice(price)
	snapshot, err := tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{MarketObservedAt: at, SignalAt: at, DecisionAt: at, Quotes: map[tradingcore.InstrumentID]tradingcore.Quote{instrument.ID: {Instrument: instrument, Bid: p, Ask: p, Last: p, ObservedAt: at}}, Universe: universe, Portfolio: portfolio, Settings: map[string]string{"target_action." + instrument.ID.String(): "hold", "no_action_code." + instrument.ID.String(): code}, Versions: tradingcore.VersionContext{Strategy: config.StrategyID + "@" + config.StrategyVersion, Dataset: config.DatasetManifestID}})
	if err != nil {
		return err
	}
	limit := mustAmount(config.InitialBalance)
	policy := tradingcore.RiskPolicy{Version: backtestPolicyVersion(config), MaxPositions: 1, MaxGrossExposure: limit, MaxPositionValue: limit, MaxTurnover: mustAmount(0), CashReserve: mustAmount(0), MaxConcurrentOrders: 1, LotSize: mustQuantity(.00000001), ExecutionCosts: tradingcore.ExecutionCostPolicy{Version: config.ExecutionPolicy.CostVersion, FeeBPS: int64(config.FeeBps), AdverseSlippageBPS: int64(config.SlippageBps)}}
	broker := tradingcore.NewBacktestBroker(tradingcore.NewFixedClock(at), tradingcore.NewSequenceIDGenerator("stage05-cash", uint64(ledger.runs+1)), tradingcore.CostModel{FeeBPS: int64(config.FeeBps), SlippageBPS: int64(config.SlippageBps), Version: config.ExecutionPolicy.CostVersion, ExecutionPrice: tradingcore.SomePrice(p)})
	runner := tradingcore.Orchestrator{Source: backtestDecisionSource{snapshot, policy}, Strategy: strategy, Risk: tradingcore.PortfolioRiskEngine{}, Broker: broker, Ledger: ledger, Observer: ledger}
	result, err := runner.Run(context.Background())
	if err != nil {
		return err
	}
	ledger.recordRun(result, at, at)
	return nil
}

func nextFillPrices(config BacktestConfig, series map[string][]services.OHLCV, symbols []string, signalAt time.Time) (time.Time, map[string]float64, bool) {
	prices := map[string]float64{}
	var common time.Time
	for _, symbol := range symbols {
		bars := config.ExecutionSeries[symbol]
		if len(bars) == 0 {
			bars = series[symbol]
		}
		idx := sort.Search(len(bars), func(i int) bool { return time.UnixMilli(bars[i].OpenTime).After(signalAt) })
		if idx >= len(bars) || !time.UnixMilli(bars[idx].OpenTime).Before(config.End) {
			return time.Time{}, nil, false
		}
		at := time.UnixMilli(bars[idx].OpenTime).UTC()
		if common.IsZero() {
			common = at
		} else if !common.Equal(at) {
			return time.Time{}, nil, false
		}
		prices[symbol] = bars[idx].Open
	}
	return common, prices, !common.IsZero()
}

func lastExecutionTimestamp(config BacktestConfig, series map[string][]services.OHLCV, reference []services.OHLCV) time.Time {
	last := time.Time{}
	for _, bar := range reference {
		at := time.UnixMilli(bar.OpenTime).UTC()
		if !at.Before(config.End) {
			break
		}
		if at.After(last) {
			last = at
		}
	}
	return last
}

func finalLiquidationPrices(config BacktestConfig, series map[string][]services.OHLCV, positions map[string]*positionState) (time.Time, time.Time, map[string]float64, bool) {
	symbols := make([]string, 0, len(positions))
	for symbol := range positions {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	fillPrices := map[string]float64{}
	var signalAt, fillAt time.Time
	for _, symbol := range symbols {
		bars := config.ExecutionSeries[symbol]
		if len(bars) == 0 {
			bars = series[symbol]
		}
		last := -1
		for i := range bars {
			if time.UnixMilli(bars[i].OpenTime).Before(config.End) {
				last = i
			}
		}
		if last < 1 {
			return time.Time{}, time.Time{}, nil, false
		}
		candidateFill := time.UnixMilli(bars[last].OpenTime).UTC()
		candidateSignal := time.UnixMilli(bars[last-1].CloseTime).UTC()
		if !candidateFill.After(candidateSignal) {
			return time.Time{}, time.Time{}, nil, false
		}
		if fillAt.IsZero() {
			fillAt, signalAt = candidateFill, candidateSignal
		} else if !fillAt.Equal(candidateFill) || !signalAt.Equal(candidateSignal) {
			return time.Time{}, time.Time{}, nil, false
		}
		fillPrices[symbol] = bars[last].Open
	}
	return signalAt, fillAt, fillPrices, true
}

func marksAsOf(series map[string][]services.OHLCV, at time.Time) map[string]float64 {
	result := map[string]float64{}
	for symbol, bars := range series {
		idx := sort.Search(len(bars), func(i int) bool { return time.UnixMilli(bars[i].CloseTime).After(at) }) - 1
		if idx >= 0 {
			result[symbol] = bars[idx].Close
		}
	}
	return result
}

func barsAvailableAsOf(bars []services.OHLCV, at time.Time) []services.OHLCV {
	idx := sort.Search(len(bars), func(i int) bool { return time.UnixMilli(bars[i].CloseTime).After(at) })
	return bars[:idx]
}

func portfolioEquity(ledger *backtestMemoryLedger, marks map[string]float64) float64 {
	value := ledger.cash
	for symbol, position := range ledger.positions {
		mark := marks[symbol]
		if mark <= 0 {
			mark = position.EntryPrice
		}
		value += position.Size * mark
	}
	return value
}

func targetsWithHeld(targets []string, positions map[string]*positionState) []string {
	set := map[string]bool{}
	for _, symbol := range targets {
		set[symbol] = true
	}
	for symbol := range positions {
		set[symbol] = true
	}
	result := make([]string, 0, len(set))
	for symbol := range set {
		result = append(result, symbol)
	}
	sort.Strings(result)
	return result
}

func appendEquity(values []EquityPoint, at time.Time, value float64) []EquityPoint {
	if len(values) > 0 && values[len(values)-1].Time.Equal(at) {
		values[len(values)-1].Value = value
		return values
	}
	return append(values, EquityPoint{Time: at.UTC(), Value: value})
}

func stage05SymbolStates(series map[string][]services.OHLCV, at time.Time) map[string]*symbolState {
	result := map[string]*symbolState{}
	for symbol, bars := range series {
		available := barsAvailableAsOf(bars, at)
		state := &symbolState{series: bars, lastIndex: len(available) - 1}
		if len(available) > 0 {
			state.lastPrice = available[len(available)-1].Close
		}
		result[symbol] = state
	}
	return result
}

// computeComparableMetrics keeps turnover, fees, and ledger reconciliation on
// exact decimal/rational paths. Returns and dispersion are analytical ratios
// over the repository's float64 OHLCV adapter; every undefined/non-finite ratio
// is represented as unavailable rather than serialized as zero, NaN, or Inf.
func computeComparableMetrics(config BacktestConfig, ledger *backtestMemoryLedger, equity []EquityPoint, marks map[string]float64, series map[string][]services.OHLCV) ComparableMetrics {
	ending := portfolioEquity(ledger, marks)
	metrics := ComparableMetrics{SchemaVersion: EvaluationSchemaVersion, StartingCapital: decimalString(config.InitialBalance), EndingEquity: decimalString(ending), TotalReturn: availableMetric(ending/config.InitialBalance - 1), MaxDrawdown: availableMetric(maxDrawdown(equity)), Turnover: exactTurnover(ledger.events), TotalCosts: exactFees(ledger.events), TradeCount: len(ledger.trades), FillCount: len(ledger.events)}
	metrics.TurnoverRatio = availableMetric(ratFloat(metrics.Turnover) / config.InitialBalance)
	returns := equityReturns(equity)
	if len(returns) >= 2 {
		mean, std := meanStd(returns)
		if std > 0 {
			metrics.Sharpe = availableMetric(mean / std * math.Sqrt(barsPerYear(config.TimeframeMinutes, config.AtrAnnualizationDays)))
		} else {
			metrics.Sharpe = unavailableMetric("zero return variance")
		}
		downside := []float64{}
		for _, value := range returns {
			if value < 0 {
				downside = append(downside, value)
			}
		}
		_, downsideStd := meanStd(downside)
		if len(downside) >= 2 && downsideStd > 0 {
			metrics.Sortino = availableMetric(mean / downsideStd * math.Sqrt(barsPerYear(config.TimeframeMinutes, config.AtrAnnualizationDays)))
		} else {
			metrics.Sortino = unavailableMetric("fewer than two non-zero downside samples")
		}
	} else {
		metrics.Sharpe = unavailableMetric("fewer than two return samples")
		metrics.Sortino = unavailableMetric("fewer than two return samples")
	}
	grossProfit, grossLoss, pnl := 0.0, 0.0, 0.0
	for _, trade := range ledger.trades {
		pnl += trade.Pnl
		if trade.Pnl > 0 {
			grossProfit += trade.Pnl
		} else if trade.Pnl < 0 {
			grossLoss -= trade.Pnl
		}
	}
	if grossLoss > 0 {
		metrics.ProfitFactor = availableMetric(grossProfit / grossLoss)
	} else {
		metrics.ProfitFactor = unavailableMetric("no losing closed trades")
	}
	if len(ledger.trades) > 0 {
		metrics.Expectancy = availableMetric(pnl / float64(len(ledger.trades)))
	} else {
		metrics.Expectancy = unavailableMetric("no closed trades")
	}
	grossSamples, exposureSamples := []float64{}, 0
	maxGross := 0.0
	quantities := map[string]float64{}
	eventIndex := 0
	for _, point := range equity {
		for eventIndex < len(ledger.events) && !ledger.events[eventIndex].At.After(point.Time) {
			event := ledger.events[eventIndex]
			quantity := ratFloat(event.Quantity)
			if event.Side == string(tradingcore.Buy) {
				quantities[event.Symbol] += quantity
			} else {
				quantities[event.Symbol] -= quantity
			}
			eventIndex++
		}
		pointMarks := marksAsOf(series, point.Time)
		gross := 0.0
		for symbol, quantity := range quantities {
			gross += math.Abs(quantity * pointMarks[symbol])
		}
		if point.Value > 0 {
			gross /= point.Value
		}
		grossSamples = append(grossSamples, gross)
		if gross > 0 {
			exposureSamples++
		}
		if gross > maxGross {
			maxGross = gross
		}
	}
	metrics.AverageGrossExposure = availableMetric(meanValue(grossSamples))
	metrics.MaximumGrossExposure = availableMetric(maxGross)
	metrics.AverageNetExposure = metrics.AverageGrossExposure
	if len(grossSamples) > 0 {
		metrics.ExposureTime = availableMetric(float64(exposureSamples) / float64(len(grossSamples)))
	} else {
		metrics.ExposureTime = unavailableMetric("no valuation samples")
	}
	metrics.BySymbol = fillConcentration(ledger.events, func(event backtestLedgerEvent) string { return event.Symbol })
	metrics.ByPeriod = fillConcentration(ledger.events, func(event backtestLedgerEvent) string { return event.At.UTC().Format("2006-01") })
	metrics.ByRegime = tradeConcentration(ledger.trades)
	metrics.Reconciled = math.Abs(ending-equity[len(equity)-1].Value) <= 1e-8 && !math.IsNaN(ending) && !math.IsInf(ending, 0) && reconcileStage05Ledger(config.InitialBalance, ledger)
	return metrics
}

func reconcileStage05Ledger(initial float64, ledger *backtestMemoryLedger) bool {
	cash, ok := new(big.Rat).SetString(decimalString(stage05EconomicFloat(initial)))
	if !ok {
		return false
	}
	quantities := map[string]*big.Rat{}
	for _, event := range ledger.events {
		quantity, quantityOK := new(big.Rat).SetString(event.Quantity)
		price, priceOK := new(big.Rat).SetString(event.Price)
		fee, feeOK := new(big.Rat).SetString(event.Fee)
		if !quantityOK || !priceOK || !feeOK || quantity.Sign() <= 0 || price.Sign() <= 0 || fee.Sign() < 0 {
			return false
		}
		notional := new(big.Rat).Mul(quantity, price)
		if quantities[event.Symbol] == nil {
			quantities[event.Symbol] = new(big.Rat)
		}
		if event.Side == string(tradingcore.Buy) {
			cash.Sub(cash, new(big.Rat).Add(notional, fee))
			quantities[event.Symbol].Add(quantities[event.Symbol], quantity)
		} else if event.Side == string(tradingcore.Sell) {
			cash.Add(cash, new(big.Rat).Sub(notional, fee))
			quantities[event.Symbol].Sub(quantities[event.Symbol], quantity)
		} else {
			return false
		}
	}
	if math.Abs(ratToFloat(cash)-ledger.cash) > 1e-8 {
		return false
	}
	for symbol, quantity := range quantities {
		held := 0.0
		if position := ledger.positions[symbol]; position != nil {
			held = position.Size
		}
		if math.Abs(ratToFloat(quantity)-held) > 1e-10 {
			return false
		}
	}
	return true
}

func ratToFloat(value *big.Rat) float64 { result, _ := value.Float64(); return result }

func equityReturns(equity []EquityPoint) []float64 {
	result := []float64{}
	for i := 1; i < len(equity); i++ {
		if equity[i-1].Value > 0 {
			value := equity[i].Value/equity[i-1].Value - 1
			if !math.IsNaN(value) && !math.IsInf(value, 0) {
				result = append(result, value)
			}
		}
	}
	return result
}

func availableMetric(value float64) OptionalMetric {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return unavailableMetric("non-finite analytical value")
	}
	return OptionalMetric{Available: true, Value: value}
}

func unavailableMetric(reason string) OptionalMetric { return OptionalMetric{Reason: reason} }

func exactFees(events []backtestLedgerEvent) string {
	total := new(big.Rat)
	for _, event := range events {
		if value, ok := new(big.Rat).SetString(event.Fee); ok {
			total.Add(total, value)
		}
	}
	return ratDecimal(total, 18)
}

func exactTurnover(events []backtestLedgerEvent) string {
	total := new(big.Rat)
	for _, event := range events {
		quantity, qOK := new(big.Rat).SetString(event.Quantity)
		price, pOK := new(big.Rat).SetString(event.Price)
		if qOK && pOK {
			total.Add(total, new(big.Rat).Mul(quantity, price))
		}
	}
	return ratDecimal(total, 18)
}

func ratDecimal(value *big.Rat, precision int) string {
	result := value.FloatString(precision)
	result = strings.TrimRight(strings.TrimRight(result, "0"), ".")
	if result == "" || result == "-0" {
		return "0"
	}
	return result
}

func ratFloat(value string) float64 { parsed, _ := strconv.ParseFloat(value, 64); return parsed }

func stage05EconomicFloat(value float64) float64 {
	// Stage 02 exact decimals permit 18 fractional digits. Twelve decimal places
	// are sufficient for the existing OHLCV float adapter while preventing a
	// binary float expansion from crossing that domain boundary.
	parsed, _ := strconv.ParseFloat(strconv.FormatFloat(value, 'f', 12, 64), 64)
	return parsed
}

func stage05RiskState(ledger *backtestMemoryLedger, marks map[string]float64) tradingcore.RiskState {
	gross := 0.0
	for symbol, position := range ledger.positions {
		mark := marks[symbol]
		if mark <= 0 {
			mark = position.EntryPrice
		}
		gross += math.Abs(position.Size * mark)
	}
	state, _ := tradingcore.NewRiskStateWithTurnover(mustAmount(stage05EconomicFloat(gross)), mustAmount(0), mustAmount(0), mustAmount(0), mustAmount(stage05EconomicFloat(ledger.turnover)))
	return state
}

func fillConcentration(events []backtestLedgerEvent, key func(backtestLedgerEvent) string) []ConcentrationMetric {
	totals := map[string]float64{}
	sum := 0.0
	for _, event := range events {
		value := ratFloat(event.Quantity) * ratFloat(event.Price)
		totals[key(event)] += value
		sum += value
	}
	result := make([]ConcentrationMetric, 0, len(totals))
	for name, value := range totals {
		fraction := 0.0
		if sum > 0 {
			fraction = value / sum
		}
		result = append(result, ConcentrationMetric{Key: name, Fraction: fraction})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func tradeConcentration(trades []Trade) []ConcentrationMetric {
	totals := map[string]float64{}
	sum := 0.0
	for _, trade := range trades {
		key := trade.RegimeState
		if key == "" {
			key = "unknown"
		}
		value := math.Abs(trade.EntryPrice * trade.Size)
		totals[key] += value
		sum += value
	}
	result := []ConcentrationMetric{}
	for key, value := range totals {
		fraction := 0.0
		if sum > 0 {
			fraction = value / sum
		}
		result = append(result, ConcentrationMetric{Key: key, Fraction: fraction})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

func buildStage05Comparison(config BacktestConfig, request Stage05RunRequest, candidate SelectedStrategy, results map[string]Stage05StrategyResult) ComparisonArtifact {
	cashReturn := results[StrategyCashID].Metrics.TotalReturn
	marketReturn := results[StrategyBenchmarkHoldID].Metrics.TotalReturn
	marketMetrics := results[StrategyBenchmarkHoldID].Metrics
	rows := []ComparisonRow{}
	ids := make([]string, 0, len(results))
	for id := range results {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		result := results[id]
		row := ComparisonRow{StrategyID: id, StrategyVersion: result.Manifest.Strategy.Descriptor.Version, Descriptor: cloneStrategyDescriptor(result.Manifest.Strategy.Descriptor), Parameters: cloneStringMap(result.Manifest.Strategy.Parameters), ManifestIdentity: result.Manifest.DatasetManifestID, Baseline: result.Manifest.Strategy.Descriptor.Baseline, Metrics: result.Metrics, Reasons: []string{}}
		if result.Metrics.TotalReturn.Available && cashReturn.Available {
			row.ExcessVsCash = availableMetric(result.Metrics.TotalReturn.Value - cashReturn.Value)
		} else {
			row.ExcessVsCash = unavailableMetric("cash-relative return unavailable")
		}
		if result.Metrics.TotalReturn.Available && marketReturn.Available {
			row.ExcessVsMarket = availableMetric(result.Metrics.TotalReturn.Value - marketReturn.Value)
		} else {
			row.ExcessVsMarket = unavailableMetric("market-relative return unavailable")
		}
		row.MetricDeltas = map[string]float64{}
		if row.ExcessVsCash.Available {
			row.MetricDeltas["return_vs_cash"] = row.ExcessVsCash.Value
		}
		if row.ExcessVsMarket.Available {
			row.MetricDeltas["return_vs_market"] = row.ExcessVsMarket.Value
		}
		if result.Metrics.AverageGrossExposure.Available && marketMetrics.AverageGrossExposure.Available {
			delta := result.Metrics.AverageGrossExposure.Value - marketMetrics.AverageGrossExposure.Value
			row.MetricDeltas["average_gross_exposure_vs_market"] = delta
			if math.Abs(delta) > 1e-8 {
				row.Reasons = append(row.Reasons, fmt.Sprintf("average_gross_exposure_difference=%+.8f", delta))
			}
		}
		row.MetricDeltas["turnover_vs_market"] = ratFloat(result.Metrics.Turnover) - ratFloat(marketMetrics.Turnover)
		row.MetricDeltas["costs_vs_market"] = ratFloat(result.Metrics.TotalCosts) - ratFloat(marketMetrics.TotalCosts)
		row.Pass = result.Metrics.Reconciled
		if !row.Pass {
			row.Reasons = append(row.Reasons, "ledger_not_reconciled")
		}
		rows = append(rows, row)
	}
	reasons := []string{}
	candidateResult, ok := results[candidate.Descriptor.ID]
	if !ok || !candidateResult.Metrics.Reconciled {
		reasons = append(reasons, "candidate_reconciled_metrics_missing")
	}
	if ok && (!candidateResult.Metrics.MaxDrawdown.Available || !candidateResult.Metrics.AverageGrossExposure.Available || !candidateResult.Metrics.ExposureTime.Available) {
		reasons = append(reasons, "candidate_risk_and_exposure_metric_evidence_missing")
	}
	bestBaselineID := StrategyBenchmarkHoldID
	bestBaselineReturn := marketReturn
	for id, result := range results {
		if id == StrategyCashID || id == candidate.Descriptor.ID || !result.Manifest.Strategy.Descriptor.Baseline || !result.Metrics.TotalReturn.Available {
			continue
		}
		if !bestBaselineReturn.Available || result.Metrics.TotalReturn.Value > bestBaselineReturn.Value {
			bestBaselineID, bestBaselineReturn = id, result.Metrics.TotalReturn
		}
	}
	if !cashReturn.Available || !marketReturn.Available || !ok || !candidateResult.Metrics.TotalReturn.Available {
		reasons = append(reasons, "baseline_relative_metric_evidence_missing")
	} else {
		if candidateResult.Metrics.TotalReturn.Value <= cashReturn.Value {
			reasons = append(reasons, "candidate_does_not_beat_cash_after_costs")
		}
		if !bestBaselineReturn.Available || candidateResult.Metrics.TotalReturn.Value <= bestBaselineReturn.Value {
			reasons = append(reasons, "candidate_does_not_beat_relevant_baseline_after_costs="+bestBaselineID)
		}
	}
	if candidate.Descriptor.Baseline || candidate.Descriptor.LegacyCompatibility {
		reasons = append(reasons, "baseline_or_legacy_compatibility_is_not_promotable_edge")
	}
	allowed := len(reasons) == 0
	for i := range rows {
		if rows[i].StrategyID == candidate.Descriptor.ID {
			rows[i].Pass = allowed
			rows[i].Reasons = append(rows[i].Reasons, reasons...)
		}
	}
	limitations := append([]string(nil), config.DatasetLimitations...)
	limitations = append(limitations, "positive historical performance requires complete external Stage 04 vendor coverage", "generalization requires Stage 07 multi-window validation", "live inference is not supported by a single backtest", "OHLCV full-fill evidence cannot prove order-book impact")
	sort.Strings(limitations)
	return ComparisonArtifact{SchemaVersion: ComparisonSchemaVersion, ManifestID: config.DatasetManifestID, Candidate: candidate.Descriptor.ID + "@" + candidate.Descriptor.Version, Assumptions: NormalizedAssumptions{StartingCapital: decimalString(config.InitialBalance), MaxGrossExposure: request.TargetGrossExposure, MaxNetExposure: request.MaxNetExposure, DatasetManifestID: config.DatasetManifestID, UniverseMode: config.UniverseMode, DecisionCadence: config.Timeframe, ExecutionPolicy: config.ExecutionPolicy, FeeBPS: config.FeeBps, SlippageBPS: config.SlippageBps, FinalPolicy: request.FinalPolicy}, Rows: rows, Governance: GovernanceGate{SchemaVersion: GovernanceSchemaVersion, OptimizationAllowed: allowed, PromotionAllowed: allowed, Reasons: reasons}, Results: results, Limitations: limitations}
}

func MarshalComparisonArtifact(value ComparisonArtifact) ([]byte, error) {
	if value.SchemaVersion != ComparisonSchemaVersion || value.Governance.SchemaVersion != GovernanceSchemaVersion || len(value.Rows) == 0 || len(value.Rows) > 16 {
		return nil, fmt.Errorf("invalid or unbounded comparison artifact")
	}
	return json.Marshal(value)
}

func UnmarshalComparisonArtifact(data []byte) (ComparisonArtifact, error) {
	if len(data) > 2<<20 {
		return ComparisonArtifact{}, fmt.Errorf("comparison artifact exceeds 2 MiB inspection limit")
	}
	var value ComparisonArtifact
	if err := json.Unmarshal(data, &value); err != nil {
		return ComparisonArtifact{}, err
	}
	if value.SchemaVersion != ComparisonSchemaVersion || value.Governance.SchemaVersion != GovernanceSchemaVersion || len(value.Rows) == 0 || len(value.Rows) > 16 {
		return ComparisonArtifact{}, fmt.Errorf("unsupported or unbounded comparison artifact")
	}
	return value, nil
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cloneOHLCVSeries(values map[string][]services.OHLCV) map[string][]services.OHLCV {
	result := make(map[string][]services.OHLCV, len(values))
	for symbol, bars := range values {
		result[symbol] = append([]services.OHLCV(nil), bars...)
	}
	return result
}
func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
