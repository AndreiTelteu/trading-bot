package tradingcore

import (
	"context"
	"math/big"
)

const (
	RiskExecutionNotAuthorized RejectionCode = "execution_not_authorized"
	RiskPendingConflict        RejectionCode = "pending_order_conflict"
	RiskPositionExists         RejectionCode = "position_exists"
	RiskPyramidLayers          RejectionCode = "max_pyramid_layers_reached"
	RiskMaxPositions           RejectionCode = "max_positions_reached"
	RiskMaxConcurrency         RejectionCode = "max_concurrent_orders_reached"
	RiskPositionExposure       RejectionCode = "per_position_exposure_exceeded"
	RiskTotalExposure          RejectionCode = "total_exposure_exceeded"
	RiskTurnover               RejectionCode = "turnover_exceeded"
	RiskInsufficientCash       RejectionCode = "insufficient_cash"
	RiskQuantityBelowLot       RejectionCode = "quantity_below_lot_size"
	RiskInsufficientAsset      RejectionCode = "insufficient_asset"
)

type PortfolioRiskEngine struct{}

func (PortfolioRiskEngine) Evaluate(_ context.Context, batch DecisionBatch, portfolio PortfolioSnapshot, policy RiskPolicy) (RiskDecision, error) {
	if err := policy.Validate(); err != nil {
		return RiskDecision{}, err
	}
	approved := make([]OrderIntent, 0)
	rejected := make([]OrderRejection, 0)
	traces := make([]RiskTrace, 0)
	positions := portfolio.Positions()
	pending := portfolio.PendingOrders()
	gross := big.NewRat(0, 1)
	turnover := big.NewRat(0, 1)
	for _, position := range positions {
		gross.Add(gross, notional(position.Quantity, position.MarkPrice))
	}
	for _, intent := range batch.Intents() {
		checks := []string{"execution_authority"}
		requested := new(big.Rat).Set(decimalRat(intent.Quantity.Decimal()))
		approvedQty := new(big.Rat).Set(requested)
		price, hasPrice := intentPrice(intent, portfolio)
		code := RejectionCode("")
		if intent.ExecutionMode == ExecutionResearch || intent.ExecutionMode == ExecutionShadow {
			code = RiskExecutionNotAuthorized
		} else if pendingConflict(pending, intent) {
			checks = append(checks, "pending_conflict")
			code = RiskPendingConflict
		} else if intent.Side == Buy && existingCount(positions, intent.Instrument) > 0 && !policy.PyramidingEnabled {
			checks = append(checks, "pyramiding")
			code = RiskPositionExists
		} else if intent.Side == Buy && policy.PyramidingEnabled && policy.MaxPyramidLayers > 0 && pyramidLayers(positions, intent.Instrument) >= policy.MaxPyramidLayers {
			checks = append(checks, "pyramid_layers")
			code = RiskPyramidLayers
		} else if policy.MaxConcurrentOrders > 0 && len(pending)+len(approved) >= policy.MaxConcurrentOrders {
			checks = append(checks, "concurrency")
			code = RiskMaxConcurrency
		} else if intent.Side == Buy && existingCount(positions, intent.Instrument) == 0 && policy.MaxPositions > 0 && len(positions)+countNewBuys(approved, positions) >= policy.MaxPositions {
			checks = append(checks, "positions")
			code = RiskMaxPositions
		}
		if code == "" {
			normalized, err := normalizedQuantity(approvedQty, policy.LotSize)
			if err != nil {
				code = RiskQuantityBelowLot
			} else {
				approvedQty = normalized
				checks = append(checks, "lot_size")
			}
		}
		requestedNotional, approvedNotional := "", ""
		if code == "" && hasPrice {
			requestedValue := new(big.Rat).Mul(requested, decimalRat(price.Decimal()))
			approvedValue := new(big.Rat).Mul(approvedQty, decimalRat(price.Decimal()))
			requestedNotional = ratString(requestedValue)
			if intent.Side == Buy {
				approvedQty, code = capBuyQuantity(approvedQty, price, gross, turnover, intent, portfolio, policy)
				approvedValue = new(big.Rat).Mul(approvedQty, decimalRat(price.Decimal()))
				if code == "" {
					gross.Add(gross, approvedValue)
					turnover.Add(turnover, approvedValue)
				}
			} else if available := positionQuantity(positions, intent.Instrument); approvedQty.Cmp(available) > 0 {
				code = RiskInsufficientAsset
			}
			approvedNotional = ratString(approvedValue)
		}
		if code != "" {
			rejected = append(rejected, OrderRejection{OrderID: intent.ID, Code: code, Message: string(code), EvaluatedAt: portfolio.AsOf(), PolicyVersion: policy.Version})
		} else {
			quantity, err := quantityFromRat(approvedQty)
			if err != nil {
				rejected = append(rejected, OrderRejection{OrderID: intent.ID, Code: RiskQuantityBelowLot, Message: string(RiskQuantityBelowLot), EvaluatedAt: portfolio.AsOf(), PolicyVersion: policy.Version})
			} else {
				intent.Quantity = quantity
				intent.Versions.Policy = policy.Version
				approved = append(approved, intent)
			}
		}
		traces = append(traces, RiskTrace{OrderID: intent.ID, PolicyVersion: policy.Version, Checks: checks, RequestedQuantity: intent.Quantity.Decimal().String(), ApprovedQuantity: ratString(approvedQty), RequestedNotional: requestedNotional, ApprovedNotional: approvedNotional})
	}
	approvedBatch, err := NewDecisionBatch(approved)
	if err != nil {
		return RiskDecision{}, err
	}
	return NewRiskDecisionWithTrace(approvedBatch, rejected, traces), nil
}

