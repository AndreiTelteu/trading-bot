package cutover

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const ParitySchemaVersion = "stage08-parity-v1"

// DecisionContext is captured and hashed once. Adapters receive independent
// JSON clones so neither can mutate the other's causal input.
type DecisionContext struct {
	ContextID, SymbolID, VenueSymbol                                                                                string
	DecisionAt, MarketAt                                                                                            time.Time
	FlagSchemaVersion, EngineVersion, StrategyVersion, PolicyVersion, ModelVersion, DatasetVersion, UniverseVersion string
	Inputs                                                                                                          map[string]string
}

type NonCapitalMode struct{ token struct{} }

func ShadowOnlyMode() NonCapitalMode { return NonCapitalMode{} }

type ShadowAdapter interface {
	Decide(context.Context, NonCapitalMode, DecisionContext, SubmitDenyBroker) (DecisionOutcome, error)
}
type SubmitDenyBroker struct{ attempts *int }

func (b SubmitDenyBroker) Submit(any) error {
	*b.attempts++
	return fmt.Errorf("shadow_execution_forbidden")
}

type DecisionOutcome struct {
	Action, SymbolID, VenueSymbol, Side, Quantity, Notional                                      string            `json:",omitempty"`
	RejectionCode                                                                                string            `json:"rejection_code,omitempty"`
	PrimaryExitReason                                                                            string            `json:"primary_exit_reason,omitempty"`
	ConcurrentExitReasons                                                                        []string          `json:"concurrent_exit_reasons,omitempty"`
	FactorTrace                                                                                  map[string]string `json:"factor_trace,omitempty"`
	EngineVersion, StrategyVersion, PolicyVersion, ModelVersion, DatasetVersion, UniverseVersion string
	SignalAt, DecisionAt                                                                         time.Time
}

type ExpectedReason struct{ Code, LegacyValue, CandidateValue, PolicyVersion string }
type ComparisonPolicy struct {
	QuantityToleranceBPS, NotionalToleranceBPS int64
	Expected                                   []ExpectedReason
}
type Comparison struct {
	ContextID, LegacyDigest, CandidateDigest, ContentDigest, Classification string
	DivergenceCodes, ExpectedReasons                                        []string
	Legacy, Candidate                                                       DecisionOutcome
	SubmitAttempts                                                          int
}

func RunParity(ctx context.Context, captured DecisionContext, legacy, candidate ShadowAdapter, policy ComparisonPolicy) (Comparison, error) {
	if legacy == nil || candidate == nil {
		return Comparison{}, fmt.Errorf("both parity adapters are required")
	}
	captured.DecisionAt = postgresTime(captured.DecisionAt)
	captured.MarketAt = postgresTime(captured.MarketAt)
	contextDigest, err := CanonicalContextID(captured)
	if err != nil {
		return Comparison{}, err
	}
	if captured.ContextID != "" && captured.ContextID != contextDigest {
		return Comparison{}, fmt.Errorf("parity context identity does not match canonical captured input")
	}
	captured.ContextID = contextDigest
	canonical, err := canonicalJSON(captured)
	if err != nil {
		return Comparison{}, err
	}
	clone := func() (DecisionContext, error) {
		var out DecisionContext
		err := json.Unmarshal(canonical, &out)
		out.ContextID = captured.ContextID
		return out, err
	}
	leftCtx, err := clone()
	if err != nil {
		return Comparison{}, err
	}
	rightCtx, err := clone()
	if err != nil {
		return Comparison{}, err
	}
	attempts := 0
	broker := SubmitDenyBroker{attempts: &attempts}
	left, err := legacy.Decide(ctx, ShadowOnlyMode(), leftCtx, broker)
	if err != nil {
		return Comparison{}, fmt.Errorf("legacy shadow adapter: %w", err)
	}
	right, err := candidate.Decide(ctx, ShadowOnlyMode(), rightCtx, broker)
	if err != nil {
		return Comparison{}, fmt.Errorf("candidate shadow adapter: %w", err)
	}
	if attempts != 0 {
		return Comparison{}, fmt.Errorf("shadow adapter attempted %d forbidden broker submissions", attempts)
	}
	left.SignalAt, left.DecisionAt = postgresTime(left.SignalAt), postgresTime(left.DecisionAt)
	right.SignalAt, right.DecisionAt = postgresTime(right.SignalAt), postgresTime(right.DecisionAt)
	leftJSON, _ := canonicalJSON(left)
	rightJSON, _ := canonicalJSON(right)
	result := Comparison{ContextID: captured.ContextID, LegacyDigest: digest(leftJSON), CandidateDigest: digest(rightJSON), Legacy: left, Candidate: right}
	result.DivergenceCodes = compareOutcomes(left, right, policy)
	if len(result.DivergenceCodes) == 0 {
		result.Classification = "match"
	} else {
		for _, code := range result.DivergenceCodes {
			if reason, ok := trustedExpected(code, left, right, policy.Expected); ok {
				result.ExpectedReasons = append(result.ExpectedReasons, reason)
			}
		}
		if len(result.ExpectedReasons) == len(result.DivergenceCodes) {
			result.Classification = "expected"
		} else {
			result.Classification = "unexplained"
		}
	}
	sort.Strings(result.DivergenceCodes)
	sort.Strings(result.ExpectedReasons)
	content, _ := canonicalJSON(struct {
		Context        string
		Left, Right    DecisionOutcome
		Codes, Reasons []string
		Class          string
	}{result.ContextID, left, right, result.DivergenceCodes, result.ExpectedReasons, result.Classification})
	result.ContentDigest = digest(content)
	return result, nil
}

