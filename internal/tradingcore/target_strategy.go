package tradingcore

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// TargetAllocationStrategy is a small shared contract for deterministic
// baseline/candidate planners. Point-in-time planners publish exact target
// quantities into immutable DecisionContext settings; this Strategy alone turns
// those targets into auditable intents used by every broker/risk mode.
type TargetAllocationStrategy struct{}

func (TargetAllocationStrategy) Decide(_ context.Context, snapshot DecisionContext) (StrategyResult, error) {
	settings := snapshot.Settings()
	portfolio := snapshot.Portfolio()
	positions := map[InstrumentID]Position{}
	for _, position := range portfolio.Positions() {
		positions[position.Instrument.ID] = position
	}
	intents := []OrderIntent{}
	noActions := []NoAction{}
	for _, candidate := range snapshot.Universe().Candidates() {
		instrument := candidate.Instrument
		key := instrument.ID.String()
		action := strings.ToLower(strings.TrimSpace(settings["target_action."+key]))
		if action == "" || action == "hold" {
			code := strings.TrimSpace(settings["no_action_code."+key])
			if code == "" {
				code = "target_unchanged"
			}
			noActions = append(noActions, NoAction{Instrument: instrument, Code: code, Reason: code, ObservedScore: candidate.Score})
			continue
		}
		quantityRaw := strings.TrimSpace(settings["target_quantity."+key])
		quantityDecimal, err := ParseDecimal(quantityRaw)
		if err != nil {
			return StrategyResult{}, fmt.Errorf("invalid target quantity for %s: %w", key, err)
		}
		quantity, err := NewQuantity(quantityDecimal)
		if err != nil {
			return StrategyResult{}, fmt.Errorf("invalid target quantity for %s: %w", key, err)
		}
		side := Buy
		semantics := QuantityCashCapped
		if action == "sell" {
			side = Sell
			semantics = QuantityExact
			if position, ok := positions[instrument.ID]; !ok || position.Quantity.Decimal().Float64()+1e-12 < quantity.Decimal().Float64() {
				return StrategyResult{}, fmt.Errorf("sell target for %s exceeds the held quantity", key)
			}
		} else if action != "buy" {
			return StrategyResult{}, fmt.Errorf("unsupported target action %q", action)
		}
		quote, ok := snapshot.Quote(instrument.ID)
		if !ok || !quote.Last.Valid() {
			return StrategyResult{}, fmt.Errorf("target quote unavailable for %s", key)
		}
		raw := fmt.Sprintf("%s|%s|%s|%s|%s", snapshot.DecisionAt().UTC().Format("20060102T150405.000000000Z07:00"), portfolio.AccountID().String(), key, side, snapshot.Versions().Strategy)
		hash := sha256.Sum256([]byte(raw))
		orderID, _ := NewOrderID(fmt.Sprintf("target-%x", hash[:12]))
		idempotency, _ := NewIdempotencyKey(fmt.Sprintf("target-intent-%x", hash[:12]))
		priority, _ := strconv.Atoi(settings["target_priority."+key])
		if priority < 0 {
			priority = 0
		}
		reason := strings.TrimSpace(settings["target_reason."+key])
		if reason == "" {
			reason = "target_allocation"
		}
		metadata := map[string]string{"target_weight": settings["target_weight."+key], "regime_state": settings["target_regime."+key], "execution_reference_price": settings["execution_reference_price."+key]}
		for setting, value := range settings {
			if strings.HasPrefix(setting, "intent_metadata.") {
				metadata[strings.TrimPrefix(setting, "intent_metadata.")] = value
			}
		}
		intent, err := NewOrderIntent(OrderIntent{ID: orderID, IdempotencyKey: idempotency, AccountID: portfolio.AccountID(), Instrument: instrument, Side: side, Type: MarketOrder, Quantity: quantity, ReferencePrice: SomePrice(quote.Last), SignalAt: snapshot.SignalAt(), DecisionAt: snapshot.DecisionAt(), CreatedAt: snapshot.DecisionAt(), ExecutionMode: portfolio.ExecutionMode(), QuantitySemantics: semantics, Priority: priority, Reason: reason, Horizon: settings["strategy_horizon"], Versions: snapshot.Versions(), Provenance: Provenance{Source: "target_allocation", Actor: snapshot.Versions().Strategy, Reason: reason}}, metadata)
		if err != nil {
			return StrategyResult{}, err
		}
		intents = append(intents, intent)
	}
	batch, err := NewDecisionBatch(intents)
	if err != nil {
		return StrategyResult{}, err
	}
	return NewStrategyResult(batch, noActions), nil
}

// TrendMomentumStrategy is the executable Stage 06 strategy.  The adapter
// supplies a canonical point-in-time TrendMomentumInput, while all feature,
// ranking, regime and target logic remains in PlanTrendMomentum.  This closes
// the old runtime hole where a candidate deployment was merely an interpreter
// for hand-populated target_action/target_quantity settings.
type TrendMomentumStrategy struct{}

