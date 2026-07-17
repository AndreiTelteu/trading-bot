package backtest

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/tradingcore"
)

const StrategyDescriptorSchemaVersion = "strategy-descriptor-v1"

const (
	StrategyCashID                 = "cash"
	StrategyBenchmarkHoldID        = "benchmark_buy_hold"
	StrategyBenchmarkTrendID       = "benchmark_trend"
	StrategyEqualWeightID          = "equal_weight_liquid_universe"
	StrategyMomentumID             = "cross_sectional_momentum"
	StrategyLegacyCompatibility    = "legacy_indicator_voting"
	StrategyTrendMomentumCandidate = "trend_momentum_candidate"
)

type StrategyDiagnosticCode string

const (
	DiagnosticUnknownStrategy       StrategyDiagnosticCode = "unknown_strategy"
	DiagnosticInvalidParameter      StrategyDiagnosticCode = "invalid_strategy_parameter"
	DiagnosticInvalidCombination    StrategyDiagnosticCode = "invalid_parameter_combination"
	DiagnosticInsufficientWarmup    StrategyDiagnosticCode = "insufficient_warmup"
	DiagnosticManifestRequired      StrategyDiagnosticCode = "point_in_time_manifest_required"
	DiagnosticManifestIncompatible  StrategyDiagnosticCode = "strategy_manifest_incompatible"
	DiagnosticBenchmarkRequired     StrategyDiagnosticCode = "benchmark_series_required"
	DiagnosticUniverseRequired      StrategyDiagnosticCode = "point_in_time_universe_required"
	DiagnosticUniverseCoverage      StrategyDiagnosticCode = "point_in_time_universe_incomplete"
	DiagnosticConstraintRequired    StrategyDiagnosticCode = "historical_constraints_required"
	DiagnosticMetricEvidenceMissing StrategyDiagnosticCode = "metric_evidence_missing"
	DiagnosticAllocationRejected    StrategyDiagnosticCode = "target_allocation_rejected"
	DiagnosticExecutionFenced       StrategyDiagnosticCode = "research_execution_fenced"
	DiagnosticStaleEvidence         StrategyDiagnosticCode = "stale_strategy_evidence"
	DiagnosticTurnoverBudget        StrategyDiagnosticCode = "turnover_budget_exhausted"
	DiagnosticUniverseIdentity      StrategyDiagnosticCode = "point_in_time_universe_identity_invalid"
	DiagnosticFeatureBucket         StrategyDiagnosticCode = "common_feature_bucket_missing"
	DiagnosticAchievedAllocation    StrategyDiagnosticCode = "achieved_allocation_out_of_bounds"
	DiagnosticAllocationReconciled  StrategyDiagnosticCode = "achieved_allocation_reconciled"
	DiagnosticIntentRuntime         StrategyDiagnosticCode = "execution_intent_runtime_mismatch"
)

type StrategyDiagnosticError struct {
	Code      StrategyDiagnosticCode       `json:"code"`
	Strategy  string                       `json:"strategy,omitempty"`
	Field     string                       `json:"field,omitempty"`
	Details   string                       `json:"details"`
	Execution *StrategyExecutionDiagnostic `json:"execution,omitempty"`
}

type StrategyExecutionDiagnostic struct {
	Symbol            string `json:"symbol"`
	Side              string `json:"side"`
	RequestedQuantity string `json:"requested_quantity"`
	ApprovedQuantity  string `json:"approved_quantity"`
	FilledQuantity    string `json:"filled_quantity"`
	ProviderCode      string `json:"provider_code,omitempty"`
	PolicyCode        string `json:"policy_code,omitempty"`
}

func (e *StrategyDiagnosticError) Error() string {
	parts := []string{string(e.Code)}
	if e.Strategy != "" {
		parts = append(parts, "strategy="+e.Strategy)
	}
	if e.Field != "" {
		parts = append(parts, "field="+e.Field)
	}
	if e.Details != "" {
		parts = append(parts, e.Details)
	}
	if e.Execution != nil {
		parts = append(parts, fmt.Sprintf("requested=%s approved=%s filled=%s provider=%s policy=%s", e.Execution.RequestedQuantity, e.Execution.ApprovedQuantity, e.Execution.FilledQuantity, e.Execution.ProviderCode, e.Execution.PolicyCode))
	}
	return strings.Join(parts, ": ")
}

