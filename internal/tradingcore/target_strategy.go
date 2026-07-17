package tradingcore

import (
	"context"
	"crypto/sha256"
	"fmt"
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
		intent, err := NewOrderIntent(OrderIntent{ID: orderID, IdempotencyKey: idempotency, AccountID: portfolio.AccountID(), Instrument: instrument, Side: side, Type: MarketOrder, Quantity: quantity, ReferencePrice: SomePrice(quote.Last), SignalAt: snapshot.SignalAt(), DecisionAt: snapshot.DecisionAt(), CreatedAt: snapshot.DecisionAt(), ExecutionMode: portfolio.ExecutionMode(), QuantitySemantics: semantics, Priority: priority, Reason: reason, Horizon: settings["strategy_horizon"], Versions: snapshot.Versions(), Provenance: Provenance{Source: "target_allocation", Actor: snapshot.Versions().Strategy, Reason: reason}}, map[string]string{"target_weight": settings["target_weight."+key], "regime_state": settings["target_regime."+key], "execution_reference_price": settings["execution_reference_price."+key]})
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
