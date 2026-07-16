package tradingcore_test

import (
	"context"
	"sync"
	"testing"
	"time"
	"trading-go/internal/tradingcore"
	"trading-go/internal/tradingcore/testkit"
)

func TestDeterministicHelpersAreReproducible(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.FixedZone("fixture", 7200))
	left := testkit.NewFixture(at)
	right := testkit.NewFixture(at)
	if !left.Clock.Now().Equal(right.Clock.Now()) || left.Clock.Now().Location() != time.UTC {
		t.Fatalf("fixed clocks differ: %v and %v", left.Clock.Now(), right.Clock.Now())
	}
	for _, want := range []string{"test-1", "test-2", "test-3"} {
		leftID, leftErr := left.IDs.NewID()
		rightID, rightErr := right.IDs.NewID()
		if leftErr != nil || rightErr != nil || leftID != want || rightID != want {
			t.Fatalf("deterministic ids = (%q, %v), (%q, %v); want %q", leftID, leftErr, rightID, rightErr, want)
		}
	}
}

func TestRandomIDGeneratorProducesDistinctPrefixedIDs(t *testing.T) {
	generator := tradingcore.RandomIDGenerator{Prefix: "order"}
	first, err := generator.NewID()
	if err != nil {
		t.Fatal(err)
	}
	second, err := generator.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || len(first) != len("order-")+36 {
		t.Fatalf("random ids are not distinct UUID-shaped values: %q, %q", first, second)
	}
}

func TestDecisionContextDeeplyIsolatesSourcesAndAccessors(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	instrument := testInstrument(t, "btc-usdt")
	price := mustPrice(t, "100.00")
	volume := mustQuantity(t, "2.5")
	bar := tradingcore.Bar{Instrument: instrument, Interval: time.Minute, OpenTime: at.Add(-time.Minute), CloseTime: at, Open: price, High: price, Low: price, Close: price, Volume: volume}
	candidates := []tradingcore.UniverseCandidate{{Instrument: instrument, Rank: 1, Score: tradingcore.MustDecimal(10, 1), Eligible: true}}
	universe, err := tradingcore.NewUniverseSnapshot(at, "policy-v1", "source", candidates)
	if err != nil {
		t.Fatal(err)
	}
	asset := instrument.QuoteAsset
	cash := map[tradingcore.AssetID]tradingcore.SignedAmount{asset: mustAmount(t, "1000.00")}
	positionID, _ := tradingcore.NewPositionID("position-1")
	positions := []tradingcore.Position{{ID: positionID, Instrument: instrument, Quantity: volume, AveragePrice: price, MarkPrice: price, OpenedAt: at}}
	account, _ := tradingcore.NewAccountID("account-1")
	portfolio, err := tradingcore.NewPortfolioSnapshot(at, account, tradingcore.ExecutionPaper, cash, positions, nil, tradingcore.RiskState{})
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{"entry_percent": "5"}
	bars := map[tradingcore.InstrumentID][]tradingcore.Bar{instrument.ID: {bar}}
	input := tradingcore.DecisionContextInput{MarketObservedAt: at.Add(-2 * time.Minute), SignalAt: at.Add(-time.Minute), DecisionAt: at, Bars: bars, Universe: universe, Portfolio: portfolio, Settings: settings}
	first, err := tradingcore.NewDecisionContext(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := tradingcore.NewDecisionContext(input)
	if err != nil {
		t.Fatal(err)
	}

	settings["entry_percent"] = "99"
	bars[instrument.ID][0] = tradingcore.Bar{}
	candidates[0].Eligible = false
	cash[asset] = mustAmount(t, "1")
	positions[0] = tradingcore.Position{}
	firstSettings := first.Settings()
	firstSettings["entry_percent"] = "77"
	firstBars := first.Bars(instrument.ID)
	firstBars[0] = tradingcore.Bar{}
	firstCandidates := first.Universe().Candidates()
	firstCandidates[0].Eligible = false
	firstCash := first.Portfolio().Cash()
	firstCash[asset] = mustAmount(t, "2")
	firstPositions := first.Portfolio().Positions()
	firstPositions[0] = tradingcore.Position{}

	for name, context := range map[string]tradingcore.DecisionContext{"first": first, "second": second} {
		if context.Settings()["entry_percent"] != "5" {
			t.Fatalf("%s settings were mutated", name)
		}
		if len(context.Bars(instrument.ID)) != 1 || context.Bars(instrument.ID)[0].CloseTime.IsZero() {
			t.Fatalf("%s bars were mutated", name)
		}
		if !context.Universe().Candidates()[0].Eligible {
			t.Fatalf("%s universe was mutated", name)
		}
		if context.Portfolio().Cash()[asset].Decimal().String() != "1000.00" {
			t.Fatalf("%s cash was mutated", name)
		}
		if context.Portfolio().Positions()[0].ID.String() != "position-1" {
			t.Fatalf("%s positions were mutated", name)
		}
	}
}

func TestDecisionContextConcurrentReadersReceiveIndependentCopies(t *testing.T) {
	fixture := testkit.NewFixture(time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC))
	fixture.Settings["mode"] = "fixed"
	contextSnapshot, err := fixture.DecisionContext()
	if err != nil {
		t.Fatal(err)
	}
	const readers = 32
	var wait sync.WaitGroup
	wait.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer wait.Done()
			for j := 0; j < 100; j++ {
				values := contextSnapshot.Settings()
				values["mode"] = "mutated"
				if contextSnapshot.Settings()["mode"] != "fixed" {
					t.Errorf("shared context mutated")
					return
				}
			}
		}()
	}
	wait.Wait()
}

