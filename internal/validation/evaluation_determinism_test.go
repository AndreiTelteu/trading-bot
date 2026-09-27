package validation

import (
	"encoding/json"
	"errors"
	"sort"
	"testing"
)

// These contributions reconcile to a one-percent return, while their absolute
// magnitudes expose non-associative float addition in aggregate domination.
func evaluationContributionFolds(t *testing.T, pnl [4]float64) ([]FoldResult, ManifestSpec) {
	t.Helper()
	manifest := manifestFixture(t)
	folds := make([]FoldResult, 0, len(manifest.Spec.Folds))
	for _, fold := range manifest.Spec.Folds {
		primitive := healthyPrimitives(fold, .01)
		primitive.Fills = nil
		for i, symbol := range []string{"A", "B", "C", "D"} {
			trade := &primitive.Trades[i]
			trade.Symbol = symbol
			trade.NetPnL = pnl[i]
			trade.GrossPnL = pnl[i] + trade.Cost
			primitive.Fills = append(primitive.Fills,
				FillPrimitive{ID: "buy-" + trade.ID, Symbol: symbol, Side: "buy", At: trade.OpenedAt, Notional: 50, AvailableLiquidity: 1000},
				FillPrimitive{ID: "sell-" + trade.ID, Symbol: symbol, Side: "sell", At: trade.ClosedAt, Notional: 50, AvailableLiquidity: 1000})
		}
		primitive.BaselineTurnover = .4
		metrics, err := DeriveFoldMetrics(primitive)
		if err != nil {
			t.Fatalf("invalid primitive fixture: %v", err)
		}
		if err := ValidateFoldMetrics(metrics, manifest.Spec.Samples); err != nil {
			t.Fatalf("invalid fold metrics fixture: %v", err)
		}
		folds = append(folds, FoldResult{Fold: fold, Primitives: primitive, Metrics: metrics})
	}
	return folds, manifest.Spec
}

func orderedContributionMap(values map[string]float64, reverse bool) map[string]float64 {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if reverse {
		sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	}
	copy := make(map[string]float64, len(values))
	for _, key := range keys {
		copy[key] = values[key]
	}
	return copy
}

func TestEvaluationCanonicalAggregateStableAcrossContributionMapOrder(t *testing.T) {
	folds, spec := evaluationContributionFolds(t, [4]float64{30, -20, 4, -4})
	var reference []byte
	for iteration := 0; iteration < 256; iteration++ {
		for i := range folds {
			folds[i].Metrics.TradeContributions = orderedContributionMap(folds[i].Metrics.TradeContributions, iteration%2 == 0)
			folds[i].Metrics.SymbolContributions = orderedContributionMap(folds[i].Metrics.SymbolContributions, iteration%2 == 0)
		}
		aggregate, err := Evaluate(folds, spec)
		if err != nil {
			t.Fatalf("valid, undominated folds rejected: %v", err)
		}
		encoded, err := json.Marshal(aggregate)
		if err != nil {
			t.Fatal(err)
		}
		if reference == nil {
			reference = encoded
		} else if string(encoded) != string(reference) {
			t.Fatalf("same validated folds yielded different canonical aggregate JSON at iteration %d: first domination=%s current domination=%s", iteration, referenceDomination(reference), referenceDomination(encoded))
		}
	}
}

func referenceDomination(encoded []byte) string {
	var aggregate Evaluation
	if err := json.Unmarshal(encoded, &aggregate); err != nil {
		return err.Error()
	}
	value, _ := json.Marshal(aggregate.Domination)
	return string(value)
}

func TestEvaluationStillRejectsGenuinelyDominatedSymbol(t *testing.T) {
	folds, spec := evaluationContributionFolds(t, [4]float64{10, 0, 0, 0})
	_, err := Evaluate(folds, spec)
	var diagnostic *DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Code != DiagnosticDominated {
		t.Fatalf("single-symbol performance concentration passed: %v", err)
	}
}
