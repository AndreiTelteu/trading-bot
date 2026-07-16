// Package testkit contains deterministic, in-memory inputs for trading-core
// contract and adapter tests. It has no database or transport dependencies.
package testkit

import (
	"time"
	"trading-go/internal/tradingcore"
)

type Fixture struct {
	Clock     tradingcore.FixedClock
	IDs       *tradingcore.SequenceIDGenerator
	Settings  map[string]string
	Bars      map[tradingcore.Symbol][]tradingcore.Bar
	Quotes    map[tradingcore.Symbol]tradingcore.Quote
	Portfolio tradingcore.Portfolio
}

func NewFixture(at time.Time) Fixture {
	at = at.UTC()
	return Fixture{
		Clock:     tradingcore.NewFixedClock(at),
		IDs:       tradingcore.NewSequenceIDGenerator("test", 1),
		Settings:  map[string]string{},
		Bars:      map[tradingcore.Symbol][]tradingcore.Bar{},
		Quotes:    map[tradingcore.Symbol]tradingcore.Quote{},
		Portfolio: tradingcore.Portfolio{AsOf: at, Cash: map[tradingcore.Currency]float64{}, Positions: []tradingcore.Position{}},
	}
}

func (fixture Fixture) DecisionContext(universe tradingcore.UniverseSnapshot) tradingcore.DecisionContext {
	return tradingcore.DecisionContext{
		AsOf: fixture.Clock.Now(), Bars: fixture.Bars, Quotes: fixture.Quotes,
		Universe: universe, Portfolio: fixture.Portfolio, Settings: fixture.Settings,
	}
}
