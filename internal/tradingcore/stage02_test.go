package tradingcore_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"trading-go/internal/tradingcore"
)

type parityFixture struct {
	DecisionAt    string `json:"decision_at"`
	Symbol        string `json:"symbol"`
	Price         string `json:"price"`
	Cash          string `json:"cash"`
	EntryPercent  string `json:"entry_percent"`
	Signal        string `json:"signal"`
	Rating        string `json:"rating"`
	PolicyVersion string `json:"policy_version"`
	FeeBPS        int64  `json:"fee_bps"`
	SlippageBPS   int64  `json:"slippage_bps"`
}

func TestFixtureParityAcrossBacktestPaperAndFencedLive(t *testing.T) {
	fixture := loadParityFixture(t)
	snapshot := parityContext(t, fixture, tradingcore.ExecutionPaper, false)
	strategy := tradingcore.LegacyRuleStrategy{IDs: tradingcore.NewSequenceIDGenerator("decision", 1)}
	strategyResult, err := strategy.Decide(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	policy := parityPolicy(t, fixture)
	riskResult, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), strategyResult.Intents(), snapshot.Portfolio(), policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(riskResult.Approved().Intents()) != 1 || len(riskResult.Rejected()) != 0 {
		t.Fatalf("risk result approved=%d rejected=%v", len(riskResult.Approved().Intents()), riskResult.Rejected())
	}

	clock := tradingcore.NewFixedClock(mustTime(t, fixture.DecisionAt).Add(time.Second))
	backtest := tradingcore.NewBacktestBroker(clock, tradingcore.NewSequenceIDGenerator("broker", 1), tradingcore.CostModel{FeeBPS: fixture.FeeBPS, SlippageBPS: fixture.SlippageBPS, Version: "cost-v1"})
	paper := tradingcore.NewPaperBroker(clock, tradingcore.NewSequenceIDGenerator("broker", 1), tradingcore.CostModel{FeeBPS: fixture.FeeBPS, SlippageBPS: fixture.SlippageBPS, Version: "cost-v1"})
	backtestOutcome, err := backtest.Submit(context.Background(), riskResult.Approved())
	if err != nil {
		t.Fatal(err)
	}
	paperOutcome, err := paper.Submit(context.Background(), riskResult.Approved())
	if err != nil {
		t.Fatal(err)
	}
	left, right := backtestOutcome.Accepted()[0].Fills()[0], paperOutcome.Accepted()[0].Fills()[0]
	if left.Quantity.Decimal().String() != right.Quantity.Decimal().String() || left.Price.Decimal().String() != right.Price.Decimal().String() || left.Fee.Decimal().String() != right.Fee.Decimal().String() {
		t.Fatalf("broker economics differ: %+v %+v", left, right)
	}

	live := tradingcore.LiveBroker{}
	requests, err := live.BuildRequests(riskResult.Approved())
	if err != nil {
		t.Fatal(err)
	}
	if len(requests) != 1 || requests[0].ClientOrderID != riskResult.Approved().Intents()[0].IdempotencyKey.String() || requests[0].Quantity != riskResult.Approved().Intents()[0].Quantity.Decimal().String() || requests[0].PolicyVersion != fixture.PolicyVersion {
		t.Fatalf("live request = %+v", requests)
	}
	liveOutcome, err := live.Submit(context.Background(), riskResult.Approved())
	if err != nil {
		t.Fatal(err)
	}
	if len(liveOutcome.Accepted()) != 0 || len(liveOutcome.Rejected()) != 1 || liveOutcome.Rejected()[0].Code != tradingcore.ExchangeExecutionFenced {
		t.Fatalf("live fence outcome = %+v", liveOutcome)
	}
}

