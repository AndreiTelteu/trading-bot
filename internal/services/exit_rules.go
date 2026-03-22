// exit_rules.go defines the exit policy engine for all position close decisions.
//
// # Exit Precedence Order
//
// Exits are evaluated in a strict precedence order. The first matching rule wins:
//
//  1. stop_loss       — hard stop at the position's StopPrice
//  2. take_profit     — hard target at the position's TakeProfitPrice
//  3. atr_trailing    — ATR-based trailing stop (if ATRTrailingEnabled)
//  4. trailing_stop   — percent-based trailing stop (if TrailingStopEnabled)
//  5. (fallback) stop_loss   — computed from EntryPrice × (1 - StopLossPercent/100) when no explicit StopPrice is set
//  6. (fallback) take_profit — computed from EntryPrice × (1 + TakeProfitPercent/100) when no explicit TakeProfitPrice is set
//  7. time_stop       — close after BarsHeld ≥ TimeStopBars
//  8. sell_signal     — close on SELL / STRONG_SELL signal when rating ≤ MinConfidenceToSell
//
// # Tick vs Bar Evaluation
//
// Exits are split into two evaluation cadences:
//
//   - Protective exits (evaluated on every tick / 1m price update via [EvaluateProtectiveExit]):
//     stop_loss, take_profit, atr_trailing, trailing_stop, and their fallback variants.
//     These fire as soon as the price condition is met and are always executed regardless of P&L.
//
//   - Discretionary exits (evaluated on 15m bar close via [EvaluateBarCloseExit]):
//     time_stop and sell_signal. These are subject to the AllowSellAtLoss gate.
//     Note: EvaluateBarCloseExit also re-checks protective exits first, so a protective
//     condition discovered at bar close still takes priority.
//
// # AllowSellAtLoss Behavior
//
// The AllowSellAtLoss setting only gates discretionary exits (time_stop, sell_signal).
// When AllowSellAtLoss is false, discretionary exits are suppressed if CurrentPrice < EntryPrice,
// meaning the position is held until it is profitable or a protective exit triggers.
// Protective exits (stop_loss, take_profit, trailing variants) always execute regardless of
// the AllowSellAtLoss setting — they are safety mechanisms that must not be blocked.
//
// # Idempotency via exit_pending
//
// The [ExecutionCoordinator] sets the position's ExitPending flag to true inside a
// database transaction before submitting the sell order. Subsequent ticks that arrive
// while ExitPending is true are ignored by the [PositionMonitor], guaranteeing that
// each position is closed at most once even under concurrent tick delivery or duplicate
// events. If the sell order fails, ExitPending is rolled back to false so the next
// evaluation cycle can retry.
package services

import "time"

const (
	CloseReasonStopLoss      = "stop_loss"
	CloseReasonTakeProfit    = "take_profit"
	CloseReasonATRTrailing   = "atr_trailing_stop"
	CloseReasonTrailingStop  = "trailing_stop"
	CloseReasonTimeStop      = "time_stop"
	CloseReasonSellSignal    = "sell_signal"
	ExecutionModePaper       = "paper"
	ExecutionModeExchange    = "exchange"
	ExecutionModeShadow      = "shadow"
	EntrySourceManual        = "manual"
	EntrySourceAutoTrend     = "auto_trend"
	EntrySourceBackfill      = "backfill"
	EntrySourcePaperTest     = "paper_test"
	DecisionTimeframeDefault = "15m"
	OrderStatusPending       = "pending"
	OrderStatusSubmitted     = "submitted"
	OrderStatusFilled        = "filled"
	OrderStatusFailed        = "failed"
)

type ExitPolicy struct {
	StopLossPercent     float64
	TakeProfitPercent   float64
	TrailingStopEnabled bool
	TrailingStopPercent float64
	ATRTrailingEnabled  bool
	ATRTrailingMult     float64
	AllowSellAtLoss     bool
	TimeStopBars        int
	SellOnSignal        bool
	MinConfidenceToSell float64
}

