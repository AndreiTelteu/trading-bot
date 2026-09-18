package tradingcore_test

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"trading-go/internal/tradingcore"
)

// This is adapter parity at the actual executable-strategy boundary: a single
// canonical point-in-time payload must yield the same exact intents regardless
// of whether the consumer is replay, shadow, or paper.
func TestTrendMomentumSharedPlannerAdapterParity(t *testing.T) {
	fixture := loadParityFixture(t)
	input := trendMomentumInputFixture(t, "BTCUSDT")
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, mode := range []tradingcore.ExecutionMode{tradingcore.ExecutionBacktest, tradingcore.ExecutionShadow, tradingcore.ExecutionPaper} {
		snapshot := parityContextWith(t, fixture, mode, map[string]string{"trend_momentum_input": string(raw)})
		result, err := (tradingcore.TrendMomentumStrategy{}).Decide(context.Background(), snapshot)
		if err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
		got := []string{}
		for _, intent := range result.Intents().Intents() {
			got = append(got, intent.Instrument.VenueSymbol+":"+string(intent.Side)+":"+intent.Quantity.Decimal().String())
		}
		if len(got) == 0 {
			t.Fatalf("%s emitted no planned target", mode)
		}
		if want == nil {
			want = got
		} else if !reflect.DeepEqual(want, got) {
			t.Fatalf("mode %s intents=%v want=%v", mode, got, want)
		}
	}
}

func trendMomentumInputFixture(t *testing.T, symbol string) tradingcore.TrendMomentumInput {
	t.Helper()
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := make([]tradingcore.TrendMomentumBar, 0, 16*31)
	for i := 0; i < 16*31; i++ {
		open := start.Add(time.Duration(i) * 15 * time.Minute)
		bars = append(bars, tradingcore.TrendMomentumBar{OpenTime: open, CloseTime: open.Add(15*time.Minute - time.Millisecond), Close: 100 + float64(i)/16})
	}
	parameters := tradingcore.DefaultTrendMomentumParameters()
	parameters["lookback_bars"], parameters["trend_bars"], parameters["regime_bars"] = "20", "20", "20"
	return tradingcore.TrendMomentumInput{Benchmark: bars, Series: map[string][]tradingcore.TrendMomentumBar{symbol: bars}, Members: []tradingcore.TrendMomentumMember{{Symbol: symbol, AssetID: "BTC", ExchangeSymbolID: "btc-usdt", Eligible: true}}, Parameters: parameters}
}
