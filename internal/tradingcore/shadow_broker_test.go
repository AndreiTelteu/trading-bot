package tradingcore

import (
	"context"
	"testing"
	"time"
)

func TestShadowBrokerRejectsAnyApprovedIntent(t *testing.T) {
	at := time.Now().UTC()
	account, _ := NewAccountID("primary")
	base, _ := NewAssetID("SHADOW")
	quote, _ := NewAssetID("USDT")
	venue, _ := NewVenueID("test")
	instrumentID, _ := NewInstrumentID("shadow-usdt")
	instrument, err := NewInstrument(instrumentID, base, quote, venue, "SHADOWUSDT")
	if err != nil {
		t.Fatal(err)
	}
	orderID, _ := NewOrderID("shadow-order")
	key, _ := NewIdempotencyKey("shadow-key")
	decimal, _ := ParseDecimal("1")
	quantity, _ := NewQuantity(decimal)
	intent, err := NewOrderIntent(OrderIntent{ID: orderID, IdempotencyKey: key, AccountID: account, Instrument: instrument, Side: Buy, Type: MarketOrder, Quantity: quantity, SignalAt: at, DecisionAt: at, CreatedAt: at, ExecutionMode: ExecutionShadow}, nil)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := NewDecisionBatch([]OrderIntent{intent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (ShadowBroker{}).Submit(context.Background(), batch); err == nil {
		t.Fatal("shadow broker accepted an approved intent")
	}
}
