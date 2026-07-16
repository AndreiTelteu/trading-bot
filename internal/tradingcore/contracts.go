// Package tradingcore defines dependency-free, exact domain contracts for the
// shared trading engine. Persistence, transports, exchange clients, and global
// mutable state belong in adapters outside this package.
package tradingcore

import (
	"context"
	"fmt"
	"sort"
	"time"
)

type MarketDataSource interface {
	Bars(context.Context, BarsRequest) (BarSeries, error)
	Quote(context.Context, Instrument, time.Time) (Quote, error)
	BenchmarkBars(context.Context, BarsRequest) (BarSeries, error)
	Coverage(context.Context, BarsRequest) (Coverage, error)
}

type UniverseProvider interface {
	Candidates(context.Context, time.Time) (UniverseSnapshot, error)
}

type OrderSide string
type OrderType string

const (
	Buy         OrderSide = "buy"
	Sell        OrderSide = "sell"
	MarketOrder OrderType = "market"
	LimitOrder  OrderType = "limit"
)

type OptionalPrice struct {
	set   bool
	value Price
}

func SomePrice(value Price) OptionalPrice      { return OptionalPrice{set: true, value: value} }
func (value OptionalPrice) Get() (Price, bool) { return value.value, value.set }

type OptionalQuantity struct {
	set   bool
	value Quantity
}

func SomeQuantity(value Quantity) OptionalQuantity   { return OptionalQuantity{set: true, value: value} }
func (value OptionalQuantity) Get() (Quantity, bool) { return value.value, value.set }

type OrderIntent struct {
	ID                              OrderID
	IdempotencyKey                  IdempotencyKey
	AccountID                       AccountID
	Instrument                      Instrument
	Side                            OrderSide
	Type                            OrderType
	Quantity                        Quantity
	LimitPrice                      OptionalPrice
	ReferencePrice                  OptionalPrice
	SignalAt, DecisionAt, CreatedAt time.Time
	ExecutionMode                   ExecutionMode
	QuantitySemantics               QuantitySemantics
	Reason, Horizon                 string
	Versions                        VersionContext
	Provenance                      Provenance
	metadata                        map[string]string
}

type QuantitySemantics string

const (
	QuantityExact      QuantitySemantics = "exact_quantity"
	QuantityCashCapped QuantitySemantics = "cash_capped"
	QuantityExitAll    QuantitySemantics = "exit_all"
)

func NewOrderIntent(intent OrderIntent, metadata map[string]string) (OrderIntent, error) {
	intent.metadata = cloneStrings(metadata)
	if intent.QuantitySemantics == "" {
		intent.QuantitySemantics = QuantityExact
	}
	if err := intent.Validate(); err != nil {
		return OrderIntent{}, err
	}
	return intent, nil
}

func (intent OrderIntent) Metadata() map[string]string { return cloneStrings(intent.metadata) }

func (intent OrderIntent) Validate() error {
	if intent.ID.String() == "" || intent.IdempotencyKey.String() == "" || intent.AccountID.String() == "" || intent.Instrument.ID.String() == "" {
		return fmt.Errorf("order identity fields are required")
	}
	if intent.Side != Buy && intent.Side != Sell {
		return fmt.Errorf("unsupported order side %q", intent.Side)
	}
	if intent.Type != MarketOrder && intent.Type != LimitOrder {
		return fmt.Errorf("unsupported order type %q", intent.Type)
	}
	if !intent.Quantity.Valid() {
		return fmt.Errorf("positive quantity is required")
	}
	switch intent.QuantitySemantics {
	case "", QuantityExact, QuantityCashCapped, QuantityExitAll:
	default:
		return fmt.Errorf("unsupported quantity semantics %q", intent.QuantitySemantics)
	}
	switch intent.ExecutionMode {
	case ExecutionResearch, ExecutionShadow, ExecutionPaper, ExecutionLimitedLive, ExecutionFullLive:
	default:
		return fmt.Errorf("unsupported execution mode %q", intent.ExecutionMode)
	}
	if intent.SignalAt.IsZero() || intent.DecisionAt.Before(intent.SignalAt) || intent.CreatedAt.Before(intent.DecisionAt) {
		return fmt.Errorf("order timestamps must be ordered signal <= decision <= created")
	}
	if intent.Type == LimitOrder {
		if price, ok := intent.LimitPrice.Get(); !ok || !price.Valid() {
			return fmt.Errorf("limit order requires a positive limit price")
		}
	}
	return nil
}