func TestFixtureAndStrategiesCannotShareMutableDecisionState(t *testing.T) {
	fixture := testkit.NewFixture(time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC))
	fixture.Settings["entry_percent"] = "5"
	first, err := fixture.DecisionContext()
	if err != nil {
		t.Fatal(err)
	}
	second, err := fixture.DecisionContext()
	if err != nil {
		t.Fatal(err)
	}
	strategy := strategyMutationProbe{}
	if _, err := strategy.Decide(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if fixture.Settings["entry_percent"] != "5" || first.Settings()["entry_percent"] != "5" || second.Settings()["entry_percent"] != "5" {
		t.Fatalf("strategy mutation escaped snapshot: fixture=%v first=%v second=%v", fixture.Settings, first.Settings(), second.Settings())
	}
	fixture.Settings["entry_percent"] = "10"
	if first.Settings()["entry_percent"] != "5" || second.Settings()["entry_percent"] != "5" {
		t.Fatal("fixture mutation changed existing decisions")
	}
}

func TestExactValuesValidationAndDeterministicOrdering(t *testing.T) {
	if _, err := tradingcore.ParseDecimal("1e-8"); err == nil {
		t.Fatal("exponent notation should be rejected")
	}
	if _, err := tradingcore.NewQuantity(tradingcore.MustDecimal(0, 0)); err == nil {
		t.Fatal("zero quantity should be rejected")
	}
	if got := tradingcore.MustDecimal(123456789, 8).String(); got != "1.23456789" {
		t.Fatalf("exact decimal = %s", got)
	}
	large, err := tradingcore.ParseDecimal("123456789012345678901234.123456789012345678")
	if err != nil || large.String() != "123456789012345678901234.123456789012345678" {
		t.Fatalf("arbitrary precision decimal=%s err=%v", large.String(), err)
	}
	first := validIntent(t, "order-b")
	second := validIntent(t, "order-a")
	batch, err := tradingcore.NewDecisionBatch([]tradingcore.OrderIntent{first, second})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Intents()[0].ID.String() != "order-a" {
		t.Fatalf("intents are not deterministically ordered: %+v", batch.Intents())
	}
}

