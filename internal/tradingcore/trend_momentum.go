package tradingcore

// This file is deliberately independent of the backtest and services
// packages.  It is the single Stage 06 feature and allocation planner used by
// replay and the runtime adapters.  Adapters only supply point-in-time bars
// and translate the resulting plan into their evidence format.

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
)

const TrendMomentumPlanSchemaVersion = "trend-momentum-plan-v1"

// DefaultTrendMomentumParameters is part of the executable strategy identity.
// Adapters may override only values already governed by their deployment
// envelope; a missing parameter never silently changes the hypothesis.
func DefaultTrendMomentumParameters() map[string]string {
	return map[string]string{"variant": "combined", "vol_normalization": "true", "lookback_bars": "30", "trend_bars": "20", "regime_bars": "30", "rebalance": "24h", "top_n": "3", "max_positions": "3", "risk_on_gross": "0.75", "neutral_gross": "0.25", "risk_off_gross": "0", "regime_band": "0.01", "position_cap": "0.25", "max_gross": "0.75", "max_net": "0.75", "cash_reserve": "0.25", "vol_floor": "0.02", "turnover_budget": "0.25", "skip_delta": "0.005", "execution_gap_reserve": "0.1", "allocation_tolerance": "0.02", "hard_stop": "0.12", "include_shortlist": "true", "execution_intent": "shadow", "target_gross": "0.75"}
}

type TrendMomentumBar struct {
	OpenTime  time.Time `json:"open_time"`
	CloseTime time.Time `json:"close_time"`
	Close     float64   `json:"close"`
}

type TrendMomentumMember struct {
	Symbol           string `json:"symbol"`
	AssetID          string `json:"asset_id"`
	ExchangeSymbolID string `json:"exchange_symbol_id"`
	Eligible         bool   `json:"eligible"`
}

type TrendMomentumPosition struct {
	Symbol     string  `json:"symbol"`
	EntryPrice float64 `json:"entry_price"`
	MarkPrice  float64 `json:"mark_price"`
}

type TrendMomentumInput struct {
	DecisionAt    time.Time                     `json:"decision_at"`
	LastRebalance time.Time                     `json:"last_rebalance"`
	Benchmark     []TrendMomentumBar            `json:"benchmark"`
	Series        map[string][]TrendMomentumBar `json:"series"`
	Members       []TrendMomentumMember         `json:"members"`
	Positions     []TrendMomentumPosition       `json:"positions"`
	LastTargets   []string                      `json:"last_targets"`
	Parameters    map[string]string             `json:"parameters"`
}

// TrendMomentumHistory is an immutable, pre-aggregated view of complete UTC
// 4h buckets. Its fields are intentionally private so callers cannot forge a
// prepared history that bypasses the canonical 15m completeness checks.
// One history may be shared safely by concurrent read-only experiment runs.
type TrendMomentumHistory struct {
	benchmark []TrendMomentumBar
	series    map[string][]TrendMomentumBar
}

// PrepareTrendMomentumHistory performs the expensive 15m -> 4h aggregation
// once. Decision-time filtering remains inside PlanTrendMomentumWithHistory,
// so precomputing later buckets cannot expose them to an earlier decision.
func PrepareTrendMomentumHistory(benchmark []TrendMomentumBar, series map[string][]TrendMomentumBar) *TrendMomentumHistory {
	history := &TrendMomentumHistory{benchmark: tmAggregate4H(benchmark), series: make(map[string][]TrendMomentumBar, len(series))}
	for symbol, bars := range series {
		history.series[symbol] = tmAggregate4H(bars)
	}
	return history
}

type TrendMomentumFactor struct {
	Symbol, AssetID, ExchangeSymbolID string
	Momentum, Volatility, Normalized  float64
	AbsoluteTrend                     bool
	Last, TrendMean                   float64
	Rank                              int
	Selected                          bool
	Reason                            string
}

type TrendMomentumExit struct {
	Primary    string
	Concurrent []string
}

type TrendMomentumPlan struct {
	SchemaVersion string                       `json:"schema_version"`
	Decide        bool                         `json:"decide"`
	Targets       []string                     `json:"targets"`
	TargetWeights map[string]float64           `json:"target_weights"`
	Regime        string                       `json:"regime"`
	RegimeReason  string                       `json:"regime_reason"`
	TargetGross   float64                      `json:"target_gross"`
	Factors       []TrendMomentumFactor        `json:"factors"`
	Exits         map[string]TrendMomentumExit `json:"exits"`
	Diagnostics   []string                     `json:"diagnostics"`
	RiskStopOnly  bool                         `json:"risk_stop_only"`
}

