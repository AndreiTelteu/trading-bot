package backtest

import (
	"math"
	"testing"
	"time"

	"trading-go/internal/services"
)

func TestStage05PortfolioEquityIsIndependentOfPositionMapOrder(t *testing.T) {
	// A large holding plus small holdings makes addition order observable even
	// though every position and mark is identical across attempts.
	values := []float64{1e16, 1, 1, 1, 1, 1, 1, 1}
	var first uint64
	for attempt := 0; attempt < 512; attempt++ {
		ledger := &backtestMemoryLedger{cash: 1, positions: map[string]*positionState{}}
		marks := map[string]float64{}
		for offset := range values {
			index := (offset + attempt) % len(values)
			symbol := string(rune('A' + index))
			ledger.positions[symbol] = &positionState{Size: values[index], EntryPrice: 1}
			marks[symbol] = 1
		}
		got := math.Float64bits(portfolioEquity(ledger, marks))
		if attempt == 0 {
			first = got
		} else if got != first {
			t.Fatalf("same economic holdings produced different equity bits: first=%x attempt=%d got=%x", first, attempt, got)
		}
	}
}

func TestStage05ExposureMetricsAreIndependentOfQuantityMapOrder(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(30 * time.Minute)
	benchmark := stage05Bars(start, []float64{100, 100})
	config := BacktestConfig{Start: start, End: end, TimeframeMinutes: 15, InitialBalance: 1000, BenchmarkSeries: benchmark}
	series := map[string][]services.OHLCV{}
	ledger := &backtestMemoryLedger{cash: 1000, positions: map[string]*positionState{}}
	values := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8}
	for index, value := range values {
		symbol := string(rune('A' + index))
		series[symbol] = stage05Bars(start, []float64{value, value})
		ledger.events = append(ledger.events, backtestLedgerEvent{At: start.Add(time.Millisecond), Symbol: symbol, Side: "buy", Quantity: "1", Price: decimalString(value), Fee: "0"})
	}
	equity := []EquityPoint{{Time: start, Value: 1000}, {Time: time.UnixMilli(benchmark[0].CloseTime), Value: 1000}, {Time: end, Value: 1000}}
	var first uint64
	for attempt := 0; attempt < 512; attempt++ {
		got := computeComparableMetrics(config, ledger, equity, map[string]float64{}, series).MaximumGrossExposure
		if !got.Available {
			t.Fatal("maximum exposure unavailable")
		}
		bits := math.Float64bits(got.Value)
		if attempt == 0 {
			first = bits
		} else if bits != first {
			t.Fatalf("same fill history produced different exposure bits: first=%x attempt=%d got=%x", first, attempt, bits)
		}
	}
}
