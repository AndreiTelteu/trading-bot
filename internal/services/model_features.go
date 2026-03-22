package services

import (
	"math"
	"sort"
	"time"
)

const ModelFeatureSpecVersion = "learned_signal_v1"

type ModelFeatureInput struct {
	Timestamp         time.Time
	Symbol            string
	Candles15m        []Candle
	Candidate         UniverseCandidateMetrics
	ActiveUniverse    []UniverseCandidateMetrics
	RegimeState       string
	BreadthRatio      float64
	BTCCandles15m     []Candle
	OpenPositionCount int
	ExposureRatio     float64
	AlreadyOpen       bool
}

type ModelFeatureRow struct {
	SpecVersion  string             `json:"spec_version"`
	Timestamp    time.Time          `json:"timestamp"`
	Symbol       string             `json:"symbol"`
	LastPrice    float64            `json:"last_price"`
	Values       map[string]float64 `json:"values"`
	QualityFlags []string           `json:"quality_flags,omitempty"`
	Valid        bool               `json:"valid"`
}

func BuildModelFeatureRow(input ModelFeatureInput) ModelFeatureRow {
	row := ModelFeatureRow{
		SpecVersion: ModelFeatureSpecVersion,
		Timestamp:   input.Timestamp.UTC(),
		Symbol:      input.Symbol,
		LastPrice:   input.Candidate.LastPrice,
		Values:      make(map[string]float64),
	}

	if len(input.Candles15m) < 120 {
		row.QualityFlags = append(row.QualityFlags, "insufficient_15m_history")
		return row
	}

	closes := candleCloses(input.Candles15m)
	volumes := candleVolumes(input.Candles15m)
	price := closes[len(closes)-1]
	if price <= 0 {
		row.QualityFlags = append(row.QualityFlags, "invalid_price")
		return row
	}
	row.LastPrice = price

	ema20 := CalculateEMA(closes, 20)
	ema50 := CalculateEMA(closes, 50)
	ema20Prev := CalculateEMA(closes[:len(closes)-1], 20)
	bollinger := CalculateBollingerBands(closes, 20, 2.0)
	rsi := CalculateRSI(closes, 14)
	macd := CalculateMACD(closes, 12, 26, 9)
	prevMACD := CalculateMACD(closes[:len(closes)-1], 12, 26, 9)
	volumeMA := CalculateVolumeMA(volumes, 20)
	atr := CalculateATR(input.Candles15m, 14)
	mean20, std20 := rollingMeanStd(closes, 20)

	btcReturn1D := 0.0
	btcTrendGap := 0.0
	if len(input.BTCCandles15m) >= 100 {
		btcCloses := candleCloses(input.BTCCandles15m)
		btcPrice := btcCloses[len(btcCloses)-1]
		btcReturn1D = CalculateReturn(btcCloses, 96)
		if btcPrice > 0 {
			btcTrendGap = (CalculateEMA(btcCloses, 20) - CalculateEMA(btcCloses, 50)) / btcPrice
		}
	} else {
		row.QualityFlags = append(row.QualityFlags, "btc_context_unavailable")
	}

	rankByScore := rankPercentileMap(input.ActiveUniverse, func(candidate UniverseCandidateMetrics) float64 {
		return candidate.RankScore
	}, true)
	rankByLiquidity := rankPercentileMap(input.ActiveUniverse, func(candidate UniverseCandidateMetrics) float64 {
		return candidate.MedianIntradayQuoteVolume
	}, true)
	rankByVolatility := rankPercentileMap(input.ActiveUniverse, func(candidate UniverseCandidateMetrics) float64 {
		return candidate.VolatilityRatio
	}, true)

	breakout20 := distanceToRollingHigh(input.Candles15m, 20, price)
	breakdown20 := distanceToRollingLow(input.Candles15m, 20, price)
	bbPercentB := CalculateBBPercentB(bollinger, price)
	priceZScore20 := 0.0
	if std20 > 0 {
		priceZScore20 = (price - mean20) / std20
	}

	atrRatio := 0.0
	if atr > 0 {
		atrRatio = atr / price
	}

	values := map[string]float64{
		"ret_15m_1":                        CalculateReturn(closes, 1),
		"ret_15m_4":                        CalculateReturn(closes, 4),
		"ret_15m_16":                       CalculateReturn(closes, 16),
		"ret_15m_96":                       CalculateReturn(closes, 96),
		"price_vs_ema20":                   safeRatioDelta(price, ema20),
		"price_vs_ema50":                   safeRatioDelta(price, ema50),
		"ema20_slope":                      safeRatioDelta(ema20, ema20Prev),
		"breakout_20":                      breakout20,
		"breakdown_20":                     breakdown20,
		"rsi_14":                           rsi.RSI,
		"rsi_centered":                     (rsi.RSI - 50.0) / 50.0,
		"bb_percent_b":                     bbPercentB,
		"price_zscore_20":                  priceZScore20,
		"macd_hist":                        macd.Histogram,
		"macd_hist_slope":                  macd.Histogram - prevMACD.Histogram,
		"momentum_3":                       CalculateMomentum(closes, 3).Momentum,
		"momentum_12":                      CalculateMomentum(closes, 12).Momentum,
		"volume_ratio_20":                  volumeMA.Ratio,
		"atr_ratio_14":                     atrRatio,
		"realized_vol_20":                  realizedVolatility(closes, 20),
		"quote_volume_24h_log":             math.Log1p(math.Max(input.Candidate.QuoteVolume24h, 0)),
		"median_intraday_quote_volume_log": math.Log1p(math.Max(input.Candidate.MedianIntradayQuoteVolume, 0)),
		"relative_strength_7d":             input.Candidate.RelativeStrength,
		"universe_rank_pct":                percentileOrDefault(rankByScore, input.Candidate.Symbol, 0.5),
		"liquidity_rank_pct":               percentileOrDefault(rankByLiquidity, input.Candidate.Symbol, 0.5),
		"volatility_rank_pct":              percentileOrDefault(rankByVolatility, input.Candidate.Symbol, 0.5),
		"regime_score":                     regimeScoreValue(input.RegimeState),
		"breadth_ratio":                    input.BreadthRatio,
		"btc_return_1d":                    btcReturn1D,
		"btc_trend_gap":                    btcTrendGap,
		"open_position_count":              float64(input.OpenPositionCount),
		"exposure_ratio":                   math.Max(0, input.ExposureRatio),
		"already_open_position":            boolToFloat(input.AlreadyOpen),
		"volume_acceleration":              input.Candidate.VolumeAcceleration,
		"overextension_penalty":            input.Candidate.OverextensionPenalty,
		"trend_quality":                    input.Candidate.TrendQuality,
		"breakout_proximity":               input.Candidate.BreakoutProximity,
		"gap_ratio":                        input.Candidate.GapRatio,
	}

	for key, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			row.QualityFlags = append(row.QualityFlags, "invalid_"+key)
			return row
		}
		row.Values[key] = value
	}

	row.QualityFlags = dedupeStrings(row.QualityFlags)
	row.Valid = true
	return row
}

