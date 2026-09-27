package backtest

import (
	"fmt"
	"math"
	"math/big"
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

func stage07EconomicPrimitives(result Stage05StrategyResult, fold int, capital float64, execution map[string][]services.OHLCV, configs ...BacktestConfig) ([]validation.TradePrimitive, []validation.FillPrimitive, stage07EndInventory, error) {
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
	v4 := result.Manifest.ExecutionPolicy.Version == "backtest-execution-v4"
	var config BacktestConfig
	if v4 {
		if len(configs) != 1 || configs[0].ConstraintResolver == nil {
			return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticCapacity, Field: "fills", Details: "v4 requires point-in-time constraints for independent capacity verification"}
		}
		config = configs[0]
	}
	orders := map[string]OrderArtifact{}
	accepted := map[string]int{}
	approved := map[string]string{}
	for _, order := range result.Artifacts.Orders {
		if v4 && (order.OrderID == "" || order.OrderID != order.IntentID || orders[order.OrderID].OrderID != "") {
			return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Field: "orders", Details: "v4 order identity is missing or duplicated"}
		}
		orders[order.OrderID] = order
	}
	for _, decision := range result.Artifacts.Decisions {
		if decision.Stage == "broker" && (decision.Code == "filled" || decision.Code == "accepted") {
			accepted[decision.IntentID]++
			approved[decision.IntentID] = decision.ApprovedQuantity
		}
	}
	filledOrders := map[string]bool{}
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
		selectedOpen := at
		if v4 {
			selectedOpen = at.Add(-time.Minute + time.Millisecond)
		}
		bar, found := stage07BarAt(execution[fill.Symbol], selectedOpen)
		if !found {
			return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticCapacity, Field: "fills", Details: fmt.Sprintf("execution bar absent for fill %s (%s at %s)", fill.FillID, fill.Symbol, at.UTC().Format(time.RFC3339Nano))}
		}
		if !stage07Positive(bar.Volume) {
			return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticCapacity, Field: "fills", Details: fmt.Sprintf("execution bar has nonpositive volume for fill %s (%s at %s)", fill.FillID, fill.Symbol, at.UTC().Format(time.RFC3339Nano))}
		}
		if v4 {
			order, ok := orders[fill.OrderID]
			signal, signalErr := time.Parse(time.RFC3339Nano, order.SignalAt)
			orderAt, orderErr := time.Parse(time.RFC3339Nano, fill.OrderAt)
			fillQty, qtyOK := new(big.Rat).SetString(fill.Quantity)
			requested, reqOK := new(big.Rat).SetString(order.Quantity)
			approvedQty, approvedOK := new(big.Rat).SetString(approved[fill.OrderID])
			if !ok || filledOrders[fill.OrderID] || accepted[fill.OrderID] != 1 || fill.IntentID != fill.OrderID || order.Symbol != fill.Symbol || order.Side != fill.Side || signalErr != nil || orderErr != nil || !signal.Before(selectedOpen) || !orderAt.Equal(signal) || !qtyOK || !reqOK || !approvedOK || fillQty.Cmp(approvedQty) != 0 || approvedQty.Cmp(requested) > 0 || order.Metadata["execution_event_at"] != canonicalTime(at) || bar.CloseTime != at.UnixMilli() || bar.CloseTime != bar.OpenTime+int64(time.Minute/time.Millisecond)-1 || !stage07Positive(bar.Open) || !stage07Positive(bar.Close) || !stage07Positive(bar.High) || !stage07Positive(bar.Low) || bar.Low > bar.High || bar.Open < bar.Low || bar.Open > bar.High || bar.Close < bar.Low || bar.Close > bar.High || !stage07DecimalMatchesFloat(fill.ExecutionReferencePrice, bar.Close) || fill.CostVersion != result.Manifest.ExecutionPolicy.CostVersion {
				return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticCapacity, Field: "fills", Details: "v4 fill is not bound to one approved selected-bar-close order"}
			}
			lot, tick, _, _, constraintErr := constraintValues(config, fill.Symbol, at)
			cap, capErr := stage05VolumeCap(bar.Volume, lot)
			capRat, capOK := new(big.Rat).SetString(cap)
			if constraintErr != nil || capErr != nil || !capOK || approvedQty.Cmp(capRat) > 0 || fillQty.Cmp(capRat) > 0 {
				return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticCapacity, Field: "fills", Details: "v4 fill exceeds point-in-time selected-bar capacity"}
			}
			if err := stage07VerifyV4FillCosts(fill, bar.Close, tick, config); err != nil {
				return nil, nil, stage07EndInventory{}, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Field: "fills", Details: err.Error()}
			}
			filledOrders[fill.OrderID] = true
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
		orderID := ""
		if result.Manifest.ExecutionPolicy.Version == "backtest-execution-v3" || v4 {
			orderID = fill.OrderID
		}
		fills = append(fills, validation.FillPrimitive{ID: fill.FillID, OrderID: orderID, Symbol: fill.Symbol, Side: fill.Side, At: at.UTC(), Notional: notional, AvailableLiquidity: liquidity})
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