type DecisionBatch struct{ intents []OrderIntent }

func NewDecisionBatch(intents []OrderIntent) (DecisionBatch, error) {
	copyValues := append([]OrderIntent(nil), intents...)
	for _, intent := range copyValues {
		if err := intent.Validate(); err != nil {
			return DecisionBatch{}, err
		}
	}
	sort.SliceStable(copyValues, func(i, j int) bool { return copyValues[i].ID.String() < copyValues[j].ID.String() })
	return DecisionBatch{copyValues}, nil
}
func (batch DecisionBatch) Intents() []OrderIntent {
	return append([]OrderIntent(nil), batch.intents...)
}

type NoAction struct {
	Instrument    Instrument
	Code, Reason  string
	ObservedScore Decimal
}

type StrategyResult struct {
	intents   DecisionBatch
	noActions []NoAction
}

func NewStrategyResult(intents DecisionBatch, noActions []NoAction) StrategyResult {
	values := append([]NoAction(nil), noActions...)
	sort.SliceStable(values, func(i, j int) bool { return values[i].Instrument.ID.String() < values[j].Instrument.ID.String() })
	return StrategyResult{intents: intents, noActions: values}
}
func (result StrategyResult) Intents() DecisionBatch {
	batch, _ := NewDecisionBatch(result.intents.Intents())
	return batch
}
func (result StrategyResult) NoActions() []NoAction {
	return append([]NoAction(nil), result.noActions...)
}

type Strategy interface {
	Decide(context.Context, DecisionContext) (StrategyResult, error)
}

type RiskPolicy struct {
	Version                            string
	MaxPositions                       int
	MaxGrossExposure, MaxPositionValue SignedAmount
	MaxTurnover, CashReserve           SignedAmount
	MaxConcurrentOrders                int
	PyramidingEnabled                  bool
	MaxPyramidLayers                   int
	LotSize                            Quantity
}

func (policy RiskPolicy) Validate() error {
	if policy.Version == "" || policy.MaxPositions < 0 {
		return fmt.Errorf("risk policy version is required and max positions cannot be negative")
	}
	if !policy.MaxGrossExposure.Valid() || !policy.MaxPositionValue.Valid() || policy.MaxGrossExposure.Decimal().Sign() < 0 || policy.MaxPositionValue.Decimal().Sign() < 0 {
		return fmt.Errorf("risk limits must be valid non-negative exact amounts")
	}
	if policy.MaxTurnover.Valid() && policy.MaxTurnover.Decimal().Sign() < 0 || policy.CashReserve.Valid() && policy.CashReserve.Decimal().Sign() < 0 {
		return fmt.Errorf("turnover and cash reserve must be non-negative")
	}
	if policy.MaxConcurrentOrders < 0 || policy.MaxPyramidLayers < 0 {
		return fmt.Errorf("concurrency and pyramid limits cannot be negative")
	}
	return nil
}

type RejectionCode string
type OrderRejection struct {
	OrderID       OrderID
	Code          RejectionCode
	Message       string
	EvaluatedAt   time.Time
	PolicyVersion string
}
type RiskDecision struct {
	approved DecisionBatch
	rejected []OrderRejection
	trace    []RiskTrace
}

type RiskTrace struct {
	OrderID           OrderID  `json:"order_id"`
	PolicyVersion     string   `json:"policy_version"`
	Checks            []string `json:"checks"`
	RequestedQuantity string   `json:"requested_quantity"`
	ApprovedQuantity  string   `json:"approved_quantity"`
	RequestedNotional string   `json:"requested_notional"`
	ApprovedNotional  string   `json:"approved_notional"`
}