func candleCloses(candles []Candle) []float64 {
	closes := make([]float64, len(candles))
	for i, candle := range candles {
		closes[i] = candle.Close
	}
	return closes
}

func candleVolumes(candles []Candle) []float64 {
	volumes := make([]float64, len(candles))
	for i, candle := range candles {
		volumes[i] = candle.Volume
	}
	return volumes
}

func rollingMeanStd(values []float64, window int) (float64, float64) {
	if len(values) == 0 || window <= 0 {
		return 0, 0
	}
	if window > len(values) {
		window = len(values)
	}
	segment := values[len(values)-window:]
	mean := 0.0
	for _, value := range segment {
		mean += value
	}
	mean /= float64(len(segment))
	variance := 0.0
	for _, value := range segment {
		delta := value - mean
		variance += delta * delta
	}
	variance /= float64(len(segment))
	return mean, math.Sqrt(variance)
}

func realizedVolatility(closes []float64, window int) float64 {
	if len(closes) < 2 {
		return 0
	}
	if window <= 0 || window >= len(closes) {
		window = len(closes) - 1
	}
	returns := make([]float64, 0, window)
	start := len(closes) - window - 1
	if start < 0 {
		start = 0
	}
	for i := start + 1; i < len(closes); i++ {
		previous := closes[i-1]
		if previous <= 0 {
			continue
		}
		returns = append(returns, (closes[i]-previous)/previous)
	}
	_, stdDev := meanStdFloat64(returns)
	return stdDev
}

func meanStdFloat64(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	mean := 0.0
	for _, value := range values {
		mean += value
	}
	mean /= float64(len(values))
	variance := 0.0
	for _, value := range values {
		delta := value - mean
		variance += delta * delta
	}
	variance /= float64(len(values))
	return mean, math.Sqrt(variance)
}

func safeRatioDelta(current float64, reference float64) float64 {
	if current == 0 || reference == 0 {
		return 0
	}
	return (current - reference) / reference
}

func distanceToRollingHigh(candles []Candle, lookback int, price float64) float64 {
	if len(candles) == 0 || lookback <= 0 || price <= 0 {
		return 0
	}
	start := len(candles) - lookback
	if start < 0 {
		start = 0
	}
	high := candles[start].High
	for i := start + 1; i < len(candles); i++ {
		if candles[i].High > high {
			high = candles[i].High
		}
	}
	if high <= 0 {
		return 0
	}
	return (price / high) - 1.0
}

func distanceToRollingLow(candles []Candle, lookback int, price float64) float64 {
	if len(candles) == 0 || lookback <= 0 || price <= 0 {
		return 0
	}
	start := len(candles) - lookback
	if start < 0 {
		start = 0
	}
	low := candles[start].Low
	for i := start + 1; i < len(candles); i++ {
		if candles[i].Low < low {
			low = candles[i].Low
		}
	}
	if low <= 0 {
		return 0
	}
	return (price / low) - 1.0
}

func rankPercentileMap(candidates []UniverseCandidateMetrics, selector func(UniverseCandidateMetrics) float64, descending bool) map[string]float64 {
	result := make(map[string]float64, len(candidates))
	if len(candidates) == 0 {
		return result
	}
	copyCandidates := append([]UniverseCandidateMetrics(nil), candidates...)
	sort.Slice(copyCandidates, func(i, j int) bool {
		left := selector(copyCandidates[i])
		right := selector(copyCandidates[j])
		if left == right {
			return copyCandidates[i].Symbol < copyCandidates[j].Symbol
		}
		if descending {
			return left > right
		}
		return left < right
	})
	denominator := math.Max(1, float64(len(copyCandidates)-1))
	for idx, candidate := range copyCandidates {
		result[candidate.Symbol] = 1.0 - (float64(idx) / denominator)
	}
	return result
}

func percentileOrDefault(values map[string]float64, symbol string, fallback float64) float64 {
	if value, ok := values[symbol]; ok {
		return value
	}
	return fallback
}

func regimeScoreValue(regime string) float64 {
	switch regime {
	case UniverseRegimeRiskOn:
		return 1.0
	case UniverseRegimeRiskOff:
		return -1.0
	default:
		return 0.0
	}
}

func boolToFloat(value bool) float64 {
	if value {
		return 1.0
	}
	return 0.0
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
