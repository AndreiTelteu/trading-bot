package tradingcore_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"trading-go/internal/tradingcore"
)

func TestRiskRejectsMissingPrice(t *testing.T) {
	intent := validIntent(t, "missing-price")
	batch, _ := tradingcore.NewDecisionBatch([]tradingcore.OrderIntent{intent})
	portfolio := riskPortfolio(t, intent.Instrument, "100", nil, tradingcore.RiskState{})
	decision, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), batch, portfolio, riskPolicy(t, "1000", 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Rejected()) != 1 || decision.Rejected()[0].Code != tradingcore.RiskMissingPrice {
		t.Fatalf("missing valuation was not rejected: %+v", decision.Rejected())
	}
}

func TestRiskReservesCumulativeCashIncludingFeesAndSlippage(t *testing.T) {
	first := pricedIntent(t, "opaque-z", testInstrument(t, "btc-usdt"), 1, tradingcore.Buy, "1", "60")
	secondInstrument := testInstrument(t, "eth-usdt")
	secondInstrument.VenueSymbol = "ETHUSDT"
	second := pricedIntent(t, "opaque-a", secondInstrument, 2, tradingcore.Buy, "1", "60")
	batch, _ := tradingcore.NewDecisionBatch([]tradingcore.OrderIntent{second, first})
	portfolio := riskPortfolio(t, first.Instrument, "100", nil, tradingcore.RiskState{})
	policy := riskPolicy(t, "1000", 100, 100)
	decision, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), batch, portfolio, policy)
	if err != nil {
		t.Fatal(err)
	}
	approved := decision.Approved().Intents()
	if len(approved) != 2 || approved[0].Instrument.ID != first.Instrument.ID {
		t.Fatalf("priority/cumulative approval = %+v rejected=%+v", approved, decision.Rejected())
	}
	traces := decision.Trace()
	if traces[0].RequestedQuantity != "1" || traces[1].PreCash == "100" || traces[1].PostCash != "0.001637200000000000" {
		t.Fatalf("cash reservation trace = %+v", traces)
	}
	if approved[1].Quantity.Decimal().String() != "0.633800000000000000" {
		t.Fatalf("second quantity did not include cumulative execution costs: %s", approved[1].Quantity.Decimal().String())
	}
}

func TestRiskCountsSellAndHistoricalTurnover(t *testing.T) {
	instrument := testInstrument(t, "btc-usdt")
	positionID, _ := tradingcore.NewPositionID("position-btc")
	position := tradingcore.Position{ID: positionID, Instrument: instrument, Quantity: mustQuantity(t, "1"), AveragePrice: mustPrice(t, "100"), MarkPrice: mustPrice(t, "100"), OpenedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), RealizedPnL: mustAmount(t, "0")}
	riskState, err := tradingcore.NewRiskStateWithTurnover(mustAmount(t, "100"), mustAmount(t, "0"), mustAmount(t, "0"), mustAmount(t, "0"), mustAmount(t, "40"))
	if err != nil {
		t.Fatal(err)
	}
	intent := pricedIntent(t, "sell", instrument, 1, tradingcore.Sell, "1", "100")
	batch, _ := tradingcore.NewDecisionBatch([]tradingcore.OrderIntent{intent})
	portfolio := riskPortfolio(t, instrument, "0", []tradingcore.Position{position}, riskState)
	policy := riskPolicy(t, "120", 0, 0)
	decision, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), batch, portfolio, policy)
	if err != nil {
		t.Fatal(err)
	}
	if got := decision.Approved().Intents()[0].Quantity.Decimal().String(); got != "0.800000000000000000" {
		t.Fatalf("sell turnover cap = %s", got)
	}
	trace := decision.Trace()[0]
	if trace.PreTurnover != "40.000000000000000000" || trace.PostTurnover != "120.000000000000000000" {
		t.Fatalf("historical sell turnover trace = %+v", trace)
	}
}

