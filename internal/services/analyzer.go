package services

import (
	"strconv"
	"trading-go/internal/database"
)

type AnalysisResult struct {
	Symbol       string                 `json:"symbol"`
	CurrentPrice float64                `json:"current_price"`
	Rating       float64                `json:"rating"`
	Signal       string                 `json:"signal"`
	Indicators   map[string]interface{} `json:"indicators"`
	Weights      map[string]float64     `json:"weights"`
	Timestamp    string                 `json:"timestamp"`
}

func GetIndicatorWeights() map[string]float64 {
	weights := map[string]float64{
		"rsi":       1.0,
		"macd":      1.0,
		"bollinger": 1.0,
		"volume":    0.5,
		"momentum":  1.0,
	}

	var dbWeights []database.IndicatorWeight
	database.DB.Find(&dbWeights)

	for _, w := range dbWeights {
		weights[w.Indicator] = w.Weight
	}

	return weights
}

func GetIndicatorSettings() IndicatorConfig {
	config := DefaultIndicatorConfig()

	var settings []database.Setting
	database.DB.Where("category = ?", "indicators").Find(&settings)

	for _, s := range settings {
		switch s.Key {
		case "rsi_period":
			if v, err := strconv.Atoi(s.Value); err == nil {
				config.RSIPeriod = v
			}
		case "rsi_oversold":
			if v, err := strconv.ParseFloat(s.Value, 64); err == nil {
				config.RSIOversold = v
			}
		case "rsi_overbought":
			if v, err := strconv.ParseFloat(s.Value, 64); err == nil {
				config.RSIOverbought = v
			}
		case "macd_fast_period":
			if v, err := strconv.Atoi(s.Value); err == nil {
				config.MACDFastPeriod = v
			}
		case "macd_slow_period":
			if v, err := strconv.Atoi(s.Value); err == nil {
				config.MACDSlowPeriod = v
			}
		case "macd_signal_period":
			if v, err := strconv.Atoi(s.Value); err == nil {
				config.MACDSignalPeriod = v
			}
		case "bb_period":
			if v, err := strconv.ParseFloat(s.Value, 64); err == nil {
				config.BBPeriod = v
			}
		case "bb_std":
			if v, err := strconv.ParseFloat(s.Value, 64); err == nil {
				config.BBStd = v
			}
		case "volume_ma_period":
			if v, err := strconv.Atoi(s.Value); err == nil {
				config.VolumeMAPeriod = v
			}
		case "momentum_period":
			if v, err := strconv.Atoi(s.Value); err == nil {
				config.MomentumPeriod = v
			}
		}
	}

	return config
}

func calculateIndicatorScore(indicator string, signal string) float64 {
	switch indicator {
	case "rsi":
		if signal == "oversold" {
			return 1.0
		} else if signal == "overbought" {
			return -1.0
		}
		return 0.0
	case "macd":
		if signal == "bullish" {
			return 1.0
		} else if signal == "bearish" {
			return -1.0
		}
		return 0.0
	case "bollinger":
		if signal == "oversold" {
			return 1.0
		} else if signal == "overbought" {
			return -1.0
		}
		return 0.0
	case "volume":
		if signal == "high" {
			return 0.5
		} else if signal == "low" {
			return -0.5
		}
		return 0.0
	case "momentum":
		if signal == "bullish" {
			return 1.0
		} else if signal == "bearish" {
			return -1.0
		}
		return 0.0
	default:
		return 0.0
	}
}

func DetermineSignal(score float64) string {
	if score >= 0.5 {
		return "BUY"
	} else if score <= -0.5 {
		return "SELL"
	}
	return "HOLD"
}

func AnalyzeSymbol(candles []Candle, symbol string, currentPrice float64) AnalysisResult {
	config := GetIndicatorSettings()
	weights := GetIndicatorWeights()

	indicators := CalculateAllIndicators(candles, config)

	var weightedScore float64
	var totalWeight float64

	for indicator, weight := range weights {
		var signal string
		switch indicator {
		case "rsi":
			if rsi, ok := indicators["rsi"].(RSIResult); ok {
				signal = rsi.Signal
			}
		case "macd":
			if macd, ok := indicators["macd"].(MACDResult); ok {
				signal = macd.Signal
			}
		case "bollinger":
			if bb, ok := indicators["bollinger"].(BollingerBandsResult); ok {
				signal = bb.Signal
			}
		case "volume":
			if vol, ok := indicators["volume"].(VolumeMAResult); ok {
				signal = vol.Signal
			}
		case "momentum":
			if mom, ok := indicators["momentum"].(MomentumResult); ok {
				signal = mom.Signal
			}
		}

		score := calculateIndicatorScore(indicator, signal)
		weightedScore += score * weight
		totalWeight += weight
	}

	normalizedScore := 0.0
	if totalWeight > 0 {
		normalizedScore = weightedScore / totalWeight
	}

	rating := (normalizedScore + 1) / 2 * 10
	signal := DetermineSignal(normalizedScore)

	return AnalysisResult{
		Symbol:       symbol,
		CurrentPrice: currentPrice,
		Rating:       rating,
		Signal:       signal,
		Indicators:   indicators,
		Weights:      weights,
		Timestamp:    "",
	}
}