type trendMomentumScore struct{ TrendMomentumFactor }

// PlanTrendMomentum uses only completed 4h buckets at or before DecisionAt.
// It intentionally does not know about broker mode, ledger state, services,
// or backtest types: those are adapter concerns and cannot affect alpha.
func PlanTrendMomentum(input TrendMomentumInput) (TrendMomentumPlan, error) {
	return PlanTrendMomentumWithHistory(input, PrepareTrendMomentumHistory(input.Benchmark, input.Series))
}

// PlanTrendMomentumWithHistory is behaviorally identical to
// PlanTrendMomentum but reuses canonical aggregation across chronological
// decisions and parameter variants.
func PlanTrendMomentumWithHistory(input TrendMomentumInput, history *TrendMomentumHistory) (TrendMomentumPlan, error) {
	p := input.Parameters
	if input.DecisionAt.IsZero() {
		return TrendMomentumPlan{}, fmt.Errorf("trend momentum decision time is required")
	}
	if p == nil {
		return TrendMomentumPlan{}, fmt.Errorf("trend momentum parameters are required")
	}
	if history == nil {
		return TrendMomentumPlan{}, fmt.Errorf("trend momentum prepared history is required")
	}
	if intent := p["execution_intent"]; intent == "live_submit" || intent == "promotion" {
		return TrendMomentumPlan{}, fmt.Errorf("trend momentum execution intent %q is fenced", intent)
	}
	result := TrendMomentumPlan{SchemaVersion: TrendMomentumPlanSchemaVersion, Targets: []string{}, TargetWeights: map[string]float64{}, Factors: []TrendMomentumFactor{}, Exits: map[string]TrendMomentumExit{}, Diagnostics: []string{}}
	rebalance, err := time.ParseDuration(p["rebalance"])
	if err != nil || rebalance <= 0 {
		return result, fmt.Errorf("invalid trend momentum rebalance")
	}
	if !input.LastRebalance.IsZero() && input.DecisionAt.Sub(input.LastRebalance) < rebalance {
		stop := tmFloat(p, "hard_stop")
		stopped := map[string]bool{}
		for _, position := range input.Positions {
			if stop > 0 && position.EntryPrice > 0 && position.MarkPrice > 0 && position.MarkPrice <= position.EntryPrice*(1-stop) {
				stopped[position.Symbol] = true
				result.Exits[position.Symbol] = TrendMomentumExit{Primary: "risk_stop"}
			}
		}
		if len(stopped) == 0 {
			result.Targets = append(result.Targets, input.LastTargets...)
			return result, nil
		}
		for _, symbol := range input.LastTargets {
			if !stopped[symbol] {
				result.Targets = append(result.Targets, symbol)
			}
		}
		result.Decide, result.RiskStopOnly = true, true
		return result, nil
	}
	// A full UTC-aligned 4h feature bucket must have closed. This is a
	// no-lookahead decision boundary shared with replay.
	benchmark := tmPreparedAsOf(history.benchmark, input.DecisionAt)
	regimeBars := tmInt(p, "regime_bars")
	if len(benchmark) < regimeBars {
		return result, fmt.Errorf("completed benchmark regime warmup unavailable")
	}
	benchmark = benchmark[len(benchmark)-regimeBars:]
	mean, price := tmMean(benchmark), benchmark[len(benchmark)-1].Close
	band := tmFloat(p, "regime_band")
	result.Regime, result.RegimeReason = "neutral", "benchmark_inside_neutral_band"
	if price > mean*(1+band) {
		result.Regime, result.RegimeReason = "risk_on", "benchmark_above_long_mean_band"
	} else if price < mean*(1-band) {
		result.Regime, result.RegimeReason = "risk_off", "benchmark_below_long_mean_band"
	}
	variant := p["variant"]
	relative := variant != "absolute_trend_only"
	benchmarkGate := variant == "combined"
	volatility := variant == "combined" && p["vol_normalization"] == "true"
	exposureRegime := result.Regime
	if !benchmarkGate {
		exposureRegime = "risk_on"
	}
	result.TargetGross = math.Min(tmFloat(p, exposureRegime+"_gross"), math.Min(tmFloat(p, "max_gross"), tmFloat(p, "target_gross")))
	lookback, trendBars := tmInt(p, "lookback_bars"), tmInt(p, "trend_bars")
	if lookback < 1 || trendBars < 1 {
		return result, fmt.Errorf("trend momentum lookbacks are required")
	}
	needed, floor := maxTrendInt(lookback+1, trendBars), tmFloat(p, "vol_floor")
	members := map[string]TrendMomentumMember{}
	for _, member := range input.Members {
		if member.Eligible {
			if member.Symbol == "" || member.AssetID == "" || member.ExchangeSymbolID == "" {
				return result, fmt.Errorf("eligible member requires stable identities")
			}
			members[member.Symbol] = member
		}
	}
	rows := []trendMomentumScore{}
	for symbol, member := range members {
		bars := tmPreparedAsOf(history.series[symbol], input.DecisionAt)
		if len(bars) < needed {
			result.Diagnostics = append(result.Diagnostics, symbol+":insufficient_warmup")
			continue
		}
		if !bars[len(bars)-1].CloseTime.Equal(benchmark[len(benchmark)-1].CloseTime) {
			result.Diagnostics = append(result.Diagnostics, symbol+":feature_bucket_mismatch")
			continue
		}
		from, last := bars[len(bars)-1-lookback].Close, bars[len(bars)-1].Close
		if from <= 0 || last <= 0 {
			result.Diagnostics = append(result.Diagnostics, symbol+":invalid_close")
			continue
		}
		momentum, vol := last/from-1, tmVolatility(bars[len(bars)-1-lookback:])
		normalized := momentum
		if volatility {
			normalized = momentum / math.Max(vol, floor)
		}
		rows = append(rows, trendMomentumScore{TrendMomentumFactor: TrendMomentumFactor{Symbol: symbol, AssetID: member.AssetID, ExchangeSymbolID: member.ExchangeSymbolID, Momentum: momentum, Volatility: vol, Normalized: normalized, AbsoluteTrend: last > tmMean(bars[len(bars)-trendBars:]), Last: last, TrendMean: tmMean(bars[len(bars)-trendBars:])}})
	}
	sort.Slice(rows, func(i, j int) bool {
		if relative && rows[i].Normalized != rows[j].Normalized {
			return rows[i].Normalized > rows[j].Normalized
		}
		if rows[i].AssetID != rows[j].AssetID {
			return rows[i].AssetID < rows[j].AssetID
		}
		return rows[i].ExchangeSymbolID < rows[j].ExchangeSymbolID
	})
	topN := minTrendInt(tmInt(p, "top_n"), tmInt(p, "max_positions"))
	selected := []int{}
	for i := range rows {
		selectable := (variant == "absolute_trend_only" && rows[i].AbsoluteTrend) || (variant == "relative_momentum_only") || (variant == "combined" && result.Regime != "risk_off" && rows[i].AbsoluteTrend)
		if selectable && len(selected) < topN {
			selected = append(selected, i)
		}
	}
	denom := 0.0
	for _, i := range selected {
		v := 1.0
		if volatility {
			v = 1 / math.Max(rows[i].Volatility, floor)
		}
		result.TargetWeights[rows[i].Symbol] = v
		denom += v
	}
	if denom > 0 {
		for symbol, weight := range result.TargetWeights {
			result.TargetWeights[symbol] = math.Min(tmFloat(p, "position_cap"), result.TargetGross*weight/denom)
		}
	}
	for pass := 0; pass < len(result.TargetWeights); pass++ {
		total, open := 0.0, 0
		for _, weight := range result.TargetWeights {
			total += weight
			if weight < tmFloat(p, "position_cap")-1e-12 {
				open++
			}
		}
		if total >= result.TargetGross-1e-12 || open == 0 {
			break
		}
		add := (result.TargetGross - total) / float64(open)
		for symbol, weight := range result.TargetWeights {
			if weight < tmFloat(p, "position_cap")-1e-12 {
				result.TargetWeights[symbol] = math.Min(tmFloat(p, "position_cap"), weight+add)
			}
		}
	}
	for i := range rows {
		_, rows[i].Selected = result.TargetWeights[rows[i].Symbol]
		rows[i].Rank = i + 1
		rows[i].Reason = "excluded_below_rank"
		if rows[i].Selected {
			rows[i].Reason = "selected"
		} else if variant != "relative_momentum_only" && !rows[i].AbsoluteTrend {
			rows[i].Reason = "excluded_absolute_trend"
		} else if result.Regime == "risk_off" {
			rows[i].Reason = "excluded_regime_risk_off"
		}
		result.Factors = append(result.Factors, rows[i].TrendMomentumFactor)
	}
	for symbol := range result.TargetWeights {
		result.Targets = append(result.Targets, symbol)
	}
	sort.Slice(result.Targets, func(i, j int) bool {
		a, b := members[result.Targets[i]], members[result.Targets[j]]
		if a.AssetID != b.AssetID {
			return a.AssetID < b.AssetID
		}
		return a.ExchangeSymbolID < b.ExchangeSymbolID
	})
	bySymbol := map[string]trendMomentumScore{}
	for _, row := range rows {
		bySymbol[row.Symbol] = row
	}
	for _, position := range input.Positions {
		reasons := []string{}
		if stop := tmFloat(p, "hard_stop"); stop > 0 && position.EntryPrice > 0 && position.MarkPrice > 0 && position.MarkPrice <= position.EntryPrice*(1-stop) {
			reasons = append(reasons, "risk_stop")
		}
		if benchmarkGate && result.Regime == "risk_off" {
			reasons = append(reasons, "regime_risk_off")
		} else if benchmarkGate && result.Regime == "neutral" {
			reasons = append(reasons, "regime_reduction")
		}
		state, ok := bySymbol[position.Symbol]
		if !ok {
			reasons = append(reasons, "loss_of_eligibility")
		} else if variant != "relative_momentum_only" && !state.AbsoluteTrend {
			reasons = append(reasons, "loss_of_absolute_trend")
		}
		if _, ok := result.TargetWeights[position.Symbol]; !ok {
			if relative {
				reasons = append(reasons, "loss_of_rank")
			} else {
				reasons = append(reasons, "loss_of_selection")
			}
		}
		if len(reasons) > 0 {
			result.Exits[position.Symbol] = TrendMomentumExit{Primary: reasons[0], Concurrent: append([]string(nil), reasons[1:]...)}
		}
	}
	result.Decide = true
	return result, nil
}

