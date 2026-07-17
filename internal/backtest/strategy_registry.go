package backtest

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

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
)

type StrategyDiagnosticError struct {
	Code     StrategyDiagnosticCode `json:"code"`
	Strategy string                 `json:"strategy,omitempty"`
	Field    string                 `json:"field,omitempty"`
	Details  string                 `json:"details"`
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
	return strings.Join(parts, ": ")
}

func IsStrategyDiagnostic(err error, code StrategyDiagnosticCode) bool {
	var target *StrategyDiagnosticError
	return errors.As(err, &target) && target.Code == code
}

type StrategyFactory func(map[string]string) (tradingcore.Strategy, error)

type registeredStrategy struct {
	descriptor StrategyDescriptor
	factory    StrategyFactory
}

type StrategyRegistry struct {
	mu      sync.RWMutex
	entries map[string]registeredStrategy
}

func NewStrategyRegistry() *StrategyRegistry {
	return &StrategyRegistry{entries: map[string]registeredStrategy{}}
}

func (registry *StrategyRegistry) Register(descriptor StrategyDescriptor, factory StrategyFactory) error {
	if registry == nil || factory == nil {
		return fmt.Errorf("strategy registry and factory are required")
	}
	if descriptor.SchemaVersion != StrategyDescriptorSchemaVersion || strings.TrimSpace(descriptor.ID) == "" || strings.TrimSpace(descriptor.Version) == "" || strings.TrimSpace(descriptor.Description) == "" {
		return fmt.Errorf("strategy descriptor identity, description, and supported schema are required")
	}
	if descriptor.WarmupBars < 0 || descriptor.DecisionCadence == "" || descriptor.RebalanceCadence == "" || !descriptor.Risk.UsesSharedRisk {
		return fmt.Errorf("strategy descriptor cadence, warmup, and shared risk declaration are required")
	}
	key := descriptor.ID + "@" + descriptor.Version
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.entries[key]; exists {
		return fmt.Errorf("strategy %s is already registered", key)
	}
	registry.entries[key] = registeredStrategy{descriptor: cloneStrategyDescriptor(descriptor), factory: factory}
	return nil
}

func (registry *StrategyRegistry) Resolve(id, version string, parameters map[string]string) (SelectedStrategy, tradingcore.Strategy, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if version == "" {
		for key, entry := range registry.entries {
			if strings.HasPrefix(key, id+"@") {
				if version != "" {
					return SelectedStrategy{}, nil, &StrategyDiagnosticError{Code: DiagnosticUnknownStrategy, Strategy: id, Details: "version is required when multiple versions exist"}
				}
				version = entry.descriptor.Version
			}
		}
	}
	entry, ok := registry.entries[id+"@"+version]
	if !ok {
		return SelectedStrategy{}, nil, &StrategyDiagnosticError{Code: DiagnosticUnknownStrategy, Strategy: id + "@" + version, Details: "not registered"}
	}
	validated, err := validateStrategyParameters(entry.descriptor, parameters)
	if err != nil {
		return SelectedStrategy{}, nil, err
	}
	strategy, err := entry.factory(cloneStringMap(validated))
	if err != nil {
		return SelectedStrategy{}, nil, err
	}
	return SelectedStrategy{Descriptor: cloneStrategyDescriptor(entry.descriptor), Parameters: cloneStringMap(validated)}, strategy, nil
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
	benchmark15m := []StrategyDataRequirement{{Role: "benchmark", Timeframe: "15m"}}
	definitions := []StrategyDescriptor{
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyCashID, Version: "1.0.0", Description: "Preserve settlement cash with auditable no-action decisions and no market exposure.", RequiredData: benchmark15m, DecisionCadence: "15m", RebalanceCadence: "never", WarmupBars: 0, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "final_policy", Type: "enum", Description: "Final position policy.", Default: "mark_to_market", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyBenchmarkHoldID, Version: "1.0.0", Description: "Buy the independently sourced benchmark once after warmup and hold.", RequiredData: benchmark15m, BenchmarkRequired: true, DecisionCadence: "15m", RebalanceCadence: "once", WarmupBars: 1, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "warmup_bars", Type: "integer", Description: "Completed benchmark bars before entry.", Default: "1", Minimum: floatPointer(1)}, {Name: "target_gross", Type: "decimal", Description: "Long benchmark gross exposure fraction.", Default: "1", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyBenchmarkTrendID, Version: "1.0.0", Description: "Long the benchmark only when its last completed hourly-sampled close is strictly above a trailing simple moving average; exit otherwise.", RequiredData: benchmark15m, BenchmarkRequired: true, DecisionCadence: "15m", RebalanceCadence: "1h", WarmupBars: 80, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "lookback_bars", Type: "integer", Description: "Trailing completed slower-timeframe closes in the simple moving average.", Default: "20", Minimum: floatPointer(2)}, {Name: "sample_bars", Type: "integer", Description: "Decision bars per completed slower-timeframe close (4x15m = 1h by default).", Default: "4", Minimum: floatPointer(1)}, {Name: "target_gross", Type: "decimal", Description: "In-market gross exposure fraction.", Default: "1", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyEqualWeightID, Version: "1.0.0", Description: "Equal-weight all eligible members in each complete persisted point-in-time liquid-universe snapshot.", RequiredData: decision15m, DecisionCadence: "15m", RebalanceCadence: "24h", WarmupBars: 1, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "rebalance", Type: "duration", Description: "Minimum interval between rebalances.", Default: "24h"}, {Name: "target_gross", Type: "decimal", Description: "Total long gross exposure fraction.", Default: "1", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyMomentumID, Version: "1.0.0", Description: "Rank eligible point-in-time members by one trailing close-to-close return; select positive top-N with symbol-ascending deterministic ties.", RequiredData: decision15m, DecisionCadence: "15m", RebalanceCadence: "24h", WarmupBars: 20, Risk: sharedRisk, Baseline: true, Parameters: []StrategyParameterSpec{{Name: "lookback_bars", Type: "integer", Description: "Completed-bar close return lookback.", Default: "20", Minimum: floatPointer(1)}, {Name: "top_n", Type: "integer", Description: "Maximum number of positive-momentum assets.", Default: "3", Minimum: floatPointer(1)}, {Name: "rebalance", Type: "duration", Description: "Minimum interval between ranks.", Default: "24h"}, {Name: "target_gross", Type: "decimal", Description: "Total long gross exposure fraction.", Default: "1", Minimum: floatPointer(0), Maximum: floatPointer(1)}, {Name: "final_policy", Type: "enum", Description: "Final valuation policy.", Default: "liquidate", Enum: []string{"mark_to_market", "liquidate"}}}},
		{SchemaVersion: StrategyDescriptorSchemaVersion, ID: StrategyLegacyCompatibility, Version: "1.0.0", Description: "Legacy composite indicator voting retained only as compatibility evidence; it is not promotion evidence.", RequiredData: decision15m, BenchmarkRequired: true, DecisionCadence: "15m", RebalanceCadence: "15m", WarmupBars: 120, Risk: sharedRisk, Baseline: true, LegacyCompatibility: true},
	}
	for _, descriptor := range definitions {
		if err := registry.Register(descriptor, func(map[string]string) (tradingcore.Strategy, error) {
			return tradingcore.TargetAllocationStrategy{}, nil
		}); err != nil {
			panic(err)
		}
	}
	return registry
}
