package services

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
	"trading-go/internal/database"
)

type fakeShortlistRuntime struct {
	openCount                             int64
	countErr                              error
	regimeOK, volOK                       bool
	marketCalls                           int
	positionExists                        bool
	lookupErr                             error
	lookupCalls, buyCalls, broadcastCalls int
	buySuccess                            bool
	buyErr, persistErr                    error
	historyAttempts, persistedHistories   []database.TrendAnalysisHistory
}

func (runtime *fakeShortlistRuntime) CountOpenPositions() (int64, error) {
	return runtime.openCount, runtime.countErr
}
func (runtime *fakeShortlistRuntime) MarketGates(*AnalyzedCoin, *UniverseSelectionResult, shortlistMarketGatePolicy) (bool, bool) {
	runtime.marketCalls++
	return runtime.regimeOK, runtime.volOK
}
func (runtime *fakeShortlistRuntime) LookupOpenPosition(string) (bool, error) {
	runtime.lookupCalls++
	return runtime.positionExists, runtime.lookupErr
}
func (runtime *fakeShortlistRuntime) ExecuteBuy(AnalyzedCoin) (bool, error) {
	runtime.buyCalls++
	return runtime.buySuccess, runtime.buyErr
}
func (runtime *fakeShortlistRuntime) PersistHistory(history database.TrendAnalysisHistory) error {
	runtime.historyAttempts = append(runtime.historyAttempts, history)
	if runtime.persistErr == nil {
		runtime.persistedHistories = append(runtime.persistedHistories, history)
	}
	return runtime.persistErr
}
func (runtime *fakeShortlistRuntime) Log(string, string, string) {}
func (runtime *fakeShortlistRuntime) BroadcastTradeUpdates()     { runtime.broadcastCalls++ }
func (runtime *fakeShortlistRuntime) Now() time.Time {
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}

func passingShortlistSettings() map[string]string {
	return map[string]string{"auto_trade_enabled": "true", "max_positions": "5", "buy_only_strong": "true", "min_confidence_to_buy": "4", "regime_gate_enabled": "true"}
}
func passingAnalysis() AnalyzedCoin {
	return AnalyzedCoin{Symbol: "BTCUSDT", Price: 100, Signal: "STRONG_BUY", Rating: 5}
}

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

func TestCharacterizationShortlistOrchestrationGateExecutionConsistency(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*AnalyzedCoin, map[string]string, *fakeShortlistRuntime)
		wantReason string
		wantBuy    int
	}{
		{"auto disabled still evaluates failing market gates first", func(_ *AnalyzedCoin, s map[string]string, r *fakeShortlistRuntime) {
			s["auto_trade_enabled"] = "false"
			r.regimeOK = false
			r.volOK = false
		}, "auto_trade_disabled", 0},
		{"signal rejection", func(a *AnalyzedCoin, _ map[string]string, _ *fakeShortlistRuntime) { a.Signal = "HOLD" }, "signal_not_qualified", 0},
		{"confidence rejection", func(a *AnalyzedCoin, _ map[string]string, _ *fakeShortlistRuntime) { a.Rating = 3 }, "confidence_not_qualified", 0},
		{"regime rejection", func(_ *AnalyzedCoin, _ map[string]string, r *fakeShortlistRuntime) { r.regimeOK = false }, "regime_gate_failed", 0},
		{"volatility rejection", func(_ *AnalyzedCoin, _ map[string]string, r *fakeShortlistRuntime) { r.volOK = false }, "vol_gate_failed", 0},
		{"position limit", func(_ *AnalyzedCoin, s map[string]string, r *fakeShortlistRuntime) {
			s["max_positions"] = "1"
			r.openCount = 1
		}, "max_positions_reached", 0},
		{"passing gates execute", func(_ *AnalyzedCoin, _ map[string]string, _ *fakeShortlistRuntime) {}, "order_executed", 1},
	}
	for _, fixture := range tests {
		t.Run(fixture.name, func(t *testing.T) {
			analysis := passingAnalysis()
			settings := passingShortlistSettings()
			runtime := &fakeShortlistRuntime{regimeOK: true, volOK: true, buySuccess: true}
			fixture.mutate(&analysis, settings, runtime)
			results, opened := executeShortlistTradesWithRuntime([]AnalyzedCoin{analysis}, nil, settings, runtime)
			if runtime.marketCalls != 1 {
				t.Fatalf("market gate calls=%d,want 1", runtime.marketCalls)
			}
			if runtime.buyCalls != fixture.wantBuy {
				t.Fatalf("buy calls=%d,want %d", runtime.buyCalls, fixture.wantBuy)
			}
			if results[0].DecisionReason != fixture.wantReason {
				t.Fatalf("reason=%q,want %q", results[0].DecisionReason, fixture.wantReason)
			}
			if len(runtime.historyAttempts) != 1 || len(runtime.persistedHistories) != 1 || runtime.historyAttempts[0].DecisionReason == nil || *runtime.historyAttempts[0].DecisionReason != fixture.wantReason {
				t.Fatalf("attempted=%+v persisted=%+v", runtime.historyAttempts, runtime.persistedHistories)
			}
			if opened != fixture.wantBuy {
				t.Fatalf("opened=%d,want %d", opened, fixture.wantBuy)
			}
		})
	}
}

