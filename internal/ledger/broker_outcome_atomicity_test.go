package ledger_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
	"trading-go/internal/testutil"
	"trading-go/internal/tradingcore"
)

func TestBrokerOutcomeSecondFillFailureRollsBackCompleteBatch(t *testing.T) {
	db := readyBrokerOutcomeDB(t)
	adapter := ledgerpkg.NewContractAdapter(db)
	approved, outcome := brokerOutcomeFixture(t, []outcomeSpec{{id: "btc", symbol: "BTCUSDT", quantities: []string{"0.4", "0.3"}, requested: "1", remaining: "0.3"}})
	writes := 0
	adapter.SetAfterWriteHook(func(stage string) error {
		if stage == "projection" {
			writes++
			if writes == 2 {
				return errors.New("deterministic second-fill failure")
			}
		}
		return nil
	})
	if err := adapter.RecordBrokerOutcome(context.Background(), approved, outcome); err == nil {
		t.Fatal("second fill failure was ignored")
	}
	assertBrokerOutcomeCounts(t, db, 0, 0, 0, 0)
	var wallet database.Wallet
	if err := db.First(&wallet).Error; err != nil || wallet.BalanceExact == nil || wallet.BalanceExact.String() != "400" {
		t.Fatalf("wallet mutated across rollback: %+v err=%v", wallet, err)
	}
}

func TestBrokerOutcomeRetryIsIdempotentAndMultipleFillsShareOrder(t *testing.T) {
	db := readyBrokerOutcomeDB(t)
	adapter := ledgerpkg.NewContractAdapter(db)
	approved, outcome := brokerOutcomeFixture(t, []outcomeSpec{{id: "btc", symbol: "BTCUSDT", quantities: []string{"0.4", "0.3"}, requested: "1", remaining: "0.3"}})
	for attempt := 0; attempt < 2; attempt++ {
		if err := adapter.RecordBrokerOutcome(context.Background(), approved, outcome); err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
	}
	assertBrokerOutcomeCounts(t, db, 1, 2, 2, 1)
	var order database.Order
	if err := db.First(&order).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != string(tradingcore.BrokerPartiallyFilled) || order.RequestedQuantityExact == nil || order.RequestedQuantityExact.String() != "1" || order.ExecutedQuantityExact == nil || order.ExecutedQuantityExact.String() != "0.7" || order.RemainingQuantityExact == nil || order.RemainingQuantityExact.String() != "0.3" {
		t.Fatalf("partial order projection = %+v", order)
	}
	var fills []database.Fill
	if err := db.Order("id").Find(&fills).Error; err != nil {
		t.Fatal(err)
	}
	for _, fill := range fills {
		if fill.OrderID != order.ID || fill.CostModelVersion != "paper-cost-v2" || fill.PolicyVersion != "risk-v7" {
			t.Fatalf("fill identity/version projection = %+v order=%d", fill, order.ID)
		}
	}
	var events []database.LedgerEvent
	if err := db.Where("event_type = ?", ledgerpkg.EventBuyFill).Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		var metadata map[string]interface{}
		if err := json.Unmarshal([]byte(event.MetadataJSON), &metadata); err != nil {
			t.Fatalf("decode ledger metadata: %v (%q)", err, event.MetadataJSON)
		}
		if event.PolicyVersion != "risk-v7" || metadata["cost_model_version"] != "paper-cost-v2" || metadata["risk_policy_version"] != "risk-v7" {
			t.Fatalf("ledger policy/cost metadata = %+v decoded=%v", event, metadata)
		}
	}
}

func TestBrokerOutcomePersistsTwoAcceptedOrdersAtomically(t *testing.T) {
	db := readyBrokerOutcomeDB(t)
	adapter := ledgerpkg.NewContractAdapter(db)
	approved, outcome := brokerOutcomeFixture(t, []outcomeSpec{{id: "btc", symbol: "BTCUSDT", quantities: []string{"1"}, requested: "1"}, {id: "eth", symbol: "ETHUSDT", quantities: []string{"2"}, requested: "2"}})
	if err := adapter.RecordBrokerOutcome(context.Background(), approved, outcome); err != nil {
		t.Fatal(err)
	}
	assertBrokerOutcomeCounts(t, db, 2, 2, 2, 1)
	var orders []database.Order
	if err := db.Order("id").Find(&orders).Error; err != nil {
		t.Fatal(err)
	}
	if orders[0].ClientOrderID == nil || orders[1].ClientOrderID == nil || *orders[0].ClientOrderID == *orders[1].ClientOrderID {
		t.Fatalf("durable order identities = %+v", orders)
	}
}

type outcomeSpec struct {
	id, symbol, requested, remaining string
	quantities                       []string
}

func readyBrokerOutcomeDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := testutil.SetupPostgresDB(t)
	if err := database.SeedData(); err != nil {
		t.Fatal(err)
	}
	return db
}

