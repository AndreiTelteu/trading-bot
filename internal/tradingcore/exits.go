package tradingcore

type ExitTriggerInput struct {
	HardStop, TakeProfit, ATRTrailing, PercentTrailing, FallbackStop, FallbackTakeProfit, TimeStop, Signal bool
	AtLoss, AllowSellAtLoss                                                                                bool
}
type ExitDecision struct {
	Exit         bool
	Reason       string
	Simultaneous []string
}

// EvaluateExitLifecycle is shared by every mode. First match preserves the
// characterized protective-first precedence; simultaneous triggers are retained
// for audit without changing the selected reason.
func EvaluateExitLifecycle(input ExitTriggerInput) ExitDecision {
	ordered := []struct {
		active     bool
		reason     string
		protective bool
	}{
		{input.HardStop, "stop_loss", true}, {input.TakeProfit, "take_profit", true},
		{input.ATRTrailing, "atr_trailing_stop", true}, {input.PercentTrailing, "trailing_stop", true},
		{input.FallbackStop, "stop_loss", true}, {input.FallbackTakeProfit, "take_profit", true},
		{input.TimeStop, "time_stop", false}, {input.Signal, "signal_exit", false},
	}
	result := ExitDecision{}
	for _, trigger := range ordered {
		if !trigger.active || (!trigger.protective && input.AtLoss && !input.AllowSellAtLoss) {
			continue
		}
		result.Simultaneous = append(result.Simultaneous, trigger.reason)
		if !result.Exit {
			result.Exit = true
			result.Reason = trigger.reason
		}
	}
	return result
}
