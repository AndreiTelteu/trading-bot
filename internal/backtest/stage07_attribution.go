package backtest

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/validation"
)

// stage07EconomicPrimitives replays the immutable Stage 05 fill sequence using
// the same weighted-average entry and proportional entry-fee rules as the
// shared memory ledger. A sell consumes exactly one closed-trade record.
type stage07EndPosition struct {
	Symbol        string
	Quantity      float64
	EntryPrice    float64
	EntryFee      float64
	EntrySlippage float64
}

type stage07EndInventory struct {
	Cash      float64
	Positions []stage07EndPosition // sorted by symbol for deterministic fold evidence
}

func stage07EconomicPrimitives(result Stage05StrategyResult, fold int, capital float64, execution map[string][]services.OHLCV) ([]validation.TradePrimitive, []validation.FillPrimitive, stage07EndInventory, error) {
	type holding struct {
		quantity, entryPrice, entryFee, entrySlippage float64
		entryAt                                       time.Time
		entryLiquidity                                float64
		entryFillIDs                                  []string
	}
	positions := map[string]*holding{}
	seen := map[string]bool{}
	trades := make([]validation.TradePrimitive, 0, len(result.Trades))
	fills := make([]validation.FillPrimitive, 0, len(result.Artifacts.Fills))
	turnover, fees, slippage := 0.0, 0.0, 0.0
	cash := capital
	lastAt := time.Time{}
	for _, fill := range result.Artifacts.Fills {
		at, timeErr := time.Parse(time.RFC3339Nano, fill.FillAt)
		quantity, qtyErr := strconv.ParseFloat(fill.Quantity, 64)
		price, priceErr := strconv.ParseFloat(fill.Price, 64)
		fee, feeErr := strconv.ParseFloat(fill.Fee, 64)
		reference, refErr := strconv.ParseFloat(fill.ExecutionReferencePrice, 64)
		if fill.FillID == "" || seen[fill.FillID] || strings.TrimSpace(fill.Symbol) == "" || (fill.Side != "buy" && fill.Side != "sell") || timeErr != nil || (!lastAt.IsZero() && at.Before(lastAt)) || qtyErr != nil || priceErr != nil || feeErr != nil || refErr != nil || !stage07Positive(quantity) || !stage07Positive(price) || !stage07Positive(reference) || !stage07Nonnegative(fee) {
			return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Field: "fills", Details: "missing, duplicated, unordered, or invalid economic fill"}
		}
		seen[fill.FillID], lastAt = true, at
		bar, found := stage07BarAt(execution[fill.Symbol], at)
		if !found {
			return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticCapacity, Field: "fills", Details: fmt.Sprintf("execution bar absent for fill %s (%s at %s)", fill.FillID, fill.Symbol, at.UTC().Format(time.RFC3339Nano))}
		}
		if !stage07Positive(bar.Volume) {
			return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticCapacity, Field: "fills", Details: fmt.Sprintf("execution bar has nonpositive volume for fill %s (%s at %s)", fill.FillID, fill.Symbol, at.UTC().Format(time.RFC3339Nano))}
		}
		notional := quantity * price
		liquidity := bar.Volume * price
		if !stage07Positive(notional) || !stage07Positive(liquidity) {
			return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticNonFinite, Field: "fills"}
		}
		fillSlippage := math.Abs(price-reference) * quantity
		turnover += notional
		fees += fee
		slippage += fillSlippage
		fills = append(fills, validation.FillPrimitive{ID: fill.FillID, Symbol: fill.Symbol, Side: fill.Side, At: at.UTC(), Notional: notional, AvailableLiquidity: liquidity})
		if fill.Side == "buy" {
			cash -= notional + fee
			if !stage07Nonnegative(cash + 1e-9) {
				return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Field: "cash", Details: "fill spends unavailable cash"}
			}
			if pos := positions[fill.Symbol]; pos != nil {
				total := pos.quantity + quantity
				pos.entryPrice = (pos.entryPrice*pos.quantity + notional) / total
				pos.quantity = total
				pos.entryFee += fee
				pos.entrySlippage += fillSlippage
				pos.entryFillIDs = append(pos.entryFillIDs, fill.FillID)
			} else {
				positions[fill.Symbol] = &holding{quantity: quantity, entryPrice: price, entryFee: fee, entrySlippage: fillSlippage, entryAt: at.UTC(), entryLiquidity: liquidity, entryFillIDs: []string{fill.FillID}}
			}
			continue
		}
		cash += notional - fee
		pos := positions[fill.Symbol]
		if pos == nil || quantity > pos.quantity+1e-12 || len(trades) >= len(result.Trades) {
			return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Field: "fills", Details: "sell cannot be attributed to a closed trade"}
		}
		trade := result.Trades[len(trades)]
		entryFee := pos.entryFee * quantity / pos.quantity
		entrySlippage := pos.entrySlippage * quantity / pos.quantity
		pnl := (price-pos.entryPrice)*quantity - entryFee - fee
		if trade.Symbol != fill.Symbol || !trade.EntryTime.Equal(pos.entryAt) || !trade.ExitTime.Equal(at) || !stage07Near(trade.Size, quantity) || !stage07Near(trade.EntryPrice, pos.entryPrice) || !stage07Near(trade.ExitPrice, price) || !stage07Near(trade.Pnl, pnl) {
			return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Field: "trade", Details: "closed trade does not reconcile to its unique fills"}
		}
		cost := entryFee + fee + entrySlippage + fillSlippage
		regime := strings.TrimSpace(trade.RegimeState)
		if regime == "" {
			regime = "unknown"
		}
		trades = append(trades, validation.TradePrimitive{ID: fmt.Sprintf("%d:%s", fold, fill.FillID), EntryFillIDs: append([]string(nil), pos.entryFillIDs...), ExitFillID: fill.FillID, Symbol: trade.Symbol, Regime: regime, OpenedAt: trade.EntryTime.UTC(), ClosedAt: trade.ExitTime.UTC(), Notional: math.Abs(trade.EntryPrice * trade.Size), AvailableLiquidity: pos.entryLiquidity, GrossPnL: trade.Pnl + cost, Cost: cost, NetPnL: trade.Pnl})
		pos.quantity -= quantity
		pos.entryFee -= entryFee
		pos.entrySlippage -= entrySlippage
		if pos.quantity <= 1e-12 {
			delete(positions, fill.Symbol)
		}
	}
	if len(trades) != len(result.Trades) || result.Metrics.FillCount != len(fills) || result.Metrics.TradeCount != len(trades) {
		return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Field: "trades", Details: "closed trade has no attributed sell fill"}
	}
	if !stage07Near(turnover/capital, metricValue(result.Metrics.TurnoverRatio)) || !stage07Near(turnover, stage07MetricFloat(result.Metrics.Turnover)) || !stage07Near(fees, stage07MetricFloat(result.Metrics.FeeCosts)) || !stage07Near(slippage, stage07MetricFloat(result.Metrics.SlippageCosts)) {
		return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Field: "fills", Details: "fills do not reconcile to Stage 05 turnover and costs"}
	}
	endPositions := make([]stage07EndPosition, 0, len(positions))
	for symbol, pos := range positions {
		endPositions = append(endPositions, stage07EndPosition{Symbol: symbol, Quantity: pos.quantity, EntryPrice: pos.entryPrice, EntryFee: pos.entryFee, EntrySlippage: pos.entrySlippage})
	}
	sort.Slice(endPositions, func(i, j int) bool { return endPositions[i].Symbol < endPositions[j].Symbol })
	return trades, fills, stage07EndInventory{Cash: cash, Positions: endPositions}, nil
}

func stage07Positive(v float64) bool    { return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 }
func stage07Nonnegative(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0 }
func stage07Near(a, b float64) bool {
	return stage07Nonnegative(math.Abs(a-b)) && math.Abs(a-b) <= 1e-8*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}
func stage07MetricFloat(raw string) float64 {
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return math.NaN()
	}
	return v
}
