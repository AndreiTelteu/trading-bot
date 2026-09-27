package validation

import (
	"math"
	"math/big"
	"sort"
	"time"
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
		regimes[trade.Regime]++
		regimeContrib[trade.Regime] += contribution
		tradeContrib[trade.ID] = contribution
		symbolContrib[trade.Symbol] += contribution
	}
	fillIDs := make(map[string]struct{}, len(p.Fills))
	fillOrderIDs := make(map[string]struct{}, len(p.Fills))
	for _, fill := range p.Fills {
		if fill.ID == "" || fill.Symbol == "" || (fill.Side != "buy" && fill.Side != "sell") || fill.At.IsZero() || !finite(fill.Notional) || fill.Notional <= 0 || !finite(fill.AvailableLiquidity) || fill.AvailableLiquidity <= 0 {
			return FoldMetrics{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "fill", Details: "complete economic fill is required"}
		}
		if _, duplicate := fillIDs[fill.ID]; duplicate {
			return FoldMetrics{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "fill.id", Details: "duplicate fill: " + fill.ID}
		}
		fillIDs[fill.ID] = struct{}{}
		if fill.OrderID != "" {
			fillOrderIDs[fill.OrderID] = struct{}{}
		}
		turnover += fill.Notional / p.StartingCapital
		if participation := fill.Notional / fill.AvailableLiquidity; participation > maxParticipation {
			maxParticipation = participation
		}
	}
	if err := validateNoFillSet(p.NoFills, fillOrderIDs, p.Curve[0].At, p.Curve[len(p.Curve)-1].At); err != nil {
		return FoldMetrics{}, err
	}
	if err := validateNoFillSet(p.BaselineNoFills, nil, p.Curve[0].At, p.Curve[len(p.Curve)-1].At); err != nil {
		return FoldMetrics{}, err
	}
	if len(p.Trades) > 0 && len(p.Fills) == 0 {
		return FoldMetrics{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "fills", Details: "trades require economic fill evidence"}
	}
	closedPnL := netPnL
	openContrib := map[string]float64{}
	for _, position := range p.ResidualPositions {
		if position.Symbol == "" || position.MarkedAt.IsZero() || position.MarkedAt.After(p.Curve[len(p.Curve)-1].At) {
			return FoldMetrics{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "residual_position", Details: "final marked inventory must identify its symbol and valuation time"}
		}
		if _, duplicate := openContrib[position.Symbol]; duplicate {
			return FoldMetrics{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "residual_position.symbol", Details: "duplicate final position"}
		}
		if !finite(position.Quantity) || position.Quantity <= 0 || !finite(position.CostBasis) || position.CostBasis <= 0 || !finite(position.EntryFee) || position.EntryFee < 0 || !finite(position.MarkPrice) || position.MarkPrice <= 0 || !finite(position.MarkValue) || position.MarkValue <= 0 || !finite(position.UnrealizedPnL) || math.Abs(position.Quantity*position.MarkPrice-position.MarkValue) > tolerance(p.StartingCapital) || math.Abs(position.MarkValue-position.CostBasis-position.EntryFee-position.UnrealizedPnL) > tolerance(p.StartingCapital) {
			return FoldMetrics{}, &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "residual_position.pnl", Details: "marked value, basis, fees and unrealized PnL do not reconcile"}
		}
		contribution := position.UnrealizedPnL / p.StartingCapital
		openContrib[position.Symbol] = contribution
		symbolContrib[position.Symbol] += contribution
		netPnL += position.UnrealizedPnL
	}
	if !finite(p.BaselineGrossExposure) || !finite(p.BaselineTurnover) || p.BaselineTurnover < 0 || math.Abs(p.BaselineGrossExposure-grossExposure/float64(len(p.Curve))) > tolerance(1) || math.Abs(p.BaselineTurnover-turnover) > tolerance(math.Max(1, turnover)) {
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
		expectancy = closedPnL / p.StartingCapital / float64(len(p.Trades))
	}
	downside, tail := downsideAndExpectedShortfall(periodReturns)
	sharpe := periodSharpe(periodReturns)
	return FoldMetrics{
		Observations: p.ObservedObservations, Trades: len(p.Trades), BenchmarkPresent: true,
		NoFillCount: len(p.NoFills), BaselineNoFillCount: len(p.BaselineNoFills),
		CoverageComplete: p.ObservedObservations == p.ExpectedObservations, Regimes: regimes,
		RegimeContributions: regimeContrib, AfterCostExpectancy: expectancy, AfterCostReturn: afterCostReturn,
		BenchmarkRelativeReturn: afterCostReturn - benchmarkReturn, MaxDrawdown: drawdown, Turnover: turnover,
		GrossExposure: grossExposure / float64(len(p.Curve)), NetExposure: netExposure / float64(len(p.Curve)),
		Coverage: float64(p.ObservedObservations) / float64(p.ExpectedObservations), DownsideDeviation: downside, ExpectedShortfall95: tail, MaxLiquidityParticipation: maxParticipation, Sharpe: sharpe, TradeContributions: tradeContrib, OpenPositionContributions: openContrib, SymbolContributions: symbolContrib,
	}, nil
}