type ExitEvaluationInput struct {
	CurrentPrice       float64
	HighPrice          float64
	LowPrice           float64
	EntryPrice         float64
	StopPrice          *float64
	TakeProfitPrice    *float64
	TrailingStopPrice  *float64
	BarsHeld           int
	MaxBarsHeld        *int
	Signal             string
	SignalRating       float64
	DecisionTimeframe  string
	ObservedAt         time.Time
	ExecutionMode      string
	AllowProtectiveGap bool
}

type ExitDecision struct {
	Reason       string
	TriggerPrice float64
	Protective   bool
}

// BuildExitPolicy constructs an ExitPolicy from the persisted settings map.
// Missing keys fall back to safe defaults (e.g., 5% stop-loss, 30% take-profit).
func BuildExitPolicy(settings map[string]string) ExitPolicy {
	return ExitPolicy{
		StopLossPercent:     getSettingFloat(settings, "stop_loss_percent", 5.0),
		TakeProfitPercent:   getSettingFloat(settings, "take_profit_percent", 30.0),
		TrailingStopEnabled: getSettingBool(settings, "trailing_stop_enabled", false),
		TrailingStopPercent: getSettingFloat(settings, "trailing_stop_percent", 10.0),
		ATRTrailingEnabled:  getSettingBool(settings, "atr_trailing_enabled", false),
		ATRTrailingMult:     getSettingFloat(settings, "atr_trailing_mult", 1.0),
		AllowSellAtLoss:     getSettingBool(settings, "allow_sell_at_loss", false),
		TimeStopBars:        getSettingInt(settings, "time_stop_bars", 0),
		SellOnSignal:        getSettingBool(settings, "sell_on_signal", true),
		MinConfidenceToSell: getSettingFloat(settings, "min_confidence_to_sell", 3.5),
	}
}

// RatchetPercentTrailingStop computes a candidate trailing stop at currentPrice × (1 - percent/100)
// and returns the higher of the candidate and the existing stop. The stop only ratchets upward
// and is not set until price exceeds entryPrice.
func RatchetPercentTrailingStop(existing *float64, currentPrice float64, entryPrice float64, trailingStopPercent float64) *float64 {
	if trailingStopPercent <= 0 || currentPrice <= 0 {
		return existing
	}
	if entryPrice > 0 && currentPrice <= entryPrice {
		return existing
	}

	candidate := currentPrice * (1 - (trailingStopPercent / 100))
	if candidate <= 0 {
		return existing
	}
	if existing == nil || candidate > *existing {
		return floatPtr(candidate)
	}
	return existing
}

// RatchetATRTrailingStop computes a candidate trailing stop at max(currentPrice, entryPrice) - atr×mult
// and returns the higher of the candidate and the existing stop. The stop only ratchets upward.
func RatchetATRTrailingStop(existing *float64, currentPrice float64, entryPrice float64, atr float64, atrTrailingMult float64) *float64 {
	if atr <= 0 || atrTrailingMult <= 0 || currentPrice <= 0 {
		return existing
	}

	reference := currentPrice
	if entryPrice > 0 && reference < entryPrice {
		reference = entryPrice
	}

	candidate := reference - (atr * atrTrailingMult)
	if candidate <= 0 {
		return existing
	}
	if existing == nil || candidate > *existing {
		return floatPtr(candidate)
	}
	return existing
}

