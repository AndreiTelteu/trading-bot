package tradingcore

import (
	"context"
	"math/big"
	"strconv"
)

const (
	RiskExecutionNotAuthorized RejectionCode = "execution_not_authorized"
	RiskMissingPrice           RejectionCode = "valuation_price_required"
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
	positions, pending := portfolio.Positions(), portfolio.PendingOrders()
	gross := portfolioGross(positions)
	turnover := big.NewRat(0, 1)
	if state := portfolio.RiskState(); state.Known() {
		stateGross, _, _, _ := state.Values()
		gross = decimalRat(stateGross.Decimal())
		turnover = decimalRat(state.Turnover().Decimal())
	}
	cash := map[AssetID]*big.Rat{}
	for asset, amount := range portfolio.Cash() {
		available := decimalRat(amount.Decimal())
		if policy.CashReserve.Valid() {
			available.Sub(available, decimalRat(policy.CashReserve.Decimal()))
		}
		cash[asset] = available
	}
	approved := []OrderIntent{}
	rejected := []OrderRejection{}
	traces := []RiskTrace{}
	for _, intent := range batch.Intents() {
		requested := decimalRat(intent.Quantity.Decimal())
		quantity := new(big.Rat).Set(requested)
		preGross := new(big.Rat).Set(gross)
		preTurnover := new(big.Rat).Set(turnover)
		preCash := new(big.Rat)
		if value := cash[intent.Instrument.QuoteAsset]; value != nil {
			preCash.Set(value)
		}
		trace := RiskTrace{OrderID: intent.ID.String(), PolicyVersion: policy.Version, Priority: intent.Priority, CostPolicyVersion: policy.ExecutionCosts.Version, RequestedQuantity: intent.Quantity.Decimal().String(), PreCash: ratString(preCash), PreGrossExposure: ratString(preGross), PreTurnover: ratString(preTurnover)}
		checks := []string{"execution_authority"}
		code := RejectionCode("")
		price, hasPrice := intentPrice(intent, portfolio)
		switch {
		case intent.ExecutionMode == ExecutionResearch || intent.ExecutionMode == ExecutionShadow || intent.ExecutionMode == ExecutionLiveDryRun:
			code = RiskExecutionNotAuthorized
		case !hasPrice:
			code = RiskMissingPrice
		case pendingConflict(pending, intent):
			checks = append(checks, "pending_conflict")
			code = RiskPendingConflict
		case intent.Side == Buy && existingCount(positions, intent.Instrument) > 0 && !policy.PyramidingEnabled:
			checks = append(checks, "pyramiding")
			code = RiskPositionExists
		case intent.Side == Buy && policy.PyramidingEnabled && policy.MaxPyramidLayers > 0 && pyramidLayers(positions, intent.Instrument) >= policy.MaxPyramidLayers:
			checks = append(checks, "pyramid_layers")
			code = RiskPyramidLayers
		case policy.MaxConcurrentOrders > 0 && len(pending)+len(approved) >= policy.MaxConcurrentOrders:
			checks = append(checks, "concurrency")
			code = RiskMaxConcurrency
		case intent.Side == Buy && existingCount(positions, intent.Instrument) == 0 && policy.MaxPositions > 0 && len(positions)+countNewBuys(approved, positions) >= policy.MaxPositions:
			checks = append(checks, "positions")
			code = RiskMaxPositions
		}
		if code == "" {
			checks = append(checks, "position_exposure", "gross_exposure", "turnover", "cash", "lot_size")
			quantity, code = capIntent(quantity, intent, price, positions, gross, turnover, cash[intent.Instrument.QuoteAsset], policy)
		}
		if code == "" {
			normalized, err := normalizedQuantity(quantity, policy.LotSize)
			if err != nil {
				code = RiskQuantityBelowLot
			} else {
				quantity = normalized
			}
		}
		approvedNotional, reservedSlip, reservedFee := big.NewRat(0, 1), big.NewRat(0, 1), big.NewRat(0, 1)
		if hasPrice && quantity.Sign() > 0 {
			approvedNotional, reservedSlip, reservedFee = costReservation(quantity, price, intent.Side, policy.ExecutionCosts)
			trace.RequestedNotional = ratString(new(big.Rat).Mul(requested, decimalRat(price.Decimal())))
			trace.ApprovedNotional = ratString(approvedNotional)
			trace.ReservedSlippage = ratString(reservedSlip)
			trace.ReservedFee = ratString(reservedFee)
		}
		if code == "" {
			approvedQuantity, err := quantityFromRat(quantity)
			if err != nil {
				code = RiskQuantityBelowLot
			} else {
				intent.Quantity = approvedQuantity
				intent.Versions.Policy = policy.Version
				metadata := intent.Metadata()
				if metadata == nil {
					metadata = map[string]string{}
				}
				metadata["cost_policy_version"] = policy.ExecutionCosts.Version
				metadata["fee_bps"] = strconv.FormatInt(policy.ExecutionCosts.FeeBPS, 10)
				metadata["slippage_bps"] = strconv.FormatInt(policy.ExecutionCosts.AdverseSlippageBPS, 10)
				intent, _ = NewOrderIntent(intent, metadata)
				approved = append(approved, intent)
				turnover.Add(turnover, approvedNotional)
				referenceNotional := new(big.Rat).Mul(quantity, decimalRat(price.Decimal()))
				if intent.Side == Buy {
					gross.Add(gross, referenceNotional)
					reservedCash := new(big.Rat).Add(new(big.Rat).Set(approvedNotional), reservedFee)
					cash[intent.Instrument.QuoteAsset].Sub(cash[intent.Instrument.QuoteAsset], reservedCash)
				} else {
					gross.Sub(gross, referenceNotional)
				}
			}
		}
		if code != "" {
			rejected = append(rejected, OrderRejection{OrderID: intent.ID, Code: code, Message: string(code), EvaluatedAt: portfolio.AsOf(), PolicyVersion: policy.Version})
		}
		trace.Checks = checks
		trace.ApprovedQuantity = ratString(quantity)
		trace.PostCash = ratString(valueOrZero(cash[intent.Instrument.QuoteAsset], preCash))
		trace.PostGrossExposure = ratString(gross)
		trace.PostTurnover = ratString(turnover)
		traces = append(traces, trace)
	}
	approvedBatch, err := NewDecisionBatch(approved)
	if err != nil {
		return RiskDecision{}, err
	}
	return NewRiskDecisionWithTrace(approvedBatch, rejected, traces), nil
}

