package tradingcore

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

const LegacyStrategyVersion = "legacy-rule-v1"

// LegacyRuleStrategy preserves the characterized gate ordering while consuming
// only immutable, point-in-time context. Indicator/model adapters publish their
// observations in versioned settings; rollout policy alone decides authority.
type LegacyRuleStrategy struct{ IDs IDGenerator }
type FixedStrategy struct{ Result StrategyResult }

func (strategy FixedStrategy) Decide(context.Context, DecisionContext) (StrategyResult, error) {
	return strategy.Result, nil
}

func (strategy LegacyRuleStrategy) Decide(_ context.Context, snapshot DecisionContext) (StrategyResult, error) {
	settings := snapshot.Settings()
	portfolio := snapshot.Portfolio()
	candidates := snapshot.Universe().Candidates()
	intents := make([]OrderIntent, 0)
	noActions := make([]NoAction, 0)
	for _, candidate := range candidates {
		if !candidate.Eligible {
			noActions = append(noActions, noAction(candidate, "universe_ineligible", candidate.RejectionReason))
			continue
		}
		id := candidate.Instrument.ID.String()
		code := legacyEntryGate(settings, id, portfolio, candidate.Instrument)
		if code != "" {
			noActions = append(noActions, noAction(candidate, code, code))
			continue
		}
		quote, ok := snapshot.Quote(candidate.Instrument.ID)
		if !ok || !quote.Last.Valid() {
			noActions = append(noActions, noAction(candidate, "quote_unavailable", "quote_unavailable"))
			continue
		}
		cash, ok := portfolio.CashAmount(candidate.Instrument.QuoteAsset)
		if !ok || cash.Decimal().Sign() <= 0 {
			noActions = append(noActions, noAction(candidate, "insufficient_cash", "insufficient_cash"))
			continue
		}
		percent := floatSetting(settings, "entry_percent", 5)
		requested := newRatPercent(decimalRat(cash.Decimal()), percent)
		quantity, err := quantityFromRat(newRatDiv(requested, decimalRat(quote.Last.Decimal())))
		if configured := strings.TrimSpace(settings["requested_quantity."+id]); configured != "" {
			decimal, parseErr := ParseDecimal(configured)
			if parseErr != nil {
				noActions = append(noActions, noAction(candidate, "invalid_position_size", parseErr.Error()))
				continue
			}
			quantity, err = NewQuantity(decimal)
		}
		if err != nil {
			noActions = append(noActions, noAction(candidate, "invalid_position_size", err.Error()))
			continue
		}
		rawID := stableStrategyIntentID(snapshot, portfolio.AccountID(), candidate.Instrument, Buy)
		orderID, _ := NewOrderID("order-" + rawID)
		key, _ := NewIdempotencyKey("intent-" + rawID)
		metadata := map[string]string{"rank": strconv.Itoa(candidate.Rank), "rollout_state": value(settings, "model_rollout_state", "shadow")}
		for _, key := range []string{"stop_price", "take_profit_price", "atr_value", "atr_trailing_mult", "max_bars_held", "entry_rank", "regime_state", "breadth_ratio", "model_version", "predicted_probability", "predicted_ev"} {
			if configured := strings.TrimSpace(settings[key+"."+id]); configured != "" {
				metadata[key] = configured
			}
		}
		createdAt := snapshot.DecisionAt()
		if configured := strings.TrimSpace(settings["order_created_at"]); configured != "" {
			parsed, parseErr := time.Parse(time.RFC3339Nano, configured)
			if parseErr != nil || parsed.Before(snapshot.DecisionAt()) {
				return StrategyResult{}, fmt.Errorf("invalid order_created_at")
			}
			createdAt = parsed
		}
		intent, err := NewOrderIntent(OrderIntent{ID: orderID, IdempotencyKey: key, AccountID: portfolio.AccountID(), Instrument: candidate.Instrument, Side: Buy, Type: MarketOrder, Quantity: quantity, ReferencePrice: SomePrice(quote.Last), SignalAt: snapshot.SignalAt(), DecisionAt: snapshot.DecisionAt(), CreatedAt: createdAt, ExecutionMode: portfolio.ExecutionMode(), QuantitySemantics: QuantityCashCapped, Priority: candidate.Rank, Reason: "passed_gates", Horizon: value(settings, "strategy_horizon", "15m"), Versions: snapshot.Versions(), Provenance: Provenance{Source: "legacy_rule_strategy", Actor: LegacyStrategyVersion, Reason: "passed_gates"}}, metadata)
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

func legacyEntryGate(settings map[string]string, id string, portfolio PortfolioSnapshot, instrument Instrument) string {
	if boolSetting(settings, "analysis_error."+id, false) {
		return "analysis_error"
	}
	if !boolSetting(settings, "auto_trade_enabled", false) {
		return "auto_trade_disabled"
	}
	if boolSetting(settings, "universe_risk_off", false) {
		return "universe_regime_risk_off"
	}
	authority := ResolveModelAuthority(portfolio.ExecutionMode(), value(settings, "model_rollout_state", "shadow"), value(settings, "model_fallback_mode", "rule_based"), boolSetting(settings, "model_available", false))
	if authority.Blocked {
		return authority.Reason
	}
	if authority.UseModel && !boolSetting(settings, "model_selected."+id, false) {
		return "model_policy_not_selected"
	}
	signal := strings.ToUpper(value(settings, "signal."+id, "NEUTRAL"))
	if !authority.UseModel && boolSetting(settings, "buy_only_strong", false) && signal != "STRONG_BUY" {
		return "signal_not_qualified"
	}
	if !authority.UseModel && signal != "BUY" && signal != "STRONG_BUY" {
		return "signal_not_qualified"
	}
	if authority.UseModel && !boolSetting(settings, "model_floor_ok."+id, false) {
		return "model_policy_floor_failed"
	}
	if !authority.UseModel && floatSetting(settings, "rating."+id, 0) < floatSetting(settings, "min_confidence_to_buy", 0) {
		return "confidence_not_qualified"
	}
	if !boolSetting(settings, "regime_ok."+id, true) {
		return "regime_gate_failed"
	}
	if !boolSetting(settings, "vol_ok."+id, true) {
		return "vol_gate_failed"
	}
	if len(portfolio.Positions()) >= intSetting(settings, "max_positions", 5) {
		return "max_positions_reached"
	}
	for _, position := range portfolio.Positions() {
		if position.Instrument.ID == instrument.ID {
			return "position_exists"
		}
	}
	return ""
}

func stableStrategyIntentID(snapshot DecisionContext, account AccountID, instrument Instrument, side OrderSide) string {
	payload := snapshot.DecisionAt().UTC().Format(time.RFC3339Nano) + "|" + account.String() + "|" + instrument.ID.String() + "|" + string(side) + "|" + snapshot.Versions().Strategy + "|" + snapshot.Versions().Policy
	sum := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", sum[:12])
}

func noAction(candidate UniverseCandidate, code, reason string) NoAction {
	return NoAction{Instrument: candidate.Instrument, Code: code, Reason: reason, ObservedScore: candidate.Score}
}
func value(settings map[string]string, key, fallback string) string {
	if v := strings.TrimSpace(settings[key]); v != "" {
		return v
	}
	return fallback
}
func boolSetting(settings map[string]string, key string, fallback bool) bool {
	v, ok := settings[key]
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return parsed
}
func floatSetting(settings map[string]string, key string, fallback float64) float64 {
	parsed, err := strconv.ParseFloat(settings[key], 64)
	if err != nil {
		return fallback
	}
	return parsed
}
func intSetting(settings map[string]string, key string, fallback int) int {
	parsed, err := strconv.Atoi(settings[key])
	if err != nil {
		return fallback
	}
	return parsed
}

func newRatPercent(value *big.Rat, percent float64) *big.Rat {
	p, _ := new(big.Rat).SetString(strconv.FormatFloat(percent/100, 'f', -1, 64))
	return new(big.Rat).Mul(value, p)
}
func newRatDiv(a, b *big.Rat) *big.Rat { return new(big.Rat).Quo(a, b) }
