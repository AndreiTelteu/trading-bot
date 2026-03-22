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
