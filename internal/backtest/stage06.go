package backtest

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/tradingcore"
)

const FactorTraceSchemaVersion = "trend-momentum-factor-trace-v1"

type FactorTrace struct {
	SchemaVersion       string                  `json:"schema_version"`
	DecisionAt          string                  `json:"decision_at"`
	ObservedAt          string                  `json:"observed_at"` // compatibility alias for AssetObservedAt
	AssetObservedAt     string                  `json:"asset_observed_at"`
	BenchmarkObservedAt string                  `json:"benchmark_observed_at"`
	StrategyVersion     string                  `json:"strategy_version"`
	Ablation            string                  `json:"ablation"`
	Symbol              string                  `json:"symbol"`
	AssetID             string                  `json:"asset_id"`
	ExchangeSymbolID    string                  `json:"exchange_symbol_id"`
	LookbackReturns     map[string]float64      `json:"lookback_returns"`
	CompositeMomentum   float64                 `json:"composite_momentum"`
	RealizedVolatility  float64                 `json:"realized_volatility"`
	VolatilityFloor     float64                 `json:"volatility_floor"`
	NormalizedMomentum  float64                 `json:"normalized_momentum"`
	AbsoluteTrend       bool                    `json:"absolute_trend"`
	AbsoluteTrendPrice  float64                 `json:"absolute_trend_price"`
	AbsoluteTrendMean   float64                 `json:"absolute_trend_mean"`
	RelativeRank        int                     `json:"relative_rank"`
	Eligible            bool                    `json:"eligible"`
	Selected            bool                    `json:"selected"`
	Regime              string                  `json:"regime"`
	TargetWeight        float64                 `json:"target_weight"`
	Reason              string                  `json:"reason"`
	ModelObservation    float64                 `json:"model_observation"`
	Components          StrategyComponentMatrix `json:"components"`
}

type StrategyComponentMatrix struct {
	BenchmarkRegime    bool `json:"benchmark_regime"`
	RelativeRanking    bool `json:"relative_ranking"`
	AssetAbsoluteTrend bool `json:"asset_absolute_trend"`
	VolatilityRanking  bool `json:"volatility_ranking"`
	VolatilitySizing   bool `json:"volatility_sizing"`
}

var stage06Components = map[string]StrategyComponentMatrix{
	"absolute_trend_only":    {AssetAbsoluteTrend: true},
	"relative_momentum_only": {RelativeRanking: true},
	"combined":               {BenchmarkRegime: true, RelativeRanking: true, AssetAbsoluteTrend: true},
}

func effectiveStage06Warmup(parameters map[string]string) int {
	lookback, _ := strconv.Atoi(parameters["lookback_bars"])
	trend, _ := strconv.Atoi(parameters["trend_bars"])
	regime, _ := strconv.Atoi(parameters["regime_bars"])
	return maxInt(lookback+1, maxInt(trend, regime)) * 16
}

type RegimeObservation struct {
	SchemaVersion string  `json:"schema_version"`
	DecisionAt    string  `json:"decision_at"`
	ObservedAt    string  `json:"observed_at"`
	State         string  `json:"state"`
	Price         float64 `json:"price"`
	LongMean      float64 `json:"long_mean"`
	Threshold     float64 `json:"threshold"`
	TargetGross   float64 `json:"target_gross"`
	TargetNet     float64 `json:"target_net"`
	Reason        string  `json:"reason"`
}

type ExitReasonTrace struct {
	Primary           string   `json:"primary"`
	Concurrent        []string `json:"concurrent"`
	DecisionAt        string   `json:"decision_at,omitempty"`
	RequestedQuantity string   `json:"requested_quantity,omitempty"`
	ApprovedQuantity  string   `json:"approved_quantity,omitempty"`
	FilledQuantity    string   `json:"filled_quantity,omitempty"`
	ResultingExposure string   `json:"resulting_exposure,omitempty"`
}

type StrategyTraceDiagnostic struct {
	Code    StrategyDiagnosticCode `json:"code"`
	Symbol  string                 `json:"symbol,omitempty"`
	Details string                 `json:"details"`
}

type SensitivityRow struct {
	SchemaVersion string            `json:"schema_version"`
	ID            string            `json:"id"`
	Ablation      string            `json:"ablation"`
	VolNormalized bool              `json:"vol_normalized"`
	LookbackBars  int               `json:"lookback_bars"`
	Rebalance     string            `json:"rebalance"`
	Parameters    map[string]string `json:"parameters"`
	RiskPolicy    map[string]string `json:"risk_policy"`
	Turnover      string            `json:"turnover"`
	TotalCosts    string            `json:"total_costs"`
	FeeCosts      string            `json:"fee_costs"`
	SlippageCosts string            `json:"slippage_costs"`
	Metrics       ComparableMetrics `json:"metrics"`
	FeeBPS        float64           `json:"fee_bps"`
	SlippageBPS   float64           `json:"slippage_bps"`
	Digest        string            `json:"digest"`
}

