package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"trading-go/internal/accounting"
	"trading-go/internal/cutover"
	"trading-go/internal/database"
	ledgerpkg "trading-go/internal/ledger"
	"trading-go/internal/operations"
	"trading-go/internal/tradingcore"
)

type runtimeDecisionSource struct {
	snapshot tradingcore.DecisionContext
	policy   tradingcore.RiskPolicy
}

func (source runtimeDecisionSource) DecisionContext(context.Context) (tradingcore.DecisionContext, tradingcore.RiskPolicy, error) {
	return source.snapshot, source.policy, nil
}

type discardOutcomeLedger struct{}

func (discardOutcomeLedger) RecordBrokerOutcome(context.Context, tradingcore.DecisionBatch, tradingcore.BrokerBatchOutcome) error {
	return nil
}

type runtimeDecisionObserver struct{ observations []tradingcore.Observation }

func (observer *runtimeDecisionObserver) Observe(_ context.Context, value tradingcore.Observation) error {
	value.Metadata = cloneStringMap(value.Metadata)
	observer.observations = append(observer.observations, value)
	return nil
}
func cloneStringMap(values map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range values {
		result[key] = value
	}
	return result
}

func executeShortlistTradesShared(analyses []AnalyzedCoin, universe *UniverseSelectionResult, settings map[string]string, mode tradingcore.ExecutionMode) ([]AnalyzedCoin, int, error) {
	now := time.Now().UTC()
	captureMode := mode
	if mode == tradingcore.ExecutionShadow {
		captureMode = tradingcore.ExecutionPaper
	}
	snapshot, policy, err := buildRuntimeDecisionContext(analyses, universe, settings, captureMode, now)
	if err != nil {
		return analyses, 0, err
	}
	if mode == tradingcore.ExecutionShadow {
		// The candidate receives only the immutable snapshot and policy. It has no
		// broker, ledger, repository, broadcaster, settings store, or service
		// handle, so an approved intent is an inert observed outcome.
		return observeSharedCandidate(context.Background(), analyses, snapshot, policy)
	}
	feeBPS := int64(getSettingInt(settings, "paper_fee_bps", 10))
	slippageBPS := int64(getSettingInt(settings, "paper_slippage_bps", 5))
	broker := tradingcore.Broker(tradingcore.NewPaperBroker(tradingcore.NewFixedClock(now), tradingcore.RandomIDGenerator{Prefix: "paper-fill"}, tradingcore.CostModel{FeeBPS: feeBPS, SlippageBPS: slippageBPS, Version: "paper-cost-v1"}))
	ledger := tradingcore.FillLedger(ledgerpkg.NewContractAdapter(database.DB))
	observer := &runtimeDecisionObserver{}
	runner := tradingcore.Orchestrator{Source: runtimeDecisionSource{snapshot, policy}, Strategy: tradingcore.LegacyRuleStrategy{}, Risk: tradingcore.PortfolioRiskEngine{}, Broker: broker, Ledger: ledger, Observer: observer}
	result, err := runner.Run(context.Background())
	if err != nil {
		operations.RecordBrokerConflict("shared-engine", err)
		return analyses, 0, err
	}
	decisionBySymbol := map[string][2]string{}
	executed := map[string]bool{}
	for _, noAction := range result.Strategy.NoActions() {
		decisionBySymbol[noAction.Instrument.VenueSymbol] = [2]string{"skip", noAction.Code}
	}
	for _, rejection := range result.Risk.Rejected() {
		for _, intent := range result.Strategy.Intents().Intents() {
			if intent.ID == rejection.OrderID {
				decisionBySymbol[intent.Instrument.VenueSymbol] = [2]string{"skip", string(rejection.Code)}
			}
		}
	}
	for _, rejection := range result.Broker.Rejected() {
		for _, intent := range result.Risk.Approved().Intents() {
			if intent.ID == rejection.OrderID {
				decisionBySymbol[intent.Instrument.VenueSymbol] = [2]string{"buy_failed", string(rejection.Code)}
			}
		}
	}
	for _, accepted := range result.Broker.Accepted() {
		for _, intent := range result.Risk.Approved().Intents() {
			if intent.ID == accepted.OrderID {
				decisionBySymbol[intent.Instrument.VenueSymbol] = [2]string{"buy", "order_executed"}
				executed[intent.Instrument.VenueSymbol] = true
			}
		}
	}
	opened := 0
	for i := range analyses {
		if decision, ok := decisionBySymbol[strings.ToUpper(analyses[i].Symbol)]; ok {
			analyses[i].Decision, analyses[i].DecisionReason = decision[0], decision[1]
			value := executed[strings.ToUpper(analyses[i].Symbol)] && mode == tradingcore.ExecutionPaper
			analyses[i].TradeExecuted = &value
			if value {
				opened++
			}
		}
		decision := analyses[i].Decision
		reason := analyses[i].DecisionReason
		autoTrade := getSettingBool(settings, "auto_trade_enabled", false)
		stage08Context := "{}"
		if flags, active := cutover.Active(); active {
			stage08Context = flags.ObservationContext(string(mode), map[string]string{"engine": "shared-engine-v1", "strategy": tradingcore.LegacyStrategyVersion, "policy": analyses[i].PolicyVersion, "model": analyses[i].ModelVersion, "universe": analyses[i].UniverseMode})
		}
		history := database.TrendAnalysisHistory{Symbol: analyses[i].Symbol, Timeframe: "15m", ModelVersion: analyses[i].ModelVersion, PolicyVersion: analyses[i].PolicyVersion, UniverseMode: analyses[i].UniverseMode, RolloutState: analyses[i].RolloutState, ExperimentID: stringPtr(analyses[i].ExperimentID), PredictionLogID: analyses[i].PredictionLogID, CurrentPrice: &analyses[i].Price, Change24h: &analyses[i].Change24h, FinalSignal: &analyses[i].Signal, FinalRating: &analyses[i].Rating, AutoTrade: &autoTrade, Decision: &decision, DecisionReason: &reason, DecisionContextJSON: string(result.Trace), Stage08ContextJSON: stage08Context, AnalyzedAt: now}
		if err := database.DB.Create(&history).Error; err != nil {
			return analyses, opened, fmt.Errorf("persist shared decision history: %w", err)
		}
	}
	if opened > 0 {
		broadcastTradeUpdates()
		NotifyPositionChanged()
	}
	return analyses, opened, nil
}

