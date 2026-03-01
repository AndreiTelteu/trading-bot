package services

import (
	"math"
)

type Candle struct {
	Close  float64
	High   float64
	Low    float64
	Volume float64
}

type RSIResult struct {
	RSI    float64
	Signal string
}

type MACDResult struct {
	MACD       float64
	SignalLine float64
	Histogram  float64
	Signal     string
}

type BollingerBandsResult struct {
	Upper  float64
	Middle float64
	Lower  float64
	Signal string
}

type VolumeMAResult struct {
	VolumeMA float64
	Ratio    float64
	Signal   string
}

type MomentumResult struct {
	Momentum float64
	Signal   string
}

type FeatureVector struct {
	RSI             float64
	MACDHistogram   float64
	BBPercentB      float64
	MomentumPercent float64
	VolumeRatio     float64
	VolatilityRatio float64
	Valid           bool
}

type IndicatorConfig struct {
	RSIPeriod        int
	RSIOversold      float64
	RSIOverbought    float64
	MACDFastPeriod   int
	MACDSlowPeriod   int
	MACDSignalPeriod int
	BBPeriod         float64
	BBStd            float64
	VolumeMAPeriod   int
	MomentumPeriod   int
}

func DefaultIndicatorConfig() IndicatorConfig {
	return IndicatorConfig{
		RSIPeriod:        14,
		RSIOversold:      30.0,
		RSIOverbought:    70.0,
		MACDFastPeriod:   12,
		MACDSlowPeriod:   26,
		MACDSignalPeriod: 9,
		BBPeriod:         20,
		BBStd:            2.0,
		VolumeMAPeriod:   20,
		MomentumPeriod:   10,
	}
}

func CalculateRSI(closes []float64, period int) RSIResult {
	if len(closes) < period+1 {
		return RSIResult{RSI: 50, Signal: "neutral"}
	}

	var gains, losses float64
	for i := len(closes) - period; i < len(closes); i++ {
		change := closes[i] - closes[i-1]
		if change > 0 {
			gains += change
		} else {
			losses += math.Abs(change)
		}
	}

	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)

	if avgLoss == 0 {
		if avgGain == 0 {
			return RSIResult{RSI: 50, Signal: "neutral"}
		}
		return RSIResult{RSI: 100, Signal: "overbought"}
	}

	rs := avgGain / avgLoss
	rsi := 100 - (100 / (1 + rs))

	signal := "neutral"
	if rsi < 30 {
		signal = "oversold"
	} else if rsi > 70 {
		signal = "overbought"
	}

	return RSIResult{RSI: rsi, Signal: signal}
}

func CalculateMACD(closes []float64, fastPeriod, slowPeriod, signalPeriod int) MACDResult {
	if len(closes) < slowPeriod+signalPeriod {
		return MACDResult{MACD: 0, SignalLine: 0, Histogram: 0, Signal: "neutral"}
	}

	fastEMA := calculateEMA(closes, fastPeriod)
	slowEMA := calculateEMA(closes, slowPeriod)

	macdLine := fastEMA - slowEMA

	macdValues := make([]float64, len(closes))
	for i := slowPeriod - 1; i < len(closes); i++ {
		macdValues[i] = calculateEMA(closes[:i+1], fastPeriod) - calculateEMA(closes[:i+1], slowPeriod)
	}

	signalLine := calculateEMA(macdValues, signalPeriod)
	histogram := macdLine - signalLine

	signal := "neutral"
	if histogram > 0 && macdLine > signalLine {
		signal = "bullish"
	} else if histogram < 0 && macdLine < signalLine {
		signal = "bearish"
	}

	return MACDResult{
		MACD:       macdLine,
		SignalLine: signalLine,
		Histogram:  histogram,
		Signal:     signal,
	}
}

func CalculateEMA(data []float64, period int) float64 {
	return calculateEMA(data, period)
}

func calculateEMA(data []float64, period int) float64 {
	if len(data) < period {
		return 0
	}

	multiplier := 2.0 / float64(period+1)
	ema := data[0]

	for i := 1; i < len(data); i++ {
		ema = (data[i]-ema)*multiplier + ema
	}

	return ema
}

func CalculateATR(candles []Candle, period int) float64 {
	if len(candles) < period+1 {
		return 0
	}

	trs := make([]float64, len(candles))
	prevClose := candles[0].Close
	for i, c := range candles {
		highLow := c.High - c.Low
		highClose := math.Abs(c.High - prevClose)
		lowClose := math.Abs(c.Low - prevClose)
		tr := math.Max(highLow, math.Max(highClose, lowClose))
		trs[i] = tr
		prevClose = c.Close
	}

	return CalculateEMA(trs, period)
}

func CalculateBollingerBands(closes []float64, period int, stdDev float64) BollingerBandsResult {
	if len(closes) < period {
		return BollingerBandsResult{Upper: 0, Middle: 0, Lower: 0, Signal: "neutral"}
	}

	recentCloses := closes[len(closes)-period:]
	sum := 0.0
	for _, c := range recentCloses {
		sum += c
	}
	middle := sum / float64(period)

	variance := 0.0
	for _, c := range recentCloses {
		diff := c - middle
		variance += diff * diff
	}
	std := math.Sqrt(variance / float64(period))

	upper := middle + (stdDev * std)
	lower := middle - (stdDev * std)

	currentPrice := closes[len(closes)-1]
	signal := "neutral"
	if currentPrice < lower {
		signal = "oversold"
	} else if currentPrice > upper {
		signal = "overbought"
	}

	return BollingerBandsResult{
		Upper:  upper,
		Middle: middle,
		Lower:  lower,
		Signal: signal,
	}
}

