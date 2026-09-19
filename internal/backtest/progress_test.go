package backtest

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestEngineProgressEveryBounds(t *testing.T) {
	if got := engineProgressEvery(0); got != 1 {
		t.Fatalf("empty timeline every = %d", got)
	}
	if got := engineProgressEvery(10); got != 64 {
		t.Fatalf("small timeline every = %d want 64", got)
	}
	if got := engineProgressEvery(10000); got != 100 {
		t.Fatalf("mid timeline every = %d want 100", got)
	}
	if got := engineProgressEvery(1_000_000); got != 2048 {
		t.Fatalf("huge timeline every = %d want 2048", got)
	}
}

func TestRateLimitedProgressEmitsFirstAndCompletion(t *testing.T) {
	var count atomic.Int64
	fn := RateLimitedProgress(time.Hour, func(ProgressUpdate) {
		count.Add(1)
	})
	fn(ProgressUpdate{Message: "first", Fraction: 0.1})
	fn(ProgressUpdate{Message: "dropped", Fraction: 0.2})
	fn(ProgressUpdate{Message: "done", Fraction: 1})
	if got := count.Load(); got != 2 {
		t.Fatalf("emits = %d want 2 (first + completion)", got)
	}
}

func TestAggregateEngineLaneProgressReportsSlowestLane(t *testing.T) {
	var updates []ProgressUpdate
	fn := AggregateEngineLaneProgress(func(update ProgressUpdate) {
		updates = append(updates, update)
	})
	fn(ProgressUpdate{Phase: "engine", Lane: string(StrategyBaseline), BarIndex: 80, BarTotal: 100, Fraction: .8})
	fn(ProgressUpdate{Phase: "engine", Lane: string(StrategyVolSizing), BarIndex: 30, BarTotal: 100, Fraction: .3})
	fn(ProgressUpdate{Phase: "engine", Lane: string(StrategyBaseline), BarIndex: 100, BarTotal: 100, Fraction: 1})

	for i, want := range []float64{0, .3, .3} {
		if updates[i].Lane != "dual" || updates[i].Fraction != want || updates[i].BarIndex != int(want*100) {
			t.Fatalf("update %d = %+v, want dual fraction %v", i, updates[i], want)
		}
	}
}