func (TrendMomentumStrategy) Decide(ctx context.Context, snapshot DecisionContext) (StrategyResult, error) {
	settings := snapshot.Settings()
	raw := strings.TrimSpace(settings["trend_momentum_input"])
	if raw == "" {
		return StrategyResult{}, fmt.Errorf("trend momentum point-in-time input is required")
	}
	var input TrendMomentumInput
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		return StrategyResult{}, fmt.Errorf("decode trend momentum input: %w", err)
	}
	if input.DecisionAt.IsZero() {
		input.DecisionAt = snapshot.DecisionAt()
	}
	if !input.DecisionAt.UTC().Equal(snapshot.DecisionAt().UTC()) {
		return StrategyResult{}, fmt.Errorf("trend momentum decision clock differs from immutable snapshot")
	}
	plan, err := PlanTrendMomentum(input)
	if err != nil {
		return StrategyResult{}, err
	}
	derived, err := trendMomentumTargetSettings(snapshot, settings, plan)
	if err != nil {
		return StrategyResult{}, err
	}
	derived["trend_momentum_plan_schema"] = plan.SchemaVersion
	derived["trend_momentum_regime"] = plan.Regime
	derived["trend_momentum_regime_reason"] = plan.RegimeReason
	derived["trend_momentum_target_gross"] = decimalPlanValue(plan.TargetGross)
	contextWithTargets, err := NewDecisionContext(DecisionContextInput{MarketObservedAt: snapshot.MarketObservedAt(), SignalAt: snapshot.SignalAt(), DecisionAt: snapshot.DecisionAt(), Quotes: snapshot.Quotes(), Universe: snapshot.Universe(), Portfolio: snapshot.Portfolio(), Settings: derived, Versions: snapshot.Versions()})
	if err != nil {
		return StrategyResult{}, err
	}
	return TargetAllocationStrategy{}.Decide(ctx, contextWithTargets)
}

func trendMomentumTargetSettings(snapshot DecisionContext, base map[string]string, plan TrendMomentumPlan) (map[string]string, error) {
	settings := cloneStrings(base)
	portfolio := snapshot.Portfolio()
	positions := map[InstrumentID]Position{}
	for _, position := range portfolio.Positions() {
		positions[position.Instrument.ID] = position
	}
	// Equity is exact even though the predeclared research weight is a decimal
	// feature output. No float quantity is ever passed to the broker contract.
	equity := new(big.Rat)
	for _, cash := range portfolio.Cash() {
		equity.Add(equity, decimalRat(cash.Decimal()))
	}
	for _, position := range portfolio.Positions() {
		equity.Add(equity, notional(position.Quantity, position.MarkPrice))
	}
	if equity.Sign() <= 0 {
		return nil, fmt.Errorf("trend momentum portfolio equity must be positive")
	}
	bySymbol := map[string]Instrument{}
	for _, candidate := range snapshot.Universe().Candidates() {
		bySymbol[candidate.Instrument.VenueSymbol] = candidate.Instrument
	}
	for _, position := range portfolio.Positions() {
		bySymbol[position.Instrument.VenueSymbol] = position.Instrument
	}
	for symbol, instrument := range bySymbol {
		key := instrument.ID.String()
		quote, ok := snapshot.Quote(instrument.ID)
		if !ok || !quote.Last.Valid() {
			return nil, fmt.Errorf("trend momentum quote unavailable for %s", key)
		}
		weight := plan.TargetWeights[symbol]
		weightDecimal, err := ParseDecimal(decimalPlanValue(weight))
		if err != nil {
			return nil, err
		}
		desired := new(big.Rat).Quo(new(big.Rat).Mul(equity, decimalRat(weightDecimal)), decimalRat(quote.Last.Decimal()))
		current := new(big.Rat)
		if position, ok := positions[instrument.ID]; ok {
			current = decimalRat(position.Quantity.Decimal())
		}
		delta := new(big.Rat).Sub(desired, current)
		if delta.Sign() == 0 {
			settings["no_action_code."+key] = "target_unchanged"
			continue
		}
		side := "buy"
		if delta.Sign() < 0 {
			side = "sell"
			delta.Neg(delta)
		}
		quantity, err := quantityFromRat(delta)
		if err != nil {
			return nil, fmt.Errorf("trend momentum %s target: %w", symbol, err)
		}
		settings["target_action."+key], settings["target_quantity."+key] = side, quantity.Decimal().String()
		settings["target_weight."+key], settings["target_regime."+key] = decimalPlanValue(weight), plan.Regime
		settings["target_reason."+key] = "trend_momentum_" + plan.Regime
	}
	return settings, nil
}

func decimalPlanValue(value float64) string { return strconv.FormatFloat(value, 'f', 18, 64) }
