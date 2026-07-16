package services

import (
	"context"
	"errors"
	"testing"
	"time"

	ledgerpkg "trading-go/internal/ledger"
	"trading-go/internal/tradingcore"
)

func TestLiveApplicationRequestUsesApprovedSharedBatchWithConfiguredDimensionsAndRemainsFenced(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	account, _ := tradingcore.NewAccountID("primary")
	currency, _ := tradingcore.NewAssetID("EUR")
	portfolio, err := tradingcore.NewPortfolioSnapshot(at, account, tradingcore.ExecutionFullLive, map[tradingcore.AssetID]tradingcore.SignedAmount{currency: mustCoreAmount("1000")}, nil, nil, tradingcore.RiskState{})
	if err != nil {
		t.Fatal(err)
	}
	limit := mustCoreAmount("10000")
	policy := tradingcore.RiskPolicy{Version: "live-risk-v1", MaxPositions: 5, MaxGrossExposure: limit, MaxPositionValue: limit, MaxTurnover: limit, CashReserve: mustCoreAmount("0"), MaxConcurrentOrders: 5, LotSize: mustCoreQuantity("0.0001"), ExecutionCosts: tradingcore.ExecutionCostPolicy{Version: "live-cost-v1"}}
	request, err := BuildApprovedFencedLiveRequest(context.Background(), "buy", .25, 100, "stable-live-intent", FencedLiveRequestConfig{AccountID: "primary", SettlementCurrency: "EUR", VenueID: "kraken", VenueSymbol: "BTCEUR", BaseAsset: "BTC", Portfolio: portfolio, Policy: policy, At: at})
	if err != nil {
		t.Fatal(err)
	}
	if request.ClientOrderID != "stable-live-intent" || request.Symbol != "BTCEUR" || request.Quantity != "0.250000000000000000" || request.PolicyVersion != "live-risk-v1" {
		t.Fatalf("live request = %+v", request)
	}
	if _, err := ExecuteBuy(BuyRequest{Symbol: "BTCUSDT", Amount: .25, Price: 100, IdempotencyKey: "stable-live-intent"}); !errors.Is(err, ledgerpkg.ErrExchangeExecutionFenced) {
		t.Fatalf("live execution fence = %v", err)
	}
}
