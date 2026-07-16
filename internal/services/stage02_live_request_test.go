package services

import (
	"errors"
	"testing"

	ledgerpkg "trading-go/internal/ledger"
)

func TestLiveApplicationRequestUsesSharedAdapterAndRemainsFenced(t *testing.T) {
	request, err := BuildFencedLiveRequest("buy", "BTCUSDT", .25, 100, "stable-live-intent")
	if err != nil {
		t.Fatal(err)
	}
	if request.ClientOrderID != "stable-live-intent" || request.Symbol != "BTCUSDT" || request.Quantity != "0.25" || request.PolicyVersion != "live-fenced-v1" {
		t.Fatalf("live request = %+v", request)
	}
	if _, err := ExecuteBuy(BuyRequest{Symbol: "BTCUSDT", Amount: .25, Price: 100, IdempotencyKey: "stable-live-intent"}); !errors.Is(err, ledgerpkg.ErrExchangeExecutionFenced) {
		t.Fatalf("live execution fence = %v", err)
	}
}