func IsStrategyDiagnostic(err error, code StrategyDiagnosticCode) bool {
	var target *StrategyDiagnosticError
	return errors.As(err, &target) && target.Code == code
}

type StrategyFactory func(map[string]string) (tradingcore.Strategy, error)

type Stage05Plan struct {
	Targets           []string
	Rankings          []RankingArtifact
	Decide            bool
	TargetWeights     map[string]float64
	Regime            string
	RegimeObservation *RegimeObservation
	Factors           []FactorTrace
	ExitReasons       map[string]ExitReasonTrace
	Diagnostics       []StrategyTraceDiagnostic
	RiskStopOnly      bool
}

type Stage05PlanningContext struct {
	Selected        SelectedStrategy
	Reference       []services.OHLCV
	Series          map[string][]services.OHLCV
	Config          BacktestConfig
	Replays         []stage05Replay
	At              time.Time
	LastRebalance   time.Time
	LastTargets     []string
	Positions       map[string]float64
	PositionEntries map[string]float64
	Marks           map[string]float64
	Fixture         bool
}

type Stage05Planner interface {
	Plan(Stage05PlanningContext) (Stage05Plan, error)
}

type Stage05PlannerFunc func(Stage05PlanningContext) (Stage05Plan, error)

func (f Stage05PlannerFunc) Plan(context Stage05PlanningContext) (Stage05Plan, error) {
	return f(context)
}

type registeredStrategy struct {
	descriptor StrategyDescriptor
	factory    StrategyFactory
	planner    Stage05Planner
}

type StrategyRegistry struct {
	mu      sync.RWMutex
	entries map[string]registeredStrategy
}

func NewStrategyRegistry() *StrategyRegistry {
	return &StrategyRegistry{entries: map[string]registeredStrategy{}}
}

func (registry *StrategyRegistry) Register(descriptor StrategyDescriptor, factory StrategyFactory) error {
	return registry.RegisterExecutable(descriptor, factory, nil)
}

func (registry *StrategyRegistry) RegisterExecutable(descriptor StrategyDescriptor, factory StrategyFactory, planner Stage05Planner) error {
	if registry == nil || factory == nil || planner == nil {
		return fmt.Errorf("strategy registry, factory, and planner are required")
	}
	if err := validateStrategyDescriptor(descriptor); err != nil {
		return err
	}
	key := descriptor.ID + "@" + descriptor.Version
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.entries[key]; exists {
		return fmt.Errorf("strategy %s is already registered", key)
	}
	registry.entries[key] = registeredStrategy{descriptor: cloneStrategyDescriptor(descriptor), factory: factory, planner: planner}
	return nil
}