func capIntent(quantity *big.Rat, intent OrderIntent, price Price, positions []Position, gross, turnover, cash *big.Rat, policy RiskPolicy) (*big.Rat, RejectionCode) {
	result := new(big.Rat).Set(quantity)
	referencePrice := decimalRat(price.Decimal())
	adverseBPS := int64(10000 + policy.ExecutionCosts.AdverseSlippageBPS)
	if intent.Side == Sell {
		adverseBPS = 10000 - policy.ExecutionCosts.AdverseSlippageBPS
	}
	adversePrice := new(big.Rat).Mul(referencePrice, big.NewRat(adverseBPS, 10000))
	feeFactor := big.NewRat(10000+policy.ExecutionCosts.FeeBPS, 10000)
	cashUnit := new(big.Rat).Mul(adversePrice, feeFactor)
	capBy := func(limit SignedAmount, used, unit *big.Rat, code RejectionCode) RejectionCode {
		if !limit.Valid() || limit.Decimal().Sign() == 0 {
			return ""
		}
		remaining := new(big.Rat).Sub(decimalRat(limit.Decimal()), used)
		if remaining.Sign() <= 0 {
			return code
		}
		candidate := new(big.Rat).Quo(remaining, unit)
		if result.Cmp(candidate) > 0 {
			result.Set(candidate)
		}
		return ""
	}
	if intent.Side == Buy {
		if code := capBy(policy.MaxPositionValue, instrumentExposure(positions, intent.Instrument), referencePrice, RiskPositionExposure); code != "" {
			return result, code
		}
		if code := capBy(policy.MaxGrossExposure, gross, referencePrice, RiskTotalExposure); code != "" {
			return result, code
		}
	}
	if code := capBy(policy.MaxTurnover, turnover, adversePrice, RiskTurnover); code != "" {
		return result, code
	}
	if intent.Side == Buy {
		if cash == nil || cash.Sign() <= 0 {
			return result, RiskInsufficientCash
		}
		candidate := new(big.Rat).Quo(cash, cashUnit)
		if result.Cmp(candidate) > 0 {
			result.Set(candidate)
		}
		if result.Sign() <= 0 {
			return result, RiskInsufficientCash
		}
	} else {
		available := positionQuantity(positions, intent.Instrument)
		if result.Cmp(available) > 0 {
			result.Set(available)
		}
		if result.Sign() <= 0 {
			return result, RiskInsufficientAsset
		}
	}
	return result, ""
}

func costReservation(quantity *big.Rat, price Price, side OrderSide, policy ExecutionCostPolicy) (gross, slippage, fee *big.Rat) {
	reference := new(big.Rat).Mul(quantity, decimalRat(price.Decimal()))
	adverseBPS := int64(10000 + policy.AdverseSlippageBPS)
	if side == Sell {
		adverseBPS = 10000 - policy.AdverseSlippageBPS
	}
	gross = new(big.Rat).Mul(reference, big.NewRat(adverseBPS, 10000))
	slippage = new(big.Rat).Sub(new(big.Rat).Set(gross), reference)
	if slippage.Sign() < 0 {
		slippage.Neg(slippage)
	}
	fee = new(big.Rat).Mul(gross, big.NewRat(policy.FeeBPS, 10000))
	return
}
func valueOrZero(value, fallback *big.Rat) *big.Rat {
	if value == nil {
		return new(big.Rat).Set(fallback)
	}
	return value
}
func portfolioGross(values []Position) *big.Rat {
	total := big.NewRat(0, 1)
	for _, value := range values {
		total.Add(total, notional(value.Quantity, value.MarkPrice))
	}
	return total
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
func instrumentExposure(values []Position, instrument Instrument) *big.Rat {
	total := big.NewRat(0, 1)
	for _, value := range values {
		if value.Instrument.ID == instrument.ID {
			total.Add(total, notional(value.Quantity, value.MarkPrice))
		}
	}
	return total
}
