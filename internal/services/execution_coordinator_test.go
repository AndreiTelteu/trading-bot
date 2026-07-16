package services

import (
	"context"
	"sync"
	"testing"
	"time"
	"trading-go/internal/accounting"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
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
	if err := database.SeedData(); err != nil {
		t.Fatalf("seed ledger: %v", err)
	}

	current := 10.0
	stop := 9.0
	opened, err := ledgerpkg.New(database.DB).ApplyFill(context.Background(), ledgerpkg.FillCommand{IdempotencyKey: "monitor-open", Symbol: "TEST", Side: "buy", Quantity: accounting.MustParse("2"), RequestedPrice: accounting.MustParse("10"), FillPrice: accounting.MustParse("10"), Fee: accounting.Zero(), FeeType: ledgerpkg.EventTradingFee, Currency: "USDT", ExecutionMode: ExecutionModePaper, Actor: "test", Reason: "monitor fixture", OccurredAt: time.Now().Add(-30 * time.Minute), EntrySource: EntrySourcePaperTest, StopPrice: &stop})
	if err != nil {
		t.Fatalf("open ledger position: %v", err)
	}
	position := opened.Position
	position.CurrentPrice = &current

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
	if refreshedWallet.BalanceExact == nil || refreshedWallet.BalanceExact.String() != "397.973009" {
		t.Fatalf("expected exact wallet balance 397.973009 after one costed round trip, got %v", refreshedWallet.BalanceExact)
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