func stage07VerifyV4FillCosts(fill FillArtifact, close float64, tick string, config BacktestConfig) error {
	if config.FeeBps < 0 || config.SlippageBps < 0 || config.FeeBps != math.Trunc(config.FeeBps) || config.SlippageBps != math.Trunc(config.SlippageBps) {
		return fmt.Errorf("v4 cost policy has invalid basis points")
	}
	closeText, err := stage05InputDecimal(close)
	if err != nil {
		return err
	}
	closeRat, _ := new(big.Rat).SetString(closeText)
	quantity, qtyOK := new(big.Rat).SetString(fill.Quantity)
	price, priceOK := new(big.Rat).SetString(fill.Price)
	fee, feeOK := new(big.Rat).SetString(fill.Fee)
	if !qtyOK || !priceOK || !feeOK || quantity.Sign() <= 0 || price.Sign() <= 0 || fee.Sign() < 0 {
		return fmt.Errorf("v4 fill has invalid exact price, quantity or fee")
	}
	factor := int64(10000 + config.SlippageBps)
	if fill.Side == "sell" {
		factor = int64(10000 - config.SlippageBps)
	}
	if factor <= 0 {
		return fmt.Errorf("v4 adverse slippage makes nonpositive price")
	}
	expected := stage07RoundRat18(new(big.Rat).Mul(closeRat, big.NewRat(factor, 10000)))
	if tick != "" {
		step, ok := new(big.Rat).SetString(tick)
		if !ok || step.Sign() <= 0 {
			return fmt.Errorf("v4 point-in-time price tick is invalid")
		}
		ratio := new(big.Rat).Quo(expected, step)
		units := new(big.Int).Quo(ratio.Num(), ratio.Denom())
		if fill.Side == "buy" && new(big.Rat).SetInt(units).Cmp(ratio) < 0 {
			units.Add(units, big.NewInt(1))
		}
		expected = new(big.Rat).Mul(new(big.Rat).SetInt(units), step)
		expected = stage07RoundRat18(expected)
	}
	if price.Cmp(expected) != 0 {
		return fmt.Errorf("v4 fill price differs from close, adverse slippage and historical tick")
	}
	expectedFee := stage07RoundRat18(new(big.Rat).Mul(new(big.Rat).Mul(quantity, price), big.NewRat(int64(config.FeeBps), 10000)))
	if fee.Cmp(expectedFee) != 0 {
		return fmt.Errorf("v4 fill fee differs from historical cost policy")
	}
	return nil
}

func stage07RoundRat18(value *big.Rat) *big.Rat {
	scale := new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil)
	scaled := new(big.Rat).Mul(value, new(big.Rat).SetInt(scale))
	units := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	remainder := new(big.Int).Rem(scaled.Num(), scaled.Denom())
	if new(big.Int).Mul(new(big.Int).Abs(remainder), big.NewInt(2)).Cmp(scaled.Denom()) >= 0 {
		if value.Sign() < 0 {
			units.Sub(units, big.NewInt(1))
		} else {
			units.Add(units, big.NewInt(1))
		}
	}
	return new(big.Rat).SetFrac(units, scale)
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
