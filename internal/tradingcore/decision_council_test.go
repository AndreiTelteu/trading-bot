package tradingcore

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"trading-go/internal/decisionmodel"
)

type fakeCouncilModel struct {
	responses []decisionmodel.Response
	errors    []error
	requests  []decisionmodel.Request
}

func (f *fakeCouncilModel) Identity() string { return "fake/test" }
func (f *fakeCouncilModel) Decide(_ context.Context, r decisionmodel.Request) (decisionmodel.Response, error) {
	i := len(f.requests)
	f.requests = append(f.requests, r)
	if i < len(f.errors) && f.errors[i] != nil {
		return decisionmodel.Response{}, f.errors[i]
	}
	if i >= len(f.responses) {
		return decisionmodel.Response{}, fmt.Errorf("unexpected request %d", i)
	}
	return f.responses[i], nil
}
func councilTestSeries(n int) []CouncilBar {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	out := make([]CouncilBar, n)
	for i := range out {
		p := 100.0 + float64(i)
		out[i] = CouncilBar{OpenTime: start.Add(time.Duration(i) * 4 * time.Hour), CloseTime: start.Add(time.Duration(i+1) * 4 * time.Hour), Open: p, High: p + 1, Low: p - 1, Close: p, Volume: 10}
	}
	return out
}
func councilTestInput() CouncilInput {
	return CouncilInput{Asset: councilTestSeries(51), Market: councilTestSeries(51), Signal: CouncilSignal{V4Action: "buy", Regime: "risk_on", Rank: 1, UniverseSize: 3, Momentum: 0.2, Normalized: 0.3, Volatility: 0.1, TargetWeight: 0.25, AbsoluteTrend: true}}
}
func councilTestModel(bull, bear, hodl float64, choice string) *fakeCouncilModel {
	return &fakeCouncilModel{responses: []decisionmodel.Response{
		{RequestDigest: "score-digest", ResolvedModel: "test-build", Cached: true, Answers: map[string]decisionmodel.Answer{
			"decision_bull": councilLevelAnswer(bull, 0.8),
			"decision_bear": councilLevelAnswer(bear, 0.7),
			"decision_hodl": councilLevelAnswer(hodl, 0.6),
		}},
		{RequestDigest: "final-digest", ResolvedModel: "test-build", Cached: true, Answers: map[string]decisionmodel.Answer{
			"decision_final": {Type: decisionmodel.QuestionChoice, Choice: choice, Probabilities: map[string]float64{"buy": 0.6, "sell": 0.2, "hodl": 0.2}, Confidence: 0.9},
		}},
	}}
}

