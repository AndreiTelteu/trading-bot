package services

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCharacterizationIndicatorScoringGolden(t *testing.T) {
	type goldenCase struct {
		Name       string             `json:"name"`
		Indicators []IndicatorResult  `json:"indicators"`
		Weights    map[string]float64 `json:"weights"`
		Rating     float64            `json:"rating"`
		Signal     string             `json:"signal"`
	}
	payload, err := os.ReadFile("testdata/indicator_scoring_golden.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []goldenCase
	if err := json.Unmarshal(payload, &cases); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range cases {
		t.Run(fixture.Name, func(t *testing.T) {
			rating, signal := CalculateFinalScore(fixture.Indicators, fixture.Weights)
			if rating != fixture.Rating || signal != fixture.Signal {
				t.Fatalf("CalculateFinalScore() = (%.2f, %s), want (%.2f, %s)", rating, signal, fixture.Rating, fixture.Signal)
			}
		})
	}
}

func TestCharacterizationEntryRejectionReasonOrdering(t *testing.T) {
	passing := entryDecisionInput{
		AutoTradeEnabled: true, ModelSelected: true, SignalQualifies: true,
		ConfidenceQualifies: true, RegimeOK: true, VolOK: true,
	}
	cases := []struct {
		name   string
		mutate func(*entryDecisionInput)
		want   string
	}{
		{"analysis error precedes every gate", func(in *entryDecisionInput) { in.AnalysisError = true; in.AutoTradeEnabled = false }, "analysis_error"},
		{"disabled precedes risk off", func(in *entryDecisionInput) { in.AutoTradeEnabled = false; in.UniverseRiskOff = true }, "auto_trade_disabled"},
		{"risk off precedes model rejection", func(in *entryDecisionInput) {
			in.UniverseRiskOff = true
			in.UseModelEntries = true
			in.ModelSelected = false
		}, "universe_regime_risk_off"},
		{"model rejection precedes signal", func(in *entryDecisionInput) {
			in.UseModelEntries = true
			in.ModelSelected = false
			in.SignalQualifies = false
		}, "model_policy_not_selected"},
		{"model rejection preserves ranked reason", func(in *entryDecisionInput) {
			in.UseModelEntries = true
			in.ModelSelected = false
			in.ModelSelectionReason = "outside_top_k"
		}, "outside_top_k"},
		{"signal precedes confidence", func(in *entryDecisionInput) { in.SignalQualifies = false; in.ConfidenceQualifies = false }, "signal_not_qualified"},
		{"rule confidence precedes regime", func(in *entryDecisionInput) { in.ConfidenceQualifies = false; in.RegimeOK = false }, "confidence_not_qualified"},
		{"model floor has distinct reason", func(in *entryDecisionInput) { in.UseModelEntries = true; in.ConfidenceQualifies = false }, "model_policy_floor_failed"},
		{"regime precedes volatility", func(in *entryDecisionInput) { in.RegimeOK = false; in.VolOK = false }, "regime_gate_failed"},
		{"volatility precedes limit", func(in *entryDecisionInput) { in.VolOK = false; in.AtPositionLimit = true }, "vol_gate_failed"},
		{"position limit is last rejection", func(in *entryDecisionInput) { in.AtPositionLimit = true }, "max_positions_reached"},
	}
	for _, fixture := range cases {
		t.Run(fixture.name, func(t *testing.T) {
			input := passing
			fixture.mutate(&input)
			decision, reason := classifyEntryDecision(input)
			if decision != "skip" || reason != fixture.want {
				t.Fatalf("classifyEntryDecision() = (%q, %q), want (skip, %q)", decision, reason, fixture.want)
			}
		})
	}
	decision, reason := classifyEntryDecision(passing)
	if decision != "buy_candidate" || reason != "passed_gates" {
		t.Fatalf("passing decision = (%q, %q)", decision, reason)
	}
}

func TestCharacterizationSizingFixtures(t *testing.T) {
	t.Run("fixed sizing uses current cash percentage", func(t *testing.T) {
		usdt, quantity, err := computeFixedPositionSize(800, 200, 5)
		if err != nil || usdt != 40 || quantity != 0.2 {
			t.Fatalf("fixed size = (%v, %v, %v), want (40, 0.2, nil)", usdt, quantity, err)
		}
	})

	t.Run("known defect rebuy and pyramid settings do not alter fixed sizing", func(t *testing.T) {
		settings := map[string]string{"entry_percent": "5", "rebuy_percent": "90", "pyramiding_enabled": "true", "max_pyramid_layers": "1", "position_scale_percent": "200"}
		usdt, quantity, err := computeFixedPositionSize(800, 200, getSettingFloat(settings, "entry_percent", 5))
		if err != nil || usdt != 40 || quantity != 0.2 {
			t.Fatalf("current fixed sizing unexpectedly consulted rebuy/pyramid controls: (%v, %v, %v)", usdt, quantity, err)
		}
	})

	t.Run("ATR risk budget is capped by max position value", func(t *testing.T) {
		settings := map[string]string{"risk_per_trade": "1", "stop_mult": "2", "tp_mult": "3", "max_position_value": "150", "time_stop_bars": "12"}
		usdt, quantity, stop, target, maxBars, err := computePositionSize(5, 100, 1000, 2000, settings)
		if err != nil || usdt != 150 || quantity != 1.5 || stop != 90 || target != 115 || maxBars == nil || *maxBars != 12 {
			t.Fatalf("ATR size = (%v, %v, %v, %v, %v, %v), want (150, 1.5, 90, 115, 12, nil)", usdt, quantity, stop, target, maxBars, err)
		}
	})

	t.Run("ATR amount is capped by available cash", func(t *testing.T) {
		settings := map[string]string{"risk_per_trade": "10", "stop_mult": "1", "tp_mult": "2"}
		usdt, quantity, stop, target, _, err := computePositionSize(1, 100, 75, 1000, settings)
		if err != nil || usdt != 75 || quantity != 0.75 || stop != 99 || target != 102 {
			t.Fatalf("cash-capped ATR size = (%v, %v, %v, %v, %v)", usdt, quantity, stop, target, err)
		}
	})
}

func TestCharacterizationExitPrecedenceWhenConditionsCollide(t *testing.T) {
	stop, target, trailing := 95.0, 105.0, 97.0
	base := ExitEvaluationInput{CurrentPrice: 100, HighPrice: 110, LowPrice: 90, EntryPrice: 100, StopPrice: &stop, TakeProfitPrice: &target, TrailingStopPrice: &trailing, BarsHeld: 10, Signal: "STRONG_SELL", SignalRating: 1}
	policy := ExitPolicy{ATRTrailingEnabled: true, TrailingStopEnabled: true, TimeStopBars: 5, SellOnSignal: true, MinConfidenceToSell: 3.5, AllowSellAtLoss: true}
	if got := EvaluateBarCloseExit(base, policy).Reason; got != CloseReasonStopLoss {
		t.Fatalf("all exits = %q, want stop loss", got)
	}
	base.StopPrice = nil
	policy.StopLossPercent = 0
	if got := EvaluateBarCloseExit(base, policy).Reason; got != CloseReasonTakeProfit {
		t.Fatalf("without stop = %q, want take profit", got)
	}
	base.TakeProfitPrice = nil
	policy.TakeProfitPercent = 0
	if got := EvaluateBarCloseExit(base, policy).Reason; got != CloseReasonATRTrailing {
		t.Fatalf("without target = %q, want ATR trailing", got)
	}
	base.TrailingStopPrice = nil
	if got := EvaluateBarCloseExit(base, policy).Reason; got != CloseReasonTimeStop {
		t.Fatalf("discretionary collision = %q, want time stop", got)
	}
}