func NewRiskDecision(approved DecisionBatch, rejected []OrderRejection) RiskDecision {
	values := append([]OrderRejection(nil), rejected...)
	sort.SliceStable(values, func(i, j int) bool { return values[i].OrderID.String() < values[j].OrderID.String() })
	return RiskDecision{approved: approved, rejected: values}
}
func NewRiskDecisionWithTrace(approved DecisionBatch, rejected []OrderRejection, trace []RiskTrace) RiskDecision {
	result := NewRiskDecision(approved, rejected)
	result.trace = append([]RiskTrace(nil), trace...)
	sort.SliceStable(result.trace, func(i, j int) bool { return result.trace[i].OrderID.String() < result.trace[j].OrderID.String() })
	return result
}
func (decision RiskDecision) Approved() DecisionBatch {
	batch, _ := NewDecisionBatch(decision.approved.Intents())
	return batch
}
func (decision RiskDecision) Rejected() []OrderRejection {
	return append([]OrderRejection(nil), decision.rejected...)
}
func (decision RiskDecision) Trace() []RiskTrace { return append([]RiskTrace(nil), decision.trace...) }

type RiskEngine interface {
	Evaluate(context.Context, DecisionBatch, PortfolioSnapshot, RiskPolicy) (RiskDecision, error)
}

type Fill struct {
	ID                                           FillID
	OrderID                                      OrderID
	ProviderFillID                               string
	Instrument                                   Instrument
	Side                                         OrderSide
	Quantity                                     Quantity
	Price                                        Price
	Fee                                          SignedAmount
	FeeAsset                                     AssetID
	OrderedAt, SubmittedAt, AcceptedAt, FilledAt time.Time
	Versions                                     VersionContext
	Provenance                                   Provenance
}

func (fill Fill) Validate() error {
	if fill.ID.String() == "" || fill.OrderID.String() == "" || fill.ProviderFillID == "" || fill.Instrument.Validate() != nil {
		return fmt.Errorf("fill identity fields are required")
	}
	if fill.Side != Buy && fill.Side != Sell {
		return fmt.Errorf("unsupported fill side %q", fill.Side)
	}
	if !fill.Quantity.Valid() || !fill.Price.Valid() || !fill.Fee.Valid() || fill.FeeAsset.String() == "" {
		return fmt.Errorf("fill quantity, price, and fee must be exact valid values")
	}
	if fill.OrderedAt.IsZero() || fill.SubmittedAt.Before(fill.OrderedAt) || fill.AcceptedAt.Before(fill.SubmittedAt) || fill.FilledAt.Before(fill.AcceptedAt) {
		return fmt.Errorf("fill timestamps must be ordered order <= submit <= accept <= fill")
	}
	return nil
}

type BrokerOrderStatus string

const (
	BrokerAccepted        BrokerOrderStatus = "accepted"
	BrokerPartiallyFilled BrokerOrderStatus = "partially_filled"
	BrokerFilled          BrokerOrderStatus = "filled"
)

type AcceptedOrder struct {
	OrderID         OrderID
	ProviderOrderID string
	Status          BrokerOrderStatus
	AcceptedAt      time.Time
	Remaining       OptionalQuantity
	fills           []Fill
}

func NewAcceptedOrder(order AcceptedOrder, fills []Fill) (AcceptedOrder, error) {
	order.fills = append([]Fill(nil), fills...)
	for _, fill := range order.fills {
		if err := fill.Validate(); err != nil {
			return AcceptedOrder{}, err
		}
		if fill.OrderID != order.OrderID {
			return AcceptedOrder{}, fmt.Errorf("fill order id does not match accepted order")
		}
	}
	sort.SliceStable(order.fills, func(i, j int) bool { return order.fills[i].ID.String() < order.fills[j].ID.String() })
	if err := order.Validate(); err != nil {
		return AcceptedOrder{}, err
	}
	return order, nil
}
func (order AcceptedOrder) Fills() []Fill { return append([]Fill(nil), order.fills...) }
func (order AcceptedOrder) Validate() error {
	if order.OrderID.String() == "" || order.ProviderOrderID == "" || order.AcceptedAt.IsZero() {
		return fmt.Errorf("accepted order identity and timestamp are required")
	}
	switch order.Status {
	case BrokerAccepted:
	case BrokerPartiallyFilled:
		if len(order.fills) == 0 {
			return fmt.Errorf("partial order requires at least one fill")
		}
		if remaining, ok := order.Remaining.Get(); !ok || !remaining.Valid() {
			return fmt.Errorf("partial order requires positive remaining quantity")
		}
	case BrokerFilled:
		if len(order.fills) == 0 {
			return fmt.Errorf("filled order requires at least one fill")
		}
		if _, ok := order.Remaining.Get(); ok {
			return fmt.Errorf("filled order cannot have remaining quantity")
		}
	default:
		return fmt.Errorf("unsupported broker order status %q", order.Status)
	}
	return nil
}