type Stage06OrderSemantic struct {
	Symbol        string `json:"symbol"`
	Side          string `json:"side"`
	Quantity      string `json:"quantity"`
	Reason        string `json:"reason"`
	PolicyVersion string `json:"policy_version"`
	ExecutionMode string `json:"execution_mode"`
	DecisionAt    string `json:"decision_at"`
}

type Stage06ParityEvidence struct {
	SchemaVersion               string                         `json:"schema_version"`
	BacktestApproved            []Stage06OrderSemantic         `json:"backtest_approved"`
	PaperShadowApproved         []Stage06OrderSemantic         `json:"paper_shadow_approved"`
	LiveDryRunRequests          []tradingcore.LiveOrderRequest `json:"live_dry_run_requests"`
	LiveFenceCodes              []string                       `json:"live_fence_codes"`
	PaperShadowFenceCodes       []string                       `json:"paper_shadow_fence_codes"`
	ExternalSubmissionPerformed bool                           `json:"external_submission_performed"`
}

type Stage06CandidateEvidence struct {
	SchemaVersion string                     `json:"schema_version"`
	FactorTraces  []FactorTrace              `json:"factor_traces"`
	Regimes       []RegimeObservation        `json:"regimes"`
	ExitReasons   map[string]ExitReasonTrace `json:"exit_reasons"`
	Diagnostics   []StrategyTraceDiagnostic  `json:"diagnostics"`
	Sensitivity   []SensitivityRow           `json:"sensitivity"`
	Parity        Stage06ParityEvidence      `json:"parity"`
}

func buildStage06ParityEvidence(ledger *backtestMemoryLedger) (Stage06ParityEvidence, error) {
	evidence := Stage06ParityEvidence{SchemaVersion: "trend-momentum-parity-v2", BacktestApproved: []Stage06OrderSemantic{}, PaperShadowApproved: []Stage06OrderSemantic{}, LiveDryRunRequests: []tradingcore.LiveOrderRequest{}, LiveFenceCodes: []string{}, PaperShadowFenceCodes: []string{}}
	live := tradingcore.LiveBroker{}
	for _, record := range ledger.runRecords {
		if record.Strategy == nil {
			continue
		}
		approved := record.Result.Risk.Approved()
		evidence.BacktestApproved = append(evidence.BacktestApproved, stage06Semantics(approved)...)

		shadowContext, err := buildStage06PaperShadowContext(record.Snapshot)
		if err != nil {
			return Stage06ParityEvidence{}, err
		}
		shadowDecision, err := record.Strategy.Decide(context.Background(), shadowContext)
		if err != nil {
			return Stage06ParityEvidence{}, err
		}
		shadowFenced, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), shadowDecision.Intents(), shadowContext.Portfolio(), record.Policy)
		if err != nil {
			return Stage06ParityEvidence{}, err
		}
		for _, rejection := range shadowFenced.Rejected() {
			evidence.PaperShadowFenceCodes = append(evidence.PaperShadowFenceCodes, string(rejection.Code))
		}
		shadowPreview, err := stage06RiskPreview(shadowDecision.Intents(), shadowContext.Portfolio(), record.Policy)
		if err != nil {
			return Stage06ParityEvidence{}, err
		}
		shadowSemantic := stage06SemanticsWithMode(shadowPreview.Approved(), tradingcore.ExecutionShadow)
		if err := validateStage06AdapterParity(evidence.BacktestApproved[len(evidence.BacktestApproved)-len(shadowSemantic):], shadowSemantic, tradingcore.ExecutionShadow); err != nil {
			return Stage06ParityEvidence{}, err
		}
		evidence.PaperShadowApproved = append(evidence.PaperShadowApproved, shadowSemantic...)

		liveContext, err := buildStage06LiveDryRunContext(record.Snapshot)
		if err != nil {
			return Stage06ParityEvidence{}, err
		}
		liveDecision, err := record.Strategy.Decide(context.Background(), liveContext)
		if err != nil {
			return Stage06ParityEvidence{}, err
		}
		liveFenced, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), liveDecision.Intents(), liveContext.Portfolio(), record.Policy)
		if err != nil {
			return Stage06ParityEvidence{}, err
		}
		for _, rejection := range liveFenced.Rejected() {
			evidence.LiveFenceCodes = append(evidence.LiveFenceCodes, string(rejection.Code))
		}
		livePreview, err := stage06RiskPreview(liveDecision.Intents(), liveContext.Portfolio(), record.Policy)
		if err != nil {
			return Stage06ParityEvidence{}, err
		}
		liveSemantic := stage06SemanticsWithMode(livePreview.Approved(), tradingcore.ExecutionLiveDryRun)
		if err := validateStage06AdapterParity(stage06Semantics(approved), liveSemantic, tradingcore.ExecutionLiveDryRun); err != nil {
			return Stage06ParityEvidence{}, err
		}
		requests, err := (stage06LiveDryRunAdapter{builder: live}).Build(livePreview.Approved())
		if err != nil {
			return Stage06ParityEvidence{}, err
		}
		evidence.LiveDryRunRequests = append(evidence.LiveDryRunRequests, requests...)
	}
	const parityLimit = 512
	if len(evidence.BacktestApproved) > parityLimit {
		evidence.BacktestApproved = evidence.BacktestApproved[len(evidence.BacktestApproved)-parityLimit:]
	}
	if len(evidence.PaperShadowApproved) > parityLimit {
		evidence.PaperShadowApproved = evidence.PaperShadowApproved[len(evidence.PaperShadowApproved)-parityLimit:]
	}
	if len(evidence.LiveDryRunRequests) > parityLimit {
		evidence.LiveDryRunRequests = evidence.LiveDryRunRequests[len(evidence.LiveDryRunRequests)-parityLimit:]
	}
	if len(evidence.LiveFenceCodes) > parityLimit {
		evidence.LiveFenceCodes = evidence.LiveFenceCodes[len(evidence.LiveFenceCodes)-parityLimit:]
	}
	if len(evidence.PaperShadowFenceCodes) > parityLimit {
		evidence.PaperShadowFenceCodes = evidence.PaperShadowFenceCodes[len(evidence.PaperShadowFenceCodes)-parityLimit:]
	}
	return evidence, nil
}

