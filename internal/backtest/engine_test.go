package backtest

import (
	"testing"
	"time"
	"trading-go/internal/services"
)

func TestAdvanceBenchmarkStateMovesOnlyForwardThroughAvailableCloses(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	state := &symbolState{lastIndex: -1, series: []services.OHLCV{
		{CloseTime: base.Add(time.Minute).UnixMilli(), Close: 10},
		{CloseTime: base.Add(2 * time.Minute).UnixMilli(), Close: 20},
		{CloseTime: base.Add(3 * time.Minute).UnixMilli(), Close: 30},
	}}

	advanceBenchmarkState(state, base.Add(2*time.Minute))
	if state.lastIndex != 1 || state.lastPrice != 20 {
		t.Fatalf("state after first advance = index %d price %v", state.lastIndex, state.lastPrice)
	}
	advanceBenchmarkState(state, base.Add(90*time.Second))
	if state.lastIndex != 1 || state.lastPrice != 20 {
		t.Fatalf("state moved backwards = index %d price %v", state.lastIndex, state.lastPrice)
	}
	advanceBenchmarkState(state, base.Add(4*time.Minute))
	if state.lastIndex != 2 || state.lastPrice != 30 {
		t.Fatalf("state after final advance = index %d price %v", state.lastIndex, state.lastPrice)
	}
}

func TestPrecomputeBarContextsMatchesChronologicalCalculation(t *testing.T) {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := make([]services.OHLCV, 700)
	for i := range bars {
		price := 100 + float64(i)/10
		bars[i] = services.OHLCV{OpenTime: base.Add(time.Duration(i) * 15 * time.Minute).UnixMilli(), CloseTime: base.Add(time.Duration(i+1)*15*time.Minute).UnixMilli() - 1, Open: price, High: price + 2, Low: price - 2, Close: price + .5, Volume: 100 + float64(i)}
	}
	config := BacktestConfig{TimeframeMinutes: 15, AtrPeriod: 14, IndicatorConfig: services.DefaultIndicatorConfig(), IndicatorWeights: map[string]float64{"rsi": 1, "macd": 1, "bollinger": 1, "volume": .5, "momentum": 1}}
	lookback := computeSignalLookback(config)
	index := lookback + 5
	window := buildCandles(bars, index, lookback)
	rating, signal := services.AnalyzeCandlesWithConfig(window, config.IndicatorConfig, config.IndicatorWeights)
	want := barContext{Rating: rating, Signal: signal, Atr: computeAtr(window, config)}

	got := precomputeBarContexts(config, map[string][]services.OHLCV{"BTCUSDT": bars})["BTCUSDT"][bars[index].OpenTime]
	if got != want {
		t.Fatalf("precomputed context = %+v, want %+v", got, want)
	}
}

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
