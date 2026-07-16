package tradingcore_test

import (
	"context"
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

type strategyStub struct{}

func (strategyStub) Decide(context.Context, tradingcore.DecisionContext) ([]tradingcore.OrderIntent, error) {
	return nil, nil
}

type riskStub struct{}

func (riskStub) Evaluate(context.Context, []tradingcore.OrderIntent, tradingcore.Portfolio, tradingcore.RiskPolicy) (tradingcore.RiskDecision, error) {
	return tradingcore.RiskDecision{}, nil
}

type brokerStub struct{}

func (brokerStub) Submit(context.Context, []tradingcore.OrderIntent) (tradingcore.BrokerResult, error) {
	return tradingcore.BrokerResult{}, nil
}

type marketDataStub struct{}

func (marketDataStub) Bars(context.Context, tradingcore.BarsRequest) ([]tradingcore.Bar, error) {
	return nil, nil
}
func (marketDataStub) Quote(context.Context, tradingcore.Symbol, time.Time) (tradingcore.Quote, error) {
	return tradingcore.Quote{}, nil
}
func (marketDataStub) BenchmarkBars(context.Context, tradingcore.BarsRequest) ([]tradingcore.Bar, error) {
	return nil, nil
}
func (marketDataStub) Coverage(context.Context, tradingcore.BarsRequest) (tradingcore.Coverage, error) {
	return tradingcore.Coverage{}, nil
}

type universeStub struct{}

func (universeStub) Candidates(context.Context, time.Time) (tradingcore.UniverseSnapshot, error) {
	return tradingcore.UniverseSnapshot{}, nil
}

type ledgerStub struct{}

func (ledgerStub) Append(context.Context, []tradingcore.LedgerEvent) error { return nil }
func (ledgerStub) Events(context.Context, string, time.Time) ([]tradingcore.LedgerEvent, error) {
	return nil, nil
}
func (ledgerStub) Reconcile(context.Context, tradingcore.Portfolio) (tradingcore.ReconciliationReport, error) {
	return tradingcore.ReconciliationReport{}, nil
}

var _ tradingcore.Strategy = strategyStub{}
var _ tradingcore.RiskEngine = riskStub{}
var _ tradingcore.Broker = brokerStub{}
var _ tradingcore.MarketDataSource = marketDataStub{}
var _ tradingcore.UniverseProvider = universeStub{}
var _ tradingcore.Ledger = ledgerStub{}