func validateStrategyDescriptor(descriptor StrategyDescriptor) error {
	if descriptor.SchemaVersion != StrategyDescriptorSchemaVersion || strings.TrimSpace(descriptor.ID) == "" || strings.TrimSpace(descriptor.Version) == "" || strings.TrimSpace(descriptor.Description) == "" {
		return fmt.Errorf("strategy descriptor identity, description, and supported schema are required")
	}
	if descriptor.WarmupBars < 0 || strings.TrimSpace(descriptor.DecisionCadence) == "" || strings.TrimSpace(descriptor.RebalanceCadence) == "" || !descriptor.Risk.UsesSharedRisk {
		return fmt.Errorf("strategy descriptor cadence, warmup, and shared risk declaration are required")
	}
	for _, cadence := range []string{descriptor.DecisionCadence, descriptor.RebalanceCadence} {
		if cadence != "never" && cadence != "once" {
			if duration, err := time.ParseDuration(cadence); err != nil || duration <= 0 {
				return fmt.Errorf("strategy cadence %q is invalid", cadence)
			}
		}
	}
	if descriptor.ID != StrategyCashID && len(descriptor.RequiredData) == 0 {
		return fmt.Errorf("strategy required data declaration is empty")
	}
	if descriptor.ResearchOnly && (descriptor.FactorTraceSchema == "" || len(descriptor.ExecutionIntents) == 0) {
		return fmt.Errorf("research strategy factor schema and execution intents are required")
	}
	for _, feature := range descriptor.Features {
		if feature.Name == "" || feature.Timeframe == "" || feature.WarmupBars < 1 || feature.TraceField == "" {
			return fmt.Errorf("strategy feature declarations must be explicit")
		}
		allowedTraceFields := map[string]bool{"long_mean": true, "lookback_returns": true, "absolute_trend": true, "realized_volatility": true}
		if !allowedTraceFields[feature.TraceField] {
			return fmt.Errorf("strategy feature %s declares unknown trace field %s", feature.Name, feature.TraceField)
		}
	}
	if descriptor.ID == StrategyTrendMomentumCandidate && (descriptor.WarmupFormula != "16*max(lookback_bars+1,trend_bars,regime_bars)" || descriptor.MaximumWarmupBars != 976) {
		return fmt.Errorf("candidate warmup formula or maximum is inconsistent")
	}
	roles := map[string]bool{"decision": true, "execution": true, "benchmark": true}
	frames := map[string]bool{"1m": true, "15m": true, "1h": true, "1d": true}
	hasBenchmark, hasExecution := false, false
	for _, requirement := range descriptor.RequiredData {
		if !roles[requirement.Role] || !frames[requirement.Timeframe] {
			return fmt.Errorf("unknown required data role/timeframe %s:%s", requirement.Role, requirement.Timeframe)
		}
		hasBenchmark = hasBenchmark || requirement.Role == "benchmark"
		hasExecution = hasExecution || requirement.Role == "execution"
	}
	if descriptor.BenchmarkRequired && !hasBenchmark {
		return fmt.Errorf("benchmark requirement is inconsistent")
	}
	if descriptor.ID != StrategyCashID && !hasExecution {
		return fmt.Errorf("executable strategies require exact execution data")
	}
	riskValues := []tradingcore.Decimal{}
	for _, raw := range []string{descriptor.Risk.MaxGrossExposure, descriptor.Risk.MaxNetExposure} {
		value, err := tradingcore.ParseDecimal(raw)
		if err != nil || value.Sign() <= 0 {
			return fmt.Errorf("risk exposure declaration is invalid")
		}
		riskValues = append(riskValues, value)
	}
	if riskValues[1].Float64() > riskValues[0].Float64() {
		return fmt.Errorf("net exposure exceeds gross exposure")
	}
	seen := map[string]bool{}
	for _, spec := range descriptor.Parameters {
		if strings.TrimSpace(spec.Name) == "" || seen[spec.Name] {
			return fmt.Errorf("parameter names must be nonempty and unique")
		}
		seen[spec.Name] = true
		if spec.Minimum != nil && spec.Maximum != nil && *spec.Minimum > *spec.Maximum {
			return fmt.Errorf("parameter %s bounds are invalid", spec.Name)
		}
		if spec.Type == "enum" && len(spec.Enum) == 0 {
			return fmt.Errorf("parameter %s enum is empty", spec.Name)
		}
		enumSeen := map[string]bool{}
		for _, value := range spec.Enum {
			if value == "" || enumSeen[value] {
				return fmt.Errorf("parameter %s enum values are invalid", spec.Name)
			}
			enumSeen[value] = true
		}
		if _, err := validateStrategyParameters(StrategyDescriptor{ID: "descriptor_validation", Parameters: []StrategyParameterSpec{spec}}, nil); err != nil {
			return fmt.Errorf("parameter %s default is invalid: %w", spec.Name, err)
		}
	}
	return nil
}

func (registry *StrategyRegistry) Resolve(id, version string, parameters map[string]string) (SelectedStrategy, tradingcore.Strategy, error) {
	selected, strategy, _, err := registry.ResolveExecutable(id, version, parameters)
	return selected, strategy, err
}

func (registry *StrategyRegistry) ResolveExecutable(id, version string, parameters map[string]string) (SelectedStrategy, tradingcore.Strategy, Stage05Planner, error) {
	registry.mu.RLock()
	if version == "" {
		matches := []string{}
		for key, entry := range registry.entries {
			if strings.HasPrefix(key, id+"@") {
				matches = append(matches, entry.descriptor.Version)
			}
		}
		if len(matches) > 1 {
			registry.mu.RUnlock()
			return SelectedStrategy{}, nil, nil, &StrategyDiagnosticError{Code: DiagnosticUnknownStrategy, Strategy: id, Details: "version is required when multiple versions exist"}
		}
		if len(matches) == 1 {
			version = matches[0]
		}
	}
	entry, ok := registry.entries[id+"@"+version]
	registry.mu.RUnlock()
	if !ok {
		return SelectedStrategy{}, nil, nil, &StrategyDiagnosticError{Code: DiagnosticUnknownStrategy, Strategy: id + "@" + version, Details: "not registered"}
	}
	validated, err := validateStrategyParameters(entry.descriptor, parameters)
	if err != nil {
		return SelectedStrategy{}, nil, nil, err
	}
	strategy, err := entry.factory(cloneStringMap(validated))
	if err != nil {
		return SelectedStrategy{}, nil, nil, err
	}
	return SelectedStrategy{Descriptor: cloneStrategyDescriptor(entry.descriptor), Parameters: cloneStringMap(validated)}, strategy, entry.planner, nil
}