// CanonicalContextID derives the decision identity from the captured causal
// input. ContextID is deliberately excluded so callers cannot nominate an
// arbitrary digest and have it treated as server-derived evidence.
func CanonicalContextID(value DecisionContext) (string, error) {
	value.ContextID = ""
	value.DecisionAt = postgresTime(value.DecisionAt)
	value.MarketAt = postgresTime(value.MarketAt)
	canonical, err := canonicalJSON(value)
	if err != nil {
		return "", err
	}
	return digest(canonical), nil
}

func postgresTime(value time.Time) time.Time {
	return time.UnixMicro(value.UTC().UnixMicro()).UTC()
}

// VerifyComparison proves that persisted parity evidence was derived from its
// exact compact outcomes instead of trusting caller-supplied digest labels.
func VerifyComparison(value Comparison) error {
	if len(value.ContextID) != 64 || value.SubmitAttempts != 0 {
		return fmt.Errorf("invalid parity context or forbidden submission attempts")
	}
	if value.Classification != "match" && value.Classification != "expected" && value.Classification != "unexplained" {
		return fmt.Errorf("invalid parity classification")
	}
	leftJSON, err := canonicalJSON(value.Legacy)
	if err != nil || value.LegacyDigest != digest(leftJSON) {
		return fmt.Errorf("legacy parity outcome digest mismatch")
	}
	rightJSON, err := canonicalJSON(value.Candidate)
	if err != nil || value.CandidateDigest != digest(rightJSON) {
		return fmt.Errorf("candidate parity outcome digest mismatch")
	}
	codes := append([]string(nil), value.DivergenceCodes...)
	reasons := append([]string(nil), value.ExpectedReasons...)
	sort.Strings(codes)
	sort.Strings(reasons)
	if !bytes.Equal(mustJSON(codes), mustJSON(value.DivergenceCodes)) || !bytes.Equal(mustJSON(reasons), mustJSON(value.ExpectedReasons)) {
		return fmt.Errorf("parity divergence evidence is not canonical")
	}
	content, err := canonicalJSON(struct {
		Context        string
		Left, Right    DecisionOutcome
		Codes, Reasons []string
		Class          string
	}{value.ContextID, value.Legacy, value.Candidate, codes, reasons, value.Classification})
	if err != nil || value.ContentDigest != digest(content) {
		return fmt.Errorf("parity comparison content digest mismatch")
	}
	return nil
}

