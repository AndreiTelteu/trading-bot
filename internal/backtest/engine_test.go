package backtest

import (
	"testing"
	"trading-go/internal/services"
)

func TestDetermineExitPriceUsesGapAwareStopFill(t *testing.T) {
	bar := services.OHLCV{Open: 90, High: 105, Low: 85, Close: 95}
	price := determineExitPrice(bar, services.ExitDecision{
		Reason:       services.CloseReasonStopLoss,
		TriggerPrice: 100,
	}, BacktestConfig{})

	if price != 90 {
		t.Fatalf("expected gap-aware stop fill at open 90, got %.2f", price)
	}
}

func TestDetermineExitPriceUsesTriggerWhenTakeProfitHasNoGap(t *testing.T) {
	bar := services.OHLCV{Open: 101, High: 112, Low: 99, Close: 110}
	price := determineExitPrice(bar, services.ExitDecision{
		Reason:       services.CloseReasonTakeProfit,
		TriggerPrice: 108,
	}, BacktestConfig{})

	if price != 108 {
		t.Fatalf("expected take-profit fill at trigger 108, got %.2f", price)
	}
}
