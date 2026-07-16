// Package tradingcore defines the dependency-free domain boundary for the
// trading engine that later reimplementation stages will build.
//
// Values passed to Strategy and RiskEngine are snapshots. Implementations must
// treat their maps and slices as read-only and must not consult mutable global
// settings, a database, HTTP state, or WebSocket state while deciding.
package tradingcore

import (
	"context"
	"time"
)

type Symbol string
type Currency string

type Bar struct {
	Symbol                         Symbol
	Interval                       time.Duration
	OpenTime                       time.Time
	CloseTime                      time.Time
	Open, High, Low, Close, Volume float64
}

type Quote struct {
	Symbol         Symbol
	Bid, Ask, Last float64
	ObservedAt     time.Time
}

type Coverage struct {
	Symbol               Symbol
	From, Through        time.Time
	ExpectedObservations int
	ActualObservations   int
	Complete             bool
	Source               string
}

type BarsRequest struct {
	Symbol        Symbol
	Interval      time.Duration
	From, Through time.Time
}

type MarketDataSource interface {
	Bars(context.Context, BarsRequest) ([]Bar, error)
	Quote(context.Context, Symbol, time.Time) (Quote, error)
	BenchmarkBars(context.Context, BarsRequest) ([]Bar, error)
	Coverage(context.Context, BarsRequest) (Coverage, error)
}

type UniverseCandidate struct {
	Symbol          Symbol
	Rank            int
	Score           float64
	Eligible        bool
	RejectionReason string
}

type UniverseSnapshot struct {
	AsOf          time.Time
	PolicyVersion string
	Source        string
	Candidates    []UniverseCandidate
}

type UniverseProvider interface {
	Candidates(context.Context, time.Time) (UniverseSnapshot, error)
}

type Position struct {
	ID, Symbol                        string
	Quantity, AveragePrice, MarkPrice float64
	OpenedAt                          time.Time
}

type Portfolio struct {
	AsOf      time.Time
	Cash      map[Currency]float64
	Positions []Position
}

type DecisionContext struct {
	AsOf          time.Time
	Bars          map[Symbol][]Bar
	Quotes        map[Symbol]Quote
	Universe      UniverseSnapshot
	Portfolio     Portfolio
	Settings      map[string]string
	PolicyVersion string
}

type OrderSide string
type OrderType string

const (
	Buy         OrderSide = "buy"
	Sell        OrderSide = "sell"
	MarketOrder OrderType = "market"
	LimitOrder  OrderType = "limit"
)

type OrderIntent struct {
	ID         string
	Symbol     Symbol
	Side       OrderSide
	Type       OrderType
	Quantity   float64
	LimitPrice *float64
	Reason     string
	CreatedAt  time.Time
}

type Strategy interface {
	Decide(context.Context, DecisionContext) ([]OrderIntent, error)
}

type RiskPolicy struct {
	Version          string
	MaxPositions     int
	MaxGrossExposure float64
	MaxPositionValue float64
}

type Rejection struct {
	IntentID string
	Code     string
	Message  string
}

type RiskDecision struct {
	Approved []OrderIntent
	Rejected []Rejection
}

type RiskEngine interface {
	Evaluate(context.Context, []OrderIntent, Portfolio, RiskPolicy) (RiskDecision, error)
}

type Fill struct {
	ID, IntentID         string
	Symbol               Symbol
	Side                 OrderSide
	Quantity, Price, Fee float64
	FeeCurrency          Currency
	FilledAt             time.Time
}

type BrokerResult struct {
	Fills    []Fill
	Rejected []Rejection
}

type Broker interface {
	Submit(context.Context, []OrderIntent) (BrokerResult, error)
}

type LedgerEventType string

const (
	LedgerCashAdjusted LedgerEventType = "cash_adjusted"
	LedgerOrderFilled  LedgerEventType = "order_filled"
	LedgerFeeCharged   LedgerEventType = "fee_charged"
)

type LedgerEvent struct {
	ID               string
	Type             LedgerEventType
	AccountID        string
	Symbol           Symbol
	Currency         Currency
	Quantity, Amount float64
	OccurredAt       time.Time
	ReferenceID      string
}

type ReconciliationReport struct {
	AsOf               time.Time
	Balanced           bool
	CashDifference     map[Currency]float64
	PositionDifference map[Symbol]float64
	Issues             []string
}

type Ledger interface {
	Append(context.Context, []LedgerEvent) error
	Events(context.Context, string, time.Time) ([]LedgerEvent, error)
	Reconcile(context.Context, Portfolio) (ReconciliationReport, error)
}