// VerifyComparisonWithPolicy also recomputes tolerance handling and the
// expected/unexplained classification from the acceptance policy bound to the
// population. A caller cannot relabel a genuine outcome pair as acceptable.
func VerifyComparisonWithPolicy(value Comparison, policy ComparisonPolicy) error {
	if err := VerifyComparison(value); err != nil {
		return err
	}
	codes := compareOutcomes(value.Legacy, value.Candidate, policy)
	var reasons []string
	classification := "match"
	if len(codes) > 0 {
		for _, code := range codes {
			if reason, ok := trustedExpected(code, value.Legacy, value.Candidate, policy.Expected); ok {
				reasons = append(reasons, reason)
			}
		}
		classification = "unexplained"
		if len(reasons) == len(codes) {
			classification = "expected"
		}
	}
	sort.Strings(codes)
	sort.Strings(reasons)
	if classification != value.Classification || !bytes.Equal(mustJSON(codes), mustJSON(value.DivergenceCodes)) || !bytes.Equal(mustJSON(reasons), mustJSON(value.ExpectedReasons)) {
		return fmt.Errorf("parity classification does not match bound policy")
	}
	return nil
}

func canonicalJSON(value any) ([]byte, error) { return json.Marshal(value) }
func digest(value []byte) string              { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func compareOutcomes(a, b DecisionOutcome, p ComparisonPolicy) []string {
	var out []string
	compare := func(code, x, y string) {
		if x != y {
			out = append(out, code)
		}
	}
	compare("action", a.Action, b.Action)
	compare("symbol", a.SymbolID+"/"+a.VenueSymbol, b.SymbolID+"/"+b.VenueSymbol)
	compare("side", a.Side, b.Side)
	if !withinBPS(a.Quantity, b.Quantity, p.QuantityToleranceBPS) {
		out = append(out, "quantity")
	}
	if !withinBPS(a.Notional, b.Notional, p.NotionalToleranceBPS) {
		out = append(out, "notional")
	}
	compare("rejection_code", a.RejectionCode, b.RejectionCode)
	compare("primary_exit_reason", a.PrimaryExitReason, b.PrimaryExitReason)
	ac, bc := append([]string(nil), a.ConcurrentExitReasons...), append([]string(nil), b.ConcurrentExitReasons...)
	sort.Strings(ac)
	sort.Strings(bc)
	if !bytes.Equal(mustJSON(ac), mustJSON(bc)) {
		out = append(out, "concurrent_exit_reasons")
	}
	if !bytes.Equal(mustJSON(a.FactorTrace), mustJSON(b.FactorTrace)) {
		out = append(out, "factor_trace")
	}
	compare("engine_version", a.EngineVersion, b.EngineVersion)
	compare("strategy_version", a.StrategyVersion, b.StrategyVersion)
	compare("policy_version", a.PolicyVersion, b.PolicyVersion)
	compare("model_version", a.ModelVersion, b.ModelVersion)
	compare("dataset_version", a.DatasetVersion, b.DatasetVersion)
	compare("universe_version", a.UniverseVersion, b.UniverseVersion)
	if !a.SignalAt.Equal(b.SignalAt) || !a.DecisionAt.Equal(b.DecisionAt) {
		out = append(out, "timestamps")
	}
	return out
}
func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }
func withinBPS(x, y string, tolerance int64) bool {
	var a, b float64
	if _, e := fmt.Sscan(x, &a); e != nil {
		return x == y
	}
	if _, e := fmt.Sscan(y, &b); e != nil {
		return false
	}
	base := math.Max(math.Abs(a), math.Abs(b))
	if base == 0 {
		return true
	}
	return math.Abs(a-b)*10000/base <= float64(tolerance)
}
func trustedExpected(code string, a, b DecisionOutcome, reasons []ExpectedReason) (string, bool) {
	values := map[string][2]string{"quantity": {a.Quantity, b.Quantity}, "notional": {a.Notional, b.Notional}, "engine_version": {a.EngineVersion, b.EngineVersion}, "strategy_version": {a.StrategyVersion, b.StrategyVersion}, "policy_version": {a.PolicyVersion, b.PolicyVersion}, "model_version": {a.ModelVersion, b.ModelVersion}, "dataset_version": {a.DatasetVersion, b.DatasetVersion}, "universe_version": {a.UniverseVersion, b.UniverseVersion}}
	for _, r := range reasons {
		pair, ok := values[code]
		if r.Code == code && ok && pair[0] == r.LegacyValue && pair[1] == r.CandidateValue && r.PolicyVersion != "" {
			return strings.Join([]string{r.Code, r.PolicyVersion, r.LegacyValue, r.CandidateValue}, ":"), true
		}
	}
	return "", false
}
