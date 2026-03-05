package services

import (
	"math"
	"testing"
)

func TestCalculateRSI(t *testing.T) {
	tests := []struct {
		name       string
		closes     []float64
		period     int
		wantRSI    float64
		wantSignal string
	}{
		{
			name:       "insufficient data",
			closes:     []float64{100, 101},
			period:     14,
			wantRSI:    50,
			wantSignal: "neutral",
		},
		{
			name:       "overbought RSI",
			closes:     []float64{100, 101, 102, 103, 104, 105, 106, 107, 108, 109, 110, 111, 112, 113, 114},
			period:     14,
			wantRSI:    100,
			wantSignal: "overbought",
		},
		{
			name:       "oversold RSI",
			closes:     []float64{114, 113, 112, 111, 110, 109, 108, 107, 106, 105, 104, 103, 102, 101, 100},
			period:     14,
			wantRSI:    0,
			wantSignal: "oversold",
		},
		{
			name:       "neutral RSI",
			closes:     []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100, 100},
			period:     14,
			wantRSI:    50,
			wantSignal: "neutral",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateRSI(tt.closes, tt.period)
			if !math.IsNaN(tt.wantRSI) && math.Abs(result.RSI-tt.wantRSI) > 0.01 {
				t.Errorf("CalculateRSI() RSI = %v, want %v", result.RSI, tt.wantRSI)
			}
			if result.Signal != tt.wantSignal {
				t.Errorf("CalculateRSI() Signal = %v, want %v", result.Signal, tt.wantSignal)
			}
		})
	}
}

func TestCalculateMACD(t *testing.T) {
	tests := []struct {
		name         string
		closes       []float64
		fastPeriod   int
		slowPeriod   int
		signalPeriod int
		wantSignal   string
	}{
		{
			name:         "insufficient data",
			closes:       []float64{100, 101, 102},
			fastPeriod:   12,
			slowPeriod:   26,
			signalPeriod: 9,
			wantSignal:   "neutral",
		},
		{
			name:         "bullish MACD",
			closes:       generateIncreasingPrices(50),
			fastPeriod:   12,
			slowPeriod:   26,
			signalPeriod: 9,
			wantSignal:   "bullish",
		},
		{
			name:         "bearish MACD",
			closes:       generateDecreasingPrices(50),
			fastPeriod:   12,
			slowPeriod:   26,
			signalPeriod: 9,
			wantSignal:   "bearish",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateMACD(tt.closes, tt.fastPeriod, tt.slowPeriod, tt.signalPeriod)
			if result.Signal != tt.wantSignal {
				t.Errorf("CalculateMACD() Signal = %v, want %v", result.Signal, tt.wantSignal)
			}
		})
	}
}

func TestCalculateBollingerBands(t *testing.T) {
	tests := []struct {
		name       string
		closes     []float64
		period     int
		stdDev     float64
		wantSignal string
	}{
		{
			name:       "insufficient data",
			closes:     []float64{100, 101},
			period:     20,
			stdDev:     2.0,
			wantSignal: "neutral",
		},
		{
			name:       "oversold - price below lower band",
			closes:     append(generateFlatPrices(20), 90.0),
			period:     20,
			stdDev:     2.0,
			wantSignal: "oversold",
		},
		{
			name:       "overbought - price above upper band",
			closes:     append(generateFlatPrices(20), 110.0),
			period:     20,
			stdDev:     2.0,
			wantSignal: "overbought",
		},
		{
			name:       "neutral - price within bands",
			closes:     generateFlatPrices(21),
			period:     20,
			stdDev:     2.0,
			wantSignal: "neutral",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateBollingerBands(tt.closes, tt.period, tt.stdDev)
			if result.Signal != tt.wantSignal {
				t.Errorf("CalculateBollingerBands() Signal = %v, want %v", result.Signal, tt.wantSignal)
			}
		})
	}
}

func TestCalculateVolumeMA(t *testing.T) {
	tests := []struct {
		name       string
		volumes    []float64
		period     int
		wantSignal string
	}{
		{
			name:       "insufficient data",
			volumes:    []float64{100, 101},
			period:     20,
			wantSignal: "neutral",
		},
		{
			name:       "high volume",
			volumes:    append(generateFlatVolumes(20), 100.0),
			period:     20,
			wantSignal: "high",
		},
		{
			name:       "low volume",
			volumes:    append(generateFlatVolumes(20), 5.0),
			period:     20,
			wantSignal: "low",
		},
		{
			name:       "neutral volume",
			volumes:    generateFlatVolumes(21),
			period:     20,
			wantSignal: "neutral",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateVolumeMA(tt.volumes, tt.period)
			if result.Signal != tt.wantSignal {
				t.Errorf("CalculateVolumeMA() Signal = %v, want %v", result.Signal, tt.wantSignal)
			}
		})
	}
}

