package validation

import (
	"encoding/json"
	"testing"
	"time"
)

func TestNoFillPrimitiveIsDigestBoundButNotEconomic(t *testing.T) {
	base := time.Date(2026, 2, 14, 3, 59, 0, 0, time.UTC)
	open := base.Add(time.Minute)
	p := FoldPrimitives{
		StartingCapital: 100, ExpectedObservations: 2, ObservedObservations: 2,
		Curve:   []CurvePrimitive{{At: base, Equity: 100, Benchmark: 100}, {At: open.Add(time.Minute), Equity: 100, Benchmark: 100}},
		NoFills: []NoFillPrimitive{{SchemaVersion: "simulated-no-fill-v1", OrderID: "order-1", Symbol: "AVAXUSDT", Side: "buy", SignalAt: base, SelectedOpenAt: open, EvaluatedAt: open.Add(time.Minute - time.Millisecond), RequestedQuantity: "1", ApprovedQuantity: "1", FilledQuantity: "0", ReferencePrice: "9.2", SelectedOpenPrice: "9.2", ExecutionPolicyVersion: "backtest-execution-v3", DatasetManifestID: "dataset-1", Reason: "simulated_no_fill_zero_trades", LiquidityEvidence: "zero_base_volume"}},
	}
	metrics, err := DeriveFoldMetrics(p)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.NoFillCount != 1 || metrics.Trades != 0 || metrics.Turnover != 0 || metrics.MaxLiquidityParticipation != 0 || metrics.Observations != 2 {
		t.Fatalf("no-fill affected economic or coverage metrics: %+v", metrics)
	}
	p.BaselineNoFills = append([]NoFillPrimitive(nil), p.NoFills...)
	baselineMetrics, err := DeriveFoldMetrics(p)
	if err != nil || baselineMetrics.BaselineNoFillCount != 1 || baselineMetrics.Trades != 0 {
		t.Fatalf("baseline no-fill not reported separately: %+v err=%v", baselineMetrics, err)
	}
	p.BaselineNoFills = nil
	if err := ValidateFoldMetrics(metrics, SampleRequirements{MinObservationsPerFold: 1, MinTradesPerFold: 1}); err == nil {
		t.Fatal("a no-fill satisfied the zero-trade gate")
	}
	before, err := FoldResultsDigest([]FoldResult{{Primitives: p, Metrics: metrics}})
	if err != nil {
		t.Fatal(err)
	}
	bad := p
	bad.NoFills = append([]NoFillPrimitive(nil), p.NoFills...)
	bad.NoFills[0].OrderID = "order-2"
	after, err := FoldResultsDigest([]FoldResult{{Primitives: bad, Metrics: metrics}})
	if err != nil || before == after {
		t.Fatalf("no-fill absent from fold digest: %v", err)
	}
	for name, mutate := range map[string]func(*NoFillPrimitive){
		"duplicate":       func(*NoFillPrimitive) {},
		"positive fill":   func(v *NoFillPrimitive) { v.FilledQuantity = "0.1" },
		"wrong close":     func(v *NoFillPrimitive) { v.EvaluatedAt = open.Add(time.Minute) },
		"outside curve":   func(v *NoFillPrimitive) { v.EvaluatedAt = open.Add(2 * time.Minute) },
		"wrong reason":    func(v *NoFillPrimitive) { v.Reason = "broker_rejected" },
		"wrong policy":    func(v *NoFillPrimitive) { v.ExecutionPolicyVersion = "backtest-execution-v2" },
		"invalid request": func(v *NoFillPrimitive) { v.RequestedQuantity = "NaN" },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := p
			invalid.NoFills = append([]NoFillPrimitive(nil), p.NoFills...)
			if name == "duplicate" {
				invalid.NoFills = append(invalid.NoFills, invalid.NoFills[0])
			} else {
				mutate(&invalid.NoFills[0])
			}
			if _, err := DeriveFoldMetrics(invalid); err == nil {
				t.Fatal("malformed no-fill accepted")
			}
		})
	}
	p.Fills = []FillPrimitive{{ID: "fill-1", OrderID: "order-1", Symbol: "AVAXUSDT", Side: "buy", At: open, Notional: 9.2, AvailableLiquidity: 100}}
	if _, err := DeriveFoldMetrics(p); err == nil {
		t.Fatal("same order was both filled and rejected")
	}
	legacy := FoldPrimitives{StartingCapital: 100, ExpectedObservations: 2, ObservedObservations: 2, Curve: p.Curve}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) == "" || containsJSONKey(encoded, "no_fills") {
		t.Fatal("old fold shape gained no-fill field")
	}
}

func containsJSONKey(encoded []byte, key string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(encoded, &fields) != nil {
		return false
	}
	_, ok := fields[key]
	return ok
}