func observeSharedCandidate(ctx context.Context, analyses []AnalyzedCoin, snapshot tradingcore.DecisionContext, policy tradingcore.RiskPolicy) ([]AnalyzedCoin, int, error) {
	strategy, err := (tradingcore.LegacyRuleStrategy{}).Decide(ctx, snapshot)
	if err != nil {
		return analyses, 0, err
	}
	risk, err := (tradingcore.PortfolioRiskEngine{}).Evaluate(ctx, strategy.Intents(), snapshot.Portfolio(), policy)
	if err != nil {
		return analyses, 0, err
	}
	bySymbol := map[string][2]string{}
	for _, value := range strategy.NoActions() {
		bySymbol[strings.ToUpper(value.Instrument.VenueSymbol)] = [2]string{"skip", value.Code}
	}
	intents := map[string]tradingcore.OrderIntent{}
	for _, value := range strategy.Intents().Intents() {
		intents[value.ID.String()] = value
	}
	for _, value := range risk.Rejected() {
		if intent, ok := intents[value.OrderID.String()]; ok {
			bySymbol[strings.ToUpper(intent.Instrument.VenueSymbol)] = [2]string{"skip", string(value.Code)}
		}
	}
	approved := map[string]tradingcore.OrderIntent{}
	for _, intent := range risk.Approved().Intents() {
		symbol := strings.ToUpper(intent.Instrument.VenueSymbol)
		bySymbol[symbol] = [2]string{"buy", "approved_observation"}
		approved[symbol] = intent
	}
	for i := range analyses {
		decision := bySymbol[strings.ToUpper(analyses[i].Symbol)]
		if decision[0] == "" {
			decision = [2]string{"skip", "not_in_candidate_population"}
		}
		analyses[i].ShadowDecision, analyses[i].ShadowReason = decision[0], decision[1]
		if intent, ok := approved[strings.ToUpper(analyses[i].Symbol)]; ok {
			analyses[i].DecisionSide = string(intent.Side)
			analyses[i].DecisionQuantity = intent.Quantity.Decimal().String()
			if price, ok := intent.ReferencePrice.Get(); ok {
				q, _ := strconv.ParseFloat(analyses[i].DecisionQuantity, 64)
				p, _ := strconv.ParseFloat(price.Decimal().String(), 64)
				analyses[i].DecisionNotional = strconv.FormatFloat(q*p, 'g', -1, 64)
			}
		}
		analyses[i].Decision, analyses[i].DecisionReason = "shadow_only", "shadow_observation"
		value := false
		analyses[i].TradeExecuted = &value
	}
	return analyses, 0, nil
}