// EvaluateProtectiveExit checks only protective (tick-level) exit conditions in precedence order:
// stop_loss → take_profit → atr_trailing → trailing_stop → fallback stop_loss → fallback take_profit.
// Returns a non-empty ExitDecision if a protective exit should fire. These exits are never gated
// by AllowSellAtLoss and always execute to protect capital.
func EvaluateProtectiveExit(input ExitEvaluationInput, policy ExitPolicy) ExitDecision {
	high := normalizedHigh(input)
	low := normalizedLow(input)

	if input.StopPrice != nil && low <= *input.StopPrice {
		return ExitDecision{Reason: CloseReasonStopLoss, TriggerPrice: *input.StopPrice, Protective: true}
	}
	if input.TakeProfitPrice != nil && high >= *input.TakeProfitPrice {
		return ExitDecision{Reason: CloseReasonTakeProfit, TriggerPrice: *input.TakeProfitPrice, Protective: true}
	}
	if policy.ATRTrailingEnabled && input.TrailingStopPrice != nil && low <= *input.TrailingStopPrice {
		return ExitDecision{Reason: CloseReasonATRTrailing, TriggerPrice: *input.TrailingStopPrice, Protective: true}
	}
	if policy.TrailingStopEnabled && input.TrailingStopPrice != nil && low <= *input.TrailingStopPrice {
		return ExitDecision{Reason: CloseReasonTrailingStop, TriggerPrice: *input.TrailingStopPrice, Protective: true}
	}

	if input.EntryPrice <= 0 {
		return ExitDecision{}
	}

	if input.StopPrice == nil && policy.StopLossPercent > 0 {
		fallbackStop := input.EntryPrice * (1 - (policy.StopLossPercent / 100))
		if fallbackStop > 0 && low <= fallbackStop {
			return ExitDecision{Reason: CloseReasonStopLoss, TriggerPrice: fallbackStop, Protective: true}
		}
	}
	if input.TakeProfitPrice == nil && policy.TakeProfitPercent > 0 {
		fallbackTarget := input.EntryPrice * (1 + (policy.TakeProfitPercent / 100))
		if fallbackTarget > 0 && high >= fallbackTarget {
			return ExitDecision{Reason: CloseReasonTakeProfit, TriggerPrice: fallbackTarget, Protective: true}
		}
	}

	return ExitDecision{}
}

// EvaluateBarCloseExit is called on 15m bar boundaries. It first re-evaluates protective exits,
// then checks discretionary exits (time_stop, sell_signal) which are subject to the AllowSellAtLoss gate.
func EvaluateBarCloseExit(input ExitEvaluationInput, policy ExitPolicy) ExitDecision {
	if decision := EvaluateProtectiveExit(input, policy); decision.Reason != "" {
		return decision
	}

	maxBars := policy.TimeStopBars
	if input.MaxBarsHeld != nil {
		maxBars = *input.MaxBarsHeld
	}
	if maxBars > 0 && input.BarsHeld >= maxBars && discretionaryExitAllowed(input, policy) {
		return ExitDecision{Reason: CloseReasonTimeStop, TriggerPrice: input.CurrentPrice}
	}

	if policy.SellOnSignal && discretionaryExitAllowed(input, policy) {
		if (input.Signal == "SELL" || input.Signal == "STRONG_SELL") && input.SignalRating <= policy.MinConfidenceToSell {
			return ExitDecision{Reason: CloseReasonSellSignal, TriggerPrice: input.CurrentPrice}
		}
	}

	return ExitDecision{}
}

// discretionaryExitAllowed returns true if a discretionary exit (time_stop, sell_signal)
// is permitted. When AllowSellAtLoss is false, the exit is blocked unless CurrentPrice ≥ EntryPrice.
func discretionaryExitAllowed(input ExitEvaluationInput, policy ExitPolicy) bool {
	if policy.AllowSellAtLoss {
		return true
	}
	return input.EntryPrice <= 0 || input.CurrentPrice >= input.EntryPrice
}

func normalizedHigh(input ExitEvaluationInput) float64 {
	if input.HighPrice > 0 {
		return input.HighPrice
	}
	return input.CurrentPrice
}

func normalizedLow(input ExitEvaluationInput) float64 {
	if input.LowPrice > 0 {
		return input.LowPrice
	}
	return input.CurrentPrice
}

func floatPtr(v float64) *float64 {
	return &v
}