func validateNoFillSet(values []NoFillPrimitive, filledOrders map[string]struct{}, curveStart, curveEnd time.Time) error {
	seen := make(map[string]struct{}, len(values))
	previousSignal := time.Time{}
	for _, rejected := range values {
		if rejected.OrderID == "" || rejected.Symbol == "" || (rejected.Side != "buy" && rejected.Side != "sell") || rejected.SignalAt.IsZero() || !rejected.SelectedOpenAt.After(rejected.SignalAt) || !rejected.EvaluatedAt.After(rejected.SelectedOpenAt) || rejected.DatasetManifestID == "" {
			return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "no_fills", Details: "incomplete simulated no-fill evidence"}
		}
		if rejected.SignalAt.Before(curveStart) || rejected.EvaluatedAt.After(curveEnd) || (!previousSignal.IsZero() && rejected.SignalAt.Before(previousSignal)) || !rejected.SelectedOpenAt.Equal(rejected.SelectedOpenAt.UTC().Truncate(time.Minute)) || !rejected.EvaluatedAt.Equal(rejected.SelectedOpenAt.Add(time.Minute-time.Millisecond)) {
			return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "no_fills", Details: "no-fill chronology differs from the fold curve or selected minute"}
		}
		requested, reqOK := new(big.Rat).SetString(rejected.RequestedQuantity)
		approved, appOK := new(big.Rat).SetString(rejected.ApprovedQuantity)
		filled, fillOK := new(big.Rat).SetString(rejected.FilledQuantity)
		reference, refOK := new(big.Rat).SetString(rejected.ReferencePrice)
		open, openOK := new(big.Rat).SetString(rejected.SelectedOpenPrice)
		if !reqOK || !appOK || !fillOK || !refOK || !openOK || requested.Sign() <= 0 || approved.Sign() <= 0 || approved.Cmp(requested) > 0 || filled.Sign() != 0 || reference.Sign() <= 0 || open.Sign() <= 0 {
			return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "no_fills", Details: "no-fill quantity or price is invalid"}
		}
		switch rejected.ExecutionPolicyVersion {
		case "backtest-execution-v3":
			if rejected.SchemaVersion != "simulated-no-fill-v1" || rejected.Reason != "simulated_no_fill_zero_trades" || rejected.LiquidityEvidence != "zero_base_volume" || rejected.SelectedClosePrice != "" || rejected.BarVolume != "" || rejected.CapacityQuantity != "" {
				return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "no_fills", Details: "v3 no-fill policy or evidence differs"}
			}
		case "backtest-execution-v4":
			if rejected.SchemaVersion != "simulated-no-fill-v2" || rejected.Reason != "simulated_no_fill_volume_cap" || rejected.LiquidityEvidence != "selected_bar_base_volume_10pct" {
				return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "no_fills", Details: "v4 capacity no-fill policy differs"}
			}
			closePrice, closeOK := new(big.Rat).SetString(rejected.SelectedClosePrice)
			volume, volumeOK := new(big.Rat).SetString(rejected.BarVolume)
			capacity, capacityOK := new(big.Rat).SetString(rejected.CapacityQuantity)
			if !closeOK || !volumeOK || !capacityOK || closePrice.Sign() <= 0 || volume.Sign() < 0 || capacity.Sign() < 0 || capacity.Cmp(new(big.Rat).Quo(volume, big.NewRat(10, 1))) > 0 || approved.Cmp(capacity) <= 0 {
				return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "no_fills", Details: "v4 no-fill price, volume or capacity is invalid"}
			}
		default:
			return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "no_fills", Details: "unsupported no-fill execution policy"}
		}
		if _, duplicate := seen[rejected.OrderID]; duplicate {
			return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "no_fills", Details: "duplicate no-fill order"}
		}
		if _, filledOrder := filledOrders[rejected.OrderID]; filledOrder {
			return &DiagnosticError{Code: DiagnosticManifestIntegrity, Field: "no_fills", Details: "no-fill order also has an economic fill"}
		}
		seen[rejected.OrderID], previousSignal = struct{}{}, rejected.SignalAt
	}
	return nil
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
