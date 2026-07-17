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

type backtestDecisionSource struct {
	snapshot tradingcore.DecisionContext
	policy   tradingcore.RiskPolicy
}

func (source backtestDecisionSource) DecisionContext(context.Context) (tradingcore.DecisionContext, tradingcore.RiskPolicy, error) {
	return source.snapshot, source.policy, nil
}

func runSharedBacktestEntry(ledger *backtestMemoryLedger, config BacktestConfig, candidate entryCandidate, indicator barContext, universe backtestUniverseSelection, positions map[string]*positionState, states map[string]*symbolState, cash float64, signalAt, decisionAt, fillAt time.Time, signalPrice, executionPrice float64) (tradingcore.RunResult, error) {
	instrument, err := backtestInstrument(config, candidate.Symbol)
	if err != nil {
		return tradingcore.RunResult{}, err
	}
	price, err := corePrice(signalPrice)
	if err != nil {
		return tradingcore.RunResult{}, err
	}
	accountName := config.AccountID
	if accountName == "" {
		accountName = "backtest"
	}
	account, _ := tradingcore.NewAccountID(accountName)
	portfolioPositions := make([]tradingcore.Position, 0, len(positions))
	for symbol, position := range positions {
		currentInstrument, conversionErr := backtestInstrument(config, symbol)
		if conversionErr != nil {
			return tradingcore.RunResult{}, conversionErr
		}
		mark := position.EntryPrice
		if state := states[symbol]; state != nil && state.lastPrice > 0 {
			mark = state.lastPrice
		}
		portfolioPositions = append(portfolioPositions, tradingcore.Position{ID: mustPositionID(symbol), Instrument: currentInstrument, Quantity: mustQuantity(position.Size), AveragePrice: mustPrice(position.EntryPrice), MarkPrice: mustPrice(mark), OpenedAt: position.EntryTime, RealizedPnL: mustAmount(0)})
	}
	portfolio, err := tradingcore.NewPortfolioSnapshot(decisionAt, account, tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{instrument.QuoteAsset: mustAmount(cash)}, portfolioPositions, nil, ledger.riskState())
	if err != nil {
		return tradingcore.RunResult{}, err
	}
	score, _ := tradingcore.ParseDecimal(decimalString(indicator.Rating))
	snapshotUniverse, err := tradingcore.NewUniverseSnapshot(decisionAt, "backtest-universe-v1", string(config.UniverseMode), []tradingcore.UniverseCandidate{{Instrument: instrument, Rank: maxInt(1, candidate.Rank), Score: score, Eligible: true}})
	if err != nil {
		return tradingcore.RunResult{}, err
	}
	rolloutState := config.ModelPolicy.RolloutState
	if rolloutState == "" {
		rolloutState = services.ModelRolloutShadow
	}
	fallbackMode := config.ModelPolicy.FallbackMode
	if fallbackMode == "" {
		fallbackMode = services.ModelFallbackRuleBased
	}
	policyVersion := backtestPolicyVersion(config)
	settings := map[string]string{"auto_trade_enabled": "true", "entry_percent": decimalString(config.EntryPercent), "signal." + instrument.ID.String(): indicator.Signal, "rating." + instrument.ID.String(): decimalString(indicator.Rating), "min_confidence_to_buy": decimalString(config.MinConfidenceToBuy), "buy_only_strong": fmt.Sprint(config.BuyOnlyStrong), "max_positions": fmt.Sprint(config.MaxPositions), "model_rollout_state": rolloutState, "model_fallback_mode": fallbackMode, "model_available": fmt.Sprint(config.ModelArtifact != nil), "model_selected." + instrument.ID.String(): fmt.Sprint(candidate.Prediction != nil), "model_floor_ok." + instrument.ID.String(): fmt.Sprint(candidate.Prediction != nil), "universe_risk_off": fmt.Sprint(universe.RegimeState == services.UniverseRegimeRiskOff)}
	settings["order_created_at"] = canonicalTime(decisionAt)
	portfolioValue := cash + currentPositionsValue(positions, states)
	_, desiredSize, _, _, sizeErr := determinePositionSize(config, indicator.Atr, signalPrice, cash, portfolioValue)
	if sizeErr == nil {
		settings["requested_quantity."+instrument.ID.String()] = decimalString(desiredSize)
		_, _, stop, target, _ := determinePositionSize(config, indicator.Atr, signalPrice, cash, portfolioValue)
		if stop != nil {
			settings["stop_price."+instrument.ID.String()] = decimalString(*stop)
		}
		if target != nil {
			settings["take_profit_price."+instrument.ID.String()] = decimalString(*target)
		}
		if config.TimeStopBars > 0 {
			settings["max_bars_held."+instrument.ID.String()] = strconv.Itoa(config.TimeStopBars)
		}
	}
	if indicator.Atr > 0 {
		settings["atr_value."+instrument.ID.String()] = decimalString(indicator.Atr)
	}
	if config.AtrTrailingMult > 0 {
		settings["atr_trailing_mult."+instrument.ID.String()] = decimalString(config.AtrTrailingMult)
	}
	settings["entry_rank."+instrument.ID.String()] = strconv.Itoa(candidate.Rank)
	settings["regime_state."+instrument.ID.String()] = universe.RegimeState
	settings["breadth_ratio."+instrument.ID.String()] = decimalString(universe.BreadthRatio)
	settings["model_version."+instrument.ID.String()] = modelVersionForCandidate(candidate)
	if value := predictionProbability(candidate.Prediction); value != nil {
		settings["predicted_probability."+instrument.ID.String()] = decimalString(*value)
	}
	if value := predictionExpectedValue(candidate.Prediction); value != nil {
		settings["predicted_ev."+instrument.ID.String()] = decimalString(*value)
	}
	quote := tradingcore.Quote{Instrument: instrument, Bid: price, Ask: price, Last: price, ObservedAt: signalAt}
	snapshot, err := tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{MarketObservedAt: signalAt, SignalAt: signalAt, DecisionAt: decisionAt, Quotes: map[tradingcore.InstrumentID]tradingcore.Quote{instrument.ID: quote}, Universe: snapshotUniverse, Portfolio: portfolio, Settings: settings, Versions: tradingcore.VersionContext{Strategy: config.StrategyVersion, Settings: config.ConfigVersion, Policy: policyVersion, Model: config.Governance.ModelVersion, Dataset: config.DatasetManifestID}})
	if err != nil {
		return tradingcore.RunResult{}, err
	}
	maxGross := mustAmount(portfolioValue)
	maxPosition := mustAmount(config.MaxPositionValue)
	if config.MaxPositionValue <= 0 {
		maxPosition = mustAmount(0)
	}
	lotSize, priceTick, minQuantity, minNotional, err := constraintValues(config, candidate.Symbol, fillAt)
	if err != nil {
		return tradingcore.RunResult{}, err
	}
	policy := tradingcore.RiskPolicy{Version: policyVersion, MaxPositions: config.MaxPositions, MaxGrossExposure: maxGross, MaxPositionValue: maxPosition, MaxTurnover: mustAmount(0), CashReserve: mustAmount(0), MaxConcurrentOrders: config.MaxPositions, LotSize: mustQuantity(lotSize), ExecutionCosts: tradingcore.ExecutionCostPolicy{Version: config.ExecutionPolicy.CostVersion, FeeBPS: int64(config.FeeBps), AdverseSlippageBPS: int64(config.SlippageBps)}}
	execPrice, err := corePrice(executionPrice)
	if err != nil {
		return tradingcore.RunResult{}, err
	}
	broker := tradingcore.NewBacktestBroker(tradingcore.NewFixedClock(fillAt), tradingcore.NewSequenceIDGenerator(candidate.Symbol+fmt.Sprint(fillAt.UnixNano())+"fill", 1), tradingcore.CostModel{FeeBPS: int64(config.FeeBps), SlippageBPS: int64(config.SlippageBps), Version: config.ExecutionPolicy.CostVersion, ExecutionPrice: tradingcore.SomePrice(execPrice), PriceTick: priceTick, MinQuantity: minQuantity, MinNotional: minNotional})
	runner := tradingcore.Orchestrator{Source: backtestDecisionSource{snapshot, policy}, Strategy: tradingcore.LegacyRuleStrategy{}, Risk: tradingcore.PortfolioRiskEngine{}, Broker: broker, Ledger: ledger, Observer: ledger}
	result, err := runner.Run(context.Background())
	if err == nil {
		ledger.recordRun(result, signalAt, decisionAt)
	}
	return result, err
}

