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
	if len(volumes) < period {
		return VolumeMAResult{VolumeMA: 0, Ratio: 1, Signal: "neutral"}
	}

	recentVolumes := volumes[len(volumes)-period:]
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
	} else if ratio < 0.5 {
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