func TestBrokerAndAtomicLedgerOutcomesAreTypedOrderedAndIsolated(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	instrument := testInstrument(t, "btc-usdt")
	orderID, _ := tradingcore.NewOrderID("order-a")
	fillID, _ := tradingcore.NewFillID("fill-a")
	fill := tradingcore.Fill{ID: fillID, OrderID: orderID, ProviderFillID: "provider-fill-a", Instrument: instrument, Side: tradingcore.Buy, Quantity: mustQuantity(t, "0.5"), Price: mustPrice(t, "100.00"), Fee: mustAmount(t, "0.10"), FeeAsset: instrument.QuoteAsset, OrderedAt: at, SubmittedAt: at.Add(time.Second), AcceptedAt: at.Add(2 * time.Second), FilledAt: at.Add(3 * time.Second)}
	accepted, err := tradingcore.NewAcceptedOrder(tradingcore.AcceptedOrder{OrderID: orderID, ProviderOrderID: "provider-order-a", Status: tradingcore.BrokerPartiallyFilled, AcceptedAt: fill.AcceptedAt, Remaining: tradingcore.SomeQuantity(mustQuantity(t, "0.5"))}, []tradingcore.Fill{fill})
	if err != nil {
		t.Fatal(err)
	}
	rejectedID, _ := tradingcore.NewOrderID("order-b")
	outcome, err := tradingcore.NewBrokerBatchOutcome(tradingcore.OutcomeComplete, []tradingcore.AcceptedOrder{accepted}, []tradingcore.OrderRejection{{OrderID: rejectedID, Code: "exposure_limit", EvaluatedAt: at, PolicyVersion: "risk-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	returnedFills := outcome.Accepted()[0].Fills()
	returnedFills[0] = tradingcore.Fill{}
	if outcome.Accepted()[0].Fills()[0].ID.String() != "fill-a" {
		t.Fatal("broker fill accessor aliases outcome")
	}

	account, _ := tradingcore.NewAccountID("account-1")
	key, _ := tradingcore.NewIdempotencyKey("event-key-a")
	eventAID, _ := tradingcore.NewEventID("event-a")
	eventBID, _ := tradingcore.NewEventID("event-b")
	posting := tradingcore.LedgerPosting{Dimension: tradingcore.PostingCash, AssetID: instrument.QuoteAsset, Amount: mustAmount(t, "-50.10")}
	eventA, err := tradingcore.NewLedgerEvent(tradingcore.LedgerEvent{ID: eventAID, IdempotencyKey: key, Type: tradingcore.LedgerCashAdjusted, AccountID: account, OrderID: orderID, FillID: fillID, VenueID: instrument.Venue, OccurredAt: at, RecordedAt: at.Add(time.Second), Versions: tradingcore.VersionContext{Strategy: "strategy-v1", Settings: "settings-v1"}, Provenance: tradingcore.Provenance{Source: "broker", Actor: "adapter", Reason: "fill"}}, []tradingcore.LedgerPosting{posting})
	if err != nil {
		t.Fatal(err)
	}
	keyB, _ := tradingcore.NewIdempotencyKey("event-key-b")
	eventB, err := tradingcore.NewLedgerEvent(tradingcore.LedgerEvent{ID: eventBID, IdempotencyKey: keyB, Type: tradingcore.LedgerCashAdjusted, AccountID: account, OccurredAt: at, RecordedAt: at}, []tradingcore.LedgerPosting{posting})
	if err != nil {
		t.Fatal(err)
	}
	batchKey, _ := tradingcore.NewIdempotencyKey("batch-key")
	batch, err := tradingcore.NewLedgerBatch(batchKey, []tradingcore.LedgerEvent{eventB, eventA})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Events()[0].ID.String() != "event-a" {
		t.Fatal("ledger events are not deterministic")
	}
	appendOutcome, err := tradingcore.NewLedgerAppendOutcome(tradingcore.LedgerAppended, batchKey, []tradingcore.EventID{eventBID, eventAID}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ids := appendOutcome.EventIDs()
	ids[0] = eventBID
	if appendOutcome.EventIDs()[0].String() != "event-a" {
		t.Fatal("ledger append outcome aliases event ids")
	}
}

func testInstrument(t *testing.T, id string) tradingcore.Instrument {
	t.Helper()
	instrumentID, _ := tradingcore.NewInstrumentID(id)
	base, _ := tradingcore.NewAssetID("asset-btc")
	quote, _ := tradingcore.NewAssetID("asset-usdt")
	venue, _ := tradingcore.NewVenueID("venue-binance")
	return tradingcore.Instrument{ID: instrumentID, BaseAsset: base, QuoteAsset: quote, Venue: venue, VenueSymbol: "BTCUSDT"}
}
func mustPrice(t *testing.T, value string) tradingcore.Price {
	t.Helper()
	decimal, err := tradingcore.ParseDecimal(value)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tradingcore.NewPrice(decimal)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func mustQuantity(t *testing.T, value string) tradingcore.Quantity {
	t.Helper()
	decimal, err := tradingcore.ParseDecimal(value)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tradingcore.NewQuantity(decimal)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func mustAmount(t *testing.T, value string) tradingcore.SignedAmount {
	t.Helper()
	decimal, err := tradingcore.ParseDecimal(value)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tradingcore.NewSignedAmount(decimal)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func validIntent(t *testing.T, id string) tradingcore.OrderIntent {
	t.Helper()
	orderID, _ := tradingcore.NewOrderID(id)
	key, _ := tradingcore.NewIdempotencyKey("key-" + id)
	account, _ := tradingcore.NewAccountID("account-1")
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return tradingcore.OrderIntent{ID: orderID, IdempotencyKey: key, AccountID: account, Instrument: testInstrument(t, "btc-usdt"), Side: tradingcore.Buy, Type: tradingcore.MarketOrder, Quantity: mustQuantity(t, "1"), SignalAt: at, DecisionAt: at, CreatedAt: at, ExecutionMode: tradingcore.ExecutionPaper}
}

type strategyStub struct{}

func (strategyStub) Decide(context.Context, tradingcore.DecisionContext) (tradingcore.DecisionBatch, error) {
	return tradingcore.NewDecisionBatch(nil)
}

type strategyMutationProbe struct{}

func (strategyMutationProbe) Decide(_ context.Context, snapshot tradingcore.DecisionContext) (tradingcore.DecisionBatch, error) {
	settings := snapshot.Settings()
	settings["entry_percent"] = "999"
	for id := range snapshot.Quotes() {
		bars := snapshot.Bars(id)
		if len(bars) > 0 {
			bars[0] = tradingcore.Bar{}
		}
	}
	portfolio := snapshot.Portfolio()
	cash := portfolio.Cash()
	for asset := range cash {
		delete(cash, asset)
	}
	universe := snapshot.Universe().Candidates()
	if len(universe) > 0 {
		universe[0].Eligible = false
	}
	return tradingcore.NewDecisionBatch(nil)
}

type riskStub struct{}

func (riskStub) Evaluate(context.Context, tradingcore.DecisionBatch, tradingcore.PortfolioSnapshot, tradingcore.RiskPolicy) (tradingcore.RiskDecision, error) {
	return tradingcore.RiskDecision{}, nil
}

type brokerStub struct{}

func (brokerStub) Submit(context.Context, tradingcore.DecisionBatch) (tradingcore.BrokerBatchOutcome, error) {
	return tradingcore.BrokerBatchOutcome{}, nil
}

type marketDataStub struct{}

func (marketDataStub) Bars(context.Context, tradingcore.BarsRequest) (tradingcore.BarSeries, error) {
	return tradingcore.BarSeries{}, nil
}
func (marketDataStub) Quote(context.Context, tradingcore.Instrument, time.Time) (tradingcore.Quote, error) {
	return tradingcore.Quote{}, nil
}
func (marketDataStub) BenchmarkBars(context.Context, tradingcore.BarsRequest) (tradingcore.BarSeries, error) {
	return tradingcore.BarSeries{}, nil
}
func (marketDataStub) Coverage(context.Context, tradingcore.BarsRequest) (tradingcore.Coverage, error) {
	return tradingcore.Coverage{}, nil
}

type universeStub struct{}

func (universeStub) Candidates(context.Context, time.Time) (tradingcore.UniverseSnapshot, error) {
	return tradingcore.UniverseSnapshot{}, nil
}

type ledgerStub struct{}

func (ledgerStub) AppendAtomic(context.Context, tradingcore.LedgerBatch) (tradingcore.LedgerAppendOutcome, error) {
	return tradingcore.LedgerAppendOutcome{}, nil
}
func (ledgerStub) Events(context.Context, tradingcore.AccountID, time.Time) ([]tradingcore.LedgerEvent, error) {
	return nil, nil
}
func (ledgerStub) Reconcile(context.Context, tradingcore.PortfolioSnapshot) (tradingcore.ReconciliationReport, error) {
	return tradingcore.ReconciliationReport{}, nil
}

var _ tradingcore.Strategy = strategyStub{}
var _ tradingcore.RiskEngine = riskStub{}
var _ tradingcore.Broker = brokerStub{}
var _ tradingcore.MarketDataSource = marketDataStub{}
var _ tradingcore.UniverseProvider = universeStub{}
var _ tradingcore.Ledger = ledgerStub{}
