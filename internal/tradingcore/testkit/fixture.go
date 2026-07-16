// Package testkit contains deterministic, in-memory inputs for trading-core
// contract and adapter tests. It has no database or transport dependencies.
package testkit

import (
	"time"
	"trading-go/internal/tradingcore"
)

type Fixture struct {
	Clock                                  tradingcore.FixedClock
	IDs                                    *tradingcore.SequenceIDGenerator
	MarketObservedAt, SignalAt, DecisionAt time.Time
	Settings                               map[string]string
	Bars                                   map[tradingcore.InstrumentID][]tradingcore.Bar
	Quotes                                 map[tradingcore.InstrumentID]tradingcore.Quote
	Universe                               tradingcore.UniverseSnapshot
	Portfolio                              tradingcore.PortfolioSnapshot
	Versions                               tradingcore.VersionContext
}

func NewFixture(at time.Time) Fixture {
	at = at.UTC()
	account, _ := tradingcore.NewAccountID("test-account")
	portfolio, _ := tradingcore.NewPortfolioSnapshot(at, account, tradingcore.ExecutionPaper, nil, nil, nil, tradingcore.RiskState{})
	universe, _ := tradingcore.NewUniverseSnapshot(at, "test-policy-v1", "fixture", nil)
	return Fixture{
		Clock: tradingcore.NewFixedClock(at), IDs: tradingcore.NewSequenceIDGenerator("test", 1),
		MarketObservedAt: at.Add(-2 * time.Minute), SignalAt: at.Add(-time.Minute), DecisionAt: at,
		Settings: map[string]string{}, Bars: map[tradingcore.InstrumentID][]tradingcore.Bar{},
		Quotes: map[tradingcore.InstrumentID]tradingcore.Quote{}, Universe: universe, Portfolio: portfolio,
		Versions: tradingcore.VersionContext{Strategy: "test-strategy-v1", Settings: "test-settings-v1", Policy: "test-policy-v1", Dataset: "test-dataset-v1"},
	}
}

// DecisionContext always creates a new deep snapshot. The fixture remains
// mutable so a test can conveniently arrange inputs before taking a snapshot.
func (fixture Fixture) DecisionContext() (tradingcore.DecisionContext, error) {
	return tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{
		MarketObservedAt: fixture.MarketObservedAt, SignalAt: fixture.SignalAt, DecisionAt: fixture.DecisionAt,
		Bars: fixture.Bars, Quotes: fixture.Quotes, Universe: fixture.Universe, Portfolio: fixture.Portfolio,
		Settings: fixture.Settings, Versions: fixture.Versions,
	})
}