func buildRuntimeDecisionContext(analyses []AnalyzedCoin, universe *UniverseSelectionResult, settings map[string]string, mode tradingcore.ExecutionMode, now time.Time) (tradingcore.DecisionContext, tradingcore.RiskPolicy, error) {
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
	}
	account, err := tradingcore.NewAccountID(defaultString(wallet.AccountID, "primary"))
	if err != nil {
		return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
	}
	quoteAsset, err := tradingcore.NewAssetID(strings.ToUpper(wallet.Currency))
	if err != nil {
		return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
	}
	venue, err := tradingcore.NewVenueID(defaultString(settings["exchange_venue_id"], "binance"))
	if err != nil {
		return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
	}
	cash, err := coreAmount(wallet.Balance)
	if err != nil {
		return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
	}
	coreSettings := make(map[string]string, len(settings)+len(analyses)*8)
	for k, v := range settings {
		coreSettings[k] = v
	}
	flags, activeFlags := cutover.Active()
	if activeFlags {
		coreSettings["stage08_flag_schema"] = flags.SchemaVersion
		coreSettings["stage08_ledger_authority"] = flags.LedgerAuthority
		coreSettings["stage08_shared_engine"] = flags.SharedEngine
		coreSettings["stage08_dual_run"] = flags.DualRun
	}
	coreSettings["model_available"] = fmt.Sprint(hasModelRankings(analyses))
	candidates := make([]tradingcore.UniverseCandidate, 0, len(analyses))
	quotes := map[tradingcore.InstrumentID]tradingcore.Quote{}
	instruments := map[string]tradingcore.Instrument{}
	marketPolicy := shortlistMarketGatePolicy{Enabled: getSettingBool(settings, "regime_gate_enabled", true), RegimeTimeframe: getSettingString(settings, "regime_timeframe", "1h"), RegimeEMAFast: getSettingInt(settings, "regime_ema_fast", 50), EMASlow: getSettingInt(settings, "regime_ema_slow", 200), VolATRPeriod: getSettingInt(settings, "vol_atr_period", 14), VolRatioMin: getSettingFloat(settings, "vol_ratio_min", .002), VolRatioMax: getSettingFloat(settings, "vol_ratio_max", .02)}
	runtime := productionShortlistRuntime{}
	portfolioValue := computePortfolioValue(wallet)
	for index, analysis := range analyses {
		symbol := strings.ToUpper(analysis.Symbol)
		baseName := strings.TrimSuffix(symbol, strings.ToUpper(wallet.Currency))
		base, _ := tradingcore.NewAssetID(baseName)
		instrumentID, _ := tradingcore.NewInstrumentID(strings.ToLower(baseName + "-" + wallet.Currency))
		instrument, err := tradingcore.NewInstrument(instrumentID, base, quoteAsset, venue, symbol)
		if err != nil {
			return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
		}
		instruments[symbol] = instrument
		price, err := corePrice(analysis.Price)
		if err != nil {
			return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
		}
		quotes[instrument.ID] = tradingcore.Quote{Instrument: instrument, Bid: price, Ask: price, Last: price, ObservedAt: now}
		score, err := coreDecimal(analysis.Rating)
		if err != nil {
			return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
		}
		candidates = append(candidates, tradingcore.UniverseCandidate{Instrument: instrument, Rank: index + 1, Score: score, Eligible: true, MembershipSource: "runtime_shortlist", MembershipVersion: "stage02"})
		id := instrument.ID.String()
		coreSettings["signal."+id] = analysis.Signal
		coreSettings["rating."+id] = fmt.Sprint(analysis.Rating)
		coreSettings["analysis_error."+id] = fmt.Sprint(analysis.Error != "")
		if analysis.PolicySelected != nil {
			coreSettings["model_selected."+id] = fmt.Sprint(*analysis.PolicySelected)
		}
		probOK := analysis.ProbUp != nil && analysis.ExpectedValue != nil && *analysis.ProbUp >= getSettingFloat(settings, "selection_policy_min_prob", .53) && *analysis.ExpectedValue >= getSettingFloat(settings, "selection_policy_min_ev", .001)
		coreSettings["model_floor_ok."+id] = fmt.Sprint(probOK)
		regimeOK, volOK := runtime.MarketGates(&analysis, universe, marketPolicy)
		coreSettings["regime_ok."+id] = fmt.Sprint(regimeOK)
		coreSettings["vol_ok."+id] = fmt.Sprint(volOK)
		if getSettingBool(settings, "vol_sizing_enabled", false) || getSettingBool(settings, "atr_trailing_enabled", false) {
			candles, candleErr := fetchCandles(symbol, "15m", 200)
			if candleErr != nil {
				return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, candleErr
			}
			atr := getAtrValue(candles, getSettingInt(settings, "atr_trailing_period", 14), getSettingBool(settings, "atr_annualization_enabled", false), 15, getSettingInt(settings, "atr_annualization_days", 365))
			coreSettings["atr_value."+id] = strconv.FormatFloat(atr, 'f', -1, 64)
			coreSettings["atr_trailing_mult."+id] = strconv.FormatFloat(getSettingFloat(settings, "atr_trailing_mult", 1), 'f', -1, 64)
			if getSettingBool(settings, "vol_sizing_enabled", false) {
				_, size, stop, target, maxBars, sizeErr := computePositionSize(atr, analysis.Price, wallet.Balance, portfolioValue, settings)
				if sizeErr != nil {
					return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, sizeErr
				}
				coreSettings["requested_quantity."+id] = strconv.FormatFloat(size, 'f', -1, 64)
				coreSettings["stop_price."+id] = strconv.FormatFloat(stop, 'f', -1, 64)
				coreSettings["take_profit_price."+id] = strconv.FormatFloat(target, 'f', -1, 64)
				if maxBars != nil {
					coreSettings["max_bars_held."+id] = strconv.Itoa(*maxBars)
				}
			}
		}
	}
	policyVersion := "legacy-risk-v1"
	for _, analysis := range analyses {
		if analysis.PolicyVersion != "" {
			policyVersion = analysis.PolicyVersion
			break
		}
	}
	universeSnapshot, err := tradingcore.NewUniverseSnapshot(now, policyVersion, "runtime_shortlist", candidates)
	if err != nil {
		return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
	}
	var rows []database.Position
	if err := database.DB.Where("status = ?", "open").Find(&rows).Error; err != nil {
		return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
	}
	positions := make([]tradingcore.Position, 0, len(rows))
	gross := accounting.Zero()
	for _, row := range rows {
		symbol := PositionPairSymbol(row.Symbol, wallet.Currency)
		instrument, ok := instruments[symbol]
		if !ok {
			baseName := strings.TrimSuffix(symbol, strings.ToUpper(wallet.Currency))
			base, baseErr := tradingcore.NewAssetID(baseName)
			instrumentID, idErr := tradingcore.NewInstrumentID(strings.ToLower(baseName + "-" + wallet.Currency))
			if baseErr != nil || idErr != nil {
				return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, fmt.Errorf("invalid open position instrument %s", symbol)
			}
			instrument, err = tradingcore.NewInstrument(instrumentID, base, quoteAsset, venue, symbol)
			if err != nil {
				return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
			}
		}
		if row.AmountExact == nil {
			return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, ledgerpkg.ErrProjectionUnavailable
		}
		quantity, err := coreQuantity(row.AmountExact.String())
		if err != nil {
			return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
		}
		mark := positionPriceForExecution(row)
		price, err := corePrice(mark)
		if err != nil {
			return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
		}
		positionID, _ := tradingcore.NewPositionID(fmt.Sprint(row.ID))
		positions = append(positions, tradingcore.Position{ID: positionID, Instrument: instrument, Quantity: quantity, AveragePrice: price, MarkPrice: price, OpenedAt: row.OpenedAt, RealizedPnL: mustCoreAmount("0"), PyramidLayers: 1})
		gross = gross.Add(row.AmountExact.Mul(accounting.MustParse(strconv.FormatFloat(mark, 'f', -1, 64))))
	}
	var orderRows []database.Order
	if err := database.DB.Where("status IN ?", []string{OrderStatusPending, OrderStatusSubmitted, string(tradingcore.BrokerAccepted), string(tradingcore.BrokerPartiallyFilled)}).Find(&orderRows).Error; err != nil {
		return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
	}
	pending := make([]tradingcore.PendingOrder, 0, len(orderRows))
	for _, row := range orderRows {
		if row.RemainingQuantityExact == nil || row.RemainingQuantityExact.Sign() <= 0 {
			return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, ledgerpkg.ErrProjectionUnavailable
		}
		symbol := PositionPairSymbol(row.Symbol, wallet.Currency)
		instrument, ok := instruments[symbol]
		if !ok {
			baseName := strings.TrimSuffix(symbol, strings.ToUpper(wallet.Currency))
			base, _ := tradingcore.NewAssetID(baseName)
			instrumentID, _ := tradingcore.NewInstrumentID(strings.ToLower(baseName + "-" + wallet.Currency))
			instrument, err = tradingcore.NewInstrument(instrumentID, base, quoteAsset, venue, symbol)
			if err != nil {
				return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
			}
		}
		quantity, quantityErr := coreQuantity(row.RemainingQuantityExact.String())
		if quantityErr != nil {
			return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, quantityErr
		}
		orderID, _ := tradingcore.NewOrderID(fmt.Sprint(row.ID))
		side := tradingcore.Buy
		if strings.EqualFold(row.OrderType, "sell") {
			side = tradingcore.Sell
		}
		submitted := row.ExecutedAt
		if row.SubmittedAt != nil {
			submitted = *row.SubmittedAt
		}
		pending = append(pending, tradingcore.PendingOrder{ID: orderID, Instrument: instrument, Side: side, Remaining: quantity, SubmittedAt: submitted})
	}
	var historicalFills []database.Fill
	if err := database.DB.Where("account_id = ? AND occurred_at <= ?", account.String(), now).Find(&historicalFills).Error; err != nil {
		return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
	}
	turnover := accounting.Zero()
	for _, fill := range historicalFills {
		turnover = turnover.Add(fill.GrossAmount)
	}
	riskState, err := tradingcore.NewRiskStateWithTurnover(mustCoreAmount(gross.String()), mustCoreAmount("0"), mustCoreAmount("0"), mustCoreAmount("0"), mustCoreAmount(turnover.String()))
	if err != nil {
		return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
	}
	portfolio, err := tradingcore.NewPortfolioSnapshot(now, account, mode, map[tradingcore.AssetID]tradingcore.SignedAmount{quoteAsset: cash}, positions, pending, riskState)
	if err != nil {
		return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
	}
	versions := tradingcore.VersionContext{Engine: "shared-engine-v1", Strategy: tradingcore.LegacyStrategyVersion, Settings: "database-settings", Policy: policyVersion, Model: getSettingString(settings, "active_model_version", ""), Dataset: getSettingString(settings, "backtest_dataset_manifest_id", ""), Universe: getSettingString(settings, "universe_policy_version", "runtime-universe")}
	if activeFlags {
		versions.FlagSchema = flags.SchemaVersion
	}
	contextSnapshot, err := tradingcore.NewDecisionContext(tradingcore.DecisionContextInput{MarketObservedAt: now, SignalAt: now, DecisionAt: now, Quotes: quotes, Universe: universeSnapshot, Portfolio: portfolio, Settings: coreSettings, Versions: versions})
	if err != nil {
		return tradingcore.DecisionContext{}, tradingcore.RiskPolicy{}, err
	}
	maxPosition := getSettingFloat(settings, "max_position_value", 0)
	maxGross := wallet.Balance + gross.Float64()
	maxTurnover := getSettingFloat(settings, "max_turnover", maxGross)
	policy := tradingcore.RiskPolicy{Version: policyVersion, MaxPositions: getSettingInt(settings, "max_positions", 5), MaxGrossExposure: mustCoreAmount(strconv.FormatFloat(maxGross, 'f', -1, 64)), MaxPositionValue: mustCoreAmount(strconv.FormatFloat(maxPosition, 'f', -1, 64)), MaxTurnover: mustCoreAmount(strconv.FormatFloat(maxTurnover, 'f', -1, 64)), CashReserve: mustCoreAmount("0"), MaxConcurrentOrders: getSettingInt(settings, "max_positions", 5), PyramidingEnabled: getSettingBool(settings, "pyramiding_enabled", false), MaxPyramidLayers: getSettingInt(settings, "max_pyramid_layers", 3), LotSize: mustCoreQuantity("0.00000001"), ExecutionCosts: tradingcore.ExecutionCostPolicy{Version: "paper-cost-v1", FeeBPS: int64(getSettingInt(settings, "paper_fee_bps", 10)), AdverseSlippageBPS: int64(getSettingInt(settings, "paper_slippage_bps", 5))}}
	return contextSnapshot, policy, nil
}