func TestCalculateMomentum(t *testing.T) {
	tests := []struct {
		name       string
		closes     []float64
		period     int
		wantSignal string
	}{
		{
			name:       "insufficient data",
			closes:     []float64{100, 101},
			period:     10,
			wantSignal: "neutral",
		},
		{
			name:       "bullish momentum",
			closes:     append(generateFlatPrices(10), 105.0),
			period:     10,
			wantSignal: "bullish",
		},
		{
			name:       "bearish momentum",
			closes:     append(generateFlatPrices(10), 95.0),
			period:     10,
			wantSignal: "bearish",
		},
		{
			name:       "neutral momentum",
			closes:     generateFlatPrices(12),
			period:     10,
			wantSignal: "neutral",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateMomentum(tt.closes, tt.period)
			if result.Signal != tt.wantSignal {
				t.Errorf("CalculateMomentum() Signal = %v, want %v", result.Signal, tt.wantSignal)
			}
		})
	}
}

func TestCalculateBBPercentB(t *testing.T) {
	bb := BollingerBandsResult{
		Upper:  110,
		Middle: 100,
		Lower:  90,
	}
	if got := CalculateBBPercentB(bb, 100); math.Abs(got-0.5) > 0.0001 {
		t.Errorf("CalculateBBPercentB() = %v, want 0.5", got)
	}
	if got := CalculateBBPercentB(BollingerBandsResult{Upper: 100, Lower: 100}, 100); math.Abs(got-0.5) > 0.0001 {
		t.Errorf("CalculateBBPercentB() denom zero = %v, want 0.5", got)
	}
}

func TestCalculateAnnualizedATR(t *testing.T) {
	var candles []Candle
	for i := 0; i < 30; i++ {
		close := 100 + float64(i)
		candles = append(candles, Candle{
			Close:  close,
			High:   close + 2,
			Low:    close - 2,
			Volume: 1000,
		})
	}
	atr := CalculateATR(candles, 14)
	if atr <= 0 {
		t.Fatalf("CalculateATR() expected > 0, got %v", atr)
	}

	annualized := CalculateAnnualizedATR(candles, 14, 60, 365)
	barsPerYear := float64(365*24) / 1
	expected := atr * math.Sqrt(barsPerYear)
	if math.Abs(annualized-expected) > 0.0001 {
		t.Errorf("CalculateAnnualizedATR() = %v, want %v", annualized, expected)
	}

	unscaled := CalculateAnnualizedATR(candles, 14, 0, 365)
	if math.Abs(unscaled-atr) > 0.0001 {
		t.Errorf("CalculateAnnualizedATR() with disabled scaling = %v, want %v", unscaled, atr)
	}
}

func TestCalculateVolumeRatio(t *testing.T) {
	if got := CalculateVolumeRatio([]float64{10, 20}, 10); math.Abs(got-2.0) > 0.0001 {
		t.Errorf("CalculateVolumeRatio() = %v, want 2.0", got)
	}
	if got := CalculateVolumeRatio([]float64{}, 10); math.Abs(got-1.0) > 0.0001 {
		t.Errorf("CalculateVolumeRatio() empty = %v, want 1.0", got)
	}
	if got := CalculateVolumeRatio([]float64{10}, 0); math.Abs(got-1.0) > 0.0001 {
		t.Errorf("CalculateVolumeRatio() ma<=0 = %v, want 1.0", got)
	}
}

func TestCalculateFeatureVector(t *testing.T) {
	config := DefaultIndicatorConfig()
	var candles []Candle
	for i := 0; i < 60; i++ {
		candles = append(candles, Candle{
			Close:  100 + float64(i),
			High:   101 + float64(i),
			Low:    99 + float64(i),
			Volume: 1000 + float64(i),
		})
	}
	features := CalculateFeatureVector(candles, config)
	if !features.Valid {
		t.Error("CalculateFeatureVector() expected Valid true")
	}
	values := []float64{features.RSI, features.MACDHistogram, features.BBPercentB, features.MomentumPercent, features.VolumeRatio, features.VolatilityRatio}
	for _, v := range values {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Errorf("CalculateFeatureVector() invalid value %v", v)
		}
	}

	features = CalculateFeatureVector(candles[:5], config)
	if features.Valid {
		t.Error("CalculateFeatureVector() expected Valid false for insufficient data")
	}
}