func TestCapacityNoFillPrimitiveValidatesBoundAndRemainsNonEconomic(t *testing.T) {
	base := time.Date(2026, 2, 14, 3, 59, 0, 0, time.UTC)
	open := base.Add(time.Minute)
	value := NoFillPrimitive{
		SchemaVersion: "simulated-no-fill-v2", OrderID: "order-cap", Symbol: "AVAXUSDT", Side: "sell",
		SignalAt: base, SelectedOpenAt: open, EvaluatedAt: open.Add(time.Minute - time.Millisecond),
		RequestedQuantity: "8.42", ApprovedQuantity: "8.42", FilledQuantity: "0",
		ReferencePrice: "9.2", SelectedOpenPrice: "9.2", SelectedClosePrice: "9.1",
		ExecutionPolicyVersion: "backtest-execution-v4", DatasetManifestID: "dataset-1",
		Reason: "simulated_no_fill_volume_cap", LiquidityEvidence: "selected_bar_base_volume_10pct",
		BarVolume: "5.19", CapacityQuantity: "0.519",
	}
	p := FoldPrimitives{StartingCapital: 100, ExpectedObservations: 2, ObservedObservations: 2,
		Curve:   []CurvePrimitive{{At: base, Equity: 100, Benchmark: 100}, {At: open.Add(time.Minute), Equity: 100, Benchmark: 100}},
		NoFills: []NoFillPrimitive{value}}
	metrics, err := DeriveFoldMetrics(p)
	if err != nil || metrics.NoFillCount != 1 || metrics.Trades != 0 || metrics.Turnover != 0 || metrics.MaxLiquidityParticipation != 0 {
		t.Fatalf("valid capacity no-fill changed economics: metrics=%+v err=%v", metrics, err)
	}
	firstDigest, err := FoldResultsDigest([]FoldResult{{Primitives: p, Metrics: metrics}})
	if err != nil {
		t.Fatal(err)
	}
	changed := p
	changed.NoFills = []NoFillPrimitive{value}
	changed.NoFills[0].CapacityQuantity = "0.518"
	secondDigest, err := FoldResultsDigest([]FoldResult{{Primitives: changed, Metrics: metrics}})
	if err != nil || firstDigest == secondDigest {
		t.Fatalf("capacity evidence absent from immutable fold digest: %v", err)
	}
	for name, mutate := range map[string]func(*NoFillPrimitive){
		"schema":              func(v *NoFillPrimitive) { v.SchemaVersion = "simulated-no-fill-v1" },
		"policy":              func(v *NoFillPrimitive) { v.ExecutionPolicyVersion = "backtest-execution-v3" },
		"reason":              func(v *NoFillPrimitive) { v.Reason = "simulated_no_fill_zero_trades" },
		"liquidity":           func(v *NoFillPrimitive) { v.LiquidityEvidence = "zero_base_volume" },
		"missing volume":      func(v *NoFillPrimitive) { v.BarVolume = "" },
		"negative volume":     func(v *NoFillPrimitive) { v.BarVolume = "-1" },
		"missing cap":         func(v *NoFillPrimitive) { v.CapacityQuantity = "" },
		"cap over 10pct":      func(v *NoFillPrimitive) { v.CapacityQuantity = "0.52" },
		"approved within cap": func(v *NoFillPrimitive) { v.ApprovedQuantity = "0.5" },
		"negative cap":        func(v *NoFillPrimitive) { v.CapacityQuantity = "-0.1" },
		"missing close":       func(v *NoFillPrimitive) { v.SelectedClosePrice = "" },
		"nonpositive close":   func(v *NoFillPrimitive) { v.SelectedClosePrice = "0" },
		"filled":              func(v *NoFillPrimitive) { v.FilledQuantity = "0.01" },
		"wrong time":          func(v *NoFillPrimitive) { v.EvaluatedAt = open.Add(time.Minute) },
		"wrong open":          func(v *NoFillPrimitive) { v.SelectedOpenAt = open.Add(time.Millisecond) },
	} {
		t.Run(name, func(t *testing.T) {
			invalid := p
			invalid.NoFills = []NoFillPrimitive{value}
			mutate(&invalid.NoFills[0])
			if _, err := DeriveFoldMetrics(invalid); err == nil {
				t.Fatal("invalid capacity no-fill accepted")
			}
		})
	}
	p.Fills = []FillPrimitive{{ID: "fill-cap", OrderID: value.OrderID, Symbol: value.Symbol, Side: value.Side, At: value.EvaluatedAt, Notional: 9.1, AvailableLiquidity: 100}}
	if _, err := DeriveFoldMetrics(p); err == nil {
		t.Fatal("capacity-cancelled order also had an economic fill")
	}
	v3 := p
	v3.Fills = nil
	v3.NoFills = []NoFillPrimitive{value}
	v3.NoFills[0].ExecutionPolicyVersion = "backtest-execution-v3"
	v3.NoFills[0].SchemaVersion = "simulated-no-fill-v1"
	v3.NoFills[0].Reason = "simulated_no_fill_zero_trades"
	v3.NoFills[0].LiquidityEvidence = "zero_base_volume"
	if _, err := DeriveFoldMetrics(v3); err == nil {
		t.Fatal("v3 accepted v4-only capacity fields")
	}
}