type stage06LiveRequestBuilder interface {
	BuildRequests(tradingcore.DecisionBatch) ([]tradingcore.LiveOrderRequest, error)
}

type stage06LiveDryRunAdapter struct{ builder stage06LiveRequestBuilder }

func (adapter stage06LiveDryRunAdapter) Build(batch tradingcore.DecisionBatch) ([]tradingcore.LiveOrderRequest, error) {
	return adapter.builder.BuildRequests(batch)
}

func buildStage06PaperShadowContext(base tradingcore.DecisionContext) (tradingcore.DecisionContext, error) {
	portfolio := base.Portfolio()
	cloned, err := tradingcore.NewPortfolioSnapshot(portfolio.AsOf(), portfolio.AccountID(), tradingcore.ExecutionShadow, portfolio.Cash(), portfolio.Positions(), portfolio.PendingOrders(), portfolio.RiskState())
	if err != nil {
		return tradingcore.DecisionContext{}, err
	}
	return tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{MarketObservedAt: base.MarketObservedAt(), SignalAt: base.SignalAt(), DecisionAt: base.DecisionAt(), Quotes: base.Quotes(), Universe: base.Universe(), Portfolio: cloned, Settings: base.Settings(), Versions: base.Versions()})
}

func buildStage06LiveDryRunContext(base tradingcore.DecisionContext) (tradingcore.DecisionContext, error) {
	portfolio := base.Portfolio()
	cloned, err := tradingcore.NewPortfolioSnapshot(portfolio.AsOf(), portfolio.AccountID(), tradingcore.ExecutionLiveDryRun, portfolio.Cash(), portfolio.Positions(), portfolio.PendingOrders(), portfolio.RiskState())
	if err != nil {
		return tradingcore.DecisionContext{}, err
	}
	return tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{MarketObservedAt: base.MarketObservedAt(), SignalAt: base.SignalAt(), DecisionAt: base.DecisionAt(), Quotes: base.Quotes(), Universe: base.Universe(), Portfolio: cloned, Settings: base.Settings(), Versions: base.Versions()})
}

func rebuildStage06AdapterContext(base tradingcore.DecisionContext, mode tradingcore.ExecutionMode) (tradingcore.DecisionContext, error) {
	portfolio := base.Portfolio()
	cloned, err := tradingcore.NewPortfolioSnapshot(portfolio.AsOf(), portfolio.AccountID(), mode, portfolio.Cash(), portfolio.Positions(), portfolio.PendingOrders(), portfolio.RiskState())
	if err != nil {
		return tradingcore.DecisionContext{}, err
	}
	return tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{MarketObservedAt: base.MarketObservedAt(), SignalAt: base.SignalAt(), DecisionAt: base.DecisionAt(), Quotes: base.Quotes(), Universe: base.Universe(), Portfolio: cloned, Settings: base.Settings(), Versions: base.Versions()})
}