func tmAggregate4H(bars []TrendMomentumBar) []TrendMomentumBar {
	buckets := map[time.Time]map[int]TrendMomentumBar{}
	for _, bar := range bars {
		if bar.Close <= 0 {
			continue
		}
		open := bar.OpenTime.UTC()
		if open.Minute()%15 != 0 || open.Second() != 0 || open.Nanosecond() != 0 {
			continue
		}
		closeAt := bar.CloseTime.UTC()
		if closeAt.Before(open) || closeAt.After(open.Add(15*time.Minute)) {
			continue
		}
		bucket := open.Truncate(4 * time.Hour)
		if buckets[bucket] == nil {
			buckets[bucket] = map[int]TrendMomentumBar{}
		}
		buckets[bucket][int(open.Sub(bucket)/(15*time.Minute))] = bar
	}
	keys := []time.Time{}
	for key, bucket := range buckets {
		if len(bucket) == 16 {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Before(keys[j]) })
	result := make([]TrendMomentumBar, 0, len(keys))
	for _, key := range keys {
		result = append(result, buckets[key][15])
	}
	return result
}

func tmPreparedAsOf(bars []TrendMomentumBar, at time.Time) []TrendMomentumBar {
	end := sort.Search(len(bars), func(i int) bool { return bars[i].CloseTime.After(at) })
	return bars[:end]
}
func tmMean(b []TrendMomentumBar) float64 {
	total := 0.0
	for _, v := range b {
		total += v.Close
	}
	return total / float64(len(b))
}
func tmVolatility(b []TrendMomentumBar) float64 {
	if len(b) < 2 {
		return 0
	}
	values := make([]float64, 0, len(b)-1)
	for i := 1; i < len(b); i++ {
		if b[i-1].Close > 0 {
			values = append(values, b[i].Close/b[i-1].Close-1)
		}
	}
	if len(values) < 2 {
		return 0
	}
	mean := 0.0
	for _, v := range values {
		mean += v
	}
	mean /= float64(len(values))
	sum := 0.0
	for _, v := range values {
		d := v - mean
		sum += d * d
	}
	return math.Sqrt(sum / float64(len(values)-1))
}
func tmFloat(p map[string]string, key string) float64 {
	v, _ := strconv.ParseFloat(p[key], 64)
	return v
}
func tmInt(p map[string]string, key string) int { v, _ := strconv.Atoi(p[key]); return v }
func minTrendInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxTrendInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