func TestLegacyGateOrderRolloutAndShadowObservationCannotAuthorize(t *testing.T) {
	fixture := loadParityFixture(t)
	snapshot := parityContext(t, fixture, tradingcore.ExecutionPaper, true)
	result, err := (tradingcore.LegacyRuleStrategy{IDs: tradingcore.NewSequenceIDGenerator("decision", 1)}).Decide(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Intents().Intents()) != 1 {
		t.Fatalf("shadow model changed rule intent: %+v", result.NoActions())
	}

	settingsSnapshot := parityContextWith(t, fixture, tradingcore.ExecutionPaper, map[string]string{"analysis_error.btc-usdt": "true", "auto_trade_enabled": "false", "universe_risk_off": "true"})
	ordered, err := (tradingcore.LegacyRuleStrategy{IDs: tradingcore.NewSequenceIDGenerator("decision", 1)}).Decide(context.Background(), settingsSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if got := ordered.NoActions()[0].Code; got != "analysis_error" {
		t.Fatalf("first rejection = %s", got)
	}

	cases := []struct {
		mode         tradingcore.ExecutionMode
		state        string
		available    bool
		fallback     string
		use, blocked bool
	}{
		{tradingcore.ExecutionResearch, "full_live", true, "rule_based", false, false},
		{tradingcore.ExecutionShadow, "full_live", true, "rule_based", false, false},
		{tradingcore.ExecutionPaper, "paper", true, "none", true, false},
		{tradingcore.ExecutionLimitedLive, "paper", true, "none", false, true},
		{tradingcore.ExecutionFullLive, "full_live", false, "none", false, true},
		{tradingcore.ExecutionFullLive, "rollback", true, "rule_based", false, false},
	}
	for _, tc := range cases {
		authority := tradingcore.ResolveModelAuthority(tc.mode, tc.state, tc.fallback, tc.available)
		if authority.UseModel != tc.use || authority.Blocked != tc.blocked {
			t.Errorf("authority(%s,%s)=%+v", tc.mode, tc.state, authority)
		}
	}
}

func TestRiskScenarioOrderingCapsPyramidingAndPendingConflict(t *testing.T) {
	fixture := loadParityFixture(t)
	base := parityContext(t, fixture, tradingcore.ExecutionPaper, false)
	strategyResult, err := (tradingcore.LegacyRuleStrategy{IDs: tradingcore.NewSequenceIDGenerator("decision", 1)}).Decide(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	intent := strategyResult.Intents().Intents()[0]
	position := tradingcore.Position{ID: mustPositionID(t, "position-1"), Instrument: intent.Instrument, Quantity: mustQuantity2(t, "1"), AveragePrice: mustPrice2(t, "100"), MarkPrice: mustPrice2(t, "100"), OpenedAt: mustTime(t, fixture.DecisionAt).Add(-time.Hour), RealizedPnL: mustAmount2(t, "0")}
	pending := tradingcore.PendingOrder{ID: mustOrderID(t, "pending-1"), Instrument: intent.Instrument, Side: tradingcore.Buy, Remaining: mustQuantity2(t, "0.1"), SubmittedAt: mustTime(t, fixture.DecisionAt).Add(-time.Minute)}
	portfolio, err := tradingcore.NewPortfolioSnapshot(mustTime(t, fixture.DecisionAt), mustAccountID(t, "primary"), tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{intent.Instrument.QuoteAsset: mustAmount2(t, "1000")}, []tradingcore.Position{position}, []tradingcore.PendingOrder{pending}, tradingcore.RiskState{})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), strategyResult.Intents(), portfolio, parityPolicy(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	if got := decision.Rejected()[0].Code; got != tradingcore.RiskPendingConflict {
		t.Fatalf("rejection order got %s", got)
	}

	portfolio, _ = tradingcore.NewPortfolioSnapshot(mustTime(t, fixture.DecisionAt), mustAccountID(t, "primary"), tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{intent.Instrument.QuoteAsset: mustAmount2(t, "35")}, nil, nil, tradingcore.RiskState{})
	decision, err = (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), strategyResult.Intents(), portfolio, parityPolicy(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(decision.Approved().Intents()) != 1 || decision.Approved().Intents()[0].Quantity.Decimal().String() != "0.349000000000000000" {
		t.Fatalf("cash cap = %+v rejected=%v", decision.Approved().Intents(), decision.Rejected())
	}

	portfolio, _ = tradingcore.NewPortfolioSnapshot(mustTime(t, fixture.DecisionAt), mustAccountID(t, "primary"), tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{intent.Instrument.QuoteAsset: mustAmount2(t, "1000")}, []tradingcore.Position{position}, nil, tradingcore.RiskState{})
	disabled := parityPolicy(t, fixture)
	decision, _ = (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), strategyResult.Intents(), portfolio, disabled)
	if decision.Rejected()[0].Code != tradingcore.RiskPositionExists {
		t.Fatalf("pyramiding disabled = %s", decision.Rejected()[0].Code)
	}
	enabled := parityPolicy(t, fixture)
	enabled.PyramidingEnabled = true
	enabled.MaxPyramidLayers = 2
	decision, _ = (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), strategyResult.Intents(), portfolio, enabled)
	if len(decision.Approved().Intents()) != 1 {
		t.Fatalf("pyramiding enabled rejected: %+v", decision.Rejected())
	}
	position.PyramidLayers = 2
	portfolio, _ = tradingcore.NewPortfolioSnapshot(mustTime(t, fixture.DecisionAt), mustAccountID(t, "primary"), tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{intent.Instrument.QuoteAsset: mustAmount2(t, "1000")}, []tradingcore.Position{position}, nil, tradingcore.RiskState{})
	decision, _ = (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), strategyResult.Intents(), portfolio, enabled)
	if decision.Rejected()[0].Code != tradingcore.RiskPyramidLayers {
		t.Fatalf("pyramid layer cap = %s", decision.Rejected()[0].Code)
	}

	zeroCash, _ := tradingcore.NewPortfolioSnapshot(mustTime(t, fixture.DecisionAt), mustAccountID(t, "primary"), tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{intent.Instrument.QuoteAsset: mustAmount2(t, "0")}, nil, nil, tradingcore.RiskState{})
	decision, _ = (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), strategyResult.Intents(), zeroCash, parityPolicy(t, fixture))
	if decision.Rejected()[0].Code != tradingcore.RiskInsufficientCash {
		t.Fatalf("zero cash = %s", decision.Rejected()[0].Code)
	}

	alt := mustInstrumentNamed(t, "eth-usdt", "ETHUSDT", "ETH")
	altPosition := tradingcore.Position{ID: mustPositionID(t, "position-eth"), Instrument: alt, Quantity: mustQuantity2(t, "10"), AveragePrice: mustPrice2(t, "100"), MarkPrice: mustPrice2(t, "100"), OpenedAt: mustTime(t, fixture.DecisionAt).Add(-time.Hour), RealizedPnL: mustAmount2(t, "0")}
	withAlt, _ := tradingcore.NewPortfolioSnapshot(mustTime(t, fixture.DecisionAt), mustAccountID(t, "primary"), tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{intent.Instrument.QuoteAsset: mustAmount2(t, "1000")}, []tradingcore.Position{altPosition}, nil, tradingcore.RiskState{})
	maxOne := parityPolicy(t, fixture)
	maxOne.MaxPositions = 1
	decision, _ = (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), strategyResult.Intents(), withAlt, maxOne)
	if decision.Rejected()[0].Code != tradingcore.RiskMaxPositions {
		t.Fatalf("max positions = %s", decision.Rejected()[0].Code)
	}
	totalCap := parityPolicy(t, fixture)
	totalCap.MaxPositions = 5
	decision, _ = (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), strategyResult.Intents(), withAlt, totalCap)
	if decision.Rejected()[0].Code != tradingcore.RiskTotalExposure {
		t.Fatalf("total exposure = %s", decision.Rejected()[0].Code)
	}
	fullPosition := position
	fullPosition.Quantity = mustQuantity2(t, "5")
	fullPosition.PyramidLayers = 1
	fullPortfolio, _ := tradingcore.NewPortfolioSnapshot(mustTime(t, fixture.DecisionAt), mustAccountID(t, "primary"), tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{intent.Instrument.QuoteAsset: mustAmount2(t, "1000")}, []tradingcore.Position{fullPosition}, nil, tradingcore.RiskState{})
	positionCap := parityPolicy(t, fixture)
	positionCap.PyramidingEnabled = true
	positionCap.MaxPyramidLayers = 3
	decision, _ = (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), strategyResult.Intents(), fullPortfolio, positionCap)
	if decision.Rejected()[0].Code != tradingcore.RiskPositionExposure {
		t.Fatalf("position exposure = %s", decision.Rejected()[0].Code)
	}
}