// stage06RiskPreview computes deterministic quantities without granting the
// non-capital adapter authority. The real shadow/dry-run intents are evaluated
// first and rejected; only cloned backtest-mode intents enter this preview.
func stage06RiskPreview(batch tradingcore.DecisionBatch, portfolio tradingcore.PortfolioSnapshot, policy tradingcore.RiskPolicy) (tradingcore.RiskDecision, error) {
	intents := batch.Intents()
	for i := range intents {
		intents[i].ExecutionMode = tradingcore.ExecutionBacktest
	}
	previewBatch, err := tradingcore.NewDecisionBatch(intents)
	if err != nil {
		return tradingcore.RiskDecision{}, err
	}
	return (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), previewBatch, portfolio, policy)
}

func equalStage06EconomicSemantics(left, right []Stage06OrderSemantic) bool {
	strip := func(values []Stage06OrderSemantic) []Stage06OrderSemantic {
		result := append([]Stage06OrderSemantic(nil), values...)
		for i := range result {
			result[i].ExecutionMode = ""
		}
		return result
	}
	return reflect.DeepEqual(strip(left), strip(right))
}

func validateStage06AdapterParity(backtest, preview []Stage06OrderSemantic, expectedMode tradingcore.ExecutionMode) error {
	for _, semantic := range preview {
		if semantic.ExecutionMode != string(expectedMode) {
			return fmt.Errorf("stage06 adapter mode mismatch: got %s want %s", semantic.ExecutionMode, expectedMode)
		}
	}
	if !equalStage06EconomicSemantics(backtest, preview) {
		return fmt.Errorf("stage06 adapter economic contract mismatch")
	}
	return nil
}

func stage06Semantics(batch tradingcore.DecisionBatch) []Stage06OrderSemantic {
	result := make([]Stage06OrderSemantic, 0, len(batch.Intents()))
	for _, intent := range batch.Intents() {
		result = append(result, Stage06OrderSemantic{Symbol: intent.Instrument.VenueSymbol, Side: string(intent.Side), Quantity: intent.Quantity.Decimal().String(), Reason: intent.Reason, PolicyVersion: intent.Versions.Policy, ExecutionMode: string(intent.ExecutionMode), DecisionAt: canonicalTime(intent.DecisionAt)})
	}
	return result
}

func stage06SemanticsWithMode(batch tradingcore.DecisionBatch, mode tradingcore.ExecutionMode) []Stage06OrderSemantic {
	result := stage06Semantics(batch)
	for i := range result {
		result[i].ExecutionMode = string(mode)
	}
	return result
}

func boundedFactors(values []FactorTrace, limit int) []FactorTrace {
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	return append([]FactorTrace(nil), values...)
}
func boundedRegimes(values []RegimeObservation, limit int) []RegimeObservation {
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	return append([]RegimeObservation(nil), values...)
}
func boundedDiagnostics(values []StrategyTraceDiagnostic, limit int) []StrategyTraceDiagnostic {
	if len(values) > limit {
		values = values[len(values)-limit:]
	}
	return append([]StrategyTraceDiagnostic(nil), values...)
}
func boundedExitReasons(values map[string]ExitReasonTrace, limit int) map[string]ExitReasonTrace {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[len(keys)-limit:]
	}
	result := map[string]ExitReasonTrace{}
	for _, key := range keys {
		result[key] = values[key]
	}
	return result
}

type trendMomentumPlanner struct{}

type candidateScore struct {
	symbol, assetID, symbolID string
	momentum, vol, normalized float64
	trend, eligible           bool
	last, trendMean           float64
	reason                    string
}

