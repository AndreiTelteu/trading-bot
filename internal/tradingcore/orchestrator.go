package tradingcore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type DecisionSource interface {
	DecisionContext(context.Context) (DecisionContext, RiskPolicy, error)
}
type FillLedger interface {
	RecordBrokerOutcome(context.Context, DecisionBatch, BrokerBatchOutcome) error
}
type Observer interface {
	Observe(context.Context, Observation) error
}
type Observation struct {
	Stage, Code string
	OrderID     OrderID
	Metadata    map[string]string
}

type RunResult struct {
	Strategy StrategyResult
	Risk     RiskDecision
	Broker   BrokerBatchOutcome
	Trace    []byte
}

type Orchestrator struct {
	Source   DecisionSource
	Strategy Strategy
	Risk     RiskEngine
	Broker   Broker
	Ledger   FillLedger
	Observer Observer
}

func (runner Orchestrator) Run(ctx context.Context) (RunResult, error) {
	if runner.Source == nil || runner.Strategy == nil || runner.Risk == nil || runner.Broker == nil || runner.Ledger == nil {
		return RunResult{}, fmt.Errorf("orchestrator dependencies are required")
	}
	snapshot, policy, err := runner.Source.DecisionContext(ctx)
	if err != nil {
		return RunResult{}, fmt.Errorf("build decision context: %w", err)
	}
	strategyResult, err := runner.Strategy.Decide(ctx, snapshot)
	if err != nil {
		return RunResult{}, fmt.Errorf("strategy: %w", err)
	}
	for _, noAction := range strategyResult.NoActions() {
		runner.observe(ctx, Observation{Stage: "strategy", Code: noAction.Code, Metadata: map[string]string{"instrument": noAction.Instrument.ID.String()}})
	}
	riskResult, err := runner.Risk.Evaluate(ctx, strategyResult.Intents(), snapshot.Portfolio(), policy)
	if err != nil {
		return RunResult{}, fmt.Errorf("risk: %w", err)
	}
	for _, rejection := range riskResult.Rejected() {
		runner.observe(ctx, Observation{Stage: "risk", Code: string(rejection.Code), OrderID: rejection.OrderID})
	}
	brokerResult, err := runner.Broker.Submit(ctx, riskResult.Approved())
	if err != nil {
		return RunResult{}, fmt.Errorf("broker: %w", err)
	}
	for _, rejection := range brokerResult.Rejected() {
		runner.observe(ctx, Observation{Stage: "broker", Code: string(rejection.Code), OrderID: rejection.OrderID})
	}
	if err := runner.Ledger.RecordBrokerOutcome(ctx, riskResult.Approved(), brokerResult); err != nil {
		return RunResult{}, fmt.Errorf("ledger: %w", err)
	}
	trace, err := stableRunTrace(snapshot, strategyResult, riskResult, brokerResult)
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{Strategy: strategyResult, Risk: riskResult, Broker: brokerResult, Trace: trace}, nil
}

func (runner Orchestrator) observe(ctx context.Context, observation Observation) {
	if runner.Observer != nil {
		_ = runner.Observer.Observe(ctx, observation)
	}
}

type traceIntent struct{ ID, Symbol, Side, Quantity, Reason, Policy string }
type traceRejection struct{ ID, Code, Policy string }
type traceNoAction struct{ Symbol, Code, Reason, Score string }
type runTrace struct {
	DecisionAt     string
	Strategy       []traceIntent
	NoActions      []traceNoAction
	RiskApproved   []traceIntent
	RiskRejected   []traceRejection
	RiskTrace      []RiskTrace
	BrokerAccepted []string
	BrokerRejected []traceRejection
}

func stableRunTrace(snapshot DecisionContext, strategy StrategyResult, risk RiskDecision, broker BrokerBatchOutcome) ([]byte, error) {
	result := runTrace{DecisionAt: snapshot.DecisionAt().UTC().Format(time.RFC3339Nano), RiskTrace: risk.Trace()}
	for _, noAction := range strategy.NoActions() {
		result.NoActions = append(result.NoActions, traceNoAction{noAction.Instrument.VenueSymbol, noAction.Code, noAction.Reason, noAction.ObservedScore.String()})
	}
	for _, intent := range strategy.Intents().Intents() {
		result.Strategy = append(result.Strategy, intentTrace(intent))
	}
	for _, intent := range risk.Approved().Intents() {
		result.RiskApproved = append(result.RiskApproved, intentTrace(intent))
	}
	for _, rejected := range risk.Rejected() {
		result.RiskRejected = append(result.RiskRejected, traceRejection{rejected.OrderID.String(), string(rejected.Code), rejected.PolicyVersion})
	}
	for _, accepted := range broker.Accepted() {
		result.BrokerAccepted = append(result.BrokerAccepted, accepted.OrderID.String())
	}
	for _, rejected := range broker.Rejected() {
		result.BrokerRejected = append(result.BrokerRejected, traceRejection{rejected.OrderID.String(), string(rejected.Code), rejected.PolicyVersion})
	}
	return json.Marshal(result)
}
func intentTrace(intent OrderIntent) traceIntent {
	return traceIntent{intent.ID.String(), intent.Instrument.VenueSymbol, string(intent.Side), intent.Quantity.Decimal().String(), intent.Reason, intent.Versions.Policy}
}
