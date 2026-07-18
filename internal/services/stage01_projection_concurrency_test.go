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

func TestOperationalMarkRaceCannotOverwriteLedgerEconomics(t *testing.T) {
	for _, scenario := range []string{"buy", "partial_sell", "full_close"} {
		t.Run(scenario, func(t *testing.T) {
			testutil.SetupPostgresDB(t)
			if err := database.SeedData(); err != nil {
				t.Fatal(err)
			}
			service := ledgerpkg.New(database.DB)
			base := ledgerpkg.FillCommand{IdempotencyKey: "open-" + scenario, Symbol: "RACE", Side: "buy", Quantity: accounting.MustParse("2"), RequestedPrice: accounting.MustParse("10"), FillPrice: accounting.MustParse("10"), Fee: accounting.Zero(), FeeType: ledgerpkg.EventTradingFee, Currency: "USDT", ExecutionMode: "paper", Actor: "test", Reason: "fixture"}
			if _, err := service.ApplyFill(context.Background(), base); err != nil {
				t.Fatal(err)
			}
			var stale database.Position
			if err := database.DB.Where("symbol='RACE'").First(&stale).Error; err != nil {
				t.Fatal(err)
			}
			at := time.Now().Add(time.Second)
			start := make(chan struct{})
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, err := updatePositionOperational(stale.ID, at, map[string]interface{}{"current_price": 12.0, "last_mark_price": 12.0, "trailing_stop_price": 11.0})
				errs <- err
			}()
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				command := base
				command.IdempotencyKey = "economic-" + scenario
				switch scenario {
				case "buy":
					command.Quantity = accounting.MustParse("1")
				case "partial_sell":
					command.Side = "sell"
					command.Quantity = accounting.MustParse("1")
					command.FillPrice = accounting.MustParse("11")
					command.RequestedPrice = command.FillPrice
				case "full_close":
					command.Side = "sell"
					command.Quantity = accounting.MustParse("2")
					command.FillPrice = accounting.MustParse("11")
					command.RequestedPrice = command.FillPrice
				}
				_, err := service.ApplyFill(context.Background(), command)
				errs <- err
			}()
			close(start)
			wg.Wait()
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			var got database.Position
			if err := database.DB.First(&got, stale.ID).Error; err != nil {
				t.Fatal(err)
			}
			wantQty, wantBasis, wantStatus := "3", "30", "open"
			if scenario == "partial_sell" {
				wantQty, wantBasis = "1", "10"
			}
			if scenario == "full_close" {
				wantQty, wantBasis, wantStatus = "0", "0", "closed"
			}
			if got.AmountExact == nil || got.AmountExact.String() != wantQty || got.CostBasisExact == nil || got.CostBasisExact.String() != wantBasis || got.Status != wantStatus {
				t.Fatalf("economics overwritten: qty=%v basis=%v status=%s", got.AmountExact, got.CostBasisExact, got.Status)
			}
			stale.CurrentPrice = floatPtr(99)
			stale.Amount = 99
			if err := database.DB.Save(&stale).Error; err == nil {
				t.Fatal("database guard allowed stale full-row economic save")
			}
		})
	}
}

type countingExecutor struct{ calls int }

func (executor *countingExecutor) ExecuteSell(string, float64, float64) (*OrderResponse, error) {
	executor.calls++
	return &OrderResponse{Status: "FILLED", ExecutedQty: 1, Price: 10}, nil
}

func TestExchangeCloseIsFencedBeforeExecutorCall(t *testing.T) {
	testutil.SetupPostgresDB(t)
	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	service := ledgerpkg.New(database.DB)
	opened, err := service.ApplyFill(context.Background(), ledgerpkg.FillCommand{IdempotencyKey: "exchange-position", Symbol: "LIVE", Side: "buy", Quantity: accounting.MustParse("1"), RequestedPrice: accounting.MustParse("10"), FillPrice: accounting.MustParse("10"), Fee: accounting.Zero(), FeeType: ledgerpkg.EventExchangeFee, Currency: "USDT", ExecutionMode: ExecutionModeExchange, VenueID: "test-venue", ProviderOrderID: "provider-order-1", ProviderFillID: "provider-fill-1", Actor: "test", Reason: "authoritative exchange fill fixture"})
	if err != nil {
		t.Fatal(err)
	}
	executor := &countingExecutor{}
	_, err = NewExecutionCoordinator(executor).RequestClose(CloseRequest{PositionID: opened.Position.ID, Reason: "test", RequestedPrice: 11, TriggeredAt: time.Now()})
	if !errors.Is(err, ledgerpkg.ErrExchangeExecutionFenced) {
		t.Fatalf("err=%v", err)
	}
	if executor.calls != 0 {
		t.Fatalf("external executor called %d times", executor.calls)
	}
}

func TestManualExchangeEntryAndExitAreFencedWithoutExchangeAccess(t *testing.T) {
	if _, err := ExecuteBuy(BuyRequest{}); !errors.Is(err, ledgerpkg.ErrExchangeExecutionFenced) {
		t.Fatalf("buy err=%v", err)
	}
	if _, err := ExecuteSell(SellRequest{}); !errors.Is(err, ledgerpkg.ErrExchangeExecutionFenced) {
		t.Fatalf("sell err=%v", err)
	}
}