func (trendMomentumPlanner) Plan(context Stage05PlanningContext) (Stage05Plan, error) {
	p := context.Selected.Parameters
	intent := p["execution_intent"]
	if intent == "paper_capital" || intent == "live_submit" || intent == "promotion" {
		return Stage05Plan{}, &StrategyDiagnosticError{Code: DiagnosticExecutionFenced, Strategy: StrategyTrendMomentumCandidate, Field: "execution_intent", Details: intent + " is unavailable before Stage 07 validation and human promotion"}
	}
	rebalance, _ := time.ParseDuration(p["rebalance"])
	// A 4h decision is emitted only after the full UTC-aligned 4h bucket is complete.
	lastDecisionOpen := time.UnixMilli(context.Reference[len(context.Reference)-1].OpenTime).UTC()
	if lastDecisionOpen.Minute() != 45 || lastDecisionOpen.Hour()%4 != 3 {
		return Stage05Plan{Targets: append([]string(nil), context.LastTargets...), Decide: false}, nil
	}
	if !context.LastRebalance.IsZero() && context.At.Sub(context.LastRebalance) < rebalance {
		stop, _ := strconv.ParseFloat(p["hard_stop"], 64)
		stopped := map[string]bool{}
		exits := map[string]ExitReasonTrace{}
		factors := []FactorTrace{}
		for symbol := range context.Positions {
			entry, mark := context.PositionEntries[symbol], context.Marks[symbol]
			if stop > 0 && entry > 0 && mark > 0 && mark <= entry*(1-stop) {
				stopped[symbol] = true
				exits[symbol] = ExitReasonTrace{Primary: "risk_stop"}
				factors = append(factors, FactorTrace{SchemaVersion: FactorTraceSchemaVersion, DecisionAt: canonicalTime(context.At), ObservedAt: canonicalTime(context.At), AssetObservedAt: canonicalTime(context.At), BenchmarkObservedAt: canonicalTime(context.At), StrategyVersion: "1.0.0", Ablation: p["variant"], Symbol: symbol, Eligible: true, Selected: false, Reason: "risk_stop", AbsoluteTrendPrice: mark, AbsoluteTrendMean: entry * (1 - stop), ModelObservation: parseFloatDefault(p["model_observation"]), Components: stage06Components[p["variant"]]})
			}
		}
		if len(stopped) == 0 {
			return Stage05Plan{Targets: append([]string(nil), context.LastTargets...), Decide: false}, nil
		}
		targets := []string{}
		for _, symbol := range context.LastTargets {
			if !stopped[symbol] {
				targets = append(targets, symbol)
			}
		}
		return Stage05Plan{Targets: targets, Decide: true, Factors: factors, ExitReasons: exits, RiskStopOnly: true}, nil
	}
	snapshot, ok := replayAsOf(context.Replays, context.At)
	if !ok || !snapshot.complete || context.At.Sub(snapshot.at) > rebalance {
		return Stage05Plan{}, &StrategyDiagnosticError{Code: DiagnosticUniverseCoverage, Strategy: StrategyTrendMomentumCandidate, Details: "complete active/shortlist point-in-time snapshot is missing or stale"}
	}
	if err := validateStage06Identities(snapshot.members, context.Config); err != nil {
		return Stage05Plan{}, err
	}
	eligible, err := eligibleReplaySymbolsWithPolicy(snapshot.members, ReplayMembershipPolicy{IncludeShortlist: p["include_shortlist"] == "true"})
	if err != nil {
		return Stage05Plan{}, &StrategyDiagnosticError{Code: DiagnosticUniverseCoverage, Strategy: StrategyTrendMomentumCandidate, Field: "stage", Details: err.Error()}
	}
	regimeBars, _ := strconv.Atoi(p["regime_bars"])
	benchmark := completedUTC4HCloses(context.Reference, context.At)
	if len(benchmark) < regimeBars {
		return Stage05Plan{}, &StrategyDiagnosticError{Code: DiagnosticInsufficientWarmup, Strategy: StrategyTrendMomentumCandidate, Field: "benchmark", Details: "completed benchmark regime warmup unavailable"}
	}
	benchmark = benchmark[len(benchmark)-regimeBars:]
	benchmarkMean := meanCloses(benchmark)
	benchmarkPrice := benchmark[len(benchmark)-1].Close
	band, _ := strconv.ParseFloat(p["regime_band"], 64)
	regime := "neutral"
	reason := "benchmark_inside_neutral_band"
	if benchmarkPrice > benchmarkMean*(1+band) {
		regime, reason = "risk_on", "benchmark_above_long_mean_band"
	} else if benchmarkPrice < benchmarkMean*(1-band) {
		regime, reason = "risk_off", "benchmark_below_long_mean_band"
	}
	variant := p["variant"]
	components := stage06Components[variant]
	if variant == "combined" && p["vol_normalization"] == "true" {
		components.VolatilityRanking, components.VolatilitySizing = true, true
	}
	exposureRegime := regime
	if !components.BenchmarkRegime {
		exposureRegime = "risk_on"
	}
	targetGross, _ := strconv.ParseFloat(p[exposureRegime+"_gross"], 64)
	maxGross, _ := strconv.ParseFloat(p["max_gross"], 64)
	normalizedCeiling, _ := strconv.ParseFloat(p["target_gross"], 64)
	targetGross = math.Min(targetGross, math.Min(maxGross, normalizedCeiling))
	benchmarkObservedAt := time.UnixMilli(benchmark[len(benchmark)-1].CloseTime).UTC()
	regimeTrace := RegimeObservation{SchemaVersion: FactorTraceSchemaVersion, DecisionAt: canonicalTime(context.At), ObservedAt: canonicalTime(benchmarkObservedAt), State: regime, Price: benchmarkPrice, LongMean: benchmarkMean, Threshold: band, TargetGross: targetGross, TargetNet: targetGross, Reason: reason}

	lookback, _ := strconv.Atoi(p["lookback_bars"])
	trendBars, _ := strconv.Atoi(p["trend_bars"])
	volFloor, _ := strconv.ParseFloat(p["vol_floor"], 64)
	modelObservation, _ := strconv.ParseFloat(p["model_observation"], 64)
	rows := []candidateScore{}
	diagnostics := []StrategyTraceDiagnostic{}
	needed := maxInt(lookback+1, trendBars)
	for _, symbol := range eligible {
		identity := replayIdentity(snapshot.members, symbol)
		bars := completedUTC4HCloses(context.Series[symbol], context.At)
		if len(bars) < needed {
			diagnostics = append(diagnostics, StrategyTraceDiagnostic{Code: DiagnosticInsufficientWarmup, Symbol: symbol, Details: "asset excluded: completed 4h feature warmup unavailable"})
			continue
		}
		observed := time.UnixMilli(bars[len(bars)-1].CloseTime).UTC()
		if !observed.Equal(benchmarkObservedAt) {
			diagnostics = append(diagnostics, StrategyTraceDiagnostic{Code: DiagnosticFeatureBucket, Symbol: symbol, Details: "asset excluded: latest completed 4h bucket does not equal benchmark feature bucket"})
			continue
		}
		from, last := bars[len(bars)-1-lookback].Close, bars[len(bars)-1].Close
		if from <= 0 || last <= 0 {
			diagnostics = append(diagnostics, StrategyTraceDiagnostic{Code: DiagnosticManifestIncompatible, Symbol: symbol, Details: "asset excluded: non-positive completed close"})
			continue
		}
		momentum := last/from - 1
		vol := realizedVolatility(bars[len(bars)-1-lookback:])
		normalized := momentum
		if components.VolatilityRanking {
			normalized = momentum / math.Max(vol, volFloor)
		}
		trendMean := meanCloses(bars[len(bars)-trendBars:])
		trend := last > trendMean
		rows = append(rows, candidateScore{symbol: symbol, assetID: identity.AssetID, symbolID: identity.ExchangeSymbolID, momentum: momentum, vol: vol, normalized: normalized, trend: trend, eligible: true, last: last, trendMean: trendMean})
	}
	sort.Slice(rows, func(i, j int) bool {
		if !components.RelativeRanking {
			if rows[i].assetID != rows[j].assetID {
				return rows[i].assetID < rows[j].assetID
			}
			return rows[i].symbolID < rows[j].symbolID
		}
		if rows[i].normalized != rows[j].normalized {
			return rows[i].normalized > rows[j].normalized
		}
		if rows[i].assetID != rows[j].assetID {
			return rows[i].assetID < rows[j].assetID
		}
		return rows[i].symbolID < rows[j].symbolID
	})
	topN, _ := strconv.Atoi(p["top_n"])
	maxPositions, _ := strconv.Atoi(p["max_positions"])
	topN = minInt(topN, maxPositions)
	selectedRows := []int{}
	for i := range rows {
		selectable := false
		switch variant {
		case "absolute_trend_only":
			selectable = rows[i].trend
		case "relative_momentum_only":
			selectable = true
		default:
			selectable = regime != "risk_off" && rows[i].trend
		}
		if selectable && len(selectedRows) < topN {
			selectedRows = append(selectedRows, i)
		}
	}
	weights := map[string]float64{}
	denominator := 0.0
	for _, index := range selectedRows {
		value := 1.0
		if components.VolatilitySizing {
			value = 1 / math.Max(rows[index].vol, volFloor)
		}
		weights[rows[index].symbol] = value
		denominator += value
	}
	positionCap, _ := strconv.ParseFloat(p["position_cap"], 64)
	for symbol, raw := range weights {
		weights[symbol] = math.Min(positionCap, targetGross*raw/denominator)
	}
	// Deterministically redistribute unused gross within the cap.
	for pass := 0; pass < len(weights); pass++ {
		total, open := 0.0, 0
		for _, value := range weights {
			total += value
			if value < positionCap-1e-12 {
				open++
			}
		}
		if total >= targetGross-1e-12 || open == 0 {
			break
		}
		add := (targetGross - total) / float64(open)
		for symbol, value := range weights {
			if value < positionCap-1e-12 {
				weights[symbol] = math.Min(positionCap, value+add)
			}
		}
	}
	targets := make([]string, 0, len(weights))
	for symbol := range weights {
		targets = append(targets, symbol)
	}
	sort.Slice(targets, func(i, j int) bool {
		a, b := replayIdentity(snapshot.members, targets[i]), replayIdentity(snapshot.members, targets[j])
		if a.AssetID != b.AssetID {
			return a.AssetID < b.AssetID
		}
		return a.ExchangeSymbolID < b.ExchangeSymbolID
	})
	rankings := []RankingArtifact{}
	factors := []FactorTrace{}
	for i, row := range rows {
		_, chosen := weights[row.symbol]
		rowReason := "excluded_below_rank"
		if chosen {
			rowReason = "selected"
		} else if variant != "relative_momentum_only" && !row.trend {
			rowReason = "excluded_absolute_trend"
		} else if regime == "risk_off" {
			rowReason = "excluded_regime_risk_off"
		}
		rankingScore := row.normalized
		if !components.RelativeRanking {
			rankingScore = 0
			if row.trend {
				rankingScore = 1
			}
		}
		rankings = append(rankings, RankingArtifact{DecisionAt: canonicalTime(context.At), Symbol: row.symbol, Rank: i + 1, Score: rankingScore, Selected: chosen, AssetID: row.assetID, ExchangeSymbolID: row.symbolID})
		factors = append(factors, FactorTrace{SchemaVersion: FactorTraceSchemaVersion, DecisionAt: canonicalTime(context.At), ObservedAt: canonicalTime(benchmarkObservedAt), AssetObservedAt: canonicalTime(benchmarkObservedAt), BenchmarkObservedAt: canonicalTime(benchmarkObservedAt), StrategyVersion: "1.0.0", Ablation: variant, Symbol: row.symbol, AssetID: row.assetID, ExchangeSymbolID: row.symbolID, LookbackReturns: map[string]float64{strconv.Itoa(lookback) + "x4h": row.momentum}, CompositeMomentum: row.momentum, RealizedVolatility: row.vol, VolatilityFloor: volFloor, NormalizedMomentum: row.normalized, AbsoluteTrend: row.trend, AbsoluteTrendPrice: row.last, AbsoluteTrendMean: row.trendMean, RelativeRank: i + 1, Eligible: row.eligible, Selected: chosen, Regime: regime, TargetWeight: weights[row.symbol], Reason: rowReason, ModelObservation: modelObservation, Components: components})
	}
	exits := determineCandidateExits(context, rows, weights, regime, p, components)
	return Stage05Plan{Targets: targets, Rankings: rankings, Decide: true, TargetWeights: weights, Regime: regime, RegimeObservation: &regimeTrace, Factors: factors, ExitReasons: exits, Diagnostics: diagnostics}, nil
}

