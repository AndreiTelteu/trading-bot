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
