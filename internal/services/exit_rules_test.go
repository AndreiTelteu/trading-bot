package services

import "testing"

func TestEvaluateProtectiveExitPrefersStopLossOnSameBarCollision(t *testing.T) {
	stop := 95.0
	takeProfit := 110.0
	decision := EvaluateProtectiveExit(ExitEvaluationInput{
		CurrentPrice:    102,
		HighPrice:       112,
		LowPrice:        94,
		EntryPrice:      100,
		StopPrice:       &stop,
		TakeProfitPrice: &takeProfit,
	}, ExitPolicy{})

	if decision.Reason != CloseReasonStopLoss {
		t.Fatalf("expected %s, got %s", CloseReasonStopLoss, decision.Reason)
	}
	if decision.TriggerPrice != stop {
		t.Fatalf("expected stop trigger %.2f, got %.2f", stop, decision.TriggerPrice)
	}
}

func TestRatchetPercentTrailingStopOnlyMovesUp(t *testing.T) {
	stop := RatchetPercentTrailingStop(nil, 110, 100, 10)
	if stop == nil || *stop != 99 {
		t.Fatalf("expected initial trailing stop 99, got %v", stop)
	}

	stop = RatchetPercentTrailingStop(stop, 105, 100, 10)
	if stop == nil || *stop != 99 {
		t.Fatalf("expected trailing stop to stay at 99, got %v", stop)
	}

	stop = RatchetPercentTrailingStop(stop, 120, 100, 10)
	if stop == nil || *stop != 108 {
		t.Fatalf("expected ratcheted trailing stop 108, got %v", stop)
	}
}

func TestRatchetATRTrailingStopUsesStoredATRDistance(t *testing.T) {
	stop := RatchetATRTrailingStop(nil, 108, 100, 4, 2)
	if stop == nil || *stop != 100 {
		t.Fatalf("expected initial ATR stop 100, got %v", stop)
	}

	stop = RatchetATRTrailingStop(stop, 120, 100, 4, 2)
	if stop == nil || *stop != 112 {
		t.Fatalf("expected ratcheted ATR stop 112, got %v", stop)
	}
}

func TestEvaluateBarCloseExitBlocksDiscretionaryLossWhenDisabled(t *testing.T) {
	decision := EvaluateBarCloseExit(ExitEvaluationInput{
		CurrentPrice: 95,
		EntryPrice:   100,
		BarsHeld:     5,
		Signal:       "SELL",
		SignalRating: 2,
		MaxBarsHeld:  intPtr(5),
	}, ExitPolicy{
		AllowSellAtLoss:     false,
		TimeStopBars:        5,
		SellOnSignal:        true,
		MinConfidenceToSell: 3.5,
	})

	if decision.Reason != "" {
		t.Fatalf("expected no discretionary close while in loss, got %s", decision.Reason)
	}
}

func TestEvaluateBarCloseExitAllowsSellSignalLossWhenEnabled(t *testing.T) {
	decision := EvaluateBarCloseExit(ExitEvaluationInput{
		CurrentPrice: 95,
		EntryPrice:   100,
		BarsHeld:     2,
		Signal:       "SELL",
		SignalRating: 2,
	}, ExitPolicy{
		AllowSellAtLoss:     true,
		SellOnSignal:        true,
		MinConfidenceToSell: 3.5,
	})

	if decision.Reason != CloseReasonSellSignal {
		t.Fatalf("expected %s, got %s", CloseReasonSellSignal, decision.Reason)
	}
}

func intPtr(v int) *int {
	return &v
}