func validateStage06Identities(members []ReplayMember, config BacktestConfig) error {
	assets, symbols := map[string]string{}, map[string]string{}
	for _, member := range members {
		eligible, err := replayMemberEligible(member.Stage, member.Shortlisted, member.RejectionReason != "", ReplayMembershipPolicy{IncludeShortlist: true})
		if err != nil {
			return &StrategyDiagnosticError{Code: DiagnosticUniverseIdentity, Strategy: StrategyTrendMomentumCandidate, Field: "stage", Details: err.Error()}
		}
		if !eligible {
			continue
		}
		if member.AssetID == "" || member.ExchangeSymbolID == "" {
			return &StrategyDiagnosticError{Code: DiagnosticUniverseIdentity, Strategy: StrategyTrendMomentumCandidate, Field: member.Symbol, Details: "eligible member requires stable asset_id and exchange_symbol_id"}
		}
		if prior := assets[member.AssetID]; prior != "" {
			return &StrategyDiagnosticError{Code: DiagnosticUniverseIdentity, Strategy: StrategyTrendMomentumCandidate, Field: member.AssetID, Details: "duplicate eligible asset identity: " + prior + "," + member.Symbol}
		}
		if prior := symbols[member.ExchangeSymbolID]; prior != "" {
			return &StrategyDiagnosticError{Code: DiagnosticUniverseIdentity, Strategy: StrategyTrendMomentumCandidate, Field: member.ExchangeSymbolID, Details: "duplicate eligible exchange symbol identity: " + prior + "," + member.Symbol}
		}
		assets[member.AssetID], symbols[member.ExchangeSymbolID] = member.Symbol, member.Symbol
		if expected := config.EconomicAssetIdentities[member.Symbol]; expected != "" && expected != member.AssetID {
			return &StrategyDiagnosticError{Code: DiagnosticUniverseIdentity, Strategy: StrategyTrendMomentumCandidate, Field: member.Symbol, Details: "asset lifecycle mapping disagrees with replay identity"}
		}
		if expected := config.SymbolIdentities[member.Symbol]; expected != "" && expected != member.ExchangeSymbolID {
			return &StrategyDiagnosticError{Code: DiagnosticUniverseIdentity, Strategy: StrategyTrendMomentumCandidate, Field: member.Symbol, Details: "symbol lifecycle mapping disagrees with replay identity"}
		}
	}
	return nil
}

