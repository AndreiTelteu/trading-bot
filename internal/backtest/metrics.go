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

func buildStrategyDiagnostics(trades []Trade, rankingMetrics *RankingMetrics, config BacktestConfig, concurrentPositionCounts []int) StrategyDiagnostics {
	return StrategyDiagnostics{
		Ranking:       rankingDiagnostics(rankingMetrics),
		RegimeSlices:  buildRegimeSliceMetrics(trades),
		SymbolCohorts: buildSymbolCohortMetrics(trades),
		Exposure:      buildExposureDiagnostics(trades, config, concurrentPositionCounts),
	}
}

func rankingDiagnostics(metrics *RankingMetrics) *RankingDiagnostics {
	if metrics == nil {
		return nil
	}
	return metrics.Diagnostics
}

func buildRankingDiagnostics(byRank []RankBucketMetric) *RankingDiagnostics {
	if len(byRank) == 0 {
		return nil
	}
	monotonicWinRate := true
	monotonicAvgPnl := true
	for i := 1; i < len(byRank); i++ {
		if byRank[i-1].WinRate < byRank[i].WinRate {
			monotonicWinRate = false
		}
		if byRank[i-1].AvgPnl < byRank[i].AvgPnl {
			monotonicAvgPnl = false
		}
	}
	first := byRank[0]
	last := byRank[len(byRank)-1]
	return &RankingDiagnostics{
		BucketsEvaluated:  len(byRank),
		MonotonicWinRate:  monotonicWinRate,
		MonotonicAvgPnl:   monotonicAvgPnl,
		TopRankWinRate:    first.WinRate,
		BottomRankWinRate: last.WinRate,
		TopRankAvgPnl:     first.AvgPnl,
		BottomRankAvgPnl:  last.AvgPnl,
		PositiveSpread:    first.AvgPnl - last.AvgPnl,
	}
}

func buildRegimeSliceMetrics(trades []Trade) []RegimeSliceMetric {
	if len(trades) == 0 {
		return nil
	}
	type regimeTotals struct {
		Trades   int
		Wins     int
		TotalPnl float64
	}
	byRegime := map[string]regimeTotals{}
	for _, trade := range trades {
		regime := trade.RegimeState
		if regime == "" {
			regime = "unknown"
		}
		totals := byRegime[regime]
		totals.Trades++
		totals.TotalPnl += trade.Pnl
		if trade.Pnl > 0 {
			totals.Wins++
		}
		byRegime[regime] = totals
	}
	regimes := make([]string, 0, len(byRegime))
	for regime := range byRegime {
		regimes = append(regimes, regime)
	}
	sort.Strings(regimes)
	results := make([]RegimeSliceMetric, 0, len(regimes))
	for _, regime := range regimes {
		totals := byRegime[regime]
		results = append(results, RegimeSliceMetric{
			Regime:   regime,
			Trades:   totals.Trades,
			WinRate:  float64(totals.Wins) / float64(totals.Trades),
			AvgPnl:   totals.TotalPnl / float64(totals.Trades),
			TotalPnl: totals.TotalPnl,
		})
	}
	return results
}

func buildSymbolCohortMetrics(trades []Trade) []SymbolCohortMetric {
	if len(trades) == 0 {
		return nil
	}
	type symbolTotals struct {
		Trades   int
		Wins     int
		TotalPnl float64
	}
	bySymbol := map[string]symbolTotals{}
	for _, trade := range trades {
		totals := bySymbol[trade.Symbol]
		totals.Trades++
		totals.TotalPnl += trade.Pnl
		if trade.Pnl > 0 {
			totals.Wins++
		}
		bySymbol[trade.Symbol] = totals
	}
	symbols := make([]string, 0, len(bySymbol))
	for symbol := range bySymbol {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	results := make([]SymbolCohortMetric, 0, len(symbols))
	for _, symbol := range symbols {
		totals := bySymbol[symbol]
		results = append(results, SymbolCohortMetric{
			Symbol:   symbol,
			Trades:   totals.Trades,
			WinRate:  float64(totals.Wins) / float64(totals.Trades),
			AvgPnl:   totals.TotalPnl / float64(totals.Trades),
			TotalPnl: totals.TotalPnl,
		})
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].TotalPnl == results[j].TotalPnl {
			return results[i].Symbol < results[j].Symbol
		}
		return results[i].TotalPnl > results[j].TotalPnl
	})
	return results
}

func buildExposureDiagnostics(trades []Trade, config BacktestConfig, concurrentPositionCounts []int) ExposureDiagnostics {
	avgConcurrent := 0.0
	maxConcurrent := 0
	if len(concurrentPositionCounts) > 0 {
		values := make([]float64, 0, len(concurrentPositionCounts))
		for _, count := range concurrentPositionCounts {
			values = append(values, float64(count))
			if count > maxConcurrent {
				maxConcurrent = count
			}
		}
		avgConcurrent = meanValue(values)
	}

	avgHoldBars := 0.0
	avgHoldHours := 0.0
	turnoverPer30d := 0.0
	if len(trades) > 0 {
		holdBars := make([]float64, 0, len(trades))
		for _, trade := range trades {
			holdBars = append(holdBars, float64(trade.HoldBars))
		}
		avgHoldBars = meanValue(holdBars)
		avgHoldHours = avgHoldBars * float64(maxInt(1, config.TimeframeMinutes)) / 60.0

		days := config.End.Sub(config.Start).Hours() / 24
		if days > 0 {
			turnoverPer30d = (float64(len(trades)) / days) * 30
		}
	}

	return ExposureDiagnostics{
		AvgConcurrentPositions: avgConcurrent,
		MaxConcurrentPositions: maxConcurrent,
		TurnoverPer30d:         turnoverPer30d,
		AvgHoldBars:            avgHoldBars,
		AvgHoldHours:           avgHoldHours,
	}
}
