package services

import (
	"testing"
	"trading-go/internal/database"
)

func TestStage07BootstrapModelCannotGainPaperAuthorityFromSettings(t *testing.T) {
	previous := database.DB
	database.DB = nil
	t.Cleanup(func() { database.DB = previous })
	settings := map[string]string{"active_model_version": DefaultActiveModelVersion, "model_rollout_state": ModelRolloutPaper, "model_fallback_mode": ModelFallbackRuleBased}
	artifact, err := LoadConfiguredModel(settings)
	if err != nil {
		t.Fatal(err)
	}
	if artifact == nil || artifact.ArtifactClass != "contract_fixture" {
		t.Fatalf("quarantined fixture=%+v", artifact)
	}
	if GetAuthorizedModelSelectionPolicy(settings).UseForLiveEntries() {
		t.Fatal("bootstrap contract fixture gained paper authority from settings")
	}
}

func TestStage07AdversarialShadowRanksCannotChangeOneSlotExecution(t *testing.T) {
	previous := database.DB
	database.DB = nil
	t.Cleanup(func() { database.DB = previous })
	first, second := passingAnalysis(), passingAnalysis()
	first.Symbol = "RULE_FIRSTUSDT"
	second.Symbol = "RULE_SECONDUSDT"
	rankTwo, rankOne := 2, 1
	notSelected, selected := false, true
	first.ModelRank = &rankTwo
	first.PolicySelected = &notSelected
	second.ModelRank = &rankOne
	second.PolicySelected = &selected
	settings := passingShortlistSettings()
	settings["max_positions"] = "1"
	settings["active_model_version"] = "adversarial"
	settings["model_rollout_state"] = ModelRolloutShadow
	settings["model_fallback_mode"] = ModelFallbackRuleBased
	observed := governedModelExecutionCopy([]AnalyzedCoin{first, second}, GetAuthorizedModelSelectionPolicy(settings))
	if observed[0].Symbol != "RULE_FIRSTUSDT" || observed[0].ModelRank == nil || *observed[0].ModelRank != 2 {
		t.Fatalf("shadow observation mutated rule order or disappeared: %+v", observed)
	}
	runtime := &fakeShortlistRuntime{regimeOK: true, volOK: true, buySuccess: true}
	withModel, opened := executeShortlistTradesWithRuntime(observed, nil, settings, runtime)
	if opened != 1 || withModel[0].TradeExecuted == nil || !*withModel[0].TradeExecuted || withModel[1].TradeExecuted != nil {
		t.Fatalf("shadow selected a different symbol: %+v", withModel)
	}
	without := []AnalyzedCoin{first, second}
	without[0].ModelRank = nil
	without[0].PolicySelected = nil
	without[1].ModelRank = nil
	without[1].PolicySelected = nil
	runtime = &fakeShortlistRuntime{regimeOK: true, volOK: true, buySuccess: true}
	baseline, opened := executeShortlistTradesWithRuntime(without, nil, passingOneSlotSettings(), runtime)
	if opened != 1 || baseline[0].TradeExecuted == nil || !*baseline[0].TradeExecuted || baseline[1].TradeExecuted != nil {
		t.Fatalf("baseline selected unexpected symbol: %+v", baseline)
	}
}

func passingOneSlotSettings() map[string]string {
	settings := passingShortlistSettings()
	settings["max_positions"] = "1"
	return settings
}

func TestStage07StrategyAuthorityNarrowFallbackMatrix(t *testing.T) {
	previous := database.DB
	database.DB = nil
	t.Cleanup(func() { database.DB = previous })
	for name, settings := range map[string]map[string]string{"implicit versioned baseline": {}, "explicit baseline": {"strategy_id": "stage05_rule_baseline", "strategy_version": "1.0.0", "baseline_fallback_policy_version": Stage05FallbackPolicyVersion}} {
		t.Run(name, func(t *testing.T) {
			if err := ResolveStrategyAuthority(settings); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, settings := range map[string]map[string]string{"experimental without deployment": {"strategy_id": "trend_momentum_candidate", "strategy_version": "1.0.0"}, "research baseline": {"strategy_id": "momentum", "strategy_version": "1.0.0"}, "wrong fallback policy": {"strategy_id": "stage05_rule_baseline", "strategy_version": "1.0.0", "baseline_fallback_policy_version": "legacy"}} {
		t.Run(name, func(t *testing.T) {
			if err := ResolveStrategyAuthority(settings); err == nil {
				t.Fatal("unauthorized strategy passed")
			}
		})
	}
}

func TestStage07AuthorityEnvelopeChangesForEveryRuntimeCategory(t *testing.T) {
	previous := database.DB
	database.DB = nil
	t.Cleanup(func() { database.DB = previous })
	base := map[string]string{"selection_policy_top_k": "2", "selection_policy_min_prob": ".6", "selection_policy_min_ev": ".01", "model_fallback_mode": "rule_based", "risk_per_trade": "1", "max_turnover": ".2", "cash_reserve_percent": "10", "universe_top_k": "20", "paper_fee_bps": "10", "paper_slippage_bps": "5", "active_model_version": "model-v1", "model_feature_schema": "features-v1", "strategy_id": "candidate", "strategy_version": "1"}
	original, err := BuildRuntimeAuthorityPolicy(base, "paper")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"selection_policy_top_k", "risk_per_trade", "max_turnover", "cash_reserve_percent", "universe_top_k", "paper_fee_bps", "paper_slippage_bps", "active_model_version", "model_feature_schema", "strategy_version"} {
		mutated := map[string]string{}
		for k, v := range base {
			mutated[k] = v
		}
		mutated[key] += "-changed"
		changed, err := BuildRuntimeAuthorityPolicy(mutated, "paper")
		if err != nil {
			continue
		}
		if changed.Digest == original.Digest {
			t.Fatalf("%s mutation retained authority digest", key)
		}
	}
}
