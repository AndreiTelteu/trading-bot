package pointintime

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"trading-go/internal/services"
)

func TestRuntimeBarsCacheSingleflightAndReuse(t *testing.T) {
	t.Setenv("BACKTEST_DATASET_CACHE_MAX_BYTES", "1048576")
	cache := newRuntimeBarsCache()
	var loads atomic.Int32
	load := func() ([]services.OHLCV, error) {
		loads.Add(1)
		return []services.OHLCV{{OpenTime: 1, CloseTime: 2, Close: 3}}, nil
	}

	const workers = 32
	results := make([][]services.OHLCV, workers)
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := range results {
		go func(index int) {
			defer wg.Done()
			values, err := cache.get("same", load)
			if err != nil {
				t.Errorf("get: %v", err)
				return
			}
			results[index] = values
		}(i)
	}
	wg.Wait()
	if got := loads.Load(); got != 1 {
		t.Fatalf("loads=%d want=1", got)
	}
	for i := 1; i < workers; i++ {
		if &results[i][0] != &results[0][0] {
			t.Fatalf("worker %d received a duplicate backing array", i)
		}
	}
	if cache.hits.Load()+cache.waits.Load() != workers-1 {
		t.Fatalf("hits=%d waits=%d", cache.hits.Load(), cache.waits.Load())
	}
}

func TestRuntimeBarsCacheDoesNotRetainFailures(t *testing.T) {
	t.Setenv("BACKTEST_DATASET_CACHE_MAX_BYTES", "1048576")
	cache := newRuntimeBarsCache()
	var loads int
	if _, err := cache.get("failure", func() ([]services.OHLCV, error) {
		loads++
		return nil, errors.New("broken")
	}); err == nil {
		t.Fatal("expected load failure")
	}
	values, err := cache.get("failure", func() ([]services.OHLCV, error) {
		loads++
		return []services.OHLCV{{OpenTime: 1}}, nil
	})
	if err != nil || len(values) != 1 || loads != 2 {
		t.Fatalf("retry values=%v err=%v loads=%d", values, err, loads)
	}
}

func TestRuntimeBarsCacheIsBoundedByBytes(t *testing.T) {
	t.Setenv("BACKTEST_DATASET_CACHE_MAX_BYTES", "56")
	cache := newRuntimeBarsCache()
	load := func(value int64) func() ([]services.OHLCV, error) {
		return func() ([]services.OHLCV, error) { return []services.OHLCV{{OpenTime: value}}, nil }
	}
	if _, err := cache.get("one", load(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := cache.get("two", load(2)); err != nil {
		t.Fatal(err)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.entries) != 1 || cache.bytes > 56 || cache.entries["two"] == nil {
		t.Fatalf("entries=%d bytes=%d newest=%v", len(cache.entries), cache.bytes, cache.entries["two"] != nil)
	}
}