type backtestLedgerEvent struct {
	Side, Symbol, Quantity, Price, Fee, CostVersion, Reason string
	ExecutionReferencePrice                                 string
	SignalAt, DecisionAt, OrderAt, At                       time.Time
	CashAfter                                               string
}
type backtestMemoryLedger struct {
	cash                       float64
	positions                  map[string]*positionState
	trades                     []Trade
	events                     []backtestLedgerEvent
	turnover                   float64
	runs                       int
	observations               []tradingcore.Observation
	config                     BacktestConfig
	runRecords                 []backtestRunRecord
	stage06PaperShadowApproved []Stage06OrderSemantic
	evidence                   RunEvidence
}

type backtestRunRecord struct {
	Result               tradingcore.RunResult
	SignalAt, DecisionAt time.Time
}

func (ledger *backtestMemoryLedger) recordRun(result tradingcore.RunResult, signalAt, decisionAt time.Time) {
	ledger.runRecords = append(ledger.runRecords, backtestRunRecord{result, signalAt, decisionAt})
	ledger.evidence.StrategyNoActions += len(result.Strategy.NoActions())
	ledger.evidence.StrategyIntents += len(result.Strategy.Intents().Intents())
	ledger.evidence.RiskRejections += len(result.Risk.Rejected())
	ledger.evidence.BrokerRejections += len(result.Broker.Rejected())
	ledger.evidence.AcceptedOrders += len(result.Broker.Accepted())
	for _, accepted := range result.Broker.Accepted() {
		ledger.evidence.Fills += len(accepted.Fills())
	}
}