func (registry *StrategyRegistry) List() []StrategyDescriptor {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make([]StrategyDescriptor, 0, len(registry.entries))
	for _, entry := range registry.entries {
		result = append(result, cloneStrategyDescriptor(entry.descriptor))
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ID != result[j].ID {
			return result[i].ID < result[j].ID
		}
		return result[i].Version < result[j].Version
	})
	return result
}

func validateStrategyParameters(descriptor StrategyDescriptor, values map[string]string) (map[string]string, error) {
	result := map[string]string{}
	specs := map[string]StrategyParameterSpec{}
	for _, spec := range descriptor.Parameters {
		specs[spec.Name] = spec
		result[spec.Name] = spec.Default
	}
	for key, value := range values {
		if _, ok := specs[key]; !ok {
			return nil, &StrategyDiagnosticError{Code: DiagnosticInvalidParameter, Strategy: descriptor.ID, Field: key, Details: "unknown parameter"}
		}
		result[key] = strings.TrimSpace(value)
	}
	for name, spec := range specs {
		value := result[name]
		switch spec.Type {
		case "integer":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return nil, invalidParameter(descriptor.ID, name, "must be an integer")
			}
			if spec.Minimum != nil && float64(parsed) < *spec.Minimum || spec.Maximum != nil && float64(parsed) > *spec.Maximum {
				return nil, invalidParameter(descriptor.ID, name, "outside declared range")
			}
		case "decimal":
			parsed, err := tradingcore.ParseDecimal(value)
			if err != nil {
				return nil, invalidParameter(descriptor.ID, name, "must be an exact base-10 decimal")
			}
			number := parsed.Float64()
			if spec.Minimum != nil && number < *spec.Minimum || spec.Maximum != nil && number > *spec.Maximum {
				return nil, invalidParameter(descriptor.ID, name, "outside declared range")
			}
		case "duration":
			if _, err := time.ParseDuration(value); err != nil {
				return nil, invalidParameter(descriptor.ID, name, "must be a Go duration")
			}
		case "enum":
			matched := false
			for _, allowed := range spec.Enum {
				matched = matched || value == allowed
			}
			if !matched {
				return nil, invalidParameter(descriptor.ID, name, "unsupported value")
			}
		default:
			return nil, invalidParameter(descriptor.ID, name, "unsupported schema type")
		}
	}
	if descriptor.ID == StrategyMomentumID {
		lookback, _ := strconv.Atoi(result["lookback_bars"])
		topN, _ := strconv.Atoi(result["top_n"])
		if lookback < 1 || topN < 1 {
			return nil, &StrategyDiagnosticError{Code: DiagnosticInvalidCombination, Strategy: descriptor.ID, Details: "lookback_bars and top_n must be positive"}
		}
	}
	if descriptor.ID == StrategyTrendMomentumCandidate {
		intent := result["execution_intent"]
		if intent == "paper_capital" || intent == "live_submit" || intent == "promotion" {
			return nil, &StrategyDiagnosticError{Code: DiagnosticExecutionFenced, Strategy: descriptor.ID, Field: "execution_intent", Details: intent + " is unavailable before Stage 07 validation and human promotion"}
		}
		if result["max_net"] != result["max_gross"] {
			return nil, &StrategyDiagnosticError{Code: DiagnosticInvalidCombination, Strategy: descriptor.ID, Field: "max_net", Details: "long-only candidate requires max_net equal to max_gross"}
		}
		positionCap, _ := tradingcore.ParseDecimal(result["position_cap"])
		gross, _ := tradingcore.ParseDecimal(result["max_gross"])
		reserve, _ := tradingcore.ParseDecimal(result["cash_reserve"])
		if positionCap.Float64() > gross.Float64() || gross.Float64()+reserve.Float64() > 1+1e-12 {
			return nil, &StrategyDiagnosticError{Code: DiagnosticInvalidCombination, Strategy: descriptor.ID, Details: "position/gross/cash caps are inconsistent"}
		}
	}
	return result, nil
}