func determineCandidateExits(context Stage05PlanningContext, rows []candidateScore, weights map[string]float64, regime string, p map[string]string, components StrategyComponentMatrix) map[string]ExitReasonTrace {
	result := map[string]ExitReasonTrace{}
	bySymbol := map[string]struct{ trend, eligible bool }{}
	for _, row := range rows {
		bySymbol[row.symbol] = struct{ trend, eligible bool }{row.trend, row.eligible}
	}
	stop, _ := strconv.ParseFloat(p["hard_stop"], 64)
	for symbol := range context.Positions {
		reasons := []string{}
		entry, mark := context.PositionEntries[symbol], context.Marks[symbol]
		if stop > 0 && entry > 0 && mark > 0 && mark <= entry*(1-stop) {
			reasons = append(reasons, "risk_stop")
		}
		if components.BenchmarkRegime && regime == "risk_off" {
			reasons = append(reasons, "regime_risk_off")
		} else if components.BenchmarkRegime && regime == "neutral" {
			reasons = append(reasons, "regime_reduction")
		}
		state, eligible := bySymbol[symbol]
		if !eligible || !state.eligible {
			reasons = append(reasons, "loss_of_eligibility")
		} else if components.AssetAbsoluteTrend && !state.trend {
			reasons = append(reasons, "loss_of_absolute_trend")
		}
		if _, selected := weights[symbol]; !selected && components.RelativeRanking {
			reasons = append(reasons, "loss_of_rank")
		} else if !selected && !components.RelativeRanking {
			reasons = append(reasons, "loss_of_selection")
		}
		if len(reasons) > 0 {
			result[symbol] = ExitReasonTrace{Primary: reasons[0], Concurrent: append([]string(nil), reasons[1:]...)}
		}
	}
	return result
}

