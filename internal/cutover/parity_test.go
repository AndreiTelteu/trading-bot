package cutover

import (
	"context"
	"strings"
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

func TestVerifyComparisonRejectsCallerForgedDigest(t *testing.T) {
	at := time.Unix(10, 0).UTC()
	ctx := DecisionContext{SymbolID: "asset", VenueSymbol: "AAAUSDT", DecisionAt: at, MarketAt: at}
	outcome := DecisionOutcome{Action: "skip", SymbolID: "asset", VenueSymbol: "AAAUSDT", Quantity: "0", Notional: "0", SignalAt: at, DecisionAt: at}
	fixed := adapterFunc(func(context.Context, NonCapitalMode, DecisionContext, SubmitDenyBroker) (DecisionOutcome, error) {
		return outcome, nil
	})
	comparison, err := RunParity(context.Background(), ctx, fixed, fixed, ComparisonPolicy{})
	if err != nil || VerifyComparison(comparison) != nil {
		t.Fatalf("genuine comparison rejected: %+v %v", comparison, err)
	}
	comparison.ContentDigest = strings.Repeat("f", 64)
	if VerifyComparison(comparison) == nil {
		t.Fatal("forged comparison digest accepted")
	}
}

func TestRunParityRejectsCallerNominatedContextDigest(t *testing.T) {
	at := time.Unix(15, 1234).UTC()
	ctx := DecisionContext{ContextID: strings.Repeat("a", 64), SymbolID: "asset", VenueSymbol: "AAAUSDT", DecisionAt: at, MarketAt: at}
	outcome := DecisionOutcome{Action: "skip", SignalAt: at, DecisionAt: at}
	fixed := adapterFunc(func(context.Context, NonCapitalMode, DecisionContext, SubmitDenyBroker) (DecisionOutcome, error) {
		return outcome, nil
	})
	if _, err := RunParity(context.Background(), ctx, fixed, fixed, ComparisonPolicy{}); err == nil {
		t.Fatal("caller-nominated context digest accepted")
	}
}

func TestVerifyComparisonWithPolicyRejectsRelabeledEvidence(t *testing.T) {
	at := time.Unix(20, 0).UTC()
	ctx := DecisionContext{SymbolID: "asset", VenueSymbol: "AAAUSDT", DecisionAt: at, MarketAt: at}
	legacyOutcome := DecisionOutcome{Action: "buy", SymbolID: "asset", VenueSymbol: "AAAUSDT", Side: "buy", Quantity: "1", Notional: "10", SignalAt: at, DecisionAt: at}
	candidateOutcome := legacyOutcome
	candidateOutcome.Quantity = "2"
	adapter := func(outcome DecisionOutcome) adapterFunc {
		return func(context.Context, NonCapitalMode, DecisionContext, SubmitDenyBroker) (DecisionOutcome, error) {
			return outcome, nil
		}
	}
	permissive := ComparisonPolicy{Expected: []ExpectedReason{{Code: "quantity", LegacyValue: "1", CandidateValue: "2", PolicyVersion: "permissive-v1"}}}
	comparison, err := RunParity(context.Background(), ctx, adapter(legacyOutcome), adapter(candidateOutcome), permissive)
	if err != nil || comparison.Classification != "expected" {
		t.Fatalf("fixture comparison: %+v err=%v", comparison, err)
	}
	if err := VerifyComparisonWithPolicy(comparison, permissive); err != nil {
		t.Fatalf("bound policy rejected genuine evidence: %v", err)
	}
	if err := VerifyComparisonWithPolicy(comparison, ComparisonPolicy{}); err == nil {
		t.Fatal("caller-relabeled expected divergence passed a stricter bound policy")
	}
}