func assertBrokerOutcomeCounts(t *testing.T, db *gorm.DB, orders, fills, fillEvents, ingestions int64) {
	t.Helper()
	for name, query := range map[string]struct {
		model interface{}
		want  int64
	}{
		"orders": {&database.Order{}, orders}, "fills": {&database.Fill{}, fills}, "ingestions": {&database.BrokerOutcomeIngestion{}, ingestions},
	} {
		var got int64
		if err := db.Model(query.model).Count(&got).Error; err != nil {
			t.Fatal(err)
		}
		if got != query.want {
			t.Fatalf("%s=%d want %d", name, got, query.want)
		}
	}
	var gotFillEvents int64
	if err := db.Model(&database.LedgerEvent{}).Where("event_type IN ?", []string{ledgerpkg.EventBuyFill, ledgerpkg.EventSellFill}).Count(&gotFillEvents).Error; err != nil {
		t.Fatal(err)
	}
	if gotFillEvents != fillEvents {
		t.Fatalf("fill events=%d want %d", gotFillEvents, fillEvents)
	}
}

func brokerOutcomeFixture(t *testing.T, specs []outcomeSpec) (tradingcore.DecisionBatch, tradingcore.BrokerBatchOutcome) {
	t.Helper()
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	account, _ := tradingcore.NewAccountID("primary")
	quote, _ := tradingcore.NewAssetID("USDT")
	venue, _ := tradingcore.NewVenueID("simulated")
	intents := make([]tradingcore.OrderIntent, 0, len(specs))
	accepted := make([]tradingcore.AcceptedOrder, 0, len(specs))
	for rank, spec := range specs {
		baseName := spec.symbol[:len(spec.symbol)-4]
		base, _ := tradingcore.NewAssetID(baseName)
		instrumentID, _ := tradingcore.NewInstrumentID("fixture-" + spec.id)
		instrument, err := tradingcore.NewInstrument(instrumentID, base, quote, venue, spec.symbol)
		if err != nil {
			t.Fatal(err)
		}
		orderID, _ := tradingcore.NewOrderID("order-" + spec.id)
		key, _ := tradingcore.NewIdempotencyKey("intent-" + spec.id)
		quantity := testCoreQuantity(t, spec.requested)
		intent, err := tradingcore.NewOrderIntent(tradingcore.OrderIntent{ID: orderID, IdempotencyKey: key, AccountID: account, Instrument: instrument, Side: tradingcore.Buy, Type: tradingcore.MarketOrder, Quantity: quantity, ReferencePrice: tradingcore.SomePrice(testCorePrice(t, "10")), SignalAt: at, DecisionAt: at, CreatedAt: at, ExecutionMode: tradingcore.ExecutionPaper, Priority: rank + 1, Reason: "atomic fixture", Versions: tradingcore.VersionContext{Strategy: "strategy-v3", Policy: "risk-v7", Model: "model-v1"}, Provenance: tradingcore.Provenance{Source: "fixture", Actor: "test"}}, map[string]string{"cost_policy_version": "paper-cost-v2"})
		if err != nil {
			t.Fatal(err)
		}
		intents = append(intents, intent)
		fillValues := make([]tradingcore.Fill, 0, len(spec.quantities))
		for index, raw := range spec.quantities {
			fillID, _ := tradingcore.NewFillID("fill-" + spec.id + "-" + string(rune('a'+index)))
			fillValues = append(fillValues, tradingcore.Fill{ID: fillID, OrderID: orderID, ProviderFillID: "provider-fill-" + spec.id + "-" + string(rune('a'+index)), Instrument: instrument, Side: tradingcore.Buy, Quantity: testCoreQuantity(t, raw), Price: testCorePrice(t, "10"), Fee: testCoreAmount(t, "0"), FeeAsset: quote, OrderedAt: at, SubmittedAt: at, AcceptedAt: at, FilledAt: at, Versions: intent.Versions, Provenance: tradingcore.Provenance{Source: "paper_broker", Actor: "paper-cost-v2"}, CostModelVersion: "paper-cost-v2"})
		}
		status := tradingcore.BrokerFilled
		remaining := tradingcore.OptionalQuantity{}
		if spec.remaining != "" {
			status = tradingcore.BrokerPartiallyFilled
			remaining = tradingcore.SomeQuantity(testCoreQuantity(t, spec.remaining))
		}
		order, err := tradingcore.NewAcceptedOrder(tradingcore.AcceptedOrder{OrderID: orderID, ProviderOrderID: "provider-order-" + spec.id, Status: status, AcceptedAt: at, Remaining: remaining}, fillValues)
		if err != nil {
			t.Fatal(err)
		}
		accepted = append(accepted, order)
	}
	batch, err := tradingcore.NewDecisionBatch(intents)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := tradingcore.NewBrokerBatchOutcome(tradingcore.OutcomeComplete, accepted, nil)
	if err != nil {
		t.Fatal(err)
	}
	return batch, outcome
}

func testCoreQuantity(t *testing.T, raw string) tradingcore.Quantity {
	t.Helper()
	decimal, err := tradingcore.ParseDecimal(raw)
	if err != nil {
		t.Fatal(err)
	}
	value, err := tradingcore.NewQuantity(decimal)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func testCorePrice(t *testing.T, raw string) tradingcore.Price {
	t.Helper()
	decimal, err := tradingcore.ParseDecimal(raw)
	if err != nil {
		t.Fatal(err)
	}
	value, err := tradingcore.NewPrice(decimal)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func testCoreAmount(t *testing.T, raw string) tradingcore.SignedAmount {
	t.Helper()
	decimal, err := tradingcore.ParseDecimal(raw)
	if err != nil {
		t.Fatal(err)
	}
	value, err := tradingcore.NewSignedAmount(decimal)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