func CalculateVolumeMA(volumes []float64, period int) VolumeMAResult {
	if len(volumes) < period+1 {
		return VolumeMAResult{VolumeMA: 0, Ratio: 1, Signal: "neutral"}
	}

	recentVolumes := volumes[len(volumes)-period-1 : len(volumes)-1]
	sum := 0.0
	for _, v := range recentVolumes {
		sum += v
	}
	volumeMA := sum / float64(period)

	currentVolume := volumes[len(volumes)-1]
	ratio := currentVolume / volumeMA

	signal := "neutral"
	if ratio > 1.5 {
		signal = "high"
	} else if ratio <= 0.5 {
		signal = "low"
	}

	return VolumeMAResult{
		VolumeMA: volumeMA,
		Ratio:    ratio,
		Signal:   signal,
	}
}

func CalculateMomentum(closes []float64, period int) MomentumResult {
	if len(closes) < period+1 {
		return MomentumResult{Momentum: 0, Signal: "neutral"}
	}

	currentClose := closes[len(closes)-1]
	pastClose := closes[len(closes)-period-1]

	momentum := ((currentClose - pastClose) / pastClose) * 100

	signal := "neutral"
	if momentum > 2 {
		signal = "bullish"
	} else if momentum < -2 {
		signal = "bearish"
	}

	return MomentumResult{Momentum: momentum, Signal: signal}
}

func CalculateBBPercentB(bb BollingerBandsResult, price float64) float64 {
	denom := bb.Upper - bb.Lower
	if denom == 0 {
		return 0.5
	}
	return (price - bb.Lower) / denom
}

func CalculateVolumeRatio(volumes []float64, ma float64) float64 {
	if len(volumes) == 0 {
		return 1
	}
	if ma <= 0 {
		return 1
	}
	return volumes[len(volumes)-1] / ma
}

func CalculateFeatureVector(candles []Candle, config IndicatorConfig) FeatureVector {
	if config.RSIPeriod <= 0 || config.MACDFastPeriod <= 0 || config.MACDSlowPeriod <= 0 || config.MACDSignalPeriod <= 0 || config.BBPeriod <= 0 || config.VolumeMAPeriod <= 0 || config.MomentumPeriod <= 0 {
		return FeatureVector{Valid: false}
	}

	minBars := config.RSIPeriod + 1
	if config.MACDSlowPeriod+config.MACDSignalPeriod > minBars {
		minBars = config.MACDSlowPeriod + config.MACDSignalPeriod
	}
	if int(config.BBPeriod) > minBars {
		minBars = int(config.BBPeriod)
	}
	if config.VolumeMAPeriod+1 > minBars {
		minBars = config.VolumeMAPeriod + 1
	}
	if config.MomentumPeriod+1 > minBars {
		minBars = config.MomentumPeriod + 1
	}
	if 15 > minBars {
		minBars = 15
	}

	if len(candles) < minBars {
		return FeatureVector{Valid: false}
	}

	closes := make([]float64, len(candles))
	volumes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
		volumes[i] = c.Volume
	}

	rsi := CalculateRSI(closes, config.RSIPeriod)
	macd := CalculateMACD(closes, config.MACDFastPeriod, config.MACDSlowPeriod, config.MACDSignalPeriod)
	bb := CalculateBollingerBands(closes, int(config.BBPeriod), config.BBStd)
	mom := CalculateMomentum(closes, config.MomentumPeriod)
	vol := CalculateVolumeMA(volumes, config.VolumeMAPeriod)

	currentPrice := closes[len(closes)-1]
	bbPercentB := CalculateBBPercentB(bb, currentPrice)
	volumeRatio := CalculateVolumeRatio(volumes, vol.VolumeMA)
	volatilityRatio := 0.0
	atr := CalculateATR(candles, 14)
	if currentPrice > 0 && atr > 0 {
		volatilityRatio = atr / currentPrice
	}

	values := []float64{rsi.RSI, macd.Histogram, bbPercentB, mom.Momentum, volumeRatio, volatilityRatio}
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return FeatureVector{Valid: false}
		}
	}

	return FeatureVector{
		RSI:             rsi.RSI,
		MACDHistogram:   macd.Histogram,
		BBPercentB:      bbPercentB,
		MomentumPercent: mom.Momentum,
		VolumeRatio:     volumeRatio,
		VolatilityRatio: volatilityRatio,
		Valid:           true,
	}
}

func CalculateAllIndicators(candles []Candle, config IndicatorConfig) map[string]interface{} {
	closes := make([]float64, len(candles))
	volumes := make([]float64, len(candles))
	for i, c := range candles {
		closes[i] = c.Close
		volumes[i] = c.Volume
	}

	results := make(map[string]interface{})

	rsiResult := CalculateRSI(closes, config.RSIPeriod)
	results["rsi"] = rsiResult

	macdResult := CalculateMACD(closes, config.MACDFastPeriod, config.MACDSlowPeriod, config.MACDSignalPeriod)
	results["macd"] = macdResult

	bbResult := CalculateBollingerBands(closes, int(config.BBPeriod), config.BBStd)
	results["bollinger"] = bbResult

	volumeResult := CalculateVolumeMA(volumes, config.VolumeMAPeriod)
	results["volume"] = volumeResult

	momentumResult := CalculateMomentum(closes, config.MomentumPeriod)
	results["momentum"] = momentumResult

	return results
}
