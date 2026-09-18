package validation

import (
	"math"
	"sort"
)

// DeriveFoldMetrics is the sole authority path from immutable trade/curve
// primitives to fold summaries. Returns and PnL are capital weighted; exposure
// is observation weighted over the declared chronological curve.
func DeriveFoldMetrics(p FoldPrimitives) (FoldMetrics, error) {
	if !finite(p.StartingCapital) || p.StartingCapital <= 0 {
		return FoldMetrics{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "starting_capital", Details: "finite positive capital is required"}
	}
	if p.ExpectedObservations <= 0 || p.ObservedObservations < 0 || p.ObservedObservations > p.ExpectedObservations {
		return FoldMetrics{}, &DiagnosticError{Code: DiagnosticIncompleteCoverage, Details: "observation coverage cannot be reconciled"}
	}
	if len(p.Curve) < 2 {
		return FoldMetrics{}, &DiagnosticError{Code: DiagnosticMissingBenchmark, Details: "aligned equity and benchmark curve is required"}
	}
	if math.Abs(p.Curve[0].Equity-p.StartingCapital) > tolerance(p.StartingCapital) {
		return FoldMetrics{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "curve", Details: "curve does not start at declared capital"}
	}
	peak, drawdown, grossExposure, netExposure := p.Curve[0].Equity, 0.0, 0.0, 0.0
	periodReturns := make([]float64, 0, len(p.Curve)-1)
	for i, point := range p.Curve {
		if point.At.IsZero() || !finite(point.Equity) || !finite(point.Benchmark) || !finite(point.GrossExposure) || !finite(point.NetExposure) || point.Equity <= 0 || point.Benchmark <= 0 {
			return FoldMetrics{}, &DiagnosticError{Code: DiagnosticNonFinite, Field: "curve"}
		}
		if i > 0 && !point.At.After(p.Curve[i-1].At) {
			return FoldMetrics{}, &DiagnosticError{Code: DiagnosticInvalidWindowOrder, Field: "curve"}
		}
		if i > 0 {
			periodReturns = append(periodReturns, point.Equity/p.Curve[i-1].Equity-1)
		}
		if point.Equity > peak {
			peak = point.Equity
		}
		if current := (peak - point.Equity) / peak; current > drawdown {
			drawdown = current
		}
		grossExposure += point.GrossExposure
		netExposure += point.NetExposure
	}
	tradeIDs := make(map[string]struct{}, len(p.Trades))
	regimes, regimeContrib, tradeContrib, symbolContrib := map[string]int{}, map[string]float64{}, map[string]float64{}, map[string]float64{}
	netPnL, turnover, maxParticipation := 0.0, 0.0, 0.0
	for _, trade := range p.Trades {
		if trade.ID == "" || trade.Symbol == "" || trade.Regime == "" || trade.OpenedAt.IsZero() || !trade.ClosedAt.After(trade.OpenedAt) {
			return FoldMetrics{}, &DiagnosticError{Code: DiagnosticInvalidManifest, Field: "trade", Details: "complete unique chronological trade is required"}
		}
		if _, duplicate := tradeIDs[trade.ID]; duplicate {
			return FoldMetrics{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "trade.id", Details: "duplicate trade: " + trade.ID}
		}
		tradeIDs[trade.ID] = struct{}{}
		if !finite(trade.Notional) || trade.Notional < 0 || !finite(trade.AvailableLiquidity) || trade.AvailableLiquidity <= 0 || !finite(trade.GrossPnL) || !finite(trade.Cost) || trade.Cost < 0 || !finite(trade.NetPnL) || math.Abs((trade.GrossPnL-trade.Cost)-trade.NetPnL) > tolerance(p.StartingCapital) {
			return FoldMetrics{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "trade.pnl", Details: "gross-cost must equal net PnL"}
		}
		contribution := trade.NetPnL / p.StartingCapital
		netPnL += trade.NetPnL
		turnover += trade.Notional / p.StartingCapital
		if participation := trade.Notional / trade.AvailableLiquidity; participation > maxParticipation {
			maxParticipation = participation
		}
		regimes[trade.Regime]++
		regimeContrib[trade.Regime] += contribution
		tradeContrib[trade.ID] = contribution
		symbolContrib[trade.Symbol] += contribution
	}
	if !finite(p.BaselineGrossExposure) || !finite(p.BaselineTurnover) || p.BaselineTurnover < 0 || math.Abs(p.BaselineGrossExposure-grossExposure/float64(len(p.Curve))) > tolerance(1) {
		return FoldMetrics{}, &DiagnosticError{Code: DiagnosticBaselineMismatch, Details: "baseline must be exposure and turnover matched"}
	}
	final := p.Curve[len(p.Curve)-1]
	if math.Abs((final.Equity-p.StartingCapital)-netPnL) > tolerance(p.StartingCapital) {
		return FoldMetrics{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "curve", Details: "final equity does not reconcile to trade PnL"}
	}
	startBenchmark := p.Curve[0].Benchmark
	benchmarkReturn := final.Benchmark/startBenchmark - 1
	afterCostReturn := netPnL / p.StartingCapital
	expectancy := 0.0
	if len(p.Trades) > 0 {
		expectancy = afterCostReturn / float64(len(p.Trades))
	}
	downside, tail := downsideAndExpectedShortfall(periodReturns)
	sharpe := periodSharpe(periodReturns)
	return FoldMetrics{
		Observations: p.ObservedObservations, Trades: len(p.Trades), BenchmarkPresent: true,
		CoverageComplete: p.ObservedObservations == p.ExpectedObservations, Regimes: regimes,
		RegimeContributions: regimeContrib, AfterCostExpectancy: expectancy, AfterCostReturn: afterCostReturn,
		BenchmarkRelativeReturn: afterCostReturn - benchmarkReturn, MaxDrawdown: drawdown, Turnover: turnover,
		GrossExposure: grossExposure / float64(len(p.Curve)), NetExposure: netExposure / float64(len(p.Curve)),
		Coverage: float64(p.ObservedObservations) / float64(p.ExpectedObservations), DownsideDeviation: downside, ExpectedShortfall95: tail, MaxLiquidityParticipation: maxParticipation, Sharpe: sharpe, TradeContributions: tradeContrib, SymbolContributions: symbolContrib,
	}, nil
}

func downsideAndExpectedShortfall(returns []float64) (float64, float64) {
	if len(returns) == 0 {
		return 0, 0
	}
	sumSquares := 0.0
	for _, r := range returns {
		if r < 0 {
			sumSquares += r * r
		}
	}
	downside := math.Sqrt(sumSquares / float64(len(returns)))
	ordered := append([]float64(nil), returns...)
	sort.Float64s(ordered)
	n := int(math.Ceil(.05 * float64(len(ordered))))
	if n < 1 {
		n = 1
	}
	sum := 0.0
	for _, r := range ordered[:n] {
		sum += r
	}
	return downside, math.Max(0, -sum/float64(n))
}

// periodSharpe deliberately stays unannualized: a validation fold may contain
// a different time scale, while the independent chronological folds supply the
// uncertainty calculation in Evaluate.
func periodSharpe(returns []float64) float64 {
	if len(returns) < 2 {
		return 0
	}
	avg := 0.0
	for _, r := range returns {
		avg += r
	}
	avg /= float64(len(returns))
	variance := 0.0
	for _, r := range returns {
		variance += (r - avg) * (r - avg)
	}
	std := math.Sqrt(variance / float64(len(returns)-1))
	if std == 0 {
		return 0
	}
	return avg / std
}

func tolerance(scale float64) float64 { return math.Max(1e-10, math.Abs(scale)*1e-10) }
