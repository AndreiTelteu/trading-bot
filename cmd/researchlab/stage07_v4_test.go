package main

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"trading-go/internal/backtest"
	"trading-go/internal/validation"
)

func stage07V4RefsFixture() []stage07SourceReference {
	refs := make([]stage07SourceReference, 0, 3)
	for _, id := range []uint{103, 104, 105} {
		refs = append(refs, stage07SourceReference{
			Comparison: backtest.Stage07ComparisonReference{
				JobID: id, ArtifactDigest: stage07V4ComparisonDigest, Candidate: "trend_momentum_candidate@1.4.0", DatasetDigest: validation.DatasetDigest(stage07V4DatasetDigest),
				Strategies: map[string]backtest.Stage07StrategyRef{
					backtest.StrategyTrendMomentumCandidate: {ImplementationDigest: validation.ImplementationDigest(stage07V4CandidateImplementation), ConfigDigest: validation.ConfigDigest(stage07V4CandidateConfig)},
					backtest.StrategyMatchedMomentumID:      {ImplementationDigest: validation.ImplementationDigest(stage07V4BaselineImplementation), ConfigDigest: validation.ConfigDigest(stage07V4BaselineConfig)},
				},
			},
			ValidationArtifactDigest: stage07V4SourceArtifactDigest,
			SourceCodeRevision:       stage07V4SourceSHA,
		})
	}
	return refs
}

func stage07V4ParametersFixture() map[string]string {
	return map[string]string{
		"variant": "combined", "vol_normalization": "true", "lookback_bars": "20", "trend_bars": "20", "regime_bars": "30", "rebalance": "48h", "top_n": "3", "max_positions": "3", "risk_on_gross": "0.75", "neutral_gross": "0.25", "risk_off_gross": "0", "regime_band": "0.02", "position_cap": "0.25", "max_gross": "0.75", "max_net": "0.75", "cash_reserve": "0.25", "vol_floor": "0.02", "turnover_budget": "0.10", "skip_delta": "0.015", "execution_gap_reserve": "0.1", "allocation_tolerance": "0.02", "hard_stop": "0.08", "include_shortlist": "true", "execution_intent": "backtest", "model_observation": "0", "final_policy": "mark_to_market", "decision_council_policy": "active_v2", "decision_model": "decider/decider-4b", "decision_council_early_exit": "false",
	}
}

func TestStage07V4SpecPinsEntryOnlyCouncilAndExecution(t *testing.T) {
	driver := strings.Repeat("9", 40)
	spec, err := buildStage07V4Spec(stage07V4RefsFixture(), stage07V4ParametersFixture(), driver, strings.Repeat("8", 64))
	if err != nil {
		t.Fatal(err)
	}
	if spec.StudyType != "exploratory" || !spec.Exploratory || spec.ConfirmatoryHoldout != nil || spec.ResearchOverride == nil || spec.CodeRevision != driver {
		t.Fatal("v4 validation must remain explicitly retrospective and exploratory")
	}
	if spec.Candidate.Version != "1.4.0" || spec.Candidate.ImplementationDigest != validation.ImplementationDigest(stage07V4CandidateImplementation) || spec.Candidate.ConfigDigest != validation.ConfigDigest(stage07V4CandidateConfig) {
		t.Fatal("candidate identity differs")
	}
	if spec.Policies.Execution != "backtest-execution-v4" || spec.ExecutionSemantics["timing"] != "selected_bar_close_after_volume" || spec.ExecutionSemantics["liquidity"] != "volume_capped" || spec.ExecutionSemantics["max_participation_bps"] != "1000" || spec.ExecutionSemantics["no_fill_rule"] != stage07V4NoFillRule {
		t.Fatal("v4 capacity execution semantics are not fully pinned")
	}
	for key, want := range map[string]string{"decision_council_policy": "active_v2", "decision_model": "decider/decider-4b", "decision_council_early_exit": "false", "final_policy": "mark_to_market"} {
		if got := spec.AllowedTuning[key]; !reflect.DeepEqual(got, []string{want}) {
			t.Fatalf("%s is not frozen: %v", key, got)
		}
	}
	if !reflect.DeepEqual(spec.FoldSourceJobIDs, []uint{103, 104, 105}) || len(spec.Folds) != 3 || spec.BootstrapIterations != 500 || spec.Samples.MinIndependentUnits != 3 {
		t.Fatal("fold sources or statistical contract differ")
	}
	if spec.Interval.Start != time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC) || spec.Interval.End != time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) {
		t.Fatal("interval differs")
	}
}

func TestStage07V4RequiresExactAuditedSourceIDs(t *testing.T) {
	ids, err := parseStage07V4SourceIDs("103,104,105")
	if err != nil || !reflect.DeepEqual(ids, []uint{103, 104, 105}) {
		t.Fatalf("audited sources rejected: %v", err)
	}
	for _, raw := range []string{"104,103,105", "103,104,106", "103,103,105", "81,82,83", "103,104"} {
		if _, err := parseStage07V4SourceIDs(raw); err == nil {
			t.Fatalf("unaudited source set accepted: %s", raw)
		}
	}
}

func TestStage07V4SourcePinsAreStable(t *testing.T) {
	if stage07V4SourceSHA != "cc39d9833aea1e1f33fa4afac437a69067bd387e" || stage07V4ComparisonDigest != "396b2aedd19f117ef5692d45c783294ca0111b72c68b1e416d6e9a616d8a88cd" || stage07V4SourceArtifactDigest != "dce8690cadea93f4782acc189d9cb3aee7a70d0342a346110b21072092dd949d" || stage07V4DatasetDigest != "32fe7e3664ab9d1a14c7cff30c0db6dc673f37ca0eea0479ed9cd812011ec9bc" {
		t.Fatal("audited v4 source identities changed")
	}
}
