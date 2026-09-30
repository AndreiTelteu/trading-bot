package tradingcore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"trading-go/internal/decisionmodel"
)

// CouncilBar is one market-data bar. Aggregated bars use UTC bucket boundaries.
type CouncilBar struct {
	OpenTime, CloseTime            time.Time
	Open, High, Low, Close, Volume float64
}

// AggregateCouncilBars4H keeps complete, aligned 4h buckets closed by at.
func AggregateCouncilBars4H(bars []CouncilBar, at time.Time, limit int) []CouncilBar {
	if limit <= 0 {
		return []CouncilBar{}
	}
	buckets := make(map[time.Time]map[int]CouncilBar)
	invalid := make(map[time.Time]bool)
	for _, bar := range bars {
		open := bar.OpenTime.UTC()
		if open.IsZero() || open.Minute()%15 != 0 || open.Second() != 0 || open.Nanosecond() != 0 ||
			bar.CloseTime.Before(bar.OpenTime) || bar.CloseTime.After(bar.OpenTime.Add(15*time.Minute)) ||
			!validCouncilBar(bar) {
			continue
		}
		bucket := open.Truncate(4 * time.Hour)
		index := int(open.Sub(bucket) / (15 * time.Minute))
		if buckets[bucket] == nil {
			buckets[bucket] = make(map[int]CouncilBar, 16)
		}
		// Duplicate slots cannot provide evidence of a complete bucket.
		if _, exists := buckets[bucket][index]; exists {
			invalid[bucket] = true
		} else {
			buckets[bucket][index] = bar
		}
	}
	keys := make([]time.Time, 0, len(buckets))
	for key, slots := range buckets {
		if !invalid[key] && len(slots) == 16 && !slots[15].CloseTime.IsZero() && !slots[15].CloseTime.After(at) {
			complete := true
			for i := 0; i < 16; i++ {
				if slots[i].CloseTime.IsZero() {
					complete = false
					break
				}
			}
			if complete {
				keys = append(keys, key)
			}
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	if len(keys) > limit {
		keys = keys[len(keys)-limit:]
	}
	out := make([]CouncilBar, 0, len(keys))
	for _, key := range keys {
		slots := buckets[key]
		bar := CouncilBar{OpenTime: key, CloseTime: slots[15].CloseTime, Open: slots[0].Open, High: slots[0].High, Low: slots[0].Low, Close: slots[15].Close}
		for i := 0; i < 16; i++ {
			if slots[i].High > bar.High {
				bar.High = slots[i].High
			}
			if slots[i].Low < bar.Low {
				bar.Low = slots[i].Low
			}
			bar.Volume += slots[i].Volume
		}
		out = append(out, bar)
	}
	return out
}

// AggregateCouncilBars4HFrom1H keeps complete four-slot UTC 4h buckets.
func AggregateCouncilBars4HFrom1H(bars []CouncilBar, at time.Time, limit int) []CouncilBar {
	if limit <= 0 {
		return []CouncilBar{}
	}
	buckets := make(map[time.Time]map[int]CouncilBar)
	for _, bar := range bars {
		open := bar.OpenTime.UTC()
		if open.Minute() != 0 || open.Second() != 0 || open.Nanosecond() != 0 || bar.CloseTime.Before(bar.OpenTime) || bar.CloseTime.After(bar.OpenTime.Add(time.Hour)) || !validCouncilBar(bar) {
			continue
		}
		bucket := open.Truncate(4 * time.Hour)
		if buckets[bucket] == nil {
			buckets[bucket] = map[int]CouncilBar{}
		}
		index := int(open.Sub(bucket) / time.Hour)
		if _, exists := buckets[bucket][index]; exists {
			delete(buckets, bucket)
			continue
		}
		buckets[bucket][index] = bar
	}
	keys := []time.Time{}
	for key, slots := range buckets {
		if len(slots) == 4 && !slots[3].CloseTime.After(at) {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	if len(keys) > limit {
		keys = keys[len(keys)-limit:]
	}
	out := make([]CouncilBar, 0, len(keys))
	for _, key := range keys {
		slots := buckets[key]
		bar := CouncilBar{OpenTime: key, CloseTime: slots[3].CloseTime, Open: slots[0].Open, High: slots[0].High, Low: slots[0].Low, Close: slots[3].Close}
		for i := 0; i < 4; i++ {
			if slots[i].High > bar.High {
				bar.High = slots[i].High
			}
			if slots[i].Low < bar.Low {
				bar.Low = slots[i].Low
			}
			bar.Volume += slots[i].Volume
		}
		out = append(out, bar)
	}
	return out
}

func validCouncilBar(b CouncilBar) bool {
	for _, v := range []float64{b.Open, b.High, b.Low, b.Close, b.Volume} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return b.Open > 0 && b.Close > 0 && b.Low > 0 && b.High >= b.Low && b.High >= b.Open && b.High >= b.Close && b.Low <= b.Open && b.Low <= b.Close && b.Volume >= 0
}

type CouncilSignal struct {
	V4Action                                       string
	Regime                                         string
	Rank, UniverseSize                             int
	Momentum, Normalized, Volatility, TargetWeight float64
	AbsoluteTrend                                  bool
}
type CouncilPosition struct {
	HasPosition            bool
	EntryPrice, MarkPrice  float64
	DurationBars           int
	MFEPercent, MAEPercent float64
}

type CouncilFactorObservation struct {
	BarsAgo                          int
	Rank                             int
	Momentum, Normalized, Volatility float64
	AbsoluteTrend                    bool
}

type CouncilCrossSection struct {
	Rank                             int
	Momentum, Normalized, Volatility float64
	AbsoluteTrend, HasPosition       bool
}

type CouncilInput struct {
	Asset, Market           []CouncilBar
	AssetDaily, MarketDaily []CouncilBar
	Signal                  CouncilSignal
	Position                CouncilPosition
	FactorHistory           []CouncilFactorObservation
	CrossSection            []CouncilCrossSection
}

const CouncilMinimumBars = 51
const councilStateSchema = "decision-council-state-v1"
const councilStateSchemaV2 = "decision-council-state-v2-compact-v1"

var ErrInsufficientCouncilData = errors.New("insufficient council data")

// BuildCouncilState emits only relative, rounded evidence in a stable key order.
func BuildCouncilState(input CouncilInput) (string, error) {
	if len(input.Asset) < CouncilMinimumBars || len(input.Market) < CouncilMinimumBars {
		return "", ErrInsufficientCouncilData
	}
	for _, series := range [][]CouncilBar{input.Asset, input.Market} {
		for i, bar := range series {
			if !validCouncilBar(bar) || (i > 0 && !bar.CloseTime.After(series[i-1].CloseTime)) {
				return "", fmt.Errorf("invalid council bars")
			}
		}
	}
	if !validSignal(input.Signal) || (input.Position.HasPosition && (!finitePositive(input.Position.EntryPrice) || !finitePositive(input.Position.MarkPrice))) {
		return "", fmt.Errorf("invalid council signal or position")
	}
	a, m := input.Asset, input.Market
	var b strings.Builder
	b.WriteString("schema=" + councilStateSchema + "\nbar=4h")
	for _, k := range []int{1, 3, 6, 12, 30} {
		fmt.Fprintf(&b, "\nasset_ret%d_pct=%.2f", k, cleanZero(councilReturn(a, k)))
	}
	fmt.Fprintf(&b, "\nasset_realized_vol30_pct=%.2f\nasset_atr14_pct=%.2f\nasset_rsi14=%.2f", cleanZero(councilVol(a, 30)), cleanZero(councilATR(a, 14)), cleanZero(councilRSI(a, 14)))
	fmt.Fprintf(&b, "\nasset_sma20_distance_pct=%.2f\nasset_sma50_distance_pct=%.2f", cleanZero(councilSMADistance(a, 20)), cleanZero(councilSMADistance(a, 50)))
	high, low := councilRange(a, 30)
	position := 0.0
	if high > low {
		position = (a[len(a)-1].Close - low) / (high - low)
	}
	fmt.Fprintf(&b, "\nasset_drawdown30_pct=%.2f\nasset_range30_position=%.2f", cleanZero((a[len(a)-1].Close/high-1)*100), cleanZero(clampCouncil(position)))
	fmt.Fprintf(&b, "\nasset_volume_ratio20=%.2f\nasset_volume_trend5_20=%.2f", cleanZero(councilRatio(a[len(a)-1].Volume, councilMeanVolume(a, 20))), cleanZero(councilRatio(councilMeanVolume(a, 5), councilMeanVolume(a, 20))))
	ups := 0
	for i := len(a) - 12; i < len(a); i++ {
		if a[i].Close > a[i].Open {
			ups++
		}
	}
	fmt.Fprintf(&b, "\nasset_up_bars12=%d\nasset_recent12=", ups)
	for i := len(a) - 12; i < len(a); i++ {
		if i > len(a)-12 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%.2f/%.2f/%.2f", cleanZero((a[i].Close/a[i-1].Close-1)*100), cleanZero((a[i].High-a[i].Low)/a[i].Close*100), cleanZero(councilRatio(a[i].Volume, councilMeanVolumeAt(a, i, 20))))
	}
	for _, k := range []int{1, 6, 30} {
		fmt.Fprintf(&b, "\nmarket_ret%d_pct=%.2f", k, cleanZero(councilReturn(m, k)))
	}
	fmt.Fprintf(&b, "\nmarket_sma30_distance_pct=%.2f\nmarket_realized_vol30_pct=%.2f\nrelative_ret30_pct=%.2f", cleanZero(councilSMADistance(m, 30)), cleanZero(councilVol(m, 30)), cleanZero(councilReturn(a, 30)-councilReturn(m, 30)))
	fmt.Fprintf(&b, "\nsignal=%s\nregime=%s\nrank=%d/%d\nmomentum=%.2f\nnormalized_momentum=%.2f\nvolatility=%.2f\nabsolute_trend=%t\ntarget_weight=%.2f\nhas_position=%t", input.Signal.V4Action, input.Signal.Regime, input.Signal.Rank, input.Signal.UniverseSize, cleanZero(input.Signal.Momentum), cleanZero(input.Signal.Normalized), cleanZero(input.Signal.Volatility), input.Signal.AbsoluteTrend, cleanZero(input.Signal.TargetWeight), input.Position.HasPosition)
	if input.Position.HasPosition {
		fmt.Fprintf(&b, "\nunrealized_pnl_pct=%.2f", cleanZero((input.Position.MarkPrice/input.Position.EntryPrice-1)*100))
	}
	return b.String(), nil
}

// BuildCouncilStateV2 emits a compact, anonymized point-in-time state. It
// summarizes long histories instead of serializing every bar, keeping local
// decision-model latency bounded without discarding multi-horizon evidence.
// Raw prices, symbols and timestamps are deliberately excluded.
func BuildCouncilStateV2(input CouncilInput) (string, error) {
	if len(input.Asset) < CouncilMinimumBars || len(input.Market) < CouncilMinimumBars {
		return "", ErrInsufficientCouncilData
	}
	if len(input.Asset) > 360 || len(input.Market) > 360 || len(input.AssetDaily) > 180 || len(input.MarketDaily) > 180 || len(input.FactorHistory) > 360 || len(input.CrossSection) > 64 {
		return "", fmt.Errorf("unbounded council v2 state")
	}
	for _, series := range [][]CouncilBar{input.Asset, input.Market, input.AssetDaily, input.MarketDaily} {
		for i, bar := range series {
			if !validCouncilBar(bar) || (i > 0 && !bar.CloseTime.After(series[i-1].CloseTime)) {
				return "", fmt.Errorf("invalid council bars")
			}
		}
	}
	if !validSignal(input.Signal) || input.Position.DurationBars < 0 || (input.Position.HasPosition && (!finitePositive(input.Position.EntryPrice) || !finitePositive(input.Position.MarkPrice))) {
		return "", fmt.Errorf("invalid council signal or position")
	}
	base, err := BuildCouncilState(input)
	if err != nil {
		return "", err
	}
	base = strings.Replace(base, "schema="+councilStateSchema, "schema="+councilStateSchemaV2, 1)
	var b strings.Builder
	b.WriteString(base)
	fmt.Fprintf(&b, "\nasset_4h_count=%d\nmarket_4h_count=%d\nasset_daily_count=%d\nmarket_daily_count=%d", len(input.Asset), len(input.Market), len(input.AssetDaily), len(input.MarketDaily))
	writeDailySummary := func(label string, bars []CouncilBar) {
		fmt.Fprintf(&b, "\n%s=", label)
		wrote := false
		for _, horizon := range []int{1, 7, 30, 90} {
			if len(bars) <= horizon {
				continue
			}
			if wrote {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, "r%d:%.2f", horizon, cleanZero(councilReturn(bars, horizon)))
			wrote = true
		}
		if len(bars) >= 31 {
			fmt.Fprintf(&b, ",vol30:%.2f,sma30:%.2f", cleanZero(councilVol(bars, 30)), cleanZero(councilSMADistance(bars, 30)))
		}
		if !wrote {
			b.WriteString("short")
		}
	}
	writeDailySummary("asset_daily_summary", input.AssetDaily)
	writeDailySummary("market_daily_summary", input.MarketDaily)
	fmt.Fprintf(&b, "\nposition_duration_4h=%d\nposition_mfe_pct=%.2f\nposition_mae_pct=%.2f", input.Position.DurationBars, cleanZero(input.Position.MFEPercent), cleanZero(input.Position.MAEPercent))
	b.WriteString("\nfactor_recent=bars_ago/rank/momentum/normalized/volatility/absolute")
	factorStart := len(input.FactorHistory) - 8
	if factorStart < 0 {
		factorStart = 0
	}
	for _, value := range input.FactorHistory[factorStart:] {
		fmt.Fprintf(&b, ";%d/%d/%.3f/%.3f/%.3f/%t", value.BarsAgo, value.Rank, cleanZero(value.Momentum), cleanZero(value.Normalized), cleanZero(value.Volatility), value.AbsoluteTrend)
	}
	b.WriteString("\ncross_top=rank/momentum/normalized/volatility/absolute/held")
	crossLimit := minCouncilInt(len(input.CrossSection), 12)
	for _, value := range input.CrossSection[:crossLimit] {
		fmt.Fprintf(&b, ";%d/%.3f/%.3f/%.3f/%t/%t", value.Rank, cleanZero(value.Momentum), cleanZero(value.Normalized), cleanZero(value.Volatility), value.AbsoluteTrend, value.HasPosition)
	}
	if b.Len() > 4<<10 {
		return "", fmt.Errorf("council v2 compact state exceeds 4 KiB")
	}
	return b.String(), nil
}

func minCouncilInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func finitePositive(v float64) bool { return v > 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
func validSignal(s CouncilSignal) bool {
	if s.V4Action != "buy" && s.V4Action != "hold" {
		return false
	}
	if s.Regime != "risk_on" && s.Regime != "neutral" && s.Regime != "risk_off" {
		return false
	}
	if s.Rank < 0 || s.UniverseSize < 1 || s.Rank > s.UniverseSize {
		return false
	}
	for _, v := range []float64{s.Momentum, s.Normalized, s.Volatility, s.TargetWeight} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}
func councilReturn(b []CouncilBar, k int) float64 {
	n := len(b) - 1
	return (b[n].Close/b[n-k].Close - 1) * 100
}
func councilMeanClose(b []CouncilBar, k int) float64 {
	s := 0.0
	for _, v := range b[len(b)-k:] {
		s += v.Close
	}
	return s / float64(k)
}
func councilSMADistance(b []CouncilBar, k int) float64 {
	return (b[len(b)-1].Close/councilMeanClose(b, k) - 1) * 100
}
func councilMeanVolumeAt(b []CouncilBar, end, k int) float64 {
	s := 0.0
	for i := end - k + 1; i <= end; i++ {
		s += b[i].Volume
	}
	return s / float64(k)
}
func councilMeanVolume(b []CouncilBar, k int) float64 { return councilMeanVolumeAt(b, len(b)-1, k) }
func councilRatio(num, den float64) float64 {
	if den == 0 {
		return 0
	}
	return num / den
}
func councilRange(b []CouncilBar, k int) (float64, float64) {
	h, l := b[len(b)-k].High, b[len(b)-k].Low
	for _, v := range b[len(b)-k:] {
		if v.High > h {
			h = v.High
		}
		if v.Low < l {
			l = v.Low
		}
	}
	return h, l
}
func councilVol(b []CouncilBar, k int) float64 {
	n := len(b) - 1
	sum := 0.0
	for i := n - k + 1; i <= n; i++ {
		sum += math.Log(b[i].Close / b[i-1].Close)
	}
	mean := sum / float64(k)
	variance := 0.0
	for i := n - k + 1; i <= n; i++ {
		d := math.Log(b[i].Close/b[i-1].Close) - mean
		variance += d * d
	}
	return math.Sqrt(variance/float64(k)) * 100
}
func councilATR(b []CouncilBar, k int) float64 {
	// Wilder smoothing: seed with the first k true ranges, then update once
	// per completed bar. This uses the full point-in-time warmup series.
	atr := 0.0
	for i := 1; i < len(b); i++ {
		v := b[i]
		tr := math.Max(v.High-v.Low, math.Max(math.Abs(v.High-b[i-1].Close), math.Abs(v.Low-b[i-1].Close)))
		if i <= k {
			atr += tr
			if i == k {
				atr /= float64(k)
			}
		} else {
			atr = (atr*float64(k-1) + tr) / float64(k)
		}
	}
	return atr / b[len(b)-1].Close * 100
}
func councilRSI(b []CouncilBar, k int) float64 {
	gain, loss := 0.0, 0.0
	for i := 1; i <= k; i++ {
		d := b[i].Close - b[i-1].Close
		if d > 0 {
			gain += d
		} else {
			loss -= d
		}
	}
	gain /= float64(k)
	loss /= float64(k)
	for i := k + 1; i < len(b); i++ {
		d := b[i].Close - b[i-1].Close
		g, l := 0.0, 0.0
		if d > 0 {
			g = d
		} else {
			l = -d
		}
		gain = (gain*float64(k-1) + g) / float64(k)
		loss = (loss*float64(k-1) + l) / float64(k)
	}
	if loss == 0 {
		if gain == 0 {
			return 50
		}
		return 100
	}
	return 100 - 100/(1+gain/loss)
}
func cleanZero(v float64) float64 {
	if math.Abs(v) < 0.005 {
		return 0
	}
	return v
}
func clampCouncil(v float64) float64 { return math.Max(0, math.Min(1, v)) }

type CouncilPolicy string

const (
	CouncilObserveV1 CouncilPolicy = "observe_v1"
	CouncilVetoV1    CouncilPolicy = "veto_v1"
	CouncilObserveV2 CouncilPolicy = "observe_v2"
	CouncilActiveV2  CouncilPolicy = "active_v2"
)

type CouncilOutcome struct {
	Bull, Bear, Hodl                                                   float64
	BullConfidence, BearConfidence, HodlConfidence                     float64
	FinalChoice                                                        string
	FinalProbabilities                                                 map[string]float64
	FinalConfidence                                                    float64
	Proposed                                                           string
	Applied                                                            bool
	Reason                                                             string
	StateDigest, ScoreRequestDigest, FinalRequestDigest, ResolvedModel string
	Cached                                                             bool
}
type DecisionCouncil struct {
	Model  decisionmodel.Model
	Policy CouncilPolicy
}

// councilLevels are ordered (lowest first) choice keys for the three council
// scores. Score questions are asked as five-option choice questions because
// the providers answer typed choices reliably; the expected level index is
// computed locally from the reported option probabilities.
var councilLevels = []string{"none", "weak", "moderate", "strong", "very_strong"}
var councilLevelMeanings = map[string]string{
	"none":        "No support at all.",
	"weak":        "Weak support.",
	"moderate":    "Moderate support.",
	"strong":      "Strong support.",
	"very_strong": "Very strong support.",
}

const councilBullInstruction = "Score upside continuation merit over the next few 4h bars. Weigh volume confirmation, momentum persistence across horizons, trend alignment with market, and orderly structure above averages; penalize overextension."
const councilBearInstruction = "Score downside or reversal risk over the next few 4h bars. Weigh exhaustion, overbought RSI, stretched distance above averages, fading volume on up moves, adverse market regime, high volatility, and breakdown from range."
const councilHodlInstruction = "If has_position=true, score merit of keeping the position open using intact trend, absence of breakdown, and unrealized P&L context. If has_position=false, score merit of staying flat or not buying when evidence conflicts or is weak."
const councilFinalInstruction = "Choose the best action for the current long-only state using the asset, market, signal, position, and council scores."
const councilEntryRule = "buy && !has_position: admit iff final=buy && bull>=0.50 && bear<0.50; else veto"
const councilHoldRule = "hold && has_position: early_exit iff final=sell && bear>=0.60 && hodl<0.40; else hold"
const councilScoreRule = "score=clamp(sum(level_index*probability[level])/(levels-1),0,1) over ordered choice levels; when probabilities absent clamp(index(choice)/(levels-1),0,1)"

func councilLevelCriteria() map[string]string {
	criteria := make(map[string]string, len(councilLevelMeanings))
	for key, meaning := range councilLevelMeanings {
		criteria[key] = meaning
	}
	return criteria
}

func councilScoreQuestions() map[string]decisionmodel.Question {
	return map[string]decisionmodel.Question{
		"decision_bull": {Type: decisionmodel.QuestionChoice, Instructions: councilBullInstruction, ChoiceCriteria: councilLevelCriteria()},
		"decision_bear": {Type: decisionmodel.QuestionChoice, Instructions: councilBearInstruction, ChoiceCriteria: councilLevelCriteria()},
		"decision_hodl": {Type: decisionmodel.QuestionChoice, Instructions: councilHodlInstruction, ChoiceCriteria: councilLevelCriteria()},
	}
}
func councilFinalQuestions() map[string]decisionmodel.Question {
	return map[string]decisionmodel.Question{"decision_final": {Type: decisionmodel.QuestionChoice, Instructions: councilFinalInstruction, ChoiceCriteria: map[string]string{"buy": "open or keep a long", "sell": "exit or avoid", "hodl": "keep current state: hold an open position or stay flat"}}}
}

// CouncilPromptDigest identifies the complete prompt and combination contract.
func CouncilPromptDigest() string {
	definition := struct {
		Schema    string                            `json:"schema"`
		Score     map[string]decisionmodel.Question `json:"score_questions"`
		Final     map[string]decisionmodel.Question `json:"final_questions"`
		ScoreRule string                            `json:"score_rule"`
		EntryRule string                            `json:"entry_rule"`
		HoldRule  string                            `json:"hold_rule"`
	}{councilStateSchema, councilScoreQuestions(), councilFinalQuestions(), councilScoreRule, councilEntryRule, councilHoldRule}
	payload, _ := json.Marshal(definition)
	h := sha256.Sum256(payload)
	return hex.EncodeToString(h[:])
}

// CouncilPromptDigestV2 binds the larger state schema and active-v2 authority
// without changing the historical v1 digest.
func CouncilPromptDigestV2() string {
	definition := struct {
		Schema    string                            `json:"schema"`
		Score     map[string]decisionmodel.Question `json:"score_questions"`
		Final     map[string]decisionmodel.Question `json:"final_questions"`
		ScoreRule string                            `json:"score_rule"`
		EntryRule string                            `json:"entry_rule"`
		HoldRule  string                            `json:"hold_rule"`
	}{councilStateSchemaV2, councilScoreQuestions(), councilFinalQuestions(), councilScoreRule, councilEntryRule, councilHoldRule}
	payload, _ := json.Marshal(definition)
	h := sha256.Sum256(payload)
	return hex.EncodeToString(h[:])
}
func councilExpectedScore(answer decisionmodel.Answer) (float64, error) {
	if answer.Type != decisionmodel.QuestionChoice {
		return 0, fmt.Errorf("invalid council score answer type")
	}
	index := make(map[string]int, len(councilLevels))
	for i, level := range councilLevels {
		index[level] = i
	}
	if len(answer.Probabilities) == 0 {
		i, ok := index[answer.Choice]
		if !ok {
			return 0, fmt.Errorf("invalid council score choice")
		}
		return clampCouncil(float64(i) / float64(len(councilLevels)-1)), nil
	}
	sum, weighted := 0.0, 0.0
	for key, p := range answer.Probabilities {
		if _, ok := index[key]; !ok || math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
			return 0, fmt.Errorf("invalid council score probability")
		}
	}
	for i, level := range councilLevels {
		p := answer.Probabilities[level]
		sum += p
		weighted += float64(i) * p
	}
	if sum <= 0 {
		return 0, fmt.Errorf("invalid council score probability sum")
	}
	return clampCouncil(weighted / float64(len(councilLevels)-1)), nil
}

func (c DecisionCouncil) Evaluate(ctx context.Context, input CouncilInput) (CouncilOutcome, error) {
	if c.Policy != CouncilObserveV1 && c.Policy != CouncilVetoV1 && c.Policy != CouncilObserveV2 && c.Policy != CouncilActiveV2 {
		return CouncilOutcome{}, fmt.Errorf("unknown council policy %q", c.Policy)
	}
	if c.Model == nil {
		return CouncilOutcome{}, fmt.Errorf("council model is required")
	}
	if !(input.Signal.V4Action == "buy" && !input.Position.HasPosition || input.Signal.V4Action == "hold" && input.Position.HasPosition) {
		return CouncilOutcome{}, fmt.Errorf("invalid council action and position combination")
	}
	if err := ctx.Err(); err != nil {
		return CouncilOutcome{}, err
	}
	state, err := BuildCouncilState(input)
	if c.Policy == CouncilObserveV2 || c.Policy == CouncilActiveV2 {
		state, err = BuildCouncilStateV2(input)
	}
	if err != nil {
		return CouncilOutcome{}, err
	}
	h := sha256.Sum256([]byte(state))
	out := CouncilOutcome{StateDigest: hex.EncodeToString(h[:]), Applied: c.Policy == CouncilVetoV1 || c.Policy == CouncilActiveV2}
	scores, err := c.Model.Decide(ctx, decisionmodel.Request{State: state, Questions: councilScoreQuestions()})
	if err != nil {
		return CouncilOutcome{}, fmt.Errorf("council scores: %w", err)
	}
	out.ScoreRequestDigest = scores.RequestDigest
	out.ResolvedModel = scores.ResolvedModel
	out.Cached = scores.Cached
	for _, item := range []struct {
		name              string
		value, confidence *float64
	}{{"decision_bull", &out.Bull, &out.BullConfidence}, {"decision_bear", &out.Bear, &out.BearConfidence}, {"decision_hodl", &out.Hodl, &out.HodlConfidence}} {
		answer, ok := scores.Answers[item.name]
		if !ok || answer.Type != decisionmodel.QuestionChoice {
			return CouncilOutcome{}, fmt.Errorf("invalid council score answer %s", item.name)
		}
		*item.value, err = councilExpectedScore(answer)
		if err != nil {
			return CouncilOutcome{}, fmt.Errorf("%s: %w", item.name, err)
		}
		*item.confidence = answer.Confidence
	}
	finalState := fmt.Sprintf("%s\ncouncil: bull=%.2f bear=%.2f hodl=%.2f", state, out.Bull, out.Bear, out.Hodl)
	final, err := c.Model.Decide(ctx, decisionmodel.Request{State: finalState, Questions: councilFinalQuestions()})
	if err != nil {
		return CouncilOutcome{}, fmt.Errorf("council final: %w", err)
	}
	answer, ok := final.Answers["decision_final"]
	if !ok || answer.Type != decisionmodel.QuestionChoice || (answer.Choice != "buy" && answer.Choice != "sell" && answer.Choice != "hodl") {
		return CouncilOutcome{}, fmt.Errorf("invalid council final answer")
	}
	out.FinalRequestDigest = final.RequestDigest
	out.FinalChoice = answer.Choice
	out.FinalProbabilities = make(map[string]float64, len(answer.Probabilities))
	for key, value := range answer.Probabilities {
		out.FinalProbabilities[key] = value
	}
	out.FinalConfidence = answer.Confidence
	out.ResolvedModel = final.ResolvedModel
	out.Cached = out.Cached && final.Cached
	if input.Signal.V4Action == "buy" {
		out.Proposed = "veto"
		if out.FinalChoice == "buy" && out.Bull >= 0.50 && out.Bear < 0.50 {
			out.Proposed = "admit"
		}
	} else {
		out.Proposed = "hold"
		if out.FinalChoice == "sell" && out.Bear >= 0.60 && out.Hodl < 0.40 {
			out.Proposed = "early_exit"
		}
	}
	out.Reason = "decision_council_" + out.Proposed
	return out, nil
}