func newBacktestMemoryLedger(config BacktestConfig) *backtestMemoryLedger {
	return &backtestMemoryLedger{cash: config.InitialBalance, positions: map[string]*positionState{}, config: config}
}
func (ledger *backtestMemoryLedger) Observe(_ context.Context, value tradingcore.Observation) error {
	value.Metadata = cloneBacktestStrings(value.Metadata)
	ledger.observations = append(ledger.observations, value)
	return nil
}
func cloneBacktestStrings(values map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range values {
		result[key] = value
	}
	return result
}
func (ledger *backtestMemoryLedger) riskState() tradingcore.RiskState {
	state, _ := tradingcore.NewRiskStateWithTurnover(mustAmount(currentPositionsValue(ledger.positions, map[string]*symbolState{})), mustAmount(0), mustAmount(0), mustAmount(0), mustAmount(ledger.turnover))
	return state
}
func (ledger *backtestMemoryLedger) RecordBrokerOutcome(_ context.Context, approved tradingcore.DecisionBatch, outcome tradingcore.BrokerBatchOutcome) error {
	ledger.runs++
	if outcome.Completeness() != tradingcore.OutcomeComplete {
		return fmt.Errorf("incomplete backtest broker outcome")
	}
	intents := map[tradingcore.OrderID]tradingcore.OrderIntent{}
	for _, intent := range approved.Intents() {
		intents[intent.ID] = intent
	}
	seen := map[tradingcore.OrderID]bool{}
	type action struct {
		intent tradingcore.OrderIntent
		fill   tradingcore.Fill
	}
	actions := []action{}
	cash := ledger.cash
	quantities := map[string]float64{}
	for symbol, pos := range ledger.positions {
		quantities[symbol] = pos.Size
	}
	for _, accepted := range outcome.Accepted() {
		intent, ok := intents[accepted.OrderID]
		if !ok {
			return fmt.Errorf("unapproved backtest order")
		}
		if seen[accepted.OrderID] {
			return fmt.Errorf("duplicate backtest order outcome")
		}
		seen[accepted.OrderID] = true
		for _, fill := range accepted.Fills() {
			quantity, price, fee := fill.Quantity.Decimal().Float64(), fill.Price.Decimal().Float64(), fill.Fee.Decimal().Float64()
			symbol := fill.Instrument.VenueSymbol
			if intent.Side == tradingcore.Buy {
				cash -= quantity*price + fee
				if cash < -1e-9 {
					return fmt.Errorf("backtest ledger insufficient cash")
				}
				quantities[symbol] += quantity
			} else {
				if quantities[symbol]+1e-12 < quantity {
					return fmt.Errorf("backtest ledger insufficient asset")
				}
				cash += quantity*price - fee
				quantities[symbol] -= quantity
			}
			actions = append(actions, action{intent, fill})
		}
	}
	for _, rejected := range outcome.Rejected() {
		if _, ok := intents[rejected.OrderID]; !ok || seen[rejected.OrderID] {
			return fmt.Errorf("invalid backtest order rejection")
		}
		seen[rejected.OrderID] = true
	}
	if len(seen) != len(intents) {
		return fmt.Errorf("complete backtest outcome did not resolve approved batch")
	}
	for _, action := range actions {
		intent, fill := action.intent, action.fill
		quantity, price, fee := fill.Quantity.Decimal().Float64(), fill.Price.Decimal().Float64(), fill.Fee.Decimal().Float64()
		symbol := fill.Instrument.VenueSymbol
		metadata := intent.Metadata()
		ledger.turnover += quantity * price
		if intent.Side == tradingcore.Buy {
			ledger.cash -= quantity*price + fee
			if existing := ledger.positions[symbol]; existing != nil {
				total := existing.Size + quantity
				existing.EntryPrice = (existing.EntryPrice*existing.Size + price*quantity) / total
				existing.Size = total
				existing.EntryFee += fee
			} else {
				ledger.positions[symbol] = &positionState{Symbol: symbol, EntryPrice: price, Size: quantity, StopPrice: metadataFloat64(metadata, "stop_price"), TakeProfit: metadataFloat64(metadata, "take_profit_price"), HighestPrice: price, EntryTime: fill.FilledAt, LastAtr: metadataFloatValue(metadata, "atr_value"), EntryFee: fee, EntryRank: metadataIntValue(metadata, "entry_rank"), RegimeState: metadata["regime_state"], BreadthRatio: metadataFloatValue(metadata, "breadth_ratio"), ModelVersion: metadata["model_version"], PredictedProb: metadataFloat64(metadata, "predicted_probability"), PredictedEV: metadataFloat64(metadata, "predicted_ev")}
			}
		} else {
			pos := ledger.positions[symbol]
			ledger.cash += quantity*price - fee
			entryFee := pos.EntryFee * quantity / pos.Size
			pnl := (price-pos.EntryPrice)*quantity - entryFee - fee
			ledger.trades = append(ledger.trades, Trade{Symbol: symbol, EntryTime: pos.EntryTime, ExitTime: fill.FilledAt, EntryPrice: pos.EntryPrice, ExitPrice: price, Size: quantity, Pnl: pnl, PnlPercent: pnl / (pos.EntryPrice * quantity) * 100, Reason: intent.Reason, HoldBars: pos.BarsHeld, EntryRank: pos.EntryRank, RegimeState: pos.RegimeState, BreadthRatio: pos.BreadthRatio, UniverseMode: ledger.config.UniverseMode, PolicyVersion: intent.Versions.Policy, RolloutState: ledger.config.Governance.RolloutState, ExperimentID: ledger.config.Governance.ExperimentID, ModelVersion: pos.ModelVersion, PredictedProbability: cloneFloat64Ptr(pos.PredictedProb), PredictedEV: cloneFloat64Ptr(pos.PredictedEV)})
			pos.Size -= quantity
			pos.EntryFee -= entryFee
			if pos.Size <= 1e-12 {
				delete(ledger.positions, symbol)
			}
		}
		ledger.events = append(ledger.events, backtestLedgerEvent{Side: string(intent.Side), Symbol: symbol, Quantity: fill.Quantity.Decimal().String(), Price: fill.Price.Decimal().String(), Fee: fill.Fee.Decimal().String(), CostVersion: fill.CostModelVersion, ExecutionReferencePrice: metadata["execution_reference_price"], Reason: intent.Reason, SignalAt: intent.SignalAt, DecisionAt: intent.DecisionAt, OrderAt: fill.OrderedAt, At: fill.FilledAt, CashAfter: decimalString(ledger.cash)})
	}
	return nil
}
func metadataFloat64(values map[string]string, key string) *float64 {
	raw := values[key]
	if raw == "" {
		return nil
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil
	}
	return &value
}
func metadataFloatValue(values map[string]string, key string) float64 {
	value := metadataFloat64(values, key)
	if value == nil {
		return 0
	}
	return *value
}
func metadataIntValue(values map[string]string, key string) int {
	value, _ := strconv.Atoi(values[key])
	return value
}

