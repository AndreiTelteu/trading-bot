package pointintime

import (
	"container/list"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"trading-go/internal/services"
)

const defaultDatasetCacheBytes int64 = 4 << 30

type DatasetCacheStats struct {
	Hits         uint64 `json:"hits"`
	Misses       uint64 `json:"misses"`
	Waits        uint64 `json:"waits"`
	Loads        uint64 `json:"loads"`
	Evictions    uint64 `json:"evictions"`
	Entries      int    `json:"entries"`
	Bytes        int64  `json:"bytes"`
	MaximumBytes int64  `json:"maximum_bytes"`
}

type manifestVerificationFlight struct {
	done chan struct{}
	err  error
}

type manifestVerificationCache struct {
	mu       sync.Mutex
	verified map[string]struct{}
	inflight map[string]*manifestVerificationFlight
}

var sharedManifestVerifications = &manifestVerificationCache{verified: map[string]struct{}{}, inflight: map[string]*manifestVerificationFlight{}}

func verifyManifestOnce(key string, verify func() error) error {
	c := sharedManifestVerifications
	c.mu.Lock()
	if _, ok := c.verified[key]; ok {
		c.mu.Unlock()
		return nil
	}
	if flight := c.inflight[key]; flight != nil {
		c.mu.Unlock()
		<-flight.done
		return flight.err
	}
	flight := &manifestVerificationFlight{done: make(chan struct{})}
	c.inflight[key] = flight
	c.mu.Unlock()

	flight.err = verify()
	c.mu.Lock()
	delete(c.inflight, key)
	if flight.err == nil {
		if len(c.verified) >= 64 {
			for old := range c.verified {
				delete(c.verified, old)
				break
			}
		}
		c.verified[key] = struct{}{}
	}
	close(flight.done)
	c.mu.Unlock()
	return flight.err
}

type runtimeBarsEntry struct {
	key   string
	bars  []services.OHLCV
	bytes int64
	item  *list.Element
}

type runtimeBarsFlight struct {
	done chan struct{}
	bars []services.OHLCV
	err  error
}

type runtimeBarsCache struct {
	mu        sync.Mutex
	entries   map[string]*runtimeBarsEntry
	inflight  map[string]*runtimeBarsFlight
	lru       *list.List
	bytes     int64
	hits      atomic.Uint64
	misses    atomic.Uint64
	waits     atomic.Uint64
	loads     atomic.Uint64
	evictions atomic.Uint64
}

var sharedRuntimeBars = newRuntimeBarsCache()

func newRuntimeBarsCache() *runtimeBarsCache {
	return &runtimeBarsCache{entries: map[string]*runtimeBarsEntry{}, inflight: map[string]*runtimeBarsFlight{}, lru: list.New()}
}

func runtimeBarsCacheKey(manifest Manifest, series SeriesKey, start, end, asOf time.Time) string {
	parts := []string{manifest.ID, manifest.ContentHash, manifest.DatasetVersion, manifest.KnowledgeCutoff, series.ExchangeSymbolID, series.AssetID, series.Ticker, series.Role, series.Timeframe, canonicalTime(start), canonicalTime(end), canonicalTime(asOf), BarsSchemaVersion}
	return strings.Join(parts, "\x00")
}

func (c *runtimeBarsCache) get(key string, load func() ([]services.OHLCV, error)) ([]services.OHLCV, error) {
	c.mu.Lock()
	if entry := c.entries[key]; entry != nil {
		c.lru.MoveToFront(entry.item)
		c.hits.Add(1)
		bars := entry.bars
		c.mu.Unlock()
		return bars, nil
	}
	if flight := c.inflight[key]; flight != nil {
		c.waits.Add(1)
		c.mu.Unlock()
		<-flight.done
		return flight.bars, flight.err
	}
	c.misses.Add(1)
	flight := &runtimeBarsFlight{done: make(chan struct{})}
	c.inflight[key] = flight
	c.mu.Unlock()

	bars, err := load()
	c.loads.Add(1)
	flight.bars, flight.err = bars, err

	c.mu.Lock()
	delete(c.inflight, key)
	if err == nil {
		c.insertLocked(key, bars, datasetCacheMaximumBytes())
	}
	close(flight.done)
	c.mu.Unlock()
	return bars, err
}

func (c *runtimeBarsCache) insertLocked(key string, bars []services.OHLCV, maximum int64) {
	bytes := int64(len(bars)) * 56 // OHLCV is two int64s and five float64s.
	if maximum <= 0 || bytes > maximum {
		return
	}
	item := c.lru.PushFront(key)
	c.entries[key] = &runtimeBarsEntry{key: key, bars: bars, bytes: bytes, item: item}
	c.bytes += bytes
	for c.bytes > maximum && c.lru.Len() > 0 {
		oldest := c.lru.Back()
		entry := c.entries[oldest.Value.(string)]
		delete(c.entries, entry.key)
		c.lru.Remove(oldest)
		c.bytes -= entry.bytes
		c.evictions.Add(1)
	}
}

func datasetCacheMaximumBytes() int64 {
	raw := strings.TrimSpace(os.Getenv("BACKTEST_DATASET_CACHE_MAX_BYTES"))
	if raw == "" {
		return defaultDatasetCacheBytes
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func RuntimeDatasetCacheStats() DatasetCacheStats {
	c := sharedRuntimeBars
	c.mu.Lock()
	defer c.mu.Unlock()
	return DatasetCacheStats{Hits: c.hits.Load(), Misses: c.misses.Load(), Waits: c.waits.Load(), Loads: c.loads.Load(), Evictions: c.evictions.Load(), Entries: len(c.entries), Bytes: c.bytes, MaximumBytes: datasetCacheMaximumBytes()}
}

// ResetRuntimeDatasetCacheForTesting is intentionally restricted to tests and
// operational test harnesses. Production invalidation is identity-based.
func ResetRuntimeDatasetCacheForTesting() {
	sharedRuntimeBars = newRuntimeBarsCache()
	sharedManifestVerifications = &manifestVerificationCache{verified: map[string]struct{}{}, inflight: map[string]*manifestVerificationFlight{}}
}