type OutcomeCompleteness string

const (
	OutcomeComplete      OutcomeCompleteness = "complete"
	OutcomeIndeterminate OutcomeCompleteness = "indeterminate"
)

type BrokerBatchOutcome struct {
	completeness OutcomeCompleteness
	accepted     []AcceptedOrder
	rejected     []OrderRejection
}

func NewBrokerBatchOutcome(completeness OutcomeCompleteness, accepted []AcceptedOrder, rejected []OrderRejection) (BrokerBatchOutcome, error) {
	if completeness != OutcomeComplete && completeness != OutcomeIndeterminate {
		return BrokerBatchOutcome{}, fmt.Errorf("unsupported outcome completeness %q", completeness)
	}
	a := append([]AcceptedOrder(nil), accepted...)
	r := append([]OrderRejection(nil), rejected...)
	for _, order := range a {
		if err := order.Validate(); err != nil {
			return BrokerBatchOutcome{}, err
		}
	}
	for _, rejection := range r {
		if rejection.OrderID.String() == "" || rejection.Code == "" || rejection.EvaluatedAt.IsZero() {
			return BrokerBatchOutcome{}, fmt.Errorf("rejection requires order id, code, and evaluation time")
		}
	}
	sort.SliceStable(a, func(i, j int) bool { return a[i].OrderID.String() < a[j].OrderID.String() })
	sort.SliceStable(r, func(i, j int) bool { return r[i].OrderID.String() < r[j].OrderID.String() })
	return BrokerBatchOutcome{completeness: completeness, accepted: a, rejected: r}, nil
}
func (outcome BrokerBatchOutcome) Completeness() OutcomeCompleteness { return outcome.completeness }
func (outcome BrokerBatchOutcome) Accepted() []AcceptedOrder {
	return append([]AcceptedOrder(nil), outcome.accepted...)
}
func (outcome BrokerBatchOutcome) Rejected() []OrderRejection {
	return append([]OrderRejection(nil), outcome.rejected...)
}

// When Submit returns an error, Completeness states whether retry safety is known.
// Accepted/rejected members are authoritative only when Completeness is complete.
type Broker interface {
	Submit(context.Context, DecisionBatch) (BrokerBatchOutcome, error)
}

type LedgerEventType string

const (
	LedgerCashAdjusted      LedgerEventType = "cash_adjusted" // Stage 00 compatibility alias.
	LedgerTradeFilled       LedgerEventType = "trade_filled"  // Stage 00 compatibility alias.
	LedgerFeeCharged        LedgerEventType = "fee_charged"   // Stage 00 compatibility alias.
	LedgerCapitalDeposit    LedgerEventType = "capital_deposit"
	LedgerCapitalWithdrawal LedgerEventType = "capital_withdrawal"
	LedgerBuyFill           LedgerEventType = "buy_fill"
	LedgerSellFill          LedgerEventType = "sell_fill"
	LedgerTradingFee        LedgerEventType = "trading_fee"
	LedgerExchangeFee       LedgerEventType = "exchange_fee"
	LedgerFundingInterest   LedgerEventType = "funding_interest"
	LedgerAdminCorrection   LedgerEventType = "administrative_correction"
	LedgerReversal          LedgerEventType = "reversal"
)

type PostingDimension string

const (
	PostingCash  PostingDimension = "cash"
	PostingAsset PostingDimension = "asset"
)