func runSharedBacktestExit(ledger *backtestMemoryLedger, config BacktestConfig, pos *positionState, signalPrice, executionPrice float64, signalAt, decisionAt, orderAt, fillAt time.Time, reason string) error {
	instrument, err := backtestInstrument(config, pos.Symbol)
	if err != nil {
		return err
	}
	price, err := corePrice(signalPrice)
	if err != nil {
		return err
	}
	accountName := config.AccountID
	if accountName == "" {
		accountName = "backtest"
	}
	account, _ := tradingcore.NewAccountID(accountName)
	corePosition := tradingcore.Position{ID: mustPositionID(pos.Symbol), Instrument: instrument, Quantity: mustQuantity(pos.Size), AveragePrice: mustPrice(pos.EntryPrice), MarkPrice: price, OpenedAt: pos.EntryTime, RealizedPnL: mustAmount(0)}
	portfolio, err := tradingcore.NewPortfolioSnapshot(decisionAt, account, tradingcore.ExecutionPaper, map[tradingcore.AssetID]tradingcore.SignedAmount{instrument.QuoteAsset: mustAmount(ledger.cash)}, []tradingcore.Position{corePosition}, nil, ledger.riskState())
	if err != nil {
		return err
	}
	universe, _ := tradingcore.NewUniverseSnapshot(decisionAt, "backtest-universe-v1", string(config.UniverseMode), nil)
	policyVersion := backtestPolicyVersion(config)
	snapshot, err := tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{MarketObservedAt: signalAt, SignalAt: signalAt, DecisionAt: decisionAt, Universe: universe, Portfolio: portfolio, Versions: tradingcore.VersionContext{Strategy: config.StrategyVersion, Settings: config.ConfigVersion, Policy: policyVersion, Dataset: config.DatasetManifestID}})
	if err != nil {
		return err
	}
	id, _ := tradingcore.NewOrderID("exit-" + strings.ToLower(pos.Symbol) + "-" + strconv.FormatInt(decisionAt.UnixNano(), 10))
	key, _ := tradingcore.NewIdempotencyKey("exit-" + strings.ToLower(pos.Symbol) + "-" + strconv.FormatInt(decisionAt.UnixNano(), 10))
	intent, _ := tradingcore.NewOrderIntent(tradingcore.OrderIntent{ID: id, IdempotencyKey: key, AccountID: account, Instrument: instrument, Side: tradingcore.Sell, Type: tradingcore.MarketOrder, Quantity: mustQuantity(pos.Size), ReferencePrice: tradingcore.SomePrice(price), SignalAt: signalAt, DecisionAt: decisionAt, CreatedAt: orderAt, ExecutionMode: tradingcore.ExecutionPaper, QuantitySemantics: tradingcore.QuantityExitAll, Priority: 1, Reason: reason, Horizon: "15m", Versions: snapshot.Versions()}, nil)
	batch, _ := tradingcore.NewDecisionBatch([]tradingcore.OrderIntent{intent})
	result := tradingcore.NewStrategyResult(batch, nil)
	limit := mustAmount(999999999999)
	lotSize, priceTick, minQuantity, minNotional, err := constraintValues(config, pos.Symbol, fillAt)
	if err != nil {
		return err
	}
	policy := tradingcore.RiskPolicy{Version: policyVersion, MaxPositions: config.MaxPositions, MaxGrossExposure: limit, MaxPositionValue: limit, MaxTurnover: mustAmount(0), CashReserve: mustAmount(0), MaxConcurrentOrders: config.MaxPositions, LotSize: mustQuantity(lotSize), ExecutionCosts: tradingcore.ExecutionCostPolicy{Version: config.ExecutionPolicy.CostVersion, FeeBPS: int64(config.FeeBps), AdverseSlippageBPS: int64(config.SlippageBps)}}
	execPrice, err := corePrice(executionPrice)
	if err != nil {
		return err
	}
	broker := tradingcore.NewBacktestBroker(tradingcore.NewFixedClock(fillAt), tradingcore.NewSequenceIDGenerator("exit-fill", uint64(len(ledger.events)+1)), tradingcore.CostModel{FeeBPS: int64(config.FeeBps), SlippageBPS: int64(config.SlippageBps), Version: config.ExecutionPolicy.CostVersion, ExecutionPrice: tradingcore.SomePrice(execPrice), PriceTick: priceTick, MinQuantity: minQuantity, MinNotional: minNotional})
	runner := tradingcore.Orchestrator{Source: backtestDecisionSource{snapshot, policy}, Strategy: tradingcore.FixedStrategy{Result: result}, Risk: tradingcore.PortfolioRiskEngine{}, Broker: broker, Ledger: ledger, Observer: ledger}
	runResult, runErr := runner.Run(context.Background())
	err = runErr
	if err == nil {
		ledger.recordRun(runResult, signalAt, decisionAt)
	}
	return err
}