func completedUTC4HCloses(bars []services.OHLCV, at time.Time) []services.OHLCV {
	available := barsAvailableAsOf(bars, at)
	buckets := map[time.Time]map[int]services.OHLCV{}
	for _, bar := range available {
		open := time.UnixMilli(bar.OpenTime).UTC()
		if open.Minute()%15 != 0 || open.Second() != 0 || open.Nanosecond() != 0 {
			continue
		}
		bucket := open.Truncate(4 * time.Hour)
		if buckets[bucket] == nil {
			buckets[bucket] = map[int]services.OHLCV{}
		}
		buckets[bucket][int(open.Sub(bucket)/(15*time.Minute))] = bar
	}
	keys := []time.Time{}
	for key, bucket := range buckets {
		if len(bucket) == 16 && !time.UnixMilli(bucket[15].CloseTime).After(at) {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	result := make([]services.OHLCV, 0, len(keys))
	for _, key := range keys {
		result = append(result, buckets[key][15])
	}
	return result
}

func meanCloses(values []services.OHLCV) float64 {
	total := 0.0
	for _, value := range values {
		total += value.Close
	}
	return total / float64(len(values))
}
func realizedVolatility(values []services.OHLCV) float64 {
	if len(values) < 2 {
		return 0
	}
	returns := make([]float64, 0, len(values)-1)
	for i := 1; i < len(values); i++ {
		if values[i-1].Close > 0 {
			returns = append(returns, values[i].Close/values[i-1].Close-1)
		}
	}
	if len(returns) == 0 {
		return 0
	}
	mean := 0.0
	for _, value := range returns {
		mean += value
	}
	mean /= float64(len(returns))
	variance := 0.0
	for _, value := range returns {
		difference := value - mean
		variance += difference * difference
	}
	return math.Sqrt(variance / float64(len(returns)))
}
func parseFloatDefault(raw string) float64 { value, _ := strconv.ParseFloat(raw, 64); return value }
