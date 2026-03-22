package services

import (
	"sync"
	"testing"
	"time"
	"trading-go/internal/database"
	"trading-go/internal/testutil"
)

type fakePriceStream struct {
	mu       sync.Mutex
	channels map[string]chan PriceEvent
}

func newFakePriceStream() *fakePriceStream {
	return &fakePriceStream{channels: make(map[string]chan PriceEvent)}
}

func (f *fakePriceStream) Subscribe(symbol string) (<-chan PriceEvent, func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan PriceEvent, 8)
	f.channels[symbol] = ch
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			close(ch)
		})
	}, nil
}

func (f *fakePriceStream) send(symbol string, event PriceEvent) {
	f.mu.Lock()
	ch := f.channels[symbol]
	f.mu.Unlock()
	if ch != nil {
		ch <- event
	}
}

func TestPositionMonitorDuplicateTicksClosePositionOnce(t *testing.T) {
	testutil.SetupPostgresDB(t)

	wallet := database.Wallet{Balance: 100, Currency: "USDT"}
	if err := database.DB.Create(&wallet).Error; err != nil {
		t.Fatalf("failed to seed wallet: %v", err)
	}

	entry := 10.0
	current := 10.0
	stop := 9.0
	position := database.Position{
		Symbol:        "TEST",
		Amount:        2,
		AvgPrice:      entry,
		EntryPrice:    &entry,
		CurrentPrice:  &current,
		StopPrice:     &stop,
		ExecutionMode: ExecutionModePaper,
		EntrySource:   EntrySourcePaperTest,
		Status:        "open",
		OpenedAt:      time.Now().Add(-30 * time.Minute),
	}
	if err := database.DB.Create(&position).Error; err != nil {
		t.Fatalf("failed to create position: %v", err)
	}

	stream := newFakePriceStream()
	monitor := NewPositionMonitor(stream, NewExecutionCoordinator(nil), time.Minute)
	if err := monitor.Reconcile([]database.Position{position}); err != nil {
		t.Fatalf("failed to reconcile monitor: %v", err)
	}
	defer monitor.Close()

	event := PriceEvent{
		Symbol:    "TESTUSDT",
		MarkPrice: 8,
		LastPrice: 8,
		Timestamp: time.Now(),
		Source:    "test_tick",
	}
	stream.send("TEST", event)
	stream.send("TEST", event)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var refreshed database.Position
		if err := database.DB.First(&refreshed, position.ID).Error; err != nil {
			t.Fatalf("failed to reload position: %v", err)
		}
		if refreshed.Status == "closed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	var refreshed database.Position
	if err := database.DB.First(&refreshed, position.ID).Error; err != nil {
		t.Fatalf("failed to reload position: %v", err)
	}
	if refreshed.Status != "closed" {
		t.Fatalf("expected position to be closed, got %s", refreshed.Status)
	}
	if refreshed.CloseReason == nil || *refreshed.CloseReason != CloseReasonStopLoss {
		t.Fatalf("expected close reason %s, got %v", CloseReasonStopLoss, refreshed.CloseReason)
	}
	if refreshed.ExitPending {
		t.Fatalf("expected exit_pending to be cleared after close")
	}

	var refreshedWallet database.Wallet
	if err := database.DB.First(&refreshedWallet).Error; err != nil {
		t.Fatalf("failed to reload wallet: %v", err)
	}
	if refreshedWallet.Balance != 116 {
		t.Fatalf("expected wallet balance 116 after one close, got %.2f", refreshedWallet.Balance)
	}

	var orders []database.Order
	if err := database.DB.Where("symbol = ? AND order_type = ?", "TEST", "sell").Find(&orders).Error; err != nil {
		t.Fatalf("failed to query sell orders: %v", err)
	}
	if len(orders) != 1 {
		t.Fatalf("expected exactly one sell order, got %d", len(orders))
	}
	if orders[0].Status != OrderStatusFilled {
		t.Fatalf("expected filled sell order, got %s", orders[0].Status)
	}
}