func constraintValues(config BacktestConfig, symbol string, at time.Time) (float64, string, string, string, error) {
	constraint, ok := SymbolConstraints{}, false
	if config.ConstraintResolver != nil {
		var err error
		constraint, err = config.ConstraintResolver(symbol, at)
		if err != nil {
			return 0, "", "", "", err
		}
		ok = true
	}
	if !ok {
		constraint, ok = config.ExecutionPolicy.Constraints[symbol]
	}
	if !ok {
		if config.DatasetManifestRequired {
			return 0, "", "", "", fmt.Errorf("historical constraints unavailable for %s at %s", symbol, at.UTC().Format(time.RFC3339Nano))
		}
		return .00000001, "", "", "", nil
	}
	lot := constraint.QuantityStep
	if lot <= 0 {
		lot = .00000001
	}
	tick := ""
	if constraint.PriceTick > 0 {
		tick = decimalString(constraint.PriceTick)
	}
	minimum := ""
	if constraint.MinQuantity > 0 {
		minimum = decimalString(constraint.MinQuantity)
	}
	minimumNotional := ""
	if constraint.MinNotional > 0 {
		minimumNotional = decimalString(constraint.MinNotional)
	}
	if config.DatasetManifestRequired && (constraint.QuantityStep <= 0 || constraint.PriceTick <= 0 || constraint.MinQuantity <= 0) {
		return 0, "", "", "", fmt.Errorf("invalid historical constraints for %s at %s", symbol, at.UTC().Format(time.RFC3339Nano))
	}
	return lot, tick, minimum, minimumNotional, nil
}

func backtestPolicyVersion(config BacktestConfig) string {
	if value := config.Governance.PolicyVersions.CompositeVersion; value != "" {
		return value
	}
	return "backtest-risk-v1"
}

func backtestInstrument(config BacktestConfig, symbol string) (tradingcore.Instrument, error) {
	normalized := strings.ToUpper(symbol)
	quoteName := strings.ToUpper(config.SettlementCurrency)
	if quoteName == "" {
		quoteName = "USDT"
	}
	baseName := strings.TrimSuffix(normalized, quoteName)
	if identity := config.EconomicAssetIdentities[normalized]; identity != "" {
		baseName = identity
	}
	id, err := tradingcore.NewInstrumentID(strings.ToLower(baseName + "-" + quoteName))
	if err != nil {
		return tradingcore.Instrument{}, err
	}
	base, _ := tradingcore.NewAssetID(baseName)
	quote, _ := tradingcore.NewAssetID(quoteName)
	venueName := config.VenueID
	if venueName == "" {
		venueName = "binance"
	}
	venue, _ := tradingcore.NewVenueID(venueName)
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
