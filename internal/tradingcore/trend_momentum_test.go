package tradingcore_test

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestPreparedTrendMomentumHistoryMatchesRawAndIsCausal(t *testing.T) {
	input := trendMomentumInputFixture(t, "BTCUSDT")
	input.DecisionAt = input.Benchmark[len(input.Benchmark)-1].CloseTime
	want, err := tradingcore.PlanTrendMomentum(input)
	if err != nil {
		t.Fatal(err)
	}

	// Preparing history may include later data, but an earlier decision must
	// remain identical because the prepared path slices strictly as-of time.
	future := append([]tradingcore.TrendMomentumBar(nil), input.Benchmark...)
	start := future[len(future)-1].OpenTime.Add(15 * time.Minute)
	for i := 0; i < 32; i++ {
		open := start.Add(time.Duration(i) * 15 * time.Minute)
		future = append(future, tradingcore.TrendMomentumBar{OpenTime: open, CloseTime: open.Add(15*time.Minute - time.Millisecond), Close: 10000 + float64(i)})
	}
	history := tradingcore.PrepareTrendMomentumHistory(future, map[string][]tradingcore.TrendMomentumBar{"BTCUSDT": future})
	got, err := tradingcore.PlanTrendMomentumWithHistory(input, history)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("prepared planner changed the causal result\nwant=%#v\ngot=%#v", want, got)
	}
}

func TestPreparedTrendMomentumHistorySupportsConcurrentExperiments(t *testing.T) {
	input := trendMomentumInputFixture(t, "BTCUSDT")
	input.DecisionAt = input.Benchmark[len(input.Benchmark)-1].CloseTime
	history := tradingcore.PrepareTrendMomentumHistory(input.Benchmark, input.Series)
	want, err := tradingcore.PlanTrendMomentumWithHistory(input, history)
	if err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 16)
	for i := 0; i < cap(errs); i++ {
		go func() {
			got, planErr := tradingcore.PlanTrendMomentumWithHistory(input, history)
			if planErr != nil {
				errs <- planErr
				return
			}
			if !reflect.DeepEqual(want, got) {
				errs <- fmt.Errorf("concurrent prepared plan differs")
				return
			}
			errs <- nil
		}()
	}
	for i := 0; i < cap(errs); i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
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

func BenchmarkTrendMomentumPreparedHistory(b *testing.B) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := make([]tradingcore.TrendMomentumBar, 0, 60000)
	for i := 0; i < 60000; i++ {
		open := start.Add(time.Duration(i) * 15 * time.Minute)
		bars = append(bars, tradingcore.TrendMomentumBar{OpenTime: open, CloseTime: open.Add(15*time.Minute - time.Millisecond), Close: 100 + float64(i)/1600})
	}
	input := tradingcore.TrendMomentumInput{DecisionAt: bars[len(bars)-1].CloseTime, Benchmark: bars, Series: map[string][]tradingcore.TrendMomentumBar{}, Parameters: tradingcore.DefaultTrendMomentumParameters()}
	for i := 0; i < 8; i++ {
		symbol := fmt.Sprintf("ASSET%dUSDT", i)
		input.Series[symbol] = bars
		input.Members = append(input.Members, tradingcore.TrendMomentumMember{Symbol: symbol, AssetID: fmt.Sprintf("asset-%d", i), ExchangeSymbolID: fmt.Sprintf("symbol-%d", i), Eligible: true})
	}
	history := tradingcore.PrepareTrendMomentumHistory(input.Benchmark, input.Series)
	b.Run("raw_reaggregation", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := tradingcore.PlanTrendMomentum(input); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("prepared_asof", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := tradingcore.PlanTrendMomentumWithHistory(input, history); err != nil {
				b.Fatal(err)
			}
		}
	})
}