type LedgerPosting struct {
	Dimension    PostingDimension
	AssetID      AssetID
	InstrumentID InstrumentID
	Amount       SignedAmount
}
type LedgerEvent struct {
	ID                     EventID
	IdempotencyKey         IdempotencyKey
	Type                   LedgerEventType
	AccountID              AccountID
	OrderID                OrderID
	FillID                 FillID
	PositionID             PositionID
	VenueID                VenueID
	OccurredAt, RecordedAt time.Time
	Versions               VersionContext
	Provenance             Provenance
	ReversesEventID        EventID
	postings               []LedgerPosting
}

func NewLedgerEvent(event LedgerEvent, postings []LedgerPosting) (LedgerEvent, error) {
	if event.ID.String() == "" || event.IdempotencyKey.String() == "" || event.AccountID.String() == "" {
		return LedgerEvent{}, fmt.Errorf("ledger event identity fields are required")
	}
	if event.OccurredAt.IsZero() || event.RecordedAt.Before(event.OccurredAt) {
		return LedgerEvent{}, fmt.Errorf("ledger timestamps must be ordered occurred <= recorded")
	}
	if len(postings) == 0 {
		return LedgerEvent{}, fmt.Errorf("ledger event requires postings")
	}
	switch event.Type {
	case LedgerCashAdjusted, LedgerTradeFilled, LedgerFeeCharged,
		LedgerCapitalDeposit, LedgerCapitalWithdrawal, LedgerBuyFill,
		LedgerSellFill, LedgerTradingFee, LedgerExchangeFee,
		LedgerFundingInterest, LedgerAdminCorrection, LedgerReversal:
	default:
		return LedgerEvent{}, fmt.Errorf("unsupported ledger event type %q", event.Type)
	}
	for _, posting := range postings {
		if !posting.Amount.Valid() {
			return LedgerEvent{}, fmt.Errorf("ledger posting amount must be exact")
		}
		switch posting.Dimension {
		case PostingCash:
			if posting.AssetID.String() == "" {
				return LedgerEvent{}, fmt.Errorf("cash posting requires asset id")
			}
		case PostingAsset:
			if posting.AssetID.String() == "" || posting.InstrumentID.String() == "" {
				return LedgerEvent{}, fmt.Errorf("asset posting requires asset and instrument ids")
			}
		default:
			return LedgerEvent{}, fmt.Errorf("unsupported posting dimension %q", posting.Dimension)
		}
	}
	event.postings = append([]LedgerPosting(nil), postings...)
	return event, nil
}
func (event LedgerEvent) Postings() []LedgerPosting {
	return append([]LedgerPosting(nil), event.postings...)
}

type LedgerBatch struct {
	idempotencyKey IdempotencyKey
	events         []LedgerEvent
}

func NewLedgerBatch(key IdempotencyKey, events []LedgerEvent) (LedgerBatch, error) {
	if key.String() == "" {
		return LedgerBatch{}, fmt.Errorf("ledger batch idempotency key is required")
	}
	values := append([]LedgerEvent(nil), events...)
	if len(values) == 0 {
		return LedgerBatch{}, fmt.Errorf("ledger batch requires events")
	}
	seen := map[EventID]struct{}{}
	for _, event := range values {
		if event.ID.String() == "" || len(event.Postings()) == 0 {
			return LedgerBatch{}, fmt.Errorf("ledger batch contains unvalidated event")
		}
		if _, exists := seen[event.ID]; exists {
			return LedgerBatch{}, fmt.Errorf("ledger batch contains duplicate event id %s", event.ID.String())
		}
		seen[event.ID] = struct{}{}
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].ID.String() < values[j].ID.String() })
	return LedgerBatch{idempotencyKey: key, events: values}, nil
}
func (batch LedgerBatch) IdempotencyKey() IdempotencyKey { return batch.idempotencyKey }
func (batch LedgerBatch) Events() []LedgerEvent          { return append([]LedgerEvent(nil), batch.events...) }

type LedgerAppendStatus string

const (
	LedgerAppended            LedgerAppendStatus = "appended"
	LedgerAlreadyApplied      LedgerAppendStatus = "already_applied"
	LedgerRejected            LedgerAppendStatus = "rejected"
	LedgerAppendIndeterminate LedgerAppendStatus = "indeterminate"
)

