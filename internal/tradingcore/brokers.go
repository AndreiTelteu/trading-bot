package tradingcore

import (
	"context"
	"fmt"
	"math/big"
	"strings"
)

const ExchangeExecutionFenced RejectionCode = "exchange_execution_fenced"

type CostModel struct {
	FeeBPS, SlippageBPS int64
	FillBPS             int64
	RejectCode          RejectionCode
	Version             string
}

type SimulationBroker struct {
	Clock Clock
	IDs   IDGenerator
	Costs CostModel
	Name  string
}
type BacktestBroker struct{ SimulationBroker }
type PaperBroker struct{ SimulationBroker }

func NewBacktestBroker(clock Clock, ids IDGenerator, costs CostModel) BacktestBroker {
	return BacktestBroker{SimulationBroker{Clock: clock, IDs: ids, Costs: costs, Name: "backtest"}}
}
func NewPaperBroker(clock Clock, ids IDGenerator, costs CostModel) PaperBroker {
	return PaperBroker{SimulationBroker{Clock: clock, IDs: ids, Costs: costs, Name: "paper"}}
}

func (broker SimulationBroker) Submit(_ context.Context, batch DecisionBatch) (BrokerBatchOutcome, error) {
	if broker.Clock == nil || broker.IDs == nil {
		return BrokerBatchOutcome{}, fmt.Errorf("simulation broker dependencies are required")
	}
	accepted := make([]AcceptedOrder, 0, len(batch.Intents()))
	rejected := make([]OrderRejection, 0)
	for _, intent := range batch.Intents() {
		metadata := intent.Metadata()
		if metadata["cost_policy_version"] != broker.Costs.Version || metadata["fee_bps"] != fmt.Sprint(broker.Costs.FeeBPS) || metadata["slippage_bps"] != fmt.Sprint(broker.Costs.SlippageBPS) {
			return BrokerBatchOutcome{}, fmt.Errorf("intent %s cost reservation does not match broker cost model", intent.ID.String())
		}
		if broker.Costs.RejectCode != "" {
			rejected = append(rejected, OrderRejection{OrderID: intent.ID, Code: broker.Costs.RejectCode, Message: string(broker.Costs.RejectCode), EvaluatedAt: broker.Clock.Now().UTC(), PolicyVersion: intent.Versions.Policy})
			continue
		}
		price, ok := intent.ReferencePrice.Get()
		if !ok {
			price, ok = intent.LimitPrice.Get()
		}
		if !ok {
			return BrokerBatchOutcome{}, fmt.Errorf("intent %s has no execution reference price", intent.ID.String())
		}
		fillPrice, err := applyBPS(price, broker.Costs.SlippageBPS, intent.Side == Buy)
		if err != nil {
			return BrokerBatchOutcome{}, err
		}
		fillBPS := broker.Costs.FillBPS
		if fillBPS == 0 {
			fillBPS = 10000
		}
		if fillBPS < 0 || fillBPS > 10000 {
			return BrokerBatchOutcome{}, fmt.Errorf("fill bps must be between 0 and 10000")
		}
		fillQuantity := intent.Quantity
		if fillBPS < 10000 {
			fillQuantity, err = quantityFromRat(newRatBPS(decimalRat(intent.Quantity.Decimal()), fillBPS))
			if err != nil {
				return BrokerBatchOutcome{}, err
			}
		}
		fee, err := amountFromRat(newRatBPS(notional(fillQuantity, fillPrice), broker.Costs.FeeBPS))
		if err != nil {
			return BrokerBatchOutcome{}, err
		}
		fillRaw, err := broker.IDs.NewID()
		if err != nil {
			return BrokerBatchOutcome{}, err
		}
		fillID, _ := NewFillID("fill-" + fillRaw)
		at := broker.Clock.Now().UTC()
		fill := Fill{ID: fillID, OrderID: intent.ID, ProviderFillID: broker.Name + "-" + fillRaw, Instrument: intent.Instrument, Side: intent.Side, Quantity: fillQuantity, Price: fillPrice, Fee: fee, FeeAsset: intent.Instrument.QuoteAsset, OrderedAt: intent.CreatedAt, SubmittedAt: at, AcceptedAt: at, FilledAt: at, Versions: intent.Versions, Provenance: Provenance{Source: broker.Name + "_broker", Actor: broker.Costs.Version, Reason: intent.Reason}, CostModelVersion: broker.Costs.Version}
		status := BrokerFilled
		remaining := OptionalQuantity{}
		if fillBPS < 10000 {
			status = BrokerPartiallyFilled
			remainingQuantity, quantityErr := quantityFromRat(new(big.Rat).Sub(decimalRat(intent.Quantity.Decimal()), decimalRat(fillQuantity.Decimal())))
			if quantityErr != nil {
				return BrokerBatchOutcome{}, quantityErr
			}
			remaining = SomeQuantity(remainingQuantity)
		}
		order, err := NewAcceptedOrder(AcceptedOrder{OrderID: intent.ID, ProviderOrderID: broker.Name + "-order-" + intent.IdempotencyKey.String(), Status: status, AcceptedAt: at, Remaining: remaining}, []Fill{fill})
		if err != nil {
			return BrokerBatchOutcome{}, err
		}
		accepted = append(accepted, order)
	}
	return NewBrokerBatchOutcome(OutcomeComplete, accepted, rejected)
}