// councilLevelAnswer places value mass on the top level and the rest on the
// bottom level so that the expected normalized level equals value exactly.
func councilLevelAnswer(value, confidence float64) decisionmodel.Answer {
	choice := "none"
	if value >= 0.5 {
		choice = "very_strong"
	}
	return decisionmodel.Answer{Type: decisionmodel.QuestionChoice, Choice: choice, Probabilities: map[string]float64{"none": 1 - value, "very_strong": value}, Confidence: confidence}
}
func TestAggregateCouncilBars4H(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	bars := make([]CouncilBar, 0, 64)
	for i := 0; i < 64; i++ {
		p := 100.0 + float64(i)
		bars = append(bars, CouncilBar{OpenTime: start.Add(time.Duration(i) * 15 * time.Minute), CloseTime: start.Add(time.Duration(i+1) * 15 * time.Minute), Open: p, High: p + 2, Low: p - 1, Close: p + 1, Volume: float64(i + 1)})
	}
	bars = append(bars, CouncilBar{OpenTime: start.Add(-15 * time.Minute), CloseTime: start, Open: 1, High: 1, Low: 1, Close: 1, Volume: 1})
	at := start.Add(12 * time.Hour)
	got := AggregateCouncilBars4H(bars, at, 2)
	if len(got) != 2 {
		t.Fatalf("got %d buckets", len(got))
	}
	if !got[0].OpenTime.Equal(start.Add(4*time.Hour)) || got[0].Open != 116 || got[0].Close != 132 || got[0].High != 133 || got[0].Low != 115 || got[0].Volume != 392 {
		t.Fatalf("first bucket: %+v", got[0])
	}
	if !got[1].CloseTime.Equal(at) {
		t.Fatalf("future bucket leaked: %+v", got[1])
	}
	if len(AggregateCouncilBars4H(bars, at.Add(-time.Nanosecond), 8)) != 2 {
		t.Fatal("incomplete decision-time bucket included")
	}
	if len(AggregateCouncilBars4H(bars[:47], at, 8)) != 2 {
		t.Fatal("incomplete bucket included")
	}
	if len(AggregateCouncilBars4H(bars, at, 0)) != 0 {
		t.Fatal("zero limit returned bars")
	}
	withDuplicate := append(append([]CouncilBar(nil), bars...), bars[16])
	if got := AggregateCouncilBars4H(withDuplicate, at, 8); len(got) != 2 {
		t.Fatalf("duplicate slot did not invalidate bucket: %d", len(got))
	}
}
func TestCouncilStateAnonymizedStableAndIndicators(t *testing.T) {
	in := councilTestInput()
	in.Asset[50].Close = 150 // same as default, explicit synthetic anchor
	in.Position = CouncilPosition{HasPosition: true, EntryPrice: 1234567, MarkPrice: 1358023.7}
	state, err := BuildCouncilState(in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := BuildCouncilState(in)
	if err != nil || state != again {
		t.Fatal("state is not byte stable")
	}
	scaled := in
	scaled.Asset = append([]CouncilBar(nil), in.Asset...)
	scaled.Market = append([]CouncilBar(nil), in.Market...)
	for _, series := range [][]CouncilBar{scaled.Asset, scaled.Market} {
		for i := range series {
			series[i].Open *= 10
			series[i].High *= 10
			series[i].Low *= 10
			series[i].Close *= 10
			series[i].Volume *= 10
		}
	}
	scaled.Position.EntryPrice *= 10
	scaled.Position.MarkPrice *= 10
	if scaledState, err := BuildCouncilState(scaled); err != nil || scaledState != state {
		t.Fatalf("absolute price or volume leaked into state: err=%v", err)
	}
	if !strings.HasPrefix(state, "schema=decision-council-state-v1\nbar=4h\n") {
		t.Fatal("schema prefix")
	}
	for _, secret := range []string{"BTCUSDT", "2025", "1234567", "1358023", "150.00"} {
		if strings.Contains(state, secret) {
			t.Fatalf("state exposed absolute value %q", secret)
		}
	}
	if regexp.MustCompile(`\d{4}-\d\d-\d\d`).MatchString(state) {
		t.Fatal("date exposed")
	}
	for _, line := range []string{
		"asset_ret1_pct=0.67", "asset_ret3_pct=2.04", "asset_ret6_pct=4.17", "asset_ret12_pct=8.70", "asset_ret30_pct=25.00",
		"asset_atr14_pct=1.33", "asset_rsi14=100.00", "asset_sma20_distance_pct=6.76", "asset_sma50_distance_pct=19.52",
		"asset_realized_vol30_pct=0.05", "market_realized_vol30_pct=0.05", "market_sma30_distance_pct=10.70",
		"asset_drawdown30_pct=-0.66", "asset_range30_position=0.97", "asset_volume_ratio20=1.00", "asset_volume_trend5_20=1.00", "asset_up_bars12=0",
		"market_ret1_pct=0.67", "market_ret6_pct=4.17", "market_ret30_pct=25.00", "relative_ret30_pct=0.00", "unrealized_pnl_pct=10.00",
	} {
		if !strings.Contains(state, line) {
			t.Errorf("missing %s\n%s", line, state)
		}
	}
	if !strings.Contains(state, "asset_recent12=") || strings.Count(strings.Split(state, "asset_recent12=")[1][:strings.Index(strings.Split(state, "asset_recent12=")[1], "\n")], ",") != 11 {
		t.Fatal("recent sequence length")
	}
}

func TestCouncilWilderIndicators(t *testing.T) {
	bars := councilTestSeries(51)
	for i := range bars {
		bars[i].Open, bars[i].Close = 100, 100
		bars[i].High, bars[i].Low = 101, 99
	}
	if got := councilRSI(bars, 14); got != 50 {
		t.Fatalf("flat RSI=%v", got)
	}
	if got := councilATR(bars, 14); got != 2 {
		t.Fatalf("flat ATR percent=%v", got)
	}
	// Only the final range changes, so one Wilder update has a closed form.
	bars[50].High, bars[50].Low = 114, 86
	wantATR := (2*13 + 28) / 14.0
	if got := councilATR(bars, 14); math.Abs(got-wantATR) > 1e-12 {
		t.Fatalf("Wilder ATR=%v want=%v", got, wantATR)
	}
	for i := 1; i <= 14; i++ {
		bars[i].Close = bars[i-1].Close + 1
	}
	for i := 15; i < len(bars); i++ {
		bars[i].Close = bars[i-1].Close - 1
	}
	wantRSI := 100 * math.Pow(13.0/14.0, 36)
	if got := councilRSI(bars, 14); math.Abs(got-wantRSI) > 1e-10 {
		t.Fatalf("Wilder RSI=%v want=%v", got, wantRSI)
	}
}
func TestCouncilInsufficientData(t *testing.T) {
	in := councilTestInput()
	in.Asset = in.Asset[:50]
	if _, err := BuildCouncilState(in); !errors.Is(err, ErrInsufficientCouncilData) {
		t.Fatalf("got %v", err)
	}
	f := councilTestModel(1, 0, 0, "buy")
	if _, err := (DecisionCouncil{Model: f, Policy: CouncilVetoV1}).Evaluate(context.Background(), in); !errors.Is(err, ErrInsufficientCouncilData) || len(f.requests) != 0 {
		t.Fatalf("err=%v calls=%d", err, len(f.requests))
	}
}
func TestCouncilRulesAndPolicies(t *testing.T) {
	cases := []struct {
		name, action     string
		held             bool
		bull, bear, hodl float64
		choice, want     string
	}{
		{"entry_admit_equal_bull", "buy", false, 0.5, 0.49, 0, "buy", "admit"},
		{"entry_veto_equal_bear", "buy", false, 0.5, 0.5, 0, "buy", "veto"},
		{"entry_veto_low_bull", "buy", false, 0.49, 0.1, 0, "buy", "veto"},
		{"entry_veto_other_choice", "buy", false, 1, 0, 0, "hodl", "veto"},
		{"hold_exit_equal_bear", "hold", true, 0, 0.6, 0.39, "sell", "early_exit"},
		{"hold_no_exit_equal_hodl", "hold", true, 0, 0.6, 0.4, "sell", "hold"},
		{"hold_no_exit_low_bear", "hold", true, 0, 0.59, 0, "sell", "hold"},
		{"hold_no_exit_other_choice", "hold", true, 0, 1, 0, "buy", "hold"},
	}
	for _, tc := range cases {
		for _, policy := range []CouncilPolicy{CouncilObserveV1, CouncilVetoV1} {
			t.Run(tc.name+string(policy), func(t *testing.T) {
				in := councilTestInput()
				in.Signal.V4Action = tc.action
				in.Position.HasPosition = tc.held
				if tc.held {
					in.Position.EntryPrice = 100
					in.Position.MarkPrice = 105
				}
				f := councilTestModel(tc.bull, tc.bear, tc.hodl, tc.choice)
				out, err := (DecisionCouncil{Model: f, Policy: policy}).Evaluate(context.Background(), in)
				if err != nil {
					t.Fatal(err)
				}
				if out.Proposed != tc.want || out.Applied != (policy == CouncilVetoV1) {
					t.Fatalf("proposed=%s applied=%t", out.Proposed, out.Applied)
				}
				if len(f.requests) != 2 || len(f.requests[0].Questions) != 3 || len(f.requests[1].Questions) != 1 {
					t.Fatalf("calls=%d", len(f.requests))
				}
				if !strings.HasSuffix(f.requests[1].State, fmt.Sprintf("council: bull=%.2f bear=%.2f hodl=%.2f", tc.bull, tc.bear, tc.hodl)) {
					t.Fatalf("final state: %s", f.requests[1].State)
				}
				if !out.Cached || out.ScoreRequestDigest != "score-digest" || out.FinalRequestDigest != "final-digest" || out.ResolvedModel != "test-build" {
					t.Fatalf("evidence: %+v", out)
				}
				if !reflect.DeepEqual(out.FinalProbabilities, map[string]float64{"buy": 0.6, "sell": 0.2, "hodl": 0.2}) {
					t.Fatalf("probabilities: %v", out.FinalProbabilities)
				}
			})
		}
	}
}
func TestCouncilNormalizedScore(t *testing.T) {
	f := councilTestModel(0, 0, 0, "buy")
	f.responses[0].Answers["decision_bull"] = decisionmodel.Answer{Type: decisionmodel.QuestionChoice, Choice: "very_strong", Probabilities: map[string]float64{"none": 0.25, "very_strong": 0.75}, Confidence: 0.8}
	f.responses[0].Answers["decision_bear"] = decisionmodel.Answer{Type: decisionmodel.QuestionChoice, Choice: "none", Probabilities: map[string]float64{"none": 0.75, "very_strong": 0.25}, Confidence: 0.7}
	out, err := (DecisionCouncil{Model: f, Policy: CouncilVetoV1}).Evaluate(context.Background(), councilTestInput())
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(out.Bull-0.75) > 1e-12 || math.Abs(out.Bear-0.25) > 1e-12 || out.Proposed != "admit" {
		t.Fatalf("normalized: %+v", out)
	}
	if !strings.Contains(f.requests[1].State, "council: bull=0.75 bear=0.25 hodl=0.00") {
		t.Fatal("normalized scores absent from final request")
	}
}
func TestCouncilRoundedProbabilitySumDoesNotRenormalize(t *testing.T) {
	f := councilTestModel(0, 0, 0, "buy")
	f.responses[0].Answers["decision_bull"] = decisionmodel.Answer{Type: decisionmodel.QuestionChoice, Choice: "none", Probabilities: map[string]float64{"none": 0.495, "very_strong": 0.495}}
	out, err := (DecisionCouncil{Model: f, Policy: CouncilVetoV1}).Evaluate(context.Background(), councilTestInput())
	if err != nil {
		t.Fatal(err)
	}
	if out.Bull != 0.495 || out.Proposed != "veto" {
		t.Fatalf("rounded score crossed the entry threshold: %+v", out)
	}
}
func TestCouncilSymmetricProbabilitiesHaveStableThreshold(t *testing.T) {
	answer := decisionmodel.Answer{Type: decisionmodel.QuestionChoice, Choice: "moderate", Probabilities: map[string]float64{"none": 0.1, "weak": 0.2, "moderate": 0.4, "strong": 0.2, "very_strong": 0.1}}
	for i := 0; i < 1000; i++ {
		got, err := councilExpectedScore(answer)
		if err != nil || got != 0.5 {
			t.Fatalf("iteration %d: score=%v err=%v", i, got, err)
		}
	}
}
func TestCouncilScoreQuestionsAreOrderedChoices(t *testing.T) {
	for name, question := range councilScoreQuestions() {
		if question.Type != decisionmodel.QuestionChoice || len(question.ScoreCriteria) != 0 || len(question.ChoiceCriteria) != len(councilLevels) {
			t.Fatalf("%s: %+v", name, question)
		}
		for _, level := range councilLevels {
			if question.ChoiceCriteria[level] == "" {
				t.Fatalf("%s missing level %s", name, level)
			}
		}
	}
	for choice, want := range map[string]float64{"none": 0, "weak": 0.25, "moderate": 0.5, "strong": 0.75, "very_strong": 1} {
		got, err := councilExpectedScore(decisionmodel.Answer{Type: decisionmodel.QuestionChoice, Choice: choice})
		if err != nil || got != want {
			t.Fatalf("%s: score=%v err=%v", choice, got, err)
		}
	}
	for _, answer := range []decisionmodel.Answer{
		{Type: decisionmodel.QuestionChoice, Choice: "extreme"},
		{Type: decisionmodel.QuestionScore, Score: 2},
		{Type: decisionmodel.QuestionChoice, Choice: "weak", Probabilities: map[string]float64{"2": 1}},
	} {
		if _, err := councilExpectedScore(answer); err == nil {
			t.Fatalf("accepted invalid score answer %+v", answer)
		}
	}
}
func TestCouncilErrors(t *testing.T) {
	in := councilTestInput()
	for _, policy := range []CouncilPolicy{"", "bad"} {
		if _, err := (DecisionCouncil{Model: councilTestModel(0, 0, 0, "buy"), Policy: policy}).Evaluate(context.Background(), in); err == nil {
			t.Fatal("unknown policy accepted")
		}
	}
	in.Position.HasPosition = true
	in.Position.EntryPrice = 100
	in.Position.MarkPrice = 100
	if _, err := (DecisionCouncil{Model: councilTestModel(0, 0, 0, "buy"), Policy: CouncilVetoV1}).Evaluate(context.Background(), in); err == nil {
		t.Fatal("invalid action accepted")
	}
	for _, index := range []int{0, 1} {
		for _, sentinel := range []error{decisionmodel.ErrUnavailable, context.Canceled, context.DeadlineExceeded} {
			f := councilTestModel(0, 0, 0, "buy")
			f.errors = make([]error, index+1)
			f.errors[index] = fmt.Errorf("wrapped: %w", sentinel)
			_, err := (DecisionCouncil{Model: f, Policy: CouncilVetoV1}).Evaluate(context.Background(), councilTestInput())
			if !errors.Is(err, sentinel) || len(f.requests) != index+1 {
				t.Fatalf("index=%d sentinel=%v err=%v", index, sentinel, err)
			}
		}
	}
}
func TestCouncilPromptDigestGolden(t *testing.T) {
	const want = "66e5bb837a7a0a3e0f8dee09260b87f24b5a87fbc133389344a4fa537a845a9d"
	if got := CouncilPromptDigest(); got != want {
		t.Fatalf("digest=%s want=%s", got, want)
	}
	const wantV2 = "688afffdb2e359a10ee99254a03bd2ca776d5cb6b8781808938f18e40255ffba"
	if got := CouncilPromptDigestV2(); got != wantV2 {
		t.Fatalf("v2 digest=%s want=%s", got, wantV2)
	}
}

func TestCouncilStateV2AnonymizedAndBounded(t *testing.T) {
	input := councilTestInput()
	input.AssetDaily = append([]CouncilBar(nil), input.Asset...)
	input.MarketDaily = append([]CouncilBar(nil), input.Market...)
	input.Position = CouncilPosition{HasPosition: true, EntryPrice: 100, MarkPrice: 104, DurationBars: 12, MFEPercent: 8, MAEPercent: -3}
	input.FactorHistory = []CouncilFactorObservation{{BarsAgo: 1, Rank: 2, Momentum: .04, Normalized: 1.2, Volatility: .03, AbsoluteTrend: true}}
	input.CrossSection = []CouncilCrossSection{{Rank: 1, Momentum: .05, AbsoluteTrend: true}}
	state, err := BuildCouncilStateV2(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(state) > 64<<10 {
		t.Fatalf("state bytes=%d", len(state))
	}
	for _, forbidden := range []string{"BTCUSDT", "asset-", "2024-"} {
		if strings.Contains(state, forbidden) {
			t.Fatalf("state leaks %q", forbidden)
		}
	}
	if !strings.Contains(state, "schema=decision-council-state-v2") || !strings.Contains(state, "position_mfe_pct=8.00") {
		t.Fatalf("state=%s", state)
	}
}