func invalidParameter(strategy, field, details string) error {
	return &StrategyDiagnosticError{Code: DiagnosticInvalidParameter, Strategy: strategy, Field: field, Details: details}
}

func cloneStrategyDescriptor(value StrategyDescriptor) StrategyDescriptor {
	value.RequiredData = append([]StrategyDataRequirement(nil), value.RequiredData...)
	value.Features = append([]StrategyFeatureDeclaration(nil), value.Features...)
	value.ExecutionIntents = append([]string(nil), value.ExecutionIntents...)
	value.AblationVariants = append([]string(nil), value.AblationVariants...)
	value.Parameters = append([]StrategyParameterSpec(nil), value.Parameters...)
	for i := range value.Parameters {
		value.Parameters[i].Enum = append([]string(nil), value.Parameters[i].Enum...)
		if value.Parameters[i].Minimum != nil {
			minimum := *value.Parameters[i].Minimum
			value.Parameters[i].Minimum = &minimum
		}
		if value.Parameters[i].Maximum != nil {
			maximum := *value.Parameters[i].Maximum
			value.Parameters[i].Maximum = &maximum
		}
	}
	return value
}

func cloneStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func floatPointer(value float64) *float64 { return &value }

var DefaultStrategyRegistry = newDefaultStrategyRegistry()

