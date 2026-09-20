package backtest

import (
	"os"
	"testing"
)

func TestStage05ConcurrencyLimitIsBounded(t *testing.T) {
	old := os.Getenv("BACKTEST_MAX_CONCURRENT_JOBS")
	t.Cleanup(func() { _ = os.Setenv("BACKTEST_MAX_CONCURRENT_JOBS", old) })
	for _, tc := range []struct {
		raw  string
		want int
	}{{"", 1}, {"invalid", 1}, {"0", 1}, {"2", 2}, {"99", 8}} {
		if err := os.Setenv("BACKTEST_MAX_CONCURRENT_JOBS", tc.raw); err != nil {
			t.Fatal(err)
		}
		if got := stage05ConcurrencyLimit(); got != tc.want {
			t.Fatalf("value %q: got %d want %d", tc.raw, got, tc.want)
		}
	}
}
