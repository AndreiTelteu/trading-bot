package backtest

import (
	"math"
	"sort"
)

func ComputeMetrics(equity []EquityPoint, trades []Trade, timeframeMinutes int, annualizationDays int) Metrics {
	metrics := Metrics{}
	if len(equity) < 2 {
		return metrics
	}

	returns := make([]float64, 0, len(equity)-1)
	for i := 1; i < len(equity); i++ {
		prev := equity[i-1].Value
		curr := equity[i].Value
		if prev <= 0 {
			continue
		}
		returns = append(returns, (curr-prev)/prev)
	}

	mean, std := meanStd(returns)
	barsPerYear := barsPerYear(timeframeMinutes, annualizationDays)
	if std > 0 {
		metrics.Sharpe = (mean / std) * math.Sqrt(barsPerYear)
	}
	metrics.ReturnVolatility = std
	metrics.MaxDrawdown = maxDrawdown(equity)
	metrics.TradeCount = len(trades)

	var wins []float64
	var losses []float64
	var grossProfit float64
	var grossLoss float64
	for _, t := range trades {
		if t.Pnl >= 0 {
			wins = append(wins, t.Pnl)
			grossProfit += t.Pnl
		} else {
			losses = append(losses, t.Pnl)
			grossLoss += math.Abs(t.Pnl)
		}
	}

	if len(trades) > 0 {
		metrics.WinRate = float64(len(wins)) / float64(len(trades))
	}
	if len(wins) > 0 {
		metrics.AvgWin = meanValue(wins)
	}
	if len(losses) > 0 {
		metrics.AvgLoss = meanValue(losses)
	}
	if grossLoss > 0 {
		metrics.ProfitFactor = grossProfit / grossLoss
	}

	return metrics
}

func maxDrawdown(equity []EquityPoint) float64 {
	if len(equity) == 0 {
		return 0
	}
	peak := equity[0].Value
	maxDD := 0.0
	for _, point := range equity {
		if point.Value > peak {
			peak = point.Value
		}
		if peak > 0 {
			drawdown := (peak - point.Value) / peak
			if drawdown > maxDD {
				maxDD = drawdown
			}
		}
	}
	return maxDD
}

func meanStd(values []float64) (float64, float64) {
	if len(values) == 0 {
		return 0, 0
	}
	mean := meanValue(values)
	variance := 0.0
	for _, v := range values {
		diff := v - mean
		variance += diff * diff
	}
	variance /= float64(len(values))
	return mean, math.Sqrt(variance)
}

func meanValue(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func barsPerYear(timeframeMinutes int, annualizationDays int) float64 {
	if timeframeMinutes <= 0 {
		timeframeMinutes = 15
	}
	if annualizationDays <= 0 {
		annualizationDays = 365
	}
	return float64(annualizationDays*24*60) / float64(timeframeMinutes)
}

func percentile(values []float64, pct float64) float64 {
	if len(values) == 0 {
		return 0
	}
	copyValues := append([]float64{}, values...)
	sort.Float64s(copyValues)
	pos := pct * float64(len(copyValues)-1)
	lower := int(math.Floor(pos))
	upper := int(math.Ceil(pos))
	if lower == upper {
		return copyValues[lower]
	}
	weight := pos - float64(lower)
	return copyValues[lower]*(1-weight) + copyValues[upper]*weight
}
