package services

import (
	"context"
	"errors"
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
	const openingStrategy = "opening-strategy@1#artifact"
	opened, err := ledgerpkg.New(database.DB).ApplyFill(context.Background(), ledgerpkg.FillCommand{IdempotencyKey: "monitor-open", Symbol: "TEST", Side: "buy", Quantity: accounting.MustParse("2"), RequestedPrice: accounting.MustParse("10"), FillPrice: accounting.MustParse("10"), Fee: accounting.Zero(), FeeType: ledgerpkg.EventTradingFee, Currency: "USDT", ExecutionMode: ExecutionModePaper, Actor: "test", Reason: "monitor fixture", OccurredAt: time.Now().Add(-30 * time.Minute), EntrySource: EntrySourcePaperTest, StopPrice: &stop, StrategyVersion: openingStrategy, ModelVersion: "different-model"})
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
	var closingFill database.Fill
	if err := database.DB.Where("position_id=? AND side='sell'", position.ID).First(&closingFill).Error; err != nil {
		t.Fatal(err)
	}
	if closingFill.StrategyVersion != openingStrategy {
		t.Fatalf("close fill strategy=%q want opening identity %q", closingFill.StrategyVersion, openingStrategy)
	}
}

func TestCloseReservationSurvivesRestartAndAppliesExactlyOnce(t *testing.T) {
	testutil.SetupPostgresDB(t)
	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	opened, err := ledgerpkg.New(database.LedgerWriter()).ApplyFill(context.Background(), ledgerpkg.FillCommand{IdempotencyKey: "restart-close-open", Symbol: "RESTART", Side: "buy", Quantity: accounting.MustParse("2"), RequestedPrice: accounting.MustParse("10"), FillPrice: accounting.MustParse("10"), Fee: accounting.Zero(), FeeType: ledgerpkg.EventTradingFee, Currency: "USDT", ExecutionMode: ExecutionModePaper, Actor: "test", Reason: "fixture", OccurredAt: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := NewExecutionCoordinator(nil)
	reservation, duplicate, err := coordinator.reserveClose(CloseRequest{PositionID: opened.Position.ID, Reason: "restart_fixture", RequestedPrice: 11, TriggeredAt: time.Now(), Source: "test"})
	if err != nil || duplicate || reservation.Status != closeRequestPending {
		t.Fatalf("reservation=%+v duplicate=%v err=%v", reservation, duplicate, err)
	}

	// Simulate a process crash after the runtime reservation committed and before
	// the isolated writer performed the economic transaction.
	recovered, err := NewExecutionCoordinator(nil).RecoverPendingCloses(context.Background())
	if err != nil || recovered != 1 {
		t.Fatalf("recovered=%d err=%v", recovered, err)
	}
	if _, err := coordinator.RecoverPendingCloses(context.Background()); err != nil {
		t.Fatalf("idempotent second recovery: %v", err)
	}
	var request database.CloseRequest
	if err := database.DB.First(&request, "id = ?", reservation.ID).Error; err != nil || request.Status != closeRequestApplied {
		t.Fatalf("request status=%s err=%v", request.Status, err)
	}
	// Model a client losing the writer's commit acknowledgement: its stale
	// failure path must not turn a committed close back into retryable work.
	if err := coordinator.recordCloseFailure(reservation.ID, closeRequestRetryable, errors.New("lost commit acknowledgement")); err != nil {
		t.Fatal(err)
	}
	if err := database.DB.First(&request, "id = ?", reservation.ID).Error; err != nil || request.Status != closeRequestApplied {
		t.Fatalf("stale failure overwrote applied close: status=%s err=%v", request.Status, err)
	}
	var sells, fills, events int64
	database.DB.Model(&database.Order{}).Where("client_order_id = ?", reservation.ID).Count(&sells)
	database.DB.Model(&database.Fill{}).Where("ledger_batch_id = ?", reservation.ID).Count(&fills)
	database.DB.Model(&database.LedgerEvent{}).Where("ledger_batch_id = ?", reservation.ID).Count(&events)
	if sells != 1 || fills != 1 || events != 2 {
		t.Fatalf("restart recovery duplicated/stranded close: orders=%d fills=%d events=%d", sells, fills, events)
	}
	var position database.Position
	if err := database.DB.First(&position, opened.Position.ID).Error; err != nil || position.Status != "closed" || position.ExitPending {
		t.Fatalf("position after recovery=%+v err=%v", position, err)
	}
}

func TestDuplicateCloseRequestsReuseOneDurableIdentity(t *testing.T) {
	testutil.SetupPostgresDB(t)
	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	opened, err := ledgerpkg.New(database.LedgerWriter()).ApplyFill(context.Background(), ledgerpkg.FillCommand{IdempotencyKey: "duplicate-close-open", Symbol: "DUP", Side: "buy", Quantity: accounting.MustParse("1"), RequestedPrice: accounting.MustParse("10"), FillPrice: accounting.MustParse("10"), Fee: accounting.Zero(), FeeType: ledgerpkg.EventTradingFee, Currency: "USDT", ExecutionMode: ExecutionModePaper, Actor: "test", Reason: "fixture", OccurredAt: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	coordinator := NewExecutionCoordinator(nil)
	first, err := coordinator.RequestClose(CloseRequest{PositionID: opened.Position.ID, Reason: "manual", RequestedPrice: 11, TriggeredAt: time.Now(), Source: "test"})
	if err != nil || !first.Closed || first.Duplicate {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := coordinator.RequestClose(CloseRequest{PositionID: opened.Position.ID, Reason: "different_later_reason", RequestedPrice: 1, TriggeredAt: time.Now().Add(time.Second), Source: "test"})
	if err != nil || !second.Duplicate || !second.Closed || second.Order.ID != first.Order.ID {
		t.Fatalf("second=%+v first=%+v err=%v", second, first, err)
	}
	var requests, sells int64
	database.DB.Model(&database.CloseRequest{}).Count(&requests)
	database.DB.Model(&database.Order{}).Where("symbol = ? AND order_type = ?", "DUP", "sell").Count(&sells)
	if requests != 1 || sells != 1 {
		t.Fatalf("duplicate close produced requests=%d sells=%d", requests, sells)
	}
}