type LiveOrderRequest struct{ ClientOrderID, Symbol, Side, Type, Quantity, LimitPrice, PolicyVersion string }
type LiveBroker struct{}

func (LiveBroker) BuildRequests(batch DecisionBatch) ([]LiveOrderRequest, error) {
	requests := make([]LiveOrderRequest, 0, len(batch.Intents()))
	for _, intent := range batch.Intents() {
		request := LiveOrderRequest{ClientOrderID: stableClientOrderID(intent.IdempotencyKey.String()), Symbol: intent.Instrument.VenueSymbol, Side: strings.ToUpper(string(intent.Side)), Type: strings.ToUpper(string(intent.Type)), Quantity: intent.Quantity.Decimal().String(), PolicyVersion: intent.Versions.Policy}
		if price, ok := intent.LimitPrice.Get(); ok {
			request.LimitPrice = price.Decimal().String()
		}
		requests = append(requests, request)
	}
	return requests, nil
}

// Submit remains intentionally fenced: Stage 02 constructs deterministic
// requests but does not claim the durable reservation/recovery guarantees that
// are required before external submission can be reachable.
func (LiveBroker) Submit(_ context.Context, batch DecisionBatch) (BrokerBatchOutcome, error) {
	rejected := make([]OrderRejection, 0, len(batch.Intents()))
	for _, intent := range batch.Intents() {
		rejected = append(rejected, OrderRejection{OrderID: intent.ID, Code: ExchangeExecutionFenced, Message: "durable live submission recovery is not available", EvaluatedAt: intent.DecisionAt, PolicyVersion: intent.Versions.Policy})
	}
	return NewBrokerBatchOutcome(OutcomeComplete, nil, rejected)
}

func stableClientOrderID(value string) string {
	value = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}
		return '-'
	}, value)
	if len(value) > 32 {
		value = value[:32]
	}
	return value
}
func applyBPS(price Price, bps int64, add bool) (Price, error) {
	factor := big.NewRat(10000+bps, 10000)
	if !add {
		factor = big.NewRat(10000-bps, 10000)
	}
	d, err := ratDecimal(new(big.Rat).Mul(decimalRat(price.Decimal()), factor))
	if err != nil {
		return Price{}, err
	}
	return NewPrice(d)
}
func newRatBPS(value *big.Rat, bps int64) *big.Rat {
	return new(big.Rat).Mul(value, big.NewRat(bps, 10000))
}