func intentPrice(intent OrderIntent, portfolio PortfolioSnapshot) (Price, bool) {
	if p, ok := intent.ReferencePrice.Get(); ok {
		return p, true
	}
	if p, ok := intent.LimitPrice.Get(); ok {
		return p, true
	}
	for _, position := range portfolio.Positions() {
		if position.Instrument.ID == intent.Instrument.ID {
			return position.MarkPrice, true
		}
	}
	return Price{}, false
}
func pendingConflict(values []PendingOrder, intent OrderIntent) bool {
	for _, value := range values {
		if value.Instrument.ID == intent.Instrument.ID {
			return true
		}
	}
	return false
}
func existingCount(values []Position, instrument Instrument) int {
	count := 0
	for _, value := range values {
		if value.Instrument.ID == instrument.ID {
			count++
		}
	}
	return count
}
func pyramidLayers(values []Position, instrument Instrument) int {
	total := 0
	for _, value := range values {
		if value.Instrument.ID == instrument.ID {
			layers := value.PyramidLayers
			if layers == 0 {
				layers = 1
			}
			total += layers
		}
	}
	return total
}
func positionQuantity(values []Position, instrument Instrument) *big.Rat {
	total := big.NewRat(0, 1)
	for _, value := range values {
		if value.Instrument.ID == instrument.ID {
			total.Add(total, decimalRat(value.Quantity.Decimal()))
		}
	}
	return total
}
func countNewBuys(intents []OrderIntent, positions []Position) int {
	count := 0
	for _, intent := range intents {
		if intent.Side == Buy && existingCount(positions, intent.Instrument) == 0 {
			count++
		}
	}
	return count
}
func ratString(value *big.Rat) string { decimal, _ := ratDecimal(value); return decimal.String() }

func capBuyQuantity(quantity *big.Rat, price Price, gross, turnover *big.Rat, intent OrderIntent, portfolio PortfolioSnapshot, policy RiskPolicy) (*big.Rat, RejectionCode) {
	priceRat := decimalRat(price.Decimal())
	capQty := new(big.Rat).Set(quantity)
	capByAmount := func(limit SignedAmount, used *big.Rat, rejection RejectionCode) RejectionCode {
		if !limit.Valid() || limit.Decimal().Sign() == 0 {
			return ""
		}
		remaining := new(big.Rat).Sub(decimalRat(limit.Decimal()), used)
		if remaining.Sign() <= 0 {
			return rejection
		}
		maxQty := new(big.Rat).Quo(remaining, priceRat)
		if capQty.Cmp(maxQty) > 0 {
			capQty.Set(maxQty)
		}
		return ""
	}
	if code := capByAmount(policy.MaxPositionValue, instrumentExposure(portfolio.Positions(), intent.Instrument), RiskPositionExposure); code != "" {
		return capQty, code
	}
	if code := capByAmount(policy.MaxGrossExposure, gross, RiskTotalExposure); code != "" {
		return capQty, code
	}
	if code := capByAmount(policy.MaxTurnover, turnover, RiskTurnover); code != "" {
		return capQty, code
	}
	cash, ok := portfolio.CashAmount(intent.Instrument.QuoteAsset)
	if !ok {
		return capQty, RiskInsufficientCash
	}
	available := decimalRat(cash.Decimal())
	if policy.CashReserve.Valid() {
		available.Sub(available, decimalRat(policy.CashReserve.Decimal()))
	}
	if available.Sign() <= 0 {
		return capQty, RiskInsufficientCash
	}
	cashQty := new(big.Rat).Quo(available, priceRat)
	if capQty.Cmp(cashQty) > 0 {
		capQty.Set(cashQty)
	}
	if capQty.Sign() <= 0 {
		return capQty, RiskInsufficientCash
	}
	normalized, err := normalizedQuantity(capQty, policy.LotSize)
	if err != nil {
		return capQty, RiskQuantityBelowLot
	}
	return normalized, ""
}

func instrumentExposure(values []Position, instrument Instrument) *big.Rat {
	total := big.NewRat(0, 1)
	for _, value := range values {
		if value.Instrument.ID == instrument.ID {
			total.Add(total, notional(value.Quantity, value.MarkPrice))
		}
	}
	return total
}
