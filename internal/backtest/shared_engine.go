package backtest

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/tradingcore"
)

func runSharedBacktestEntry(config BacktestConfig, candidate entryCandidate, indicator barContext, universe backtestUniverseSelection, positions map[string]*positionState, states map[string]*symbolState, cash float64, at time.Time, referencePrice float64) (*tradingcore.Fill, error) {
	instrument, err := backtestInstrument(candidate.Symbol)
	if err != nil {
		return nil, err
	}
	price, err := corePrice(referencePrice)
	if err != nil {
		return nil, err
	}
	account, _ := tradingcore.NewAccountID("backtest")
	portfolioPositions := make([]tradingcore.Position, 0, len(positions))
	for symbol, position := range positions {
		currentInstrument, conversionErr := backtestInstrument(symbol)
		if conversionErr != nil {
			return nil, conversionErr
		}
		mark := position.EntryPrice
		if state := states[symbol]; state != nil && state.lastPrice > 0 {
			mark = state.lastPrice
		}
		portfolioPositions = append(portfolioPositions, tradingcore.Position{ID: mustPositionID(symbol), Instrument: currentInstrument, Quantity: mustQuantity(position.Size), AveragePrice: mustPrice(position.EntryPrice), MarkPrice: mustPrice(mark), OpenedAt: position.EntryTime, RealizedPnL: mustAmount(0)})
	}
	portfolio, err := tradingcore.NewPortfolioSnapshot(at, account, tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{instrument.QuoteAsset: mustAmount(cash)}, portfolioPositions, nil, tradingcore.RiskState{})
	if err != nil {
		return nil, err
	}
	score, _ := tradingcore.ParseDecimal(decimalString(indicator.Rating))
	snapshotUniverse, err := tradingcore.NewUniverseSnapshot(at, "backtest-universe-v1", string(config.UniverseMode), []tradingcore.UniverseCandidate{{Instrument: instrument, Rank: maxInt(1, candidate.Rank), Score: score, Eligible: true}})
	if err != nil {
		return nil, err
	}
	rolloutState := config.ModelPolicy.RolloutState
	if rolloutState == "" {
		rolloutState = services.ModelRolloutShadow
	}
	fallbackMode := config.ModelPolicy.FallbackMode
	if fallbackMode == "" {
		fallbackMode = services.ModelFallbackRuleBased
	}
	policyVersion := config.Governance.PolicyVersions.CompositeVersion
	if policyVersion == "" {
		policyVersion = "backtest-risk-v1"
	}
	settings := map[string]string{"auto_trade_enabled": "true", "entry_percent": decimalString(config.EntryPercent), "signal." + instrument.ID.String(): indicator.Signal, "rating." + instrument.ID.String(): decimalString(indicator.Rating), "min_confidence_to_buy": decimalString(config.MinConfidenceToBuy), "buy_only_strong": fmt.Sprint(config.BuyOnlyStrong), "max_positions": fmt.Sprint(config.MaxPositions), "model_rollout_state": rolloutState, "model_fallback_mode": fallbackMode, "model_available": fmt.Sprint(config.ModelArtifact != nil), "model_selected." + instrument.ID.String(): fmt.Sprint(candidate.Prediction != nil), "model_floor_ok." + instrument.ID.String(): fmt.Sprint(candidate.Prediction != nil), "universe_risk_off": fmt.Sprint(universe.RegimeState == services.UniverseRegimeRiskOff)}
	portfolioValue := cash + currentPositionsValue(positions, states)
	_, desiredSize, _, _, sizeErr := determinePositionSize(config, indicator.Atr, referencePrice, cash, portfolioValue)
	if sizeErr == nil {
		settings["requested_quantity."+instrument.ID.String()] = decimalString(desiredSize)
	}
	quote := tradingcore.Quote{Instrument: instrument, Bid: price, Ask: price, Last: price, ObservedAt: at}
	snapshot, err := tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{MarketObservedAt: at, SignalAt: at, DecisionAt: at, Quotes: map[tradingcore.InstrumentID]tradingcore.Quote{instrument.ID: quote}, Universe: snapshotUniverse, Portfolio: portfolio, Settings: settings, Versions: tradingcore.VersionContext{Strategy: tradingcore.LegacyStrategyVersion, Settings: "backtest-config", Policy: policyVersion, Model: config.Governance.ModelVersion}})
	if err != nil {
		return nil, err
	}
	strategyResult, err := (tradingcore.LegacyRuleStrategy{IDs: tradingcore.NewSequenceIDGenerator(candidate.Symbol+fmt.Sprint(at.UnixMilli()), 1)}).Decide(context.Background(), snapshot)
	if err != nil {
		return nil, err
	}
	if len(strategyResult.Intents().Intents()) == 0 {
		return nil, nil
	}
	maxGross := mustAmount(portfolioValue)
	maxPosition := mustAmount(config.MaxPositionValue)
	if config.MaxPositionValue <= 0 {
		maxPosition = mustAmount(0)
	}
	policy := tradingcore.RiskPolicy{Version: policyVersion, MaxPositions: config.MaxPositions, MaxGrossExposure: maxGross, MaxPositionValue: maxPosition, MaxTurnover: maxGross, CashReserve: mustAmount(0), MaxConcurrentOrders: config.MaxPositions, LotSize: mustQuantity(.00000001)}
	risk, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(context.Background(), strategyResult.Intents(), portfolio, policy)
	if err != nil {
		return nil, err
	}
	if len(risk.Approved().Intents()) == 0 {
		return nil, nil
	}
	broker := tradingcore.NewBacktestBroker(tradingcore.NewFixedClock(at), tradingcore.NewSequenceIDGenerator(candidate.Symbol+fmt.Sprint(at.UnixMilli())+"fill", 1), tradingcore.CostModel{FeeBPS: int64(config.FeeBps), SlippageBPS: int64(config.SlippageBps), Version: "backtest-cost-v1"})
	outcome, err := broker.Submit(context.Background(), risk.Approved())
	if err != nil {
		return nil, err
	}
	if len(outcome.Accepted()) == 0 {
		return nil, nil
	}
	fill := outcome.Accepted()[0].Fills()[0]
	return &fill, nil
}