func TestRiskPriorityNeverDependsOnOpaqueIDs(t *testing.T) {
	winner := testInstrument(t, "ranked-winner")
	winner.VenueSymbol = "WINUSDT"
	loser := testInstrument(t, "ranked-loser")
	loser.VenueSymbol = "LOSEUSDT"
	for iteration := 0; iteration < 50; iteration++ {
		high := pricedIntent(t, fmt.Sprintf("opaque-%02d-z", iteration), winner, 1, tradingcore.Buy, "1", "10")
		low := pricedIntent(t, fmt.Sprintf("opaque-%02d-a", 49-iteration), loser, 2, tradingcore.Buy, "1", "10")
		batch, _ := tradingcore.NewDecisionBatch([]tradingcore.OrderIntent{low, high})
		portfolio := riskPortfolio(t, winner, "100", nil, tradingcore.RiskState{})
		policy := riskPolicy(t, "1000", 0, 0)
		policy.MaxPositions = 1
		decision, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), batch, portfolio, policy)
		if err != nil {
			t.Fatal(err)
		}
		approved := decision.Approved().Intents()
		if len(approved) != 1 || approved[0].Instrument.ID != winner.ID {
			t.Fatalf("iteration %d selected %+v", iteration, approved)
		}
	}
}

func TestLegacyStrategySkipsExistingPositionWhileRiskSupportsExplicitPyramiding(t *testing.T) {
	fixture := loadParityFixture(t)
	base := parityContext(t, fixture, tradingcore.ExecutionPaper, false)
	instrument := base.Universe().Candidates()[0].Instrument
	positionID, _ := tradingcore.NewPositionID("existing")
	position := tradingcore.Position{ID: positionID, Instrument: instrument, Quantity: mustQuantity(t, "1"), AveragePrice: mustPrice(t, "100"), MarkPrice: mustPrice(t, "100"), OpenedAt: base.DecisionAt().Add(-time.Hour), RealizedPnL: mustAmount(t, "0"), PyramidLayers: 1}
	portfolio := riskPortfolio(t, instrument, fixture.Cash, []tradingcore.Position{position}, tradingcore.RiskState{})
	settings := base.Settings()
	settings["pyramiding_enabled"] = "true"
	snapshot, err := tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{MarketObservedAt: base.MarketObservedAt(), SignalAt: base.SignalAt(), DecisionAt: base.DecisionAt(), Quotes: base.Quotes(), Universe: base.Universe(), Portfolio: portfolio, Settings: settings, Versions: base.Versions()})
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := (tradingcore.LegacyRuleStrategy{}).Decide(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacy.Intents().Intents()) != 0 || legacy.NoActions()[0].Code != "position_exists" {
		t.Fatalf("legacy existing-position behavior changed: %+v", legacy)
	}

	manual := pricedIntent(t, "manual-pyramid", instrument, 1, tradingcore.Buy, "0.1", "100")
	batch, _ := tradingcore.NewDecisionBatch([]tradingcore.OrderIntent{manual})
	policy := riskPolicy(t, "1000", 0, 0)
	policy.PyramidingEnabled, policy.MaxPyramidLayers = true, 2
	decision, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), batch, portfolio, policy)
	if err != nil || len(decision.Approved().Intents()) != 1 {
		t.Fatalf("explicit generic pyramiding rejected: %+v err=%v", decision.Rejected(), err)
	}
}