func TestCharacterizationShortlistExistingOpenAndErrorSemantics(t *testing.T) {
	t.Run("risk-off universe still evaluates market gates before rejection", func(t *testing.T) {
		runtime := &fakeShortlistRuntime{regimeOK: false, volOK: false, buySuccess: true}
		universe := &UniverseSelectionResult{RegimeState: UniverseRegimeRiskOff}
		results, _ := executeShortlistTradesWithRuntime([]AnalyzedCoin{passingAnalysis()}, universe, passingShortlistSettings(), runtime)
		if runtime.marketCalls != 1 || runtime.buyCalls != 0 || results[0].DecisionReason != "universe_regime_risk_off" {
			t.Fatalf("risk-off result=%+v runtime=%+v", results[0], runtime)
		}
	})
	t.Run("existing open skips even when pyramid settings enabled", func(t *testing.T) {
		settings := passingShortlistSettings()
		settings["pyramiding_enabled"] = "true"
		settings["max_pyramid_layers"] = "9"
		settings["rebuy_percent"] = "80"
		runtime := &fakeShortlistRuntime{regimeOK: true, volOK: true, positionExists: true, buySuccess: true}
		results, opened := executeShortlistTradesWithRuntime([]AnalyzedCoin{passingAnalysis()}, nil, settings, runtime)
		if runtime.buyCalls != 0 || opened != 0 || results[0].DecisionReason != "position_exists" {
			t.Fatalf("existing-position result=%+v,buyCalls=%d,opened=%d", results[0], runtime.buyCalls, opened)
		}
	})
	t.Run("lookup error is treated as absent and purchase is attempted", func(t *testing.T) {
		runtime := &fakeShortlistRuntime{regimeOK: true, volOK: true, lookupErr: errors.New("database unavailable"), buySuccess: true}
		results, opened := executeShortlistTradesWithRuntime([]AnalyzedCoin{passingAnalysis()}, nil, passingShortlistSettings(), runtime)
		if runtime.buyCalls != 1 || opened != 1 || results[0].DecisionReason != "order_executed" {
			t.Fatalf("lookup-error result=%+v,buyCalls=%d", results[0], runtime.buyCalls)
		}
	})
	t.Run("count and persistence errors are ignored", func(t *testing.T) {
		runtime := &fakeShortlistRuntime{countErr: errors.New("count failed"), regimeOK: true, volOK: true, buySuccess: true, persistErr: errors.New("history failed")}
		results, opened := executeShortlistTradesWithRuntime([]AnalyzedCoin{passingAnalysis()}, nil, passingShortlistSettings(), runtime)
		if runtime.buyCalls != 1 || opened != 1 || len(runtime.historyAttempts) != 1 || len(runtime.persistedHistories) != 0 || results[0].DecisionReason != "order_executed" {
			t.Fatalf("ignored-error result=%+v runtime=%+v", results[0], runtime)
		}
	})
	t.Run("purchase error is persisted as attempted decision", func(t *testing.T) {
		runtime := &fakeShortlistRuntime{regimeOK: true, volOK: true, buyErr: errors.New("purchase failed")}
		results, opened := executeShortlistTradesWithRuntime([]AnalyzedCoin{passingAnalysis()}, nil, passingShortlistSettings(), runtime)
		if opened != 0 || results[0].Decision != "buy_failed" || results[0].DecisionReason != "purchase failed" || len(runtime.persistedHistories) != 1 || runtime.persistedHistories[0].Decision == nil || *runtime.persistedHistories[0].Decision != "buy_failed" {
			t.Fatalf("purchase-error result=%+v persisted=%+v", results[0], runtime.persistedHistories)
		}
	})
}