func backtestInstrument(symbol string) (tradingcore.Instrument, error) {
	normalized := strings.ToUpper(symbol)
	quoteName := "USDT"
	baseName := strings.TrimSuffix(normalized, quoteName)
	id, err := tradingcore.NewInstrumentID(strings.ToLower(baseName + "-" + quoteName))
	if err != nil {
		return tradingcore.Instrument{}, err
	}
	base, _ := tradingcore.NewAssetID(baseName)
	quote, _ := tradingcore.NewAssetID(quoteName)
	venue, _ := tradingcore.NewVenueID("binance")
	return tradingcore.NewInstrument(id, base, quote, venue, normalized)
}
func mustAmount(value float64) tradingcore.SignedAmount {
	d, e := tradingcore.ParseDecimal(decimalString(value))
	if e != nil {
		panic(e)
	}
	result, e := tradingcore.NewSignedAmount(d)
	if e != nil {
		panic(e)
	}
	return result
}
func mustQuantity(value float64) tradingcore.Quantity {
	d, e := tradingcore.ParseDecimal(decimalString(value))
	if e != nil {
		panic(e)
	}
	result, e := tradingcore.NewQuantity(d)
	if e != nil {
		panic(e)
	}
	return result
}
func mustPrice(value float64) tradingcore.Price {
	d, e := tradingcore.ParseDecimal(decimalString(value))
	if e != nil {
		panic(e)
	}
	result, e := tradingcore.NewPrice(d)
	if e != nil {
		panic(e)
	}
	return result
}
func corePrice(value float64) (tradingcore.Price, error) {
	d, e := tradingcore.ParseDecimal(decimalString(value))
	if e != nil {
		return tradingcore.Price{}, e
	}
	return tradingcore.NewPrice(d)
}
func mustPositionID(symbol string) tradingcore.PositionID {
	result, _ := tradingcore.NewPositionID("backtest-" + strings.ToLower(symbol))
	return result
}

func decimalString(value float64) string { return strconv.FormatFloat(value, 'f', -1, 64) }