func TestComputeProbGate(t *testing.T) {
	settings := map[string]string{
		"prob_model_beta0": "0",
		"prob_model_beta1": "0.01",
		"prob_model_beta2": "0.5",
		"prob_model_beta3": "0.1",
		"prob_model_beta4": "0.01",
		"prob_model_beta5": "0.2",
		"prob_model_beta6": "0.3",
		"prob_p_min":       "0.1",
		"prob_ev_min":      "-1",
		"prob_avg_gain":    "0.02",
		"prob_avg_loss":    "0.01",
	}
	features := FeatureVector{
		RSI:             50,
		MACDHistogram:   0.5,
		BBPercentB:      0.6,
		MomentumPercent: 1.2,
		VolumeRatio:     1.1,
		VolatilityRatio: 0.01,
		Valid:           true,
	}
	pUp, ev, ok := computeProbGate(features, settings)
	if !ok {
		t.Error("computeProbGate() expected ok true")
	}
	if pUp <= 0 || pUp >= 1 {
		t.Errorf("computeProbGate() pUp = %v, want between 0 and 1", pUp)
	}
	if math.IsNaN(ev) || math.IsInf(ev, 0) {
		t.Errorf("computeProbGate() ev invalid: %v", ev)
	}

	_, _, ok = computeProbGate(FeatureVector{Valid: false}, settings)
	if ok {
		t.Error("computeProbGate() expected ok false for invalid features")
	}
}

func TestDefaultIndicatorConfig(t *testing.T) {
	config := DefaultIndicatorConfig()

	if config.RSIPeriod != 14 {
		t.Errorf("DefaultIndicatorConfig() RSIPeriod = %v, want 14", config.RSIPeriod)
	}
	if config.RSIOversold != 30.0 {
		t.Errorf("DefaultIndicatorConfig() RSIOversold = %v, want 30.0", config.RSIOversold)
	}
	if config.RSIOverbought != 70.0 {
		t.Errorf("DefaultIndicatorConfig() RSIOverbought = %v, want 70.0", config.RSIOverbought)
	}
	if config.MACDFastPeriod != 12 {
		t.Errorf("DefaultIndicatorConfig() MACDFastPeriod = %v, want 12", config.MACDFastPeriod)
	}
	if config.MACDSlowPeriod != 26 {
		t.Errorf("DefaultIndicatorConfig() MACDSlowPeriod = %v, want 26", config.MACDSlowPeriod)
	}
	if config.BBPeriod != 20 {
		t.Errorf("DefaultIndicatorConfig() BBPeriod = %v, want 20", config.BBPeriod)
	}
	if config.BBStd != 2.0 {
		t.Errorf("DefaultIndicatorConfig() BBStd = %v, want 2.0", config.BBStd)
	}
}

func TestCalculateAllIndicators(t *testing.T) {
	candles := []Candle{}
	for i := 0; i < 50; i++ {
		candles = append(candles, Candle{
			Close:  100 + float64(i),
			High:   105 + float64(i),
			Low:    95 + float64(i),
			Volume: 1000,
		})
	}

	config := DefaultIndicatorConfig()
	results := CalculateAllIndicators(candles, config)

	if _, ok := results["rsi"]; !ok {
		t.Error("CalculateAllIndicators() missing rsi result")
	}
	if _, ok := results["macd"]; !ok {
		t.Error("CalculateAllIndicators() missing macd result")
	}
	if _, ok := results["bollinger"]; !ok {
		t.Error("CalculateAllIndicators() missing bollinger result")
	}
	if _, ok := results["volume"]; !ok {
		t.Error("CalculateAllIndicators() missing volume result")
	}
	if _, ok := results["momentum"]; !ok {
		t.Error("CalculateAllIndicators() missing momentum result")
	}
}

func generateIncreasingPrices(n int) []float64 {
	prices := make([]float64, n)
	for i := range prices {
		prices[i] = 100 + float64(i)
	}
	return prices
}

func generateDecreasingPrices(n int) []float64 {
	prices := make([]float64, n)
	for i := range prices {
		prices[i] = 100 - float64(i)
	}
	return prices
}

func generateFlatPrices(n int) []float64 {
	prices := make([]float64, n)
	for i := range prices {
		prices[i] = 100
	}
	return prices
}

func generateFlatVolumes(n int) []float64 {
	volumes := make([]float64, n)
	for i := range volumes {
		volumes[i] = 10
	}
	return volumes
}
