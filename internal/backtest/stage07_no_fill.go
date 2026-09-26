package backtest

import (
	"fmt"
	"math"
	"math/big"
	"reflect"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/validation"
)

// stage07NoFillPrimitives checks the independent intent, broker-decision and
// selected-bar evidence before admitting a non-economic fold primitive.
func stage07NoFillPrimitives(result Stage05StrategyResult, execution map[string][]services.OHLCV, curveStart, curveEnd time.Time) ([]validation.NoFillPrimitive, error) {
	policy := result.Manifest.ExecutionPolicy.Version
	if len(result.NoFills) != len(result.Artifacts.NoFills) || (len(result.NoFills) > 0 && !reflect.DeepEqual(result.NoFills, result.Artifacts.NoFills)) {
		return nil, stage07NoFillError("strategy and artifact no-fill traces differ")
	}
	if policy != "backtest-execution-v3" {
		if len(result.NoFills) != 0 {
			return nil, stage07NoFillError("no-fill evidence has a non-v3 execution policy")
		}
		return nil, nil
	}
	windowStart, startErr := time.Parse(time.RFC3339Nano, result.Manifest.Start)
	windowEnd, endErr := time.Parse(time.RFC3339Nano, result.Manifest.End)
	if startErr != nil || endErr != nil || !windowStart.Before(windowEnd) || result.Manifest.DatasetManifestID == "" || curveStart.IsZero() || curveEnd.Before(curveStart) {
		return nil, stage07NoFillError("v3 result has incomplete window or dataset identity")
	}
	orders := make(map[string]OrderArtifact, len(result.Artifacts.Orders))
	for _, order := range result.Artifacts.Orders {
		if order.OrderID == "" || order.OrderID != order.IntentID || orders[order.OrderID].OrderID != "" {
			return nil, stage07NoFillError("missing or duplicate source order identity")
		}
		orders[order.OrderID] = order
	}
	riskRejected := map[string]bool{}
	brokerNoFills := map[string]int{}
	brokerApproved := map[string]string{}
	brokerOtherRejected := map[string]bool{}
	for _, decision := range result.Artifacts.Decisions {
		if decision.Stage == "risk" {
			riskRejected[decision.IntentID] = true
		}
		if decision.Stage == "broker" && decision.Code == "simulated_no_fill_zero_trades" {
			brokerNoFills[decision.IntentID]++
			brokerApproved[decision.IntentID] = decision.ApprovedQuantity
		} else if decision.Stage == "broker" && decision.Code != "" && decision.Code != "filled" && decision.Code != "accepted" {
			brokerOtherRejected[decision.IntentID] = true
		}
	}
	filledOrders := map[string]bool{}
	for _, fill := range result.Artifacts.Fills {
		filledOrders[fill.OrderID] = true
	}
	seen := map[string]bool{}
	primitives := make([]validation.NoFillPrimitive, 0, len(result.NoFills))
	previousSignal := time.Time{}
	for _, noFill := range result.NoFills {
		if seen[noFill.OrderID] || brokerNoFills[noFill.OrderID] != 1 || !stage07DecimalEqual(brokerApproved[noFill.OrderID], noFill.ApprovedQuantity) || riskRejected[noFill.OrderID] || brokerOtherRejected[noFill.OrderID] || filledOrders[noFill.OrderID] || noFill.SchemaVersion != "simulated-no-fill-v1" || noFill.ExecutionPolicyVersion != policy || noFill.DatasetManifestID != result.Manifest.DatasetManifestID || noFill.Reason != "simulated_no_fill_zero_trades" || noFill.LiquidityEvidence != "zero_base_volume" {
			return nil, stage07NoFillError("duplicate, conflicting or unbound no-fill outcome")
		}
		order, exists := orders[noFill.OrderID]
		if !exists || order.Symbol != noFill.Symbol || order.Side != noFill.Side || !stage07DecimalEqual(order.Quantity, noFill.RequestedQuantity) || !stage07DecimalEqual(order.Metadata["decision_reference_price"], noFill.ReferencePrice) || order.Metadata["execution_event_at"] != noFill.SelectedOpenAt {
			return nil, stage07NoFillError("no-fill does not match its source intent")
		}
		signal, signalErr := time.Parse(time.RFC3339Nano, noFill.SignalAt)
		selected, selectedErr := time.Parse(time.RFC3339Nano, noFill.SelectedOpenAt)
		evaluated, evaluatedErr := time.Parse(time.RFC3339Nano, noFill.EvaluatedAt)
		orderSignal, orderSignalErr := time.Parse(time.RFC3339Nano, order.SignalAt)
		if signalErr != nil || selectedErr != nil || evaluatedErr != nil || orderSignalErr != nil || !signal.Equal(orderSignal) || signal.Before(windowStart) || !signal.Before(selected) || !selected.Before(evaluated) || !evaluated.Before(windowEnd) || signal.Before(curveStart) || evaluated.After(curveEnd) || (!previousSignal.IsZero() && signal.Before(previousSignal)) {
			return nil, stage07NoFillError("no-fill chronology differs from fold and source order")
		}
		bar, exists := stage07BarAt(execution[noFill.Symbol], selected)
		if !exists || bar.CloseTime != evaluated.UnixMilli() || bar.CloseTime != bar.OpenTime+int64(time.Minute/time.Millisecond)-1 || !stage07Positive(bar.Open) || !stage07Positive(bar.High) || !stage07Positive(bar.Low) || !stage07Positive(bar.Close) || bar.Low > bar.High || bar.Open < bar.Low || bar.Open > bar.High || bar.Close < bar.Low || bar.Close > bar.High || math.IsNaN(bar.Volume) || math.IsInf(bar.Volume, 0) || bar.Volume != 0 || !stage07DecimalMatchesFloat(noFill.SelectedOpenPrice, bar.Open) {
			return nil, stage07NoFillError("no-fill does not match a verified zero-volume selected minute")
		}
		requested, requestOK := new(big.Rat).SetString(noFill.RequestedQuantity)
		approved, approvedOK := new(big.Rat).SetString(noFill.ApprovedQuantity)
		filled, filledOK := new(big.Rat).SetString(noFill.FilledQuantity)
		reference, referenceOK := new(big.Rat).SetString(noFill.ReferencePrice)
		if !requestOK || !approvedOK || !filledOK || !referenceOK || requested.Sign() <= 0 || approved.Sign() <= 0 || approved.Cmp(requested) > 0 || filled.Sign() != 0 || reference.Sign() <= 0 {
			return nil, stage07NoFillError("invalid no-fill quantities or reference price")
		}
		primitives = append(primitives, validation.NoFillPrimitive{SchemaVersion: noFill.SchemaVersion, OrderID: noFill.OrderID, Symbol: noFill.Symbol, Side: noFill.Side, SignalAt: signal.UTC(), SelectedOpenAt: selected.UTC(), EvaluatedAt: evaluated.UTC(), RequestedQuantity: noFill.RequestedQuantity, ApprovedQuantity: noFill.ApprovedQuantity, FilledQuantity: noFill.FilledQuantity, ReferencePrice: noFill.ReferencePrice, SelectedOpenPrice: noFill.SelectedOpenPrice, ExecutionPolicyVersion: noFill.ExecutionPolicyVersion, DatasetManifestID: noFill.DatasetManifestID, Reason: noFill.Reason, LiquidityEvidence: noFill.LiquidityEvidence})
		seen[noFill.OrderID], previousSignal = true, signal
	}
	for orderID, count := range brokerNoFills {
		if count != 1 || !seen[orderID] {
			return nil, stage07NoFillError("broker no-fill decision lacks its unique outcome")
		}
	}
	for orderID := range orders {
		if !riskRejected[orderID] && !brokerOtherRejected[orderID] && !filledOrders[orderID] && !seen[orderID] {
			return nil, stage07NoFillError("source intent has no terminal economic or no-fill outcome")
		}
	}
	return primitives, nil
}

func stage07DecimalEqual(left, right string) bool {
	a, okA := new(big.Rat).SetString(left)
	b, okB := new(big.Rat).SetString(right)
	return okA && okB && a.Cmp(b) == 0
}

func stage07DecimalMatchesFloat(raw string, value float64) bool {
	parsed, ok := new(big.Rat).SetString(raw)
	if !ok {
		return false
	}
	f, _ := parsed.Float64()
	return !math.IsNaN(f) && !math.IsInf(f, 0) && f == value
}

func stage07NoFillError(detail string) error {
	return &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Field: "no_fills", Details: fmt.Sprintf("Stage 07 source: %s", detail)}
}
