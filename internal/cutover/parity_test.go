package cutover

import (
	"context"
	"testing"
	"time"
)

type adapterFunc func(context.Context, NonCapitalMode, DecisionContext, SubmitDenyBroker) (DecisionOutcome, error)

func (f adapterFunc) Decide(c context.Context, m NonCapitalMode, d DecisionContext, b SubmitDenyBroker) (DecisionOutcome, error) {
	return f(c, m, d, b)
}

func TestParityImmutableClonesAndTrustedClassification(t *testing.T) {
	at := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	captured := DecisionContext{SymbolID: "asset:btc", VenueSymbol: "BTCUSDT", DecisionAt: at, MarketAt: at, FlagSchemaVersion: FlagSchemaVersion, Inputs: map[string]string{"signal": "buy"}}
	legacy := adapterFunc(func(_ context.Context, _ NonCapitalMode, d DecisionContext, _ SubmitDenyBroker) (DecisionOutcome, error) {
		d.Inputs["signal"] = "sell"
		return DecisionOutcome{Action: "buy", SymbolID: d.SymbolID, VenueSymbol: d.VenueSymbol, Side: "buy", Quantity: "1", Notional: "10", PolicyVersion: "legacy-v1", SignalAt: at, DecisionAt: at}, nil
	})
	candidate := adapterFunc(func(_ context.Context, _ NonCapitalMode, d DecisionContext, _ SubmitDenyBroker) (DecisionOutcome, error) {
		if d.Inputs["signal"] != "buy" {
			t.Fatal("candidate observed legacy mutation")
		}
		return DecisionOutcome{Action: "buy", SymbolID: d.SymbolID, VenueSymbol: d.VenueSymbol, Side: "buy", Quantity: "1.0001", Notional: "10", PolicyVersion: "new-v2", SignalAt: at, DecisionAt: at}, nil
	})
	policy := ComparisonPolicy{QuantityToleranceBPS: 2, Expected: []ExpectedReason{{Code: "policy_version", LegacyValue: "legacy-v1", CandidateValue: "new-v2", PolicyVersion: "declared-parity-policy-v1"}}}
	first, err := RunParity(context.Background(), captured, legacy, candidate, policy)
	if err != nil {
		t.Fatal(err)
	}
	second, _ := RunParity(context.Background(), captured, legacy, candidate, policy)
	if first.ContentDigest != second.ContentDigest || first.Classification != "expected" {
		t.Fatalf("non-deterministic or unexpected: %+v %+v", first, second)
	}
	forged := policy
	forged.Expected = []ExpectedReason{{Code: "action", LegacyValue: "buy", CandidateValue: "sell", PolicyVersion: "caller-says-expected"}}
	mismatchCandidate := adapterFunc(func(_ context.Context, _ NonCapitalMode, d DecisionContext, _ SubmitDenyBroker) (DecisionOutcome, error) {
		return DecisionOutcome{Action: "sell", SymbolID: d.SymbolID, VenueSymbol: d.VenueSymbol, Side: "sell", Quantity: "1", Notional: "10", PolicyVersion: "new-v2", SignalAt: at, DecisionAt: at}, nil
	})
	result, err := RunParity(context.Background(), captured, legacy, mismatchCandidate, forged)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != "unexplained" {
		t.Fatalf("caller forged expected mismatch: %+v", result)
	}
}

func TestShadowBrokerSubmissionFailsRun(t *testing.T) {
	at := time.Now().UTC()
	evil := adapterFunc(func(_ context.Context, _ NonCapitalMode, d DecisionContext, b SubmitDenyBroker) (DecisionOutcome, error) {
		_ = b.Submit("order")
		return DecisionOutcome{Action: "none", SignalAt: at, DecisionAt: at}, nil
	})
	safe := adapterFunc(func(_ context.Context, _ NonCapitalMode, d DecisionContext, b SubmitDenyBroker) (DecisionOutcome, error) {
		return DecisionOutcome{Action: "none", SignalAt: at, DecisionAt: at}, nil
	})
	if _, err := RunParity(context.Background(), DecisionContext{DecisionAt: at, MarketAt: at}, evil, safe, ComparisonPolicy{}); err == nil {
		t.Fatal("malicious shadow broker attempt passed")
	}
}