func TestSharedExitLifecyclePreservesSimultaneousTriggerPrecedence(t *testing.T) {
	decision := tradingcore.EvaluateExitLifecycle(tradingcore.ExitTriggerInput{HardStop: true, TakeProfit: true, ATRTrailing: true, TimeStop: true, Signal: true, AtLoss: true, AllowSellAtLoss: false})
	if !decision.Exit || decision.Reason != "stop_loss" || !reflect.DeepEqual(decision.Simultaneous, []string{"stop_loss", "take_profit", "atr_trailing_stop"}) {
		t.Fatalf("exit decision = %+v", decision)
	}
}

func TestCommonBrokerOutcomeRepresentsRejectionAndPartialFill(t *testing.T) {
	fixture := loadParityFixture(t)
	snapshot := parityContext(t, fixture, tradingcore.ExecutionPaper, false)
	strategy, err := (tradingcore.LegacyRuleStrategy{IDs: tradingcore.NewSequenceIDGenerator("decision", 1)}).Decide(context.Background(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	risk, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), strategy.Intents(), snapshot.Portfolio(), parityPolicy(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	clock := tradingcore.NewFixedClock(mustTime(t, fixture.DecisionAt).Add(time.Second))
	partial := tradingcore.NewPaperBroker(clock, tradingcore.NewSequenceIDGenerator("partial", 1), tradingcore.CostModel{FillBPS: 4000, FeeBPS: fixture.FeeBPS, SlippageBPS: fixture.SlippageBPS, Version: "cost-v1"})
	outcome, err := partial.Submit(context.Background(), risk.Approved())
	if err != nil {
		t.Fatal(err)
	}
	accepted := outcome.Accepted()[0]
	if accepted.Status != tradingcore.BrokerPartiallyFilled || len(accepted.Fills()) != 1 {
		t.Fatalf("partial outcome = %+v", accepted)
	}
	remaining, ok := accepted.Remaining.Get()
	if !ok || remaining.Decimal().String() != "0.600000000000000000" {
		t.Fatalf("remaining = %s, %v", remaining.Decimal().String(), ok)
	}

	rejecting := tradingcore.NewPaperBroker(clock, tradingcore.NewSequenceIDGenerator("reject", 1), tradingcore.CostModel{RejectCode: "venue_rejected", FeeBPS: fixture.FeeBPS, SlippageBPS: fixture.SlippageBPS, Version: "cost-v1"})
	outcome, err = rejecting.Submit(context.Background(), risk.Approved())
	if err != nil {
		t.Fatal(err)
	}
	if len(outcome.Accepted()) != 0 || len(outcome.Rejected()) != 1 || outcome.Rejected()[0].Code != "venue_rejected" {
		t.Fatalf("rejection outcome = %+v", outcome)
	}
}

func TestOrchestratorProducesByteStableTraceAndLedgerIsOnlyMutationPort(t *testing.T) {
	fixture := loadParityFixture(t)
	snapshot := parityContext(t, fixture, tradingcore.ExecutionPaper, false)
	policy := parityPolicy(t, fixture)
	run := func() ([]byte, *recordingLedger) {
		ledger := &recordingLedger{}
		runner := tradingcore.Orchestrator{Source: staticSource{snapshot, policy}, Strategy: tradingcore.LegacyRuleStrategy{IDs: tradingcore.NewSequenceIDGenerator("decision", 1)}, Risk: tradingcore.PortfolioRiskEngine{}, Broker: tradingcore.NewPaperBroker(tradingcore.NewFixedClock(mustTime(t, fixture.DecisionAt).Add(time.Second)), tradingcore.NewSequenceIDGenerator("broker", 1), tradingcore.CostModel{FeeBPS: 10, SlippageBPS: 5, Version: "cost-v1"}), Ledger: ledger}
		result, err := runner.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		return result.Trace, ledger
	}
	first, ledger1 := run()
	second, ledger2 := run()
	if string(first) != string(second) {
		t.Fatalf("traces differ\n%s\n%s", first, second)
	}
	if ledger1.calls != 1 || ledger2.calls != 1 {
		t.Fatalf("ledger calls = %d,%d", ledger1.calls, ledger2.calls)
	}
	if !json.Valid(first) {
		t.Fatalf("invalid trace: %s", first)
	}
}

type staticSource struct {
	snapshot tradingcore.DecisionContext
	policy   tradingcore.RiskPolicy
}

func (source staticSource) DecisionContext(context.Context) (tradingcore.DecisionContext, tradingcore.RiskPolicy, error) {
	return source.snapshot, source.policy, nil
}

type recordingLedger struct{ calls int }

func (ledger *recordingLedger) RecordBrokerOutcome(context.Context, tradingcore.DecisionBatch, tradingcore.BrokerBatchOutcome) error {
	ledger.calls++
	return nil
}

func loadParityFixture(t *testing.T) parityFixture {
	t.Helper()
	data, err := os.ReadFile("testdata/stage02_parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture parityFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}
func parityContext(t *testing.T, fixture parityFixture, mode tradingcore.ExecutionMode, shadow bool) tradingcore.DecisionContext {
	extra := map[string]string{}
	if shadow {
		extra["model_available"] = "true"
		extra["model_selected.btc-usdt"] = "false"
	}
	return parityContextWith(t, fixture, mode, extra)
}
func parityContextWith(t *testing.T, fixture parityFixture, mode tradingcore.ExecutionMode, extra map[string]string) tradingcore.DecisionContext {
	t.Helper()
	at := mustTime(t, fixture.DecisionAt)
	instrument := mustInstrument2(t)
	universe, err := tradingcore.NewUniverseSnapshot(at, "universe-v1", "fixture", []tradingcore.UniverseCandidate{{Instrument: instrument, Rank: 1, Score: tradingcore.MustDecimal(25, 1), Eligible: true}})
	if err != nil {
		t.Fatal(err)
	}
	portfolio, err := tradingcore.NewPortfolioSnapshot(at, mustAccountID(t, "primary"), mode, map[tradingcore.AssetID]tradingcore.SignedAmount{instrument.QuoteAsset: mustAmount2(t, fixture.Cash)}, nil, nil, tradingcore.RiskState{})
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{"auto_trade_enabled": "true", "entry_percent": fixture.EntryPercent, "signal.btc-usdt": fixture.Signal, "rating.btc-usdt": fixture.Rating, "min_confidence_to_buy": "1", "max_positions": "5", "model_rollout_state": "shadow", "model_fallback_mode": "rule_based"}
	for k, v := range extra {
		settings[k] = v
	}
	quote := tradingcore.Quote{Instrument: instrument, Bid: mustPrice2(t, fixture.Price), Ask: mustPrice2(t, fixture.Price), Last: mustPrice2(t, fixture.Price), ObservedAt: at}
	snapshot, err := tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{MarketObservedAt: at.Add(-time.Minute), SignalAt: at, DecisionAt: at, Quotes: map[tradingcore.InstrumentID]tradingcore.Quote{instrument.ID: quote}, Universe: universe, Portfolio: portfolio, Settings: settings, Versions: tradingcore.VersionContext{Strategy: tradingcore.LegacyStrategyVersion, Settings: "settings-v1", Policy: fixture.PolicyVersion, Model: "model-v1", FeatureSpec: "feature-v1", Dataset: "dataset-v1"}})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
func parityPolicy(t *testing.T, fixture parityFixture) tradingcore.RiskPolicy {
	return tradingcore.RiskPolicy{Version: fixture.PolicyVersion, MaxPositions: 5, MaxGrossExposure: mustAmount2(t, "1000"), MaxPositionValue: mustAmount2(t, "500"), MaxTurnover: mustAmount2(t, "1000"), CashReserve: mustAmount2(t, "0"), MaxConcurrentOrders: 5, LotSize: mustQuantity2(t, "0.001"), ExecutionCosts: tradingcore.ExecutionCostPolicy{Version: "cost-v1", FeeBPS: fixture.FeeBPS, AdverseSlippageBPS: fixture.SlippageBPS}}
}
func mustTime(t *testing.T, v string) time.Time {
	x, e := time.Parse(time.RFC3339, v)
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func mustAmount2(t *testing.T, v string) tradingcore.SignedAmount {
	d, e := tradingcore.ParseDecimal(v)
	if e != nil {
		t.Fatal(e)
	}
	x, e := tradingcore.NewSignedAmount(d)
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func mustQuantity2(t *testing.T, v string) tradingcore.Quantity {
	d, e := tradingcore.ParseDecimal(v)
	if e != nil {
		t.Fatal(e)
	}
	x, e := tradingcore.NewQuantity(d)
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func mustPrice2(t *testing.T, v string) tradingcore.Price {
	d, e := tradingcore.ParseDecimal(v)
	if e != nil {
		t.Fatal(e)
	}
	x, e := tradingcore.NewPrice(d)
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func mustAccountID(t *testing.T, v string) tradingcore.AccountID {
	x, e := tradingcore.NewAccountID(v)
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func mustOrderID(t *testing.T, v string) tradingcore.OrderID {
	x, e := tradingcore.NewOrderID(v)
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func mustPositionID(t *testing.T, v string) tradingcore.PositionID {
	x, e := tradingcore.NewPositionID(v)
	if e != nil {
		t.Fatal(e)
	}
	return x
}
func mustInstrument2(t *testing.T) tradingcore.Instrument {
	return mustInstrumentNamed(t, "btc-usdt", "BTCUSDT", "BTC")
}
func mustInstrumentNamed(t *testing.T, idValue, symbol, baseValue string) tradingcore.Instrument {
	id, _ := tradingcore.NewInstrumentID(idValue)
	base, _ := tradingcore.NewAssetID(baseValue)
	quote, _ := tradingcore.NewAssetID("USDT")
	venue, _ := tradingcore.NewVenueID("binance")
	instrument, e := tradingcore.NewInstrument(id, base, quote, venue, symbol)
	if e != nil {
		t.Fatal(e)
	}
	return instrument
}
