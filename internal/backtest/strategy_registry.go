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
	StrategyCashID              = "cash"
	StrategyBenchmarkHoldID     = "benchmark_buy_hold"
	StrategyBenchmarkTrendID    = "benchmark_trend"
	StrategyEqualWeightID       = "equal_weight_liquid_universe"
	StrategyMomentumID          = "cross_sectional_momentum"
	StrategyLegacyCompatibility = "legacy_indicator_voting"
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
	Targets  []string
	Rankings []RankingArtifact
	Decide   bool
}

type Stage05PlanningContext struct {
	Selected      SelectedStrategy
	Reference     []services.OHLCV
	Series        map[string][]services.OHLCV
	Config        BacktestConfig
	Replays       []stage05Replay
	At            time.Time
	LastRebalance time.Time
	LastTargets   []string
	Fixture       bool
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
	return result, nil
}

func invalidParameter(strategy, field, details string) error {
	return &StrategyDiagnosticError{Code: DiagnosticInvalidParameter, Strategy: strategy, Field: field, Details: details}
}

func cloneStrategyDescriptor(value StrategyDescriptor) StrategyDescriptor {
	value.RequiredData = append([]StrategyDataRequirement(nil), value.RequiredData...)
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
	decision15m := []StrategyDataRequirement{{Role: "decision", Timeframe: "15m"}, {Role: "execution", Timeframe: "1m"}}
	benchmark15m := []StrategyDataRequirement{{Role: "benchmark", Timeframe: "15m"}, {Role: "execution", Timeframe: "1m"}}
	legacyData := append(append([]StrategyDataRequirement(nil), decision15m...), StrategyDataRequirement{Role: "benchmark", Timeframe: "15m"})
	definitions := []StrategyDescriptor{
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyCashID, Version: "1.0.0", Description: "Preserve settlement cash with auditable no-action decisions and no market exposure.", RequiredData: benchmark15m, DecisionCadence: "15m", RebalanceCadence: "never", WarmupBars: 0, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "final_policy", Type: "enum", Description: "Final position policy.", Default: "mark_to_market", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyBenchmarkHoldID, Version: "1.0.0", Description: "Buy the independently sourced benchmark once after warmup and hold.", RequiredData: benchmark15m, BenchmarkRequired: true, DecisionCadence: "15m", RebalanceCadence: "once", WarmupBars: 1, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "warmup_bars", Type: "integer", Description: "Completed benchmark bars before entry.", Default: "1", Minimum: floatPointer(1)}, {Name: "target_gross", Type: "decimal", Description: "Long benchmark gross exposure fraction.", Default: "1", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyBenchmarkTrendID, Version: "1.0.0", Description: "Long the benchmark only when its last UTC-aligned completed hourly close is strictly above a trailing simple moving average; exit otherwise.", RequiredData: benchmark15m, BenchmarkRequired: true, DecisionCadence: "15m", RebalanceCadence: "1h", WarmupBars: 81, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "lookback_bars", Type: "integer", Description: "Trailing completed slower-timeframe closes in the simple moving average.", Default: "20", Minimum: floatPointer(2)}, {Name: "sample_bars", Type: "integer", Description: "Four complete UTC-aligned 15m bars form one hourly close; 1 is retained for bounded legacy fixtures only.", Default: "4", Minimum: floatPointer(1), Maximum: floatPointer(4)}, {Name: "target_gross", Type: "decimal", Description: "In-market gross exposure fraction.", Default: "1", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyEqualWeightID, Version: "1.0.0", Description: "Equal-weight all eligible members in each complete persisted point-in-time liquid-universe snapshot.", RequiredData: decision15m, DecisionCadence: "15m", RebalanceCadence: "24h", WarmupBars: 1, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "rebalance", Type: "duration", Description: "Minimum interval between rebalances.", Default: "24h"}, {Name: "target_gross", Type: "decimal", Description: "Total long gross exposure fraction.", Default: "1", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyMomentumID, Version: "1.0.0", Description: "Rank eligible point-in-time members by one trailing close-to-close return; select positive top-N with symbol-ascending deterministic ties.", RequiredData: decision15m, DecisionCadence: "15m", RebalanceCadence: "24h", WarmupBars: 20, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "lookback_bars", Type: "integer", Description: "Completed-bar close return lookback.", Default: "20", Minimum: floatPointer(1)}, {Name: "top_n", Type: "integer", Description: "Maximum number of positive-momentum assets.", Default: "3", Minimum: floatPointer(1)}, {Name: "rebalance", Type: "duration", Description: "Minimum interval between ranks.", Default: "24h"}, {Name: "target_gross", Type: "decimal", Description: "Total long gross exposure fraction.", Default: "1", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyLegacyCompatibility, Version: "1.0.0", Description: "Legacy composite indicator voting retained only as compatibility evidence; it is not promotion evidence.", RequiredData: legacyData, BenchmarkRequired: true, DecisionCadence: "15m", RebalanceCadence: "15m", WarmupBars: 120, Risk: sharedRisk, Baseline: true, LegacyCompatibility: true},
	}
	for _, descriptor := range definitions {
		if descriptor.ID == StrategyEqualWeightID || descriptor.ID == StrategyMomentumID {
			descriptor.Parameters = append(descriptor.Parameters, StrategyParameterSpec{Name: "include_shortlist", Type: "enum", Description: "Whether persisted shortlist members join active members in the tradable baseline universe.", Default: "true", Enum: []string{"false", "true"}})
		}
		if err := registry.RegisterExecutable(descriptor, func(map[string]string) (tradingcore.Strategy, error) {
			return tradingcore.TargetAllocationStrategy{}, nil
		}, stage05BuiltinPlanner{}); err != nil {
			panic(err)
		}
	}
	return registry
}
