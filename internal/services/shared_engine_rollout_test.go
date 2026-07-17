package services

import (
	"errors"
	"testing"

	"trading-go/internal/cutover"
	"trading-go/internal/tradingcore"
)

func TestTradingEngineModesAndFallbackSafetyBoundaries(t *testing.T) {
	for _, mode := range []string{"legacy", "shared", "shadow_compare"} {
		if err := validateTradingEngineMode(mode); err != nil {
			t.Fatalf("valid mode %q rejected: %v", mode, err)
		}
	}
	if err := validateTradingEngineMode("shared_typo"); err == nil {
		t.Fatal("unknown engine mode did not fail closed")
	}

	settings := map[string]string{"trading_engine_fallback": "legacy"}
	preSubmission := &tradingcore.RunError{Stage: tradingcore.RunStageRisk, SideEffectsPossible: false, Err: errors.New("risk unavailable")}
	if !allowLegacyEngineFallback(settings, preSubmission) {
		t.Fatal("explicit pre-submission fallback was not permitted")
	}
	for _, stage := range []tradingcore.RunStage{tradingcore.RunStageBroker, tradingcore.RunStageLedger, tradingcore.RunStageTrace} {
		err := &tradingcore.RunError{Stage: stage, SideEffectsPossible: true, Err: errors.New("boundary failure")}
		if allowLegacyEngineFallback(settings, err) {
			t.Fatalf("fallback was permitted after %s boundary", stage)
		}
	}
	settings["trading_engine_fallback"] = "disabled"
	if allowLegacyEngineFallback(settings, preSubmission) {
		t.Fatal("disabled fallback was permitted")
	}
	if allowLegacyEngineFallback(map[string]string{}, errors.New("unclassified")) {
		t.Fatal("unclassified error was permitted to fall back")
	}
}

func TestApplicationEngineRoutingSharedShadowInvalidAndFallback(t *testing.T) {
	analysis := []AnalyzedCoin{{Symbol: "BTCUSDT"}}
	sharedCalls, legacyCalls := 0, 0
	shared := func(values []AnalyzedCoin, _ *UniverseSelectionResult, _ map[string]string, mode tradingcore.ExecutionMode) ([]AnalyzedCoin, int, error) {
		sharedCalls++
		values[0].Decision = string(mode)
		return values, 1, nil
	}
	legacy := func(values []AnalyzedCoin, _ *UniverseSelectionResult, _ map[string]string) ([]AnalyzedCoin, int) {
		legacyCalls++
		values[0].Decision = "legacy"
		return values, 2
	}
	result, opened := executeShortlistTradesRouted(append([]AnalyzedCoin(nil), analysis...), nil, map[string]string{"trading_engine_mode": "shared"}, shared, legacy)
	if result[0].Decision != string(tradingcore.ExecutionPaper) || opened != 1 || sharedCalls != 1 || legacyCalls != 0 {
		t.Fatalf("shared route result=%+v opened=%d calls=%d/%d", result, opened, sharedCalls, legacyCalls)
	}
	sharedCalls, legacyCalls = 0, 0
	result, opened = executeShortlistTradesRouted(append([]AnalyzedCoin(nil), analysis...), nil, map[string]string{"trading_engine_mode": "shadow_compare"}, shared, legacy)
	if result[0].Decision != "legacy" || opened != 2 || sharedCalls != 1 || legacyCalls != 1 {
		t.Fatalf("shadow route result=%+v opened=%d calls=%d/%d", result, opened, sharedCalls, legacyCalls)
	}
	sharedCalls, legacyCalls = 0, 0
	result, opened = executeShortlistTradesRouted(append([]AnalyzedCoin(nil), analysis...), nil, map[string]string{"trading_engine_mode": "invalid"}, shared, legacy)
	if opened != 0 || result[0].Decision != "buy_failed" || sharedCalls != 0 || legacyCalls != 0 {
		t.Fatalf("invalid route did not fail closed: %+v calls=%d/%d", result, sharedCalls, legacyCalls)
	}

	pre := &tradingcore.RunError{Stage: tradingcore.RunStageRisk, Err: errors.New("pre")}
	post := &tradingcore.RunError{Stage: tradingcore.RunStageBroker, SideEffectsPossible: true, Err: errors.New("post")}
	for _, test := range []struct {
		name, fallback string
		err            error
		wantLegacy     int
	}{
		{name: "explicit safe fallback", fallback: "legacy", err: pre, wantLegacy: 1},
		{name: "disabled fallback", fallback: "disabled", err: pre},
		{name: "post broker suppression", fallback: "legacy", err: post},
	} {
		t.Run(test.name, func(t *testing.T) {
			legacyCalls = 0
			failing := func(values []AnalyzedCoin, _ *UniverseSelectionResult, _ map[string]string, _ tradingcore.ExecutionMode) ([]AnalyzedCoin, int, error) {
				return values, 0, test.err
			}
			result, _ := executeShortlistTradesRouted(append([]AnalyzedCoin(nil), analysis...), nil, map[string]string{"trading_engine_mode": "shared", "trading_engine_fallback": test.fallback}, failing, legacy)
			if legacyCalls != test.wantLegacy {
				t.Fatalf("legacy calls=%d want=%d result=%+v", legacyCalls, test.wantLegacy, result)
			}
		})
	}
}

func TestDualRunAdaptersReceiveIndependentDeepClones(t *testing.T) {
	flags := cutover.SafeFlags()
	flags.SharedEngine = "shadow"
	flags.DualRun = "observe"
	if err := cutover.Activate(flags); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cutover.ResetForTest)
	input := []AnalyzedCoin{{Symbol: "BTCUSDT", RankComponents: map[string]float64{"momentum": 1}}}
	shared := func(values []AnalyzedCoin, _ *UniverseSelectionResult, settings map[string]string, _ tradingcore.ExecutionMode) ([]AnalyzedCoin, int, error) {
		values[0].RankComponents["momentum"] = 99
		settings["mutated"] = "yes"
		values[0].Decision = "shadow"
		return values, 0, nil
	}
	legacy := func(values []AnalyzedCoin, _ *UniverseSelectionResult, settings map[string]string) ([]AnalyzedCoin, int) {
		if values[0].RankComponents["momentum"] != 1 || settings["mutated"] != "" {
			t.Fatal("legacy observed shadow mutation")
		}
		values[0].Decision = "legacy"
		return values, 0
	}
	result, _ := executeShortlistTradesRouted(input, nil, map[string]string{"trading_engine_mode": "shadow_compare"}, shared, legacy)
	if result[0].Decision != "legacy" || input[0].RankComponents["momentum"] != 1 {
		t.Fatalf("causal input mutated: input=%+v result=%+v", input, result)
	}
}