type LedgerAppendOutcome struct {
	status                 LedgerAppendStatus
	batchKey               IdempotencyKey
	eventIDs               []EventID
	rejectionCode, message string
}

func NewLedgerAppendOutcome(status LedgerAppendStatus, batchKey IdempotencyKey, eventIDs []EventID, rejectionCode, message string) (LedgerAppendOutcome, error) {
	if batchKey.String() == "" {
		return LedgerAppendOutcome{}, fmt.Errorf("ledger append outcome requires batch key")
	}
	switch status {
	case LedgerAppended, LedgerAlreadyApplied:
		if len(eventIDs) == 0 {
			return LedgerAppendOutcome{}, fmt.Errorf("successful ledger append outcome requires event ids")
		}
	case LedgerRejected:
		if rejectionCode == "" {
			return LedgerAppendOutcome{}, fmt.Errorf("rejected ledger append outcome requires code")
		}
	case LedgerAppendIndeterminate:
	default:
		return LedgerAppendOutcome{}, fmt.Errorf("unsupported ledger append status %q", status)
	}
	values := append([]EventID(nil), eventIDs...)
	for _, id := range values {
		if id.String() == "" {
			return LedgerAppendOutcome{}, fmt.Errorf("ledger append outcome contains invalid event id")
		}
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].String() < values[j].String() })
	return LedgerAppendOutcome{status: status, batchKey: batchKey, eventIDs: values, rejectionCode: rejectionCode, message: message}, nil
}
func (outcome LedgerAppendOutcome) Status() LedgerAppendStatus { return outcome.status }
func (outcome LedgerAppendOutcome) BatchKey() IdempotencyKey   { return outcome.batchKey }
func (outcome LedgerAppendOutcome) EventIDs() []EventID {
	return append([]EventID(nil), outcome.eventIDs...)
}
func (outcome LedgerAppendOutcome) Rejection() (string, string) {
	return outcome.rejectionCode, outcome.message
}

type ReconciliationReport struct {
	asOf                time.Time
	accountID           AccountID
	balanced            bool
	cashDifferences     map[AssetID]SignedAmount
	positionDifferences map[InstrumentID]SignedAmount
	issues              []string
}

func NewReconciliationReport(asOf time.Time, accountID AccountID, balanced bool, cash map[AssetID]SignedAmount, positions map[InstrumentID]SignedAmount, issues []string) ReconciliationReport {
	cashCopy := make(map[AssetID]SignedAmount, len(cash))
	for key, value := range cash {
		cashCopy[key] = value
	}
	positionCopy := make(map[InstrumentID]SignedAmount, len(positions))
	for key, value := range positions {
		positionCopy[key] = value
	}
	return ReconciliationReport{asOf: asOf, accountID: accountID, balanced: balanced, cashDifferences: cashCopy, positionDifferences: positionCopy, issues: append([]string(nil), issues...)}
}
func (report ReconciliationReport) AsOf() time.Time      { return report.asOf }
func (report ReconciliationReport) AccountID() AccountID { return report.accountID }
func (report ReconciliationReport) Balanced() bool       { return report.balanced }
func (report ReconciliationReport) CashDifferences() map[AssetID]SignedAmount {
	result := make(map[AssetID]SignedAmount, len(report.cashDifferences))
	for key, value := range report.cashDifferences {
		result[key] = value
	}
	return result
}
func (report ReconciliationReport) PositionDifferences() map[InstrumentID]SignedAmount {
	result := make(map[InstrumentID]SignedAmount, len(report.positionDifferences))
	for key, value := range report.positionDifferences {
		result[key] = value
	}
	return result
}
func (report ReconciliationReport) Issues() []string { return append([]string(nil), report.issues...) }

// AppendAtomic commits every event or none. Indeterminate means the caller must
// retry with the same batch idempotency key before constructing another batch.
type Ledger interface {
	AppendAtomic(context.Context, LedgerBatch) (LedgerAppendOutcome, error)
	Events(context.Context, AccountID, time.Time) ([]LedgerEvent, error)
	Reconcile(context.Context, PortfolioSnapshot) (ReconciliationReport, error)
}
