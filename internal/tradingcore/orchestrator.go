package tradingcore

import (
	"context"
	"encoding/json"
	"errors"
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
type RunStage string

const (
	RunStageSource   RunStage = "source"
	RunStageStrategy RunStage = "strategy"
	RunStageRisk     RunStage = "risk"
	RunStageBroker   RunStage = "broker"
	RunStageLedger   RunStage = "ledger"
	RunStageTrace    RunStage = "trace"
)

type RunError struct {
	Stage               RunStage
	SideEffectsPossible bool
	Err                 error
}

func (err *RunError) Error() string { return fmt.Sprintf("%s: %v", err.Stage, err.Err) }
func (err *RunError) Unwrap() error { return err.Err }
func IsPreSubmissionFailure(err error) bool {
	var runErr *RunError
	return errors.As(err, &runErr) && !runErr.SideEffectsPossible && (runErr.Stage == RunStageSource || runErr.Stage == RunStageStrategy || runErr.Stage == RunStageRisk)
}
func runError(stage RunStage, sideEffects bool, err error) error {
	return &RunError{Stage: stage, SideEffectsPossible: sideEffects, Err: err}
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
		return RunResult{}, runError(RunStageSource, false, err)
	}
	strategyResult, err := runner.Strategy.Decide(ctx, snapshot)
	if err != nil {
		return RunResult{}, runError(RunStageStrategy, false, err)
	}
	for _, noAction := range strategyResult.NoActions() {
		runner.observe(ctx, Observation{Stage: "strategy", Code: noAction.Code, Metadata: map[string]string{"instrument": noAction.Instrument.ID.String()}})
	}
	riskResult, err := runner.Risk.Evaluate(ctx, strategyResult.Intents(), snapshot.Portfolio(), policy)
	if err != nil {
		return RunResult{}, runError(RunStageRisk, false, err)
	}
	for _, rejection := range riskResult.Rejected() {
		runner.observe(ctx, Observation{Stage: "risk", Code: string(rejection.Code), OrderID: rejection.OrderID})
	}
	brokerResult, err := runner.Broker.Submit(ctx, riskResult.Approved())
	if err != nil {
		return RunResult{}, runError(RunStageBroker, true, err)
	}
	for _, rejection := range brokerResult.Rejected() {
		runner.observe(ctx, Observation{Stage: "broker", Code: string(rejection.Code), OrderID: rejection.OrderID})
	}
	if err := runner.Ledger.RecordBrokerOutcome(ctx, riskResult.Approved(), brokerResult); err != nil {
		return RunResult{}, runError(RunStageLedger, true, err)
	}
	trace, err := stableRunTrace(snapshot, strategyResult, riskResult, brokerResult)
	if err != nil {
		return RunResult{}, runError(RunStageTrace, true, err)
	}
	return RunResult{Strategy: strategyResult, Risk: riskResult, Broker: brokerResult, Trace: trace}, nil
}

func (runner Orchestrator) observe(ctx context.Context, observation Observation) {
	if runner.Observer != nil {
		observation.Metadata = cloneStrings(observation.Metadata)
		_ = runner.Observer.Observe(ctx, observation)
	}
}

type traceIntent struct{ ID, Symbol, Side, Quantity, Reason, Policy string }
type traceRejection struct{ ID, Code, Policy string }
type traceNoAction struct{ Symbol, Code, Reason, Score string }
type traceFill struct{ ID, OrderID, ProviderFillID, Quantity, Price, Fee, FeeAsset, CostModelVersion string }
type traceAccepted struct {
	OrderID, ProviderOrderID, Status, Remaining string
	Fills                                       []traceFill
}
type runTrace struct {
	DecisionAt         string
	Strategy           []traceIntent
	NoActions          []traceNoAction
	RiskApproved       []traceIntent
	RiskRejected       []traceRejection
	RiskTrace          []RiskTrace
	BrokerCompleteness OutcomeCompleteness
	BrokerAccepted     []traceAccepted
	BrokerRejected     []traceRejection
}

func stableRunTrace(snapshot DecisionContext, strategy StrategyResult, risk RiskDecision, broker BrokerBatchOutcome) ([]byte, error) {
	result := runTrace{DecisionAt: snapshot.DecisionAt().UTC().Format(time.RFC3339Nano), RiskTrace: risk.Trace(), BrokerCompleteness: broker.Completeness()}
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
		item := traceAccepted{OrderID: accepted.OrderID.String(), ProviderOrderID: accepted.ProviderOrderID, Status: string(accepted.Status)}
		if remaining, ok := accepted.Remaining.Get(); ok {
			item.Remaining = remaining.Decimal().String()
		}
		for _, fill := range accepted.Fills() {
			item.Fills = append(item.Fills, traceFill{fill.ID.String(), fill.OrderID.String(), fill.ProviderFillID, fill.Quantity.Decimal().String(), fill.Price.Decimal().String(), fill.Fee.Decimal().String(), fill.FeeAsset.String(), fill.CostModelVersion})
		}
		result.BrokerAccepted = append(result.BrokerAccepted, item)
	}
	for _, rejected := range broker.Rejected() {
		result.BrokerRejected = append(result.BrokerRejected, traceRejection{rejected.OrderID.String(), string(rejected.Code), rejected.PolicyVersion})
	}
	return json.Marshal(result)
}
func intentTrace(intent OrderIntent) traceIntent {
	return traceIntent{intent.ID.String(), intent.Instrument.VenueSymbol, string(intent.Side), intent.Quantity.Decimal().String(), intent.Reason, intent.Versions.Policy}
}