func markSharedEngineFailure(analyses []AnalyzedCoin, err error) []AnalyzedCoin {
	for i := range analyses {
		value := false
		analyses[i].TradeExecuted = &value
		analyses[i].Decision = "buy_failed"
		analyses[i].DecisionReason = "shared_engine_error: " + err.Error()
	}
	return analyses
}
func coreDecimal(value float64) (tradingcore.Decimal, error) {
	return tradingcore.ParseDecimal(strconv.FormatFloat(value, 'f', -1, 64))
}
func coreAmount(value float64) (tradingcore.SignedAmount, error) {
	decimal, err := coreDecimal(value)
	if err != nil {
		return tradingcore.SignedAmount{}, err
	}
	return tradingcore.NewSignedAmount(decimal)
}
func corePrice(value float64) (tradingcore.Price, error) {
	decimal, err := coreDecimal(value)
	if err != nil {
		return tradingcore.Price{}, err
	}
	return tradingcore.NewPrice(decimal)
}
func coreQuantity(value string) (tradingcore.Quantity, error) {
	decimal, err := tradingcore.ParseDecimal(value)
	if err != nil {
		return tradingcore.Quantity{}, err
	}
	return tradingcore.NewQuantity(decimal)
}
func mustCoreAmount(value string) tradingcore.SignedAmount {
	decimal, err := tradingcore.ParseDecimal(value)
	if err != nil {
		panic(err)
	}
	result, err := tradingcore.NewSignedAmount(decimal)
	if err != nil {
		panic(err)
	}
	return result
}
func mustCoreQuantity(value string) tradingcore.Quantity {
	decimal, err := tradingcore.ParseDecimal(value)
	if err != nil {
		panic(err)
	}
	result, err := tradingcore.NewQuantity(decimal)
	if err != nil {
		panic(err)
	}
	return result
}