func TestCharacterizationShortlistModelRolloutControlsActualExecution(t *testing.T) {
	states := []struct {
		state   string
		wantBuy int
	}{{ModelRolloutResearchOnly, 0}, {ModelRolloutShadow, 0}, {ModelRolloutPaper, 1}, {ModelRolloutLimitedLive, 1}, {ModelRolloutFullLive, 1}, {ModelRolloutRollback, 0}}
	for _, fixture := range states {
		t.Run(fixture.state, func(t *testing.T) {
			selected := true
			rank := 1
			probability := 0.9
			expectedValue := 0.1
			analysis := passingAnalysis()
			analysis.Signal = "HOLD"
			analysis.PolicySelected = &selected
			analysis.ModelRank = &rank
			analysis.ProbUp = &probability
			analysis.ExpectedValue = &expectedValue
			settings := passingShortlistSettings()
			settings["active_model_version"] = "model-v1"
			settings["model_rollout_state"] = fixture.state
			runtime := &fakeShortlistRuntime{regimeOK: true, volOK: true, buySuccess: true}
			results, _ := executeShortlistTradesWithRuntime([]AnalyzedCoin{analysis}, nil, settings, runtime)
			if runtime.buyCalls != fixture.wantBuy {
				t.Fatalf("state %s buy calls=%d,want %d,result=%+v", fixture.state, runtime.buyCalls, fixture.wantBuy, results[0])
			}
		})
	}
}