func newDefaultStrategyRegistry() *StrategyRegistry {
	registry := NewStrategyRegistry()
	sharedRisk := StrategyRiskDeclaration{MaxGrossExposure: "1", MaxNetExposure: "1", LongOnly: true, UsesSharedRisk: true}
	candidateRisk := StrategyRiskDeclaration{MaxGrossExposure: "0.75", MaxNetExposure: "0.75", LongOnly: true, UsesSharedRisk: true}
	decision15m := []StrategyDataRequirement{{Role: "decision", Timeframe: "15m"}, {Role: "execution", Timeframe: "1m"}}
	benchmark15m := []StrategyDataRequirement{{Role: "benchmark", Timeframe: "15m"}, {Role: "execution", Timeframe: "1m"}}
	legacyData := append(append([]StrategyDataRequirement(nil), decision15m...), StrategyDataRequirement{Role: "benchmark", Timeframe: "15m"})
	candidateData := append(append([]StrategyDataRequirement(nil), decision15m...), StrategyDataRequirement{Role: "benchmark", Timeframe: "15m"})
	definitions := []StrategyDescriptor{
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyCashID, Version: "1.0.0", Description: "Preserve settlement cash with auditable no-action decisions and no market exposure.", RequiredData: benchmark15m, DecisionCadence: "15m", RebalanceCadence: "never", WarmupBars: 0, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "final_policy", Type: "enum", Description: "Final position policy.", Default: "mark_to_market", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyBenchmarkHoldID, Version: "1.0.0", Description: "Buy the independently sourced benchmark once after warmup and hold.", RequiredData: benchmark15m, BenchmarkRequired: true, DecisionCadence: "15m", RebalanceCadence: "once", WarmupBars: 1, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "warmup_bars", Type: "integer", Description: "Completed benchmark bars before entry.", Default: "1", Minimum: floatPointer(1)}, {Name: "target_gross", Type: "decimal", Description: "Long benchmark gross exposure fraction.", Default: "1", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyBenchmarkTrendID, Version: "1.0.0", Description: "Long the benchmark only when its last UTC-aligned completed hourly close is strictly above a trailing simple moving average; exit otherwise.", RequiredData: benchmark15m, BenchmarkRequired: true, DecisionCadence: "15m", RebalanceCadence: "1h", WarmupBars: 81, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "lookback_bars", Type: "integer", Description: "Trailing completed slower-timeframe closes in the simple moving average.", Default: "20", Minimum: floatPointer(2)}, {Name: "sample_bars", Type: "integer", Description: "Four complete UTC-aligned 15m bars form one hourly close; 1 is retained for bounded legacy fixtures only.", Default: "4", Minimum: floatPointer(1), Maximum: floatPointer(4)}, {Name: "target_gross", Type: "decimal", Description: "In-market gross exposure fraction.", Default: "1", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyEqualWeightID, Version: "1.0.0", Description: "Equal-weight all eligible members in each complete persisted point-in-time liquid-universe snapshot.", RequiredData: decision15m, DecisionCadence: "15m", RebalanceCadence: "24h", WarmupBars: 1, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "rebalance", Type: "duration", Description: "Minimum interval between rebalances.", Default: "24h"}, {Name: "target_gross", Type: "decimal", Description: "Total long gross exposure fraction.", Default: "1", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyMomentumID, Version: "1.0.0", Description: "Rank eligible point-in-time members by one trailing close-to-close return; select positive top-N with symbol-ascending deterministic ties.", RequiredData: decision15m, DecisionCadence: "15m", RebalanceCadence: "24h", WarmupBars: 20, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "lookback_bars", Type: "integer", Description: "Completed-bar close return lookback.", Default: "20", Minimum: floatPointer(1)}, {Name: "top_n", Type: "integer", Description: "Maximum number of positive-momentum assets.", Default: "3", Minimum: floatPointer(1)}, {Name: "rebalance", Type: "duration", Description: "Minimum interval between ranks.", Default: "24h"}, {Name: "target_gross", Type: "decimal", Description: "Total long gross exposure fraction.", Default: "1", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyLegacyCompatibility, Version: "1.0.0", Description: "Legacy composite indicator voting retained only as compatibility evidence; it is not promotion evidence.", RequiredData: legacyData, BenchmarkRequired: true, DecisionCadence: "15m", RebalanceCadence: "15m", WarmupBars: 120, Risk: sharedRisk, Baseline: true, LegacyCompatibility: true},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyTrendMomentumCandidate, Version: "1.0.0", Description: "Research-only long-only hypothesis: own top relative-momentum eligible assets with positive absolute trend only in a supportive completed-data benchmark regime.", RequiredData: candidateData, BenchmarkRequired: true, DecisionCadence: "4h", RebalanceCadence: "24h", WarmupBars: 496, WarmupFormula: "16*max(lookback_bars+1,trend_bars,regime_bars)", MaximumWarmupBars: 976, Risk: candidateRisk, FactorTraceSchema: "trend-momentum-factor-trace-v1", ResearchOnly: true, ExecutionIntents: []string{"research", "shadow", "backtest", "live_dry_run"}, AblationVariants: []string{"absolute_trend_only", "relative_momentum_only", "combined"}, Features: []StrategyFeatureDeclaration{{Name: "benchmark_regime_sma", Timeframe: "4h", WarmupBars: 30, TraceField: "long_mean"}, {Name: "relative_momentum", Timeframe: "4h", WarmupBars: 31, TraceField: "lookback_returns"}, {Name: "asset_absolute_trend", Timeframe: "4h", WarmupBars: 20, TraceField: "absolute_trend"}, {Name: "realized_volatility", Timeframe: "4h", WarmupBars: 31, TraceField: "realized_volatility"}}, Parameters: []StrategyParameterSpec{
			{Name: "variant", Type: "enum", Description: "Pre-registered ablation identity.", Default: "combined", Enum: []string{"absolute_trend_only", "relative_momentum_only", "combined"}},
			{Name: "vol_normalization", Type: "enum", Description: "Pre-registered volatility normalization switch.", Default: "true", Enum: []string{"false", "true"}},
			{Name: "lookback_bars", Type: "enum", Description: "Completed 4h momentum lookback.", Default: "30", Enum: []string{"20", "30", "60"}},
			{Name: "trend_bars", Type: "enum", Description: "Completed 4h asset trend average lookback.", Default: "20", Enum: []string{"20", "30", "60"}},
			{Name: "regime_bars", Type: "enum", Description: "Completed 4h benchmark regime average lookback.", Default: "30", Enum: []string{"20", "30", "60"}},
			{Name: "rebalance", Type: "enum", Description: "Pre-registered rebalance cadence.", Default: "24h", Enum: []string{"24h", "48h"}},
			{Name: "top_n", Type: "integer", Description: "Maximum concurrent ranked positions.", Default: "3", Minimum: floatPointer(1), Maximum: floatPointer(10)},
			{Name: "max_positions", Type: "integer", Description: "Hard concurrent position cap.", Default: "3", Minimum: floatPointer(1), Maximum: floatPointer(10)},
			{Name: "risk_on_gross", Type: "decimal", Description: "Risk-on gross and net target.", Default: "0.75", Minimum: floatPointer(0), Maximum: floatPointer(1)},
			{Name: "neutral_gross", Type: "decimal", Description: "Neutral gross and net target.", Default: "0.25", Minimum: floatPointer(0), Maximum: floatPointer(1)},
			{Name: "risk_off_gross", Type: "decimal", Description: "Risk-off gross and net target.", Default: "0", Minimum: floatPointer(0), Maximum: floatPointer(0)},
			{Name: "regime_band", Type: "decimal", Description: "Symmetric neutral band around benchmark average.", Default: "0.01", Minimum: floatPointer(0), Maximum: floatPointer(0.05)},
			{Name: "position_cap", Type: "decimal", Description: "Per-position fraction of executable equity.", Default: "0.25", Minimum: floatPointer(0.01), Maximum: floatPointer(0.5)},
			{Name: "max_gross", Type: "decimal", Description: "Hard gross exposure fraction.", Default: "0.75", Minimum: floatPointer(0.1), Maximum: floatPointer(1)},
			{Name: "max_net", Type: "decimal", Description: "Hard long-only net exposure fraction.", Default: "0.75", Minimum: floatPointer(0.1), Maximum: floatPointer(1)},
			{Name: "cash_reserve", Type: "decimal", Description: "Minimum cash fraction.", Default: "0.25", Minimum: floatPointer(0), Maximum: floatPointer(0.9)},
			{Name: "vol_floor", Type: "decimal", Description: "Realized-volatility sizing floor.", Default: "0.02", Minimum: floatPointer(0.005), Maximum: floatPointer(0.2)},
			{Name: "turnover_budget", Type: "decimal", Description: "Per-rebalance one-way turnover fraction.", Default: "0.25", Minimum: floatPointer(0), Maximum: floatPointer(1)},
			{Name: "skip_delta", Type: "decimal", Description: "Immaterial target-weight delta fraction.", Default: "0.005", Minimum: floatPointer(0), Maximum: floatPointer(0.05)},
			{Name: "execution_gap_reserve", Type: "decimal", Description: "Causal decision-price reserve for adverse next-event gaps.", Default: "0.1", Minimum: floatPointer(0), Maximum: floatPointer(0.25)},
			{Name: "allocation_tolerance", Type: "decimal", Description: "Maximum achieved exposure overshoot caused by execution gaps.", Default: "0.02", Minimum: floatPointer(0), Maximum: floatPointer(0.1)},
			{Name: "hard_stop", Type: "decimal", Description: "Causal close-based hard loss fraction; zero disables.", Default: "0.12", Minimum: floatPointer(0), Maximum: floatPointer(0.25)},
			{Name: "include_shortlist", Type: "enum", Description: "Include explicit shortlist alongside active eligible members.", Default: "true", Enum: []string{"false", "true"}},
			{Name: "execution_intent", Type: "enum", Description: "Research fence intent.", Default: "research", Enum: []string{"research", "shadow", "backtest", "live_dry_run", "paper_capital", "live_submit", "promotion"}},
			{Name: "model_observation", Type: "decimal", Description: "Recorded shadow-only observation with no rule influence.", Default: "0", Minimum: floatPointer(-1), Maximum: floatPointer(1)},
			{Name: "target_gross", Type: "decimal", Description: "Normalized comparison ceiling.", Default: "0.75", Minimum: floatPointer(0), Maximum: floatPointer(1)},
			{Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}},
		}},
	}
	for _, descriptor := range definitions {
		if descriptor.ID == StrategyEqualWeightID || descriptor.ID == StrategyMomentumID {
			descriptor.Parameters = append(descriptor.Parameters, StrategyParameterSpec{Name: "include_shortlist", Type: "enum", Description: "Whether persisted shortlist members join active members in the tradable baseline universe.", Default: "true", Enum: []string{"false", "true"}})
		}
		planner := Stage05Planner(stage05BuiltinPlanner{})
		if descriptor.ID == StrategyTrendMomentumCandidate {
			planner = trendMomentumPlanner{}
		}
		if err := registry.RegisterExecutable(descriptor, func(map[string]string) (tradingcore.Strategy, error) {
			return tradingcore.TargetAllocationStrategy{}, nil
		}, planner); err != nil {
			panic(err)
		}
	}
	return registry
}
