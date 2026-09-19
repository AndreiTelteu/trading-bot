package backtest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProgressUpdate is operator telemetry only. It must never influence control
// flow, decisions, fills, digests, or any deterministic backtest output.
type ProgressUpdate struct {
	Phase       string  `json:"phase"`
	Lane        string  `json:"lane,omitempty"`
	BarIndex    int     `json:"bar_index,omitempty"`
	BarTotal    int     `json:"bar_total,omitempty"`
	WindowIndex int     `json:"window_index,omitempty"`
	WindowTotal int     `json:"window_total,omitempty"`
	Fraction    float64 `json:"fraction,omitempty"`
	Message     string  `json:"message,omitempty"`
	ElapsedMS   int64   `json:"elapsed_ms,omitempty"`
	RSSBytes    uint64  `json:"rss_bytes,omitempty"`
}

// ProgressFunc receives non-deterministic telemetry. Callers may drop updates.
type ProgressFunc func(update ProgressUpdate)

// PhaseTimers records wall-clock phase durations for operator diagnosis.
// Values are observational and must not be fed back into trading logic.
type PhaseTimers struct {
	PrepMS             int64   `json:"prep_ms"`
	LaneBaselineMS     int64   `json:"lane_baseline_ms"`
	LaneVolMS          int64   `json:"lane_vol_ms"`
	ValidationMS       int64   `json:"validation_ms"`
	ValidationWindowMS []int64 `json:"validation_window_ms,omitempty"`
	TotalMS            int64   `json:"total_ms"`
}

type phaseClock struct {
	start time.Time
}

func startPhaseClock() phaseClock {
	return phaseClock{start: time.Now()}
}

func (c phaseClock) ms() int64 {
	if c.start.IsZero() {
		return 0
	}
	return time.Since(c.start).Milliseconds()
}

// RateLimitedProgress drops updates more frequent than minInterval, except the
// first update and any update with Fraction >= 1. Safe for concurrent use.
func RateLimitedProgress(minInterval time.Duration, next ProgressFunc) ProgressFunc {
	if next == nil {
		return nil
	}
	if minInterval <= 0 {
		minInterval = 30 * time.Second
	}
	var mu sync.Mutex
	var last time.Time
	var seen atomic.Bool
	return func(update ProgressUpdate) {
		now := time.Now()
		mu.Lock()
		emit := !seen.Load() || update.Fraction >= 1 || last.IsZero() || now.Sub(last) >= minInterval
		if emit {
			last = now
			seen.Store(true)
		}
		mu.Unlock()
		if emit {
			next(update)
		}
	}
}

// StderrProgressWriter writes one JSON object per line to stderr.
func StderrProgressWriter() ProgressFunc {
	return func(update ProgressUpdate) {
		if update.ElapsedMS == 0 {
			// leave zero; callers may set it
		}
		payload, err := json.Marshal(update)
		if err != nil {
			fmt.Fprintf(os.Stderr, "backtest progress: phase=%s lane=%s bars=%d/%d\n", update.Phase, update.Lane, update.BarIndex, update.BarTotal)
			return
		}
		fmt.Fprintf(os.Stderr, "backtest_progress %s\n", payload)
	}
}

// MergeProgress fans an update out to multiple sinks (nil-safe).
func MergeProgress(fns ...ProgressFunc) ProgressFunc {
	filtered := make([]ProgressFunc, 0, len(fns))
	for _, fn := range fns {
		if fn != nil {
			filtered = append(filtered, fn)
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return func(update ProgressUpdate) {
		for _, fn := range filtered {
			fn(update)
		}
	}
}

// AggregateEngineLaneProgress reports the slowest engine lane so operator
// progress cannot reach 100% while the paired lane is still running. It also
// prevents a fast lane from monopolizing a shared rate limiter.
func AggregateEngineLaneProgress(next ProgressFunc) ProgressFunc {
	if next == nil {
		return nil
	}
	var mu sync.Mutex
	fractions := map[string]float64{string(StrategyBaseline): 0, string(StrategyVolSizing): 0}
	return func(update ProgressUpdate) {
		if update.Phase != "engine" || update.Lane == "" {
			next(update)
			return
		}
		mu.Lock()
		fractions[update.Lane] = update.Fraction
		fraction := fractions[string(StrategyBaseline)]
		if candidate := fractions[string(StrategyVolSizing)]; candidate < fraction {
			fraction = candidate
		}
		aggregated := update
		aggregated.Lane = "dual"
		aggregated.Fraction = fraction
		if aggregated.BarTotal > 0 {
			aggregated.BarIndex = int(fraction * float64(aggregated.BarTotal))
		}
		aggregated.Message = "engine_bar_slowest_lane"
		mu.Unlock()
		next(aggregated)
	}
}

func emitProgress(fn ProgressFunc, update ProgressUpdate) {
	if fn == nil {
		return
	}
	fn(update)
}

// engineProgressEvery returns how often the bar loop should emit telemetry.
// Pure function of total bars — no wall clock — so emission cadence is stable.
func engineProgressEvery(totalBars int) int {
	if totalBars <= 0 {
		return 1
	}
	// ~100 updates max per lane in the worst case before rate limiting.
	every := totalBars / 100
	if every < 64 {
		every = 64
	}
	if every > 2048 {
		every = 2048
	}
	return every
}

func currentRSSBytes() uint64 {
	file, err := os.Open("/proc/self/statm")
	if err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		if scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) >= 2 {
				if pages, parseErr := strconv.ParseUint(fields[1], 10, 64); parseErr == nil {
					return pages * uint64(os.Getpagesize())
				}
			}
		}
	}
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.Sys
}