func TestRiskTraceIsDeeplyImmutableAndCanonicalTraceIncludesFillEconomics(t *testing.T) {
	fixture := loadParityFixture(t)
	snapshot := parityContext(t, fixture, tradingcore.ExecutionPaper, false)
	ledger := &recordingLedger{}
	runner := tradingcore.Orchestrator{Source: staticSource{snapshot, parityPolicy(t, fixture)}, Strategy: tradingcore.LegacyRuleStrategy{}, Risk: tradingcore.PortfolioRiskEngine{}, Broker: tradingcore.NewPaperBroker(tradingcore.NewFixedClock(snapshot.DecisionAt().Add(time.Second)), tradingcore.NewSequenceIDGenerator("audit", 1), tradingcore.CostModel{FeeBPS: fixture.FeeBPS, SlippageBPS: fixture.SlippageBPS, Version: "cost-v1"}), Ledger: ledger}
	result, err := runner.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	trace := result.Risk.Trace()
	trace[0].Checks[0] = "mutated"
	if result.Risk.Trace()[0].Checks[0] == "mutated" {
		t.Fatal("nested risk checks alias immutable result")
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal(result.Trace, &decoded); err != nil {
		t.Fatal(err)
	}
	text := string(result.Trace)
	for _, required := range []string{"\"order_id\":\"order-", "\"BrokerCompleteness\":\"complete\"", "\"ProviderFillID\":\"paper-audit-1\"", "\"CostModelVersion\":\"cost-v1\"", "\"Fee\":"} {
		if !strings.Contains(text, required) {
			t.Fatalf("canonical trace lacks %s: %s", required, text)
		}
	}
}

func TestOrchestratorClassifiesFallbackBoundaryConservatively(t *testing.T) {
	fixture := loadParityFixture(t)
	snapshot := parityContext(t, fixture, tradingcore.ExecutionPaper, false)
	base := tradingcore.Orchestrator{Source: staticSource{snapshot, parityPolicy(t, fixture)}, Strategy: tradingcore.LegacyRuleStrategy{}, Risk: tradingcore.PortfolioRiskEngine{}, Broker: tradingcore.NewPaperBroker(tradingcore.NewFixedClock(snapshot.DecisionAt()), tradingcore.NewSequenceIDGenerator("boundary", 1), tradingcore.CostModel{FeeBPS: fixture.FeeBPS, SlippageBPS: fixture.SlippageBPS, Version: "cost-v1"}), Ledger: &recordingLedger{}}
	base.Source = failingSource{}
	_, err := base.Run(context.Background())
	if !tradingcore.IsPreSubmissionFailure(err) {
		t.Fatalf("source failure classification = %v", err)
	}
	base.Source = staticSource{snapshot, parityPolicy(t, fixture)}
	base.Broker = failingBroker{}
	_, err = base.Run(context.Background())
	if tradingcore.IsPreSubmissionFailure(err) {
		t.Fatalf("broker failure permitted fallback: %v", err)
	}
	base.Broker = tradingcore.NewPaperBroker(tradingcore.NewFixedClock(snapshot.DecisionAt()), tradingcore.NewSequenceIDGenerator("boundary", 1), tradingcore.CostModel{FeeBPS: fixture.FeeBPS, SlippageBPS: fixture.SlippageBPS, Version: "cost-v1"})
	base.Ledger = failingFillLedger{}
	_, err = base.Run(context.Background())
	if tradingcore.IsPreSubmissionFailure(err) {
		t.Fatalf("ledger failure permitted fallback: %v", err)
	}
}

type failingSource struct{}

func (failingSource) DecisionContext(context.Context) (tradingcore.DecisionContext, tradingcore.RiskPolicy, error) {
	return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, errors.New("source failure")
}

type failingBroker struct{}

func (failingBroker) Submit(context.Context, tradingcore.DecisionBatch) (tradingcore.BrokerBatchOutcome, error) {
	return tradingcore.BrokerBatchOutcome{}, errors.New("broker failure")
}

type failingFillLedger struct{}

func (failingFillLedger) RecordBrokerOutcome(context.Context, tradingcore.DecisionBatch, tradingcore.BrokerBatchOutcome) error {
	return errors.New("ledger failure")
}

func pricedIntent(t *testing.T, id string, instrument tradingcore.Instrument, priority int, side tradingcore.OrderSide, quantity, price string) tradingcore.OrderIntent {
	t.Helper()
	intent := validIntent(t, id)
	intent.Instrument, intent.Priority, intent.Side = instrument, priority, side
	intent.Quantity, intent.ReferencePrice = mustQuantity(t, quantity), tradingcore.SomePrice(mustPrice(t, price))
	return intent
}

func riskPortfolio(t *testing.T, instrument tradingcore.Instrument, cash string, positions []tradingcore.Position, state tradingcore.RiskState) tradingcore.PortfolioSnapshot {
	t.Helper()
	account, _ := tradingcore.NewAccountID("account-1")
	portfolio, err := tradingcore.NewPortfolioSnapshot(time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC), account, tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{instrument.QuoteAsset: mustAmount(t, cash)}, positions, nil, state)
	if err != nil {
		t.Fatal(err)
	}
	return portfolio
}

func riskPolicy(t *testing.T, turnover string, fee, slippage int64) tradingcore.RiskPolicy {
	t.Helper()
	return tradingcore.RiskPolicy{Version: "risk-v1", MaxPositions: 5, MaxGrossExposure: mustAmount(t, "10000"), MaxPositionValue: mustAmount(t, "10000"), MaxTurnover: mustAmount(t, turnover), CashReserve: mustAmount(t, "0"), MaxConcurrentOrders: 5, LotSize: mustQuantity(t, "0.0001"), ExecutionCosts: tradingcore.ExecutionCostPolicy{Version: "cost-v1", FeeBPS: fee, AdverseSlippageBPS: slippage}}
}