func TestCharacterizationClosedReopenAndLowLevelAddition(t *testing.T) {
	closedAt := time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC)
	reason := "manual"
	position := database.Position{Amount: 2, AvgPrice: 100, Pnl: 9, PnlPercent: 4, Status: "closed", ClosedAt: &closedAt, CloseReason: &reason}
	openedAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	resetClosedPositionForAutoBuy(&position, 3, 120, openedAt)
	if position.Status != "open" || position.Amount != 3 || position.AvgPrice != 120 || position.Pnl != 0 || position.PnlPercent != 0 || position.ClosedAt != nil || position.CloseReason != nil || !position.OpenedAt.Equal(openedAt) {
		t.Fatalf("reopened position=%+v", position)
	}
	settings := map[string]string{"rebuy_percent": "99", "pyramiding_enabled": "false", "max_pyramid_layers": "0", "position_scale_percent": "1"}
	_ = settings // These persisted settings are not inputs to the current low-level addition branch.
	applyAutoBuyAddition(&position, 1, 180)
	if position.Amount != 4 || position.AvgPrice != 135 {
		t.Fatalf("weighted addition amount=%v avg=%v,want 4 and 135", position.Amount, position.AvgPrice)
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
	allConditions := ExitEvaluationInput{CurrentPrice: 100, HighPrice: 110, LowPrice: 90, EntryPrice: 100, StopPrice: &stop, TakeProfitPrice: &target, TrailingStopPrice: &trailing, BarsHeld: 10, Signal: "STRONG_SELL", SignalRating: 1}
	discretionary := ExitPolicy{TimeStopBars: 5, SellOnSignal: true, MinConfidenceToSell: 3.5, AllowSellAtLoss: true}
	tests := []struct {
		name   string
		input  ExitEvaluationInput
		policy ExitPolicy
		want   string
	}{
		{"explicit stop precedes every condition", allConditions, ExitPolicy{ATRTrailingEnabled: true, TrailingStopEnabled: true, TimeStopBars: 5, SellOnSignal: true, MinConfidenceToSell: 3.5, AllowSellAtLoss: true}, CloseReasonStopLoss},
		{"explicit target precedes trailing and discretionary", func() ExitEvaluationInput { v := allConditions; v.StopPrice = nil; return v }(), ExitPolicy{ATRTrailingEnabled: true, TrailingStopEnabled: true, TimeStopBars: 5, SellOnSignal: true, MinConfidenceToSell: 3.5, AllowSellAtLoss: true}, CloseReasonTakeProfit},
		{"ATR trailing precedes percent trailing", func() ExitEvaluationInput { v := allConditions; v.StopPrice = nil; v.TakeProfitPrice = nil; return v }(), ExitPolicy{ATRTrailingEnabled: true, TrailingStopEnabled: true, TimeStopBars: 5, SellOnSignal: true, MinConfidenceToSell: 3.5, AllowSellAtLoss: true}, CloseReasonATRTrailing},
		{"percent trailing precedes fallback and discretionary", func() ExitEvaluationInput { v := allConditions; v.StopPrice = nil; v.TakeProfitPrice = nil; return v }(), ExitPolicy{TrailingStopEnabled: true, StopLossPercent: 5, TakeProfitPercent: 5, TimeStopBars: 5, SellOnSignal: true, MinConfidenceToSell: 3.5, AllowSellAtLoss: true}, CloseReasonTrailingStop},
		{"fallback stop precedes simultaneous fallback target", ExitEvaluationInput{CurrentPrice: 100, HighPrice: 120, LowPrice: 90, EntryPrice: 100, BarsHeld: 10, Signal: "SELL", SignalRating: 1}, ExitPolicy{StopLossPercent: 5, TakeProfitPercent: 10, TimeStopBars: 5, SellOnSignal: true, MinConfidenceToSell: 3.5, AllowSellAtLoss: true}, CloseReasonStopLoss},
		{"fallback target precedes discretionary", ExitEvaluationInput{CurrentPrice: 110, HighPrice: 111, LowPrice: 99, EntryPrice: 100, BarsHeld: 10, Signal: "SELL", SignalRating: 1}, ExitPolicy{StopLossPercent: 5, TakeProfitPercent: 10, TimeStopBars: 5, SellOnSignal: true, MinConfidenceToSell: 3.5, AllowSellAtLoss: true}, CloseReasonTakeProfit},
		{"time stop precedes sell signal", ExitEvaluationInput{CurrentPrice: 101, HighPrice: 101, LowPrice: 101, EntryPrice: 100, BarsHeld: 5, Signal: "SELL", SignalRating: 1}, discretionary, CloseReasonTimeStop},
		{"sell signal is final matching exit", ExitEvaluationInput{CurrentPrice: 101, HighPrice: 101, LowPrice: 101, EntryPrice: 100, BarsHeld: 1, Signal: "SELL", SignalRating: 1}, discretionary, CloseReasonSellSignal},
		{"loss gate blocks time and signal", ExitEvaluationInput{CurrentPrice: 95, HighPrice: 95, LowPrice: 95, EntryPrice: 100, BarsHeld: 5, Signal: "SELL", SignalRating: 1}, ExitPolicy{TimeStopBars: 5, SellOnSignal: true, MinConfidenceToSell: 3.5, AllowSellAtLoss: false}, ""},
		{"loss gate never blocks protective stop", ExitEvaluationInput{CurrentPrice: 95, HighPrice: 95, LowPrice: 94, EntryPrice: 100, StopPrice: &stop, BarsHeld: 5, Signal: "SELL", SignalRating: 1}, ExitPolicy{TimeStopBars: 5, SellOnSignal: true, MinConfidenceToSell: 3.5, AllowSellAtLoss: false}, CloseReasonStopLoss},
	}
	for _, fixture := range tests {
		t.Run(fixture.name, func(t *testing.T) {
			if got := EvaluateBarCloseExit(fixture.input, fixture.policy).Reason; got != fixture.want {
				t.Fatalf("exit=%q,want %q", got, fixture.want)
			}
		})
	}
}
