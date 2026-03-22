package services

import (
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

func TestStreamSupervisorShouldFallbackWhenStreamUnhealthy(t *testing.T) {
	testutil.SetupPostgresDB(t)

	// Enable stream exits
	database.DB.Create(&database.Setting{Key: "stream_exit_enabled", Value: "true"})

	stream := newMockPriceStream()
	coord := NewExecutionCoordinator(nil)
	monitor := NewPositionMonitor(stream, coord, 100*time.Millisecond)
	defer monitor.Close()

	supervisor := &StreamSupervisor{monitor: monitor, started: true}

	// No positions subscribed — should fallback for any symbol
	if !supervisor.ShouldFallback("BTC") {
		t.Fatal("expected ShouldFallback=true when no workers exist for symbol")
	}

	// Subscribe BTC but don't send any events — lastEvent is zero, so unhealthy
	positions := []database.Position{
		{Symbol: "BTC", Status: "open"},
	}
	if err := monitor.Reconcile(positions); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	if !supervisor.ShouldFallback("BTC") {
		t.Fatal("expected ShouldFallback=true when no events have been received")
	}

	// Send an event to make BTC healthy
	stream.send("BTC", PriceEvent{
		Symbol:    "BTCUSDT",
		MarkPrice: 50000,
		LastPrice: 50000,
		Timestamp: time.Now(),
		Source:    "test",
	})
	time.Sleep(50 * time.Millisecond)

	if supervisor.ShouldFallback("BTC") {
		t.Fatal("expected ShouldFallback=false when stream is healthy")
	}

	// Wait for stale timeout
	time.Sleep(200 * time.Millisecond)

	if !supervisor.ShouldFallback("BTC") {
		t.Fatal("expected ShouldFallback=true after stream becomes stale")
	}

	// Nil supervisor should always fallback
	var nilSupervisor *StreamSupervisor
	if !nilSupervisor.ShouldFallback("BTC") {
		t.Fatal("expected nil supervisor to always fallback")
	}
}

func TestStreamSupervisorEnabledReadsSettings(t *testing.T) {
	testutil.SetupPostgresDB(t)

	stream := newMockPriceStream()
	coord := NewExecutionCoordinator(nil)
	monitor := NewPositionMonitor(stream, coord, time.Minute)
	defer monitor.Close()

	supervisor := &StreamSupervisor{monitor: monitor, started: true}

	// Default (no setting) — Enabled() defaults to true per getSettingBool default
	if !supervisor.Enabled() {
		t.Fatal("expected Enabled=true when no setting exists (default)")
	}

	// Explicitly disable
	database.DB.Create(&database.Setting{Key: "stream_exit_enabled", Value: "false"})
	if supervisor.Enabled() {
		t.Fatal("expected Enabled=false when stream_exit_enabled=false")
	}

	// When disabled, ShouldFallback should always return true
	if !supervisor.ShouldFallback("BTC") {
		t.Fatal("expected ShouldFallback=true when stream exits are disabled")
	}

	// Re-enable
	database.DB.Model(&database.Setting{}).Where("key = ?", "stream_exit_enabled").Update("value", "true")
	if !supervisor.Enabled() {
		t.Fatal("expected Enabled=true after re-enabling")
	}
}
