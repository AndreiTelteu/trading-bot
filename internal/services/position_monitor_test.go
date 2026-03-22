package services

import (
	"sync"
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

// reusable mock stream for position_monitor tests
type mockPriceStream struct {
	mu       sync.Mutex
	channels map[string]chan PriceEvent
	cancels  map[string]int // track cancel calls per symbol
}

func newMockPriceStream() *mockPriceStream {
	return &mockPriceStream{
		channels: make(map[string]chan PriceEvent),
		cancels:  make(map[string]int),
	}
}

func (m *mockPriceStream) Subscribe(symbol string) (<-chan PriceEvent, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ch := make(chan PriceEvent, 8)
	m.channels[symbol] = ch
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			m.mu.Lock()
			m.cancels[symbol]++
			m.mu.Unlock()
			close(ch)
		})
	}, nil
}

func (m *mockPriceStream) send(symbol string, event PriceEvent) {
	m.mu.Lock()
	ch := m.channels[symbol]
	m.mu.Unlock()
	if ch != nil {
		ch <- event
	}
}

func (m *mockPriceStream) cancelCount(symbol string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cancels[symbol]
}

func TestPositionMonitorReconcileSubscribesNewPositions(t *testing.T) {
	testutil.SetupPostgresDB(t)

	stream := newMockPriceStream()
	coord := NewExecutionCoordinator(nil)
	monitor := NewPositionMonitor(stream, coord, time.Minute)
	defer monitor.Close()

	positions := []database.Position{
		{Symbol: "BTC", Status: "open"},
		{Symbol: "ETH", Status: "open"},
	}

	if err := monitor.Reconcile(positions); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// Verify workers were created for both symbols
	monitor.mu.RLock()
	btcWorker := monitor.workers["BTC"]
	ethWorker := monitor.workers["ETH"]
	workerCount := len(monitor.workers)
	monitor.mu.RUnlock()

	if workerCount != 2 {
		t.Fatalf("expected 2 workers, got %d", workerCount)
	}
	if btcWorker == nil {
		t.Fatal("expected BTC worker to exist")
	}
	if ethWorker == nil {
		t.Fatal("expected ETH worker to exist")
	}

	// Verify stream subscriptions were made
	stream.mu.Lock()
	_, btcSub := stream.channels["BTC"]
	_, ethSub := stream.channels["ETH"]
	stream.mu.Unlock()

	if !btcSub {
		t.Fatal("expected stream subscription for BTC")
	}
	if !ethSub {
		t.Fatal("expected stream subscription for ETH")
	}
}

func TestPositionMonitorReconcileUnsubscribesClosedPositions(t *testing.T) {
	testutil.SetupPostgresDB(t)

	stream := newMockPriceStream()
	coord := NewExecutionCoordinator(nil)
	monitor := NewPositionMonitor(stream, coord, time.Minute)
	defer monitor.Close()

	// Start with two open positions
	positions := []database.Position{
		{Symbol: "BTC", Status: "open"},
		{Symbol: "ETH", Status: "open"},
	}
	if err := monitor.Reconcile(positions); err != nil {
		t.Fatalf("first Reconcile failed: %v", err)
	}

	// Wait briefly for workers to start
	time.Sleep(50 * time.Millisecond)

	// Now reconcile with only BTC — ETH should be unsubscribed
	positions = []database.Position{
		{Symbol: "BTC", Status: "open"},
	}
	if err := monitor.Reconcile(positions); err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}

	// Allow time for cleanup
	time.Sleep(50 * time.Millisecond)

	monitor.mu.RLock()
	_, ethExists := monitor.workers["ETH"]
	_, btcExists := monitor.workers["BTC"]
	workerCount := len(monitor.workers)
	monitor.mu.RUnlock()

	if ethExists {
		t.Fatal("expected ETH worker to be removed after reconcile")
	}
	if !btcExists {
		t.Fatal("expected BTC worker to still exist")
	}
	if workerCount != 1 {
		t.Fatalf("expected 1 worker, got %d", workerCount)
	}

	// Verify ETH cancel was called
	if stream.cancelCount("ETH") != 1 {
		t.Fatalf("expected ETH cancel to be called once, got %d", stream.cancelCount("ETH"))
	}
}

func TestPositionMonitorReconnectResumesMonitoring(t *testing.T) {
	testutil.SetupPostgresDB(t)

	stream := newMockPriceStream()
	coord := NewExecutionCoordinator(nil)
	monitor := NewPositionMonitor(stream, coord, time.Minute)
	defer monitor.Close()

	positions := []database.Position{
		{Symbol: "BTC", Status: "open"},
	}
	if err := monitor.Reconcile(positions); err != nil {
		t.Fatalf("first Reconcile failed: %v", err)
	}

	// Close the stream channel to simulate a disconnect
	stream.mu.Lock()
	ch := stream.channels["BTC"]
	delete(stream.channels, "BTC")
	stream.mu.Unlock()
	close(ch)

	// Wait for the worker goroutine to detect the closed channel and clean up
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		monitor.mu.RLock()
		_, exists := monitor.workers["BTC"]
		monitor.mu.RUnlock()
		if !exists {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Worker should be cleaned up
	monitor.mu.RLock()
	_, exists := monitor.workers["BTC"]
	monitor.mu.RUnlock()
	if exists {
		t.Fatal("expected BTC worker to be cleaned up after channel close")
	}

	// A new Reconcile should re-subscribe
	if err := monitor.Reconcile(positions); err != nil {
		t.Fatalf("second Reconcile failed: %v", err)
	}

	monitor.mu.RLock()
	_, resubscribed := monitor.workers["BTC"]
	monitor.mu.RUnlock()
	if !resubscribed {
		t.Fatal("expected BTC worker to be re-created after Reconcile")
	}

	// Verify a new channel was created
	stream.mu.Lock()
	_, newSub := stream.channels["BTC"]
	stream.mu.Unlock()
	if !newSub {
		t.Fatal("expected new stream subscription for BTC after reconnect")
	}
}

func TestPositionMonitorIsHealthyReturnsFalseWhenStale(t *testing.T) {
	testutil.SetupPostgresDB(t)

	stream := newMockPriceStream()
	coord := NewExecutionCoordinator(nil)
	// Use a very short stale timeout for testing
	monitor := NewPositionMonitor(stream, coord, 100*time.Millisecond)
	defer monitor.Close()

	positions := []database.Position{
		{Symbol: "BTC", Status: "open"},
	}
	if err := monitor.Reconcile(positions); err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	// No events sent yet — lastEvent is zero, should be unhealthy
	if monitor.IsHealthy("BTC") {
		t.Fatal("expected BTC to be unhealthy with no events received")
	}

	// Send an event
	stream.send("BTC", PriceEvent{
		Symbol:    "BTCUSDT",
		MarkPrice: 50000,
		LastPrice: 50000,
		Timestamp: time.Now(),
		Source:    "test",
	})

	// Wait for the event to be processed
	time.Sleep(50 * time.Millisecond)

	// Should now be healthy (within stale threshold)
	if !monitor.IsHealthy("BTC") {
		t.Fatal("expected BTC to be healthy after receiving an event")
	}

	// Wait for stale timeout to expire
	time.Sleep(200 * time.Millisecond)

	// Should now be unhealthy (stale)
	if monitor.IsHealthy("BTC") {
		t.Fatal("expected BTC to be unhealthy after stale timeout")
	}

	// Unknown symbol should also be unhealthy
	if monitor.IsHealthy("UNKNOWN") {
		t.Fatal("expected unknown symbol to be unhealthy")
	}
}
