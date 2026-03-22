package backtest

import (
	"fmt"
	"sort"
	"time"
	"trading-go/internal/services"
)

type positionState struct {
	Symbol        string
	EntryPrice    float64
	Size          float64
	EntryRank     int
	RegimeState   string
	BreadthRatio  float64
	ModelVersion  string
	PredictedProb *float64
	PredictedEV   *float64
	StopPrice     *float64
	TakeProfit    *float64
	TrailingStop  *float64
	HighestPrice  float64
	BarsHeld      int
	EntryTime     time.Time
	LastAtr       float64
	EntryFee      float64
}

type barContext struct {
	Rating float64
	Signal string
	Atr    float64
}

type entryCandidate struct {
	Symbol     string
	Rank       int
	Prediction *services.ModelPrediction
}

type backtestUniverseSelection struct {
	RegimeState    string
	BreadthRatio   float64
	ActiveUniverse []services.UniverseCandidateMetrics
	Shortlist      []services.UniverseCandidateMetrics
}

type symbolState struct {
	series       []services.OHLCV
	indexByTime  map[int64]int
	lastIndex    int
	lastPrice    float64
	currentIndex int
}

func RunBacktest(config BacktestConfig, series map[string][]services.OHLCV) (BacktestResult, error) {
	if config.InitialBalance <= 0 {
		return BacktestResult{}, fmt.Errorf("initial balance must be > 0")
	}
	indicatorConfig := config.IndicatorConfig
	if indicatorConfig == (services.IndicatorConfig{}) {
		indicatorConfig = services.GetIndicatorSettings()
	}
	indicatorWeights := config.IndicatorWeights
	if len(indicatorWeights) == 0 {
		indicatorWeights = services.GetIndicatorWeights()
	}
	prepared := filterSeriesWindow(series, config.Start, config.End)
	if len(prepared) == 0 {
		return BacktestResult{}, fmt.Errorf("no data available")
	}

	symbols := sortedSymbols(prepared)
	states := buildSymbolStates(prepared)
	timeline := buildTimeline(prepared)
	if len(timeline) == 0 {
		return BacktestResult{}, fmt.Errorf("no data available")
	}

	policy := services.ExitPolicy{
		StopLossPercent:     config.StopLossPercent,
		TakeProfitPercent:   config.TakeProfitPercent,
		TrailingStopEnabled: config.TrailingStopEnabled,
		TrailingStopPercent: config.TrailingStopPercent,
		ATRTrailingEnabled:  config.AtrTrailingEnabled,
		ATRTrailingMult:     config.AtrTrailingMult,
		AllowSellAtLoss:     config.AllowSellAtLoss,
		TimeStopBars:        config.TimeStopBars,
		SellOnSignal:        config.SellOnSignal,
		MinConfidenceToSell: config.MinConfidenceToSell,
	}

	lookback := computeSignalLookback(config)
	cash := config.InitialBalance
	positions := map[string]*positionState{}
	var equity []EquityPoint
	equityBySymbol := map[string][]EquityPoint{}
	var trades []Trade
	var concurrentPositionCounts []int
	for _, symbol := range symbols {
		equityBySymbol[symbol] = []EquityPoint{}
	}

	currentUniverse := backtestUniverseSelection{}
	lastRebalance := time.Time{}

	for _, ts := range timeline {
		currentTime := time.UnixMilli(ts)
		contexts := map[string]barContext{}
		currentBars := map[string]services.OHLCV{}

		for _, symbol := range symbols {
			state := states[symbol]
			idx, ok := state.indexByTime[ts]
			if !ok {
				continue
			}
			state.currentIndex = idx
			state.lastIndex = idx
			state.lastPrice = state.series[idx].Close
			bar := state.series[idx]
			currentBars[symbol] = bar
			if idx < lookback {
				continue
			}
			window := buildCandles(state.series, idx, lookback)
			rating, signal := services.AnalyzeCandlesWithConfig(window, indicatorConfig, indicatorWeights)
			contexts[symbol] = barContext{Rating: rating, Signal: signal, Atr: computeAtr(window, config)}
		}

		if config.UniverseMode == UniverseDynamicRecompute {
			if lastRebalance.IsZero() || currentTime.Sub(lastRebalance) >= config.UniversePolicy.RebalanceInterval {
				currentUniverse = buildBacktestUniverseSelection(config, states, currentBars, lookback, true)
				lastRebalance = currentTime
			}
		} else {
			currentUniverse = buildBacktestUniverseSelection(config, states, currentBars, lookback, false)
		}

		for _, symbol := range symbols {
			pos := positions[symbol]
			if pos == nil {
				continue
			}
			bar, ok := currentBars[symbol]
			if !ok {
				continue
			}
			ctx, ok := contexts[symbol]
			if !ok {
				continue
			}
			pos.BarsHeld++
			if bar.High > pos.HighestPrice {
				pos.HighestPrice = bar.High
			}

			if config.AtrTrailingEnabled && config.AtrTrailingMult > 0 {
				if ctx.Atr > 0 {
					pos.LastAtr = ctx.Atr
					pos.TrailingStop = services.RatchetATRTrailingStop(pos.TrailingStop, bar.High, pos.EntryPrice, ctx.Atr, config.AtrTrailingMult)
				}
			} else if config.TrailingStopEnabled {
				pos.TrailingStop = services.RatchetPercentTrailingStop(pos.TrailingStop, bar.High, pos.EntryPrice, config.TrailingStopPercent)
			}

			decision := services.EvaluateBarCloseExit(services.ExitEvaluationInput{
				CurrentPrice:      bar.Close,
				HighPrice:         bar.High,
				LowPrice:          bar.Low,
				EntryPrice:        pos.EntryPrice,
				StopPrice:         pos.StopPrice,
				TakeProfitPrice:   pos.TakeProfit,
				TrailingStopPrice: pos.TrailingStop,
				BarsHeld:          pos.BarsHeld,
				Signal:            ctx.Signal,
				SignalRating:      ctx.Rating,
			}, policy)

			if decision.Reason == "" {
				continue
			}
			exitPrice := determineExitPrice(bar, decision, config)
			proceeds := pos.Size * exitPrice
			exitFee := proceeds * (config.FeeBps / 10000)
			cash += proceeds - exitFee
			pnl := (exitPrice-pos.EntryPrice)*pos.Size - pos.EntryFee - exitFee
			trades = append(trades, Trade{
				Symbol:               symbol,
				EntryTime:            pos.EntryTime,
				ExitTime:             time.UnixMilli(bar.CloseTime),
				EntryPrice:           pos.EntryPrice,
				ExitPrice:            exitPrice,
				Size:                 pos.Size,
				Pnl:                  pnl,
				PnlPercent:           pnl / (pos.EntryPrice * pos.Size) * 100,
				Reason:               decision.Reason,
				HoldBars:             pos.BarsHeld,
				EntryRank:            pos.EntryRank,
				RegimeState:          pos.RegimeState,
				BreadthRatio:         pos.BreadthRatio,
				UniverseMode:         config.UniverseMode,
				PolicyVersion:        config.Governance.PolicyVersions.CompositeVersion,
				RolloutState:         config.Governance.RolloutState,
				ExperimentID:         config.Governance.ExperimentID,
				ModelVersion:         pos.ModelVersion,
				PredictedProbability: cloneFloat64Ptr(pos.PredictedProb),
				PredictedEV:          cloneFloat64Ptr(pos.PredictedEV),
			})
			delete(positions, symbol)
		}

		entryCandidates := buildEntryCandidates(config, currentUniverse, states, positions, cash, currentTime, lookback)
		for _, candidate := range entryCandidates {
			if len(positions) >= config.MaxPositions {
				break
			}
			symbol := candidate.Symbol
			if positions[symbol] != nil {
				continue
			}
			bar, ok := currentBars[symbol]
			if !ok {
				continue
			}
			ctx, ok := contexts[symbol]
			if !ok {
				continue
			}
			if config.ModelArtifact == nil {
				if config.BuyOnlyStrong && ctx.Signal != "STRONG_BUY" {
					continue
				}
				if ctx.Signal != "BUY" && ctx.Signal != "STRONG_BUY" {
					continue
				}
				if ctx.Rating < config.MinConfidenceToBuy {
					continue
				}
			}

			portfolioValue := cash + currentPositionsValue(positions, states)
			entryPrice := applySlippage(bar.Close, config.SlippageBps, true)
			amountUsdt, size, stopPrice, takeProfitPrice, err := determinePositionSize(config, ctx.Atr, entryPrice, cash, portfolioValue)
			if err != nil {
				continue
			}
			entryCost := size * entryPrice
			entryFee := entryCost * (config.FeeBps / 10000)
			if entryCost+entryFee > cash {
				continue
			}
			cash -= entryCost + entryFee

			var trailingStop *float64
			if config.AtrTrailingEnabled && ctx.Atr > 0 && config.AtrTrailingMult > 0 {
				entryStop := entryPrice - (ctx.Atr * config.AtrTrailingMult)
				if entryStop > 0 {
					trailingStop = &entryStop
				}
			}

			positions[symbol] = &positionState{
				Symbol:        symbol,
				EntryPrice:    entryPrice,
				Size:          size,
				StopPrice:     stopPrice,
				TakeProfit:    takeProfitPrice,
				TrailingStop:  trailingStop,
				HighestPrice:  entryPrice,
				BarsHeld:      0,
				EntryTime:     currentTime,
				LastAtr:       ctx.Atr,
				EntryFee:      entryFee,
				EntryRank:     candidate.Rank,
				RegimeState:   currentUniverse.RegimeState,
				BreadthRatio:  currentUniverse.BreadthRatio,
				ModelVersion:  modelVersionForCandidate(candidate),
				PredictedProb: predictionProbability(candidate.Prediction),
				PredictedEV:   predictionExpectedValue(candidate.Prediction),
			}

			if amountUsdt <= 0 {
				continue
			}
		}

		totalValue := cash + currentPositionsValue(positions, states)
		equity = append(equity, EquityPoint{Time: currentTime, Value: totalValue})
		concurrentPositionCounts = append(concurrentPositionCounts, len(positions))
		for _, symbol := range symbols {
			value := 0.0
			if pos := positions[symbol]; pos != nil {
				markPrice := states[symbol].lastPrice
				if markPrice <= 0 {
					markPrice = pos.EntryPrice
				}
				value = pos.Size * markPrice
			}
			equityBySymbol[symbol] = append(equityBySymbol[symbol], EquityPoint{Time: currentTime, Value: value})
		}
	}

	metrics := ComputeMetrics(equity, trades, config.TimeframeMinutes, config.AtrAnnualizationDays)
	rankingMetrics := buildRankingMetrics(trades, config)
	diagnostics := buildStrategyDiagnostics(trades, rankingMetrics, config, concurrentPositionCounts)
	return BacktestResult{
		Mode:           config.StrategyMode,
		Metrics:        metrics,
		ModelVersion:   config.Governance.ModelVersion,
		PolicyVersion:  config.Governance.PolicyVersions.CompositeVersion,
		RolloutState:   config.Governance.RolloutState,
		RankingMetrics: rankingMetrics,
		Diagnostics:    diagnostics,
		Equity:         equity,
		EquityBySymbol: equityBySymbol,
		Trades:         trades,
	}, nil
}

func buildBacktestUniverseSelection(config BacktestConfig, states map[string]*symbolState, currentBars map[string]services.OHLCV, lookback int, applyPolicy bool) backtestUniverseSelection {
	barsPerDay := maxInt(1, (24*60)/maxInt(1, config.TimeframeMinutes))
	barsPerHour := maxInt(1, 60/maxInt(1, config.TimeframeMinutes))
	windowSize := maxInt(lookback, barsPerDay*14)

	btcReturn7D := 0.0
	btcHigherTrend := false
	btcDailyTrend := false
	if btcState, ok := states["BTCUSDT"]; ok && btcState.lastIndex >= barsPerDay {
		window := recentWindow(btcState.series, btcState.lastIndex, windowSize)
		daily := aggregateOHLCVByBars(window, barsPerDay)
		hourly := aggregateOHLCVByBars(window, barsPerHour)
		btcReturn7D = services.CalculateReturn(closesFromOHLCV(daily), minInt(7, len(daily)-1))
		btcHigherTrend = backtestRegimeGate(candlesFromOHLCV(hourly), 20, 50)
		btcDailyTrend = backtestRegimeGate(candlesFromOHLCV(daily), 20, 50)
	}

	accepted := make([]services.UniverseCandidateMetrics, 0, len(currentBars))
	for symbol := range currentBars {
		state := states[symbol]
		if state == nil || state.lastIndex < lookback {
			continue
		}
		window := recentWindow(state.series, state.lastIndex, windowSize)
		daily := aggregateOHLCVByBars(window, barsPerDay)
		hourly := aggregateOHLCVByBars(window, barsPerHour)
		if len(daily) < 2 || len(hourly) < 10 {
			continue
		}
		quoteVolume24h := sumQuoteVolume(window[maxInt(0, len(window)-barsPerDay):])
		change24h := services.CalculateReturn(closesFromOHLCV(window), minInt(barsPerDay, len(window)-1))
		candidate := services.BuildUniverseCandidateMetrics(symbol, "", "USDT", currentBars[symbol].Close, change24h, quoteVolume24h, daily, hourly, btcReturn7D)
		candidate.ListingAgeDays = (state.lastIndex + 1) / barsPerDay
		if applyPolicy {
			if rejection := services.UniverseHardFilterReason(candidate, config.UniversePolicy); rejection != "" {
				continue
			}
		}
		accepted = append(accepted, candidate)
	}

	breadth := services.ComputeUniverseBreadth(accepted)
	regime := services.DetermineUniverseRegime(btcHigherTrend, btcDailyTrend, breadth)
	ranked := services.RankUniverseCandidates(accepted, config.UniversePolicy)
	if len(ranked) == 0 {
		return backtestUniverseSelection{RegimeState: regime, BreadthRatio: breadth}
	}

	active := append([]services.UniverseCandidateMetrics(nil), ranked...)
	shortlist := append([]services.UniverseCandidateMetrics(nil), ranked...)
	if applyPolicy {
		active, shortlist = services.SelectUniverseCandidates(ranked, config.UniversePolicy, regime)
		if len(shortlist) == 0 {
			shortlist = append([]services.UniverseCandidateMetrics(nil), active...)
		}
		if len(active) == 0 {
			active = append([]services.UniverseCandidateMetrics(nil), ranked...)
		}
	}

	return backtestUniverseSelection{
		RegimeState:    regime,
		BreadthRatio:   breadth,
		ActiveUniverse: active,
		Shortlist:      shortlist,
	}
}

func buildEntryCandidates(config BacktestConfig, selection backtestUniverseSelection, states map[string]*symbolState, positions map[string]*positionState, cash float64, currentTime time.Time, lookback int) []entryCandidate {
	candidateUniverse := selection.Shortlist
	if len(candidateUniverse) == 0 {
		candidateUniverse = selection.ActiveUniverse
	}
	if len(candidateUniverse) == 0 {
		return nil
	}

	if config.ModelArtifact == nil {
		entries := make([]entryCandidate, 0, len(candidateUniverse))
		for _, candidate := range candidateUniverse {
			entries = append(entries, entryCandidate{Symbol: candidate.Symbol})
		}
		return entries
	}

	portfolioValue := cash + currentPositionsValue(positions, states)
	exposureRatio := 0.0
	if portfolioValue > 0 {
		exposureRatio = currentPositionsValue(positions, states) / portfolioValue
	}
	btcCandles := recentBacktestCandles(states["BTCUSDT"], 200)
	scored := make([]services.ModelRankedCandidate, 0, len(candidateUniverse))
	predictions := make(map[string]services.ModelPrediction, len(candidateUniverse))

	for _, candidate := range candidateUniverse {
		state := states[candidate.Symbol]
		if state == nil || state.lastIndex < lookback {
			continue
		}
		candles := buildCandles(state.series, state.lastIndex, maxInt(lookback, 200))
		featureRow := services.BuildModelFeatureRow(services.ModelFeatureInput{
			Timestamp:         currentTime,
			Symbol:            candidate.Symbol,
			Candles15m:        candles,
			Candidate:         candidate,
			ActiveUniverse:    selection.ActiveUniverse,
			RegimeState:       selection.RegimeState,
			BreadthRatio:      selection.BreadthRatio,
			BTCCandles15m:     btcCandles,
			OpenPositionCount: len(positions),
			ExposureRatio:     exposureRatio,
			AlreadyOpen:       positions[candidate.Symbol] != nil,
		})
		if !featureRow.Valid {
			continue
		}
		prediction, err := config.ModelArtifact.PredictRow(featureRow)
		if err != nil {
			continue
		}
		predictions[candidate.Symbol] = prediction
		scored = append(scored, services.ModelRankedCandidate{
			Symbol:        candidate.Symbol,
			Probability:   prediction.Probability,
			ExpectedValue: prediction.ExpectedValue,
			RawScore:      prediction.RawScore,
		})
	}

	ranked := services.RankModelPredictions(scored, config.ModelPolicy)
	entries := make([]entryCandidate, 0, len(ranked))
	for _, candidate := range ranked {
		if !candidate.Selected {
			continue
		}
		prediction := predictions[candidate.Symbol]
		predictionCopy := prediction
		entries = append(entries, entryCandidate{
			Symbol:     candidate.Symbol,
			Rank:       candidate.Rank,
			Prediction: &predictionCopy,
		})
	}
	return entries
}

func determinePositionSize(config BacktestConfig, atr float64, price float64, cash float64, portfolioValue float64) (float64, float64, *float64, *float64, error) {
	if price <= 0 {
		return 0, 0, nil, nil, fmt.Errorf("invalid price")
	}
	if cash <= 0 {
		return 0, 0, nil, nil, fmt.Errorf("insufficient cash")
	}
	if config.StrategyMode == StrategyBaseline {
		amountUsdt := cash * (config.EntryPercent / 100)
		if amountUsdt <= 0 {
			return 0, 0, nil, nil, fmt.Errorf("invalid entry percent")
		}
		if amountUsdt > cash {
			amountUsdt = cash
		}
		size := amountUsdt / price
		return amountUsdt, size, nil, nil, nil
	}
	if atr <= 0 {
		return 0, 0, nil, nil, fmt.Errorf("invalid ATR")
	}
	if config.RiskPerTrade <= 0 || config.RiskPerTrade > 100 {
		return 0, 0, nil, nil, fmt.Errorf("invalid risk_per_trade")
	}
	volStop := atr * config.StopMult
	if volStop <= 0 {
		return 0, 0, nil, nil, fmt.Errorf("invalid stop distance")
	}
	riskBudget := portfolioValue * (config.RiskPerTrade / 100)
	size := riskBudget / volStop
	amountUsdt := size * price
	if config.MaxPositionValue > 0 && amountUsdt > config.MaxPositionValue {
		amountUsdt = config.MaxPositionValue
		size = amountUsdt / price
	}
	if amountUsdt > cash {
		amountUsdt = cash
		size = amountUsdt / price
	}
	stopPrice := price - volStop
	tpPrice := price + (atr * config.TpMult)
	if stopPrice <= 0 || tpPrice <= 0 {
		return 0, 0, nil, nil, fmt.Errorf("invalid stop or take-profit")
	}
	return amountUsdt, size, &stopPrice, &tpPrice, nil
}

func currentPositionsValue(positions map[string]*positionState, states map[string]*symbolState) float64 {
	total := 0.0
	for symbol, pos := range positions {
		markPrice := pos.EntryPrice
		if state := states[symbol]; state != nil && state.lastPrice > 0 {
			markPrice = state.lastPrice
		}
		total += pos.Size * markPrice
	}
	return total
}

func recentBacktestCandles(state *symbolState, size int) []services.Candle {
	if state == nil || state.lastIndex < 0 {
		return nil
	}
	return candlesFromOHLCV(recentWindow(state.series, state.lastIndex, size))
}

func cloneFloat64Ptr(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func modelVersionForCandidate(candidate entryCandidate) string {
	if candidate.Prediction == nil {
		return ""
	}
	return candidate.Prediction.ModelVersion
}

func predictionProbability(prediction *services.ModelPrediction) *float64 {
	if prediction == nil {
		return nil
	}
	value := prediction.Probability
	return &value
}

func predictionExpectedValue(prediction *services.ModelPrediction) *float64 {
	if prediction == nil {
		return nil
	}
	value := prediction.ExpectedValue
	return &value
}

func buildRankingMetrics(trades []Trade, config BacktestConfig) *RankingMetrics {
	if config.ModelArtifact == nil || len(trades) == 0 {
		return nil
	}
	buckets := make(map[int][]Trade)
	selected := 0
	for _, trade := range trades {
		if trade.EntryRank <= 0 {
			continue
		}
		selected++
		buckets[trade.EntryRank] = append(buckets[trade.EntryRank], trade)
	}
	if selected == 0 {
		return nil
	}
	ranks := make([]int, 0, len(buckets))
	for rank := range buckets {
		ranks = append(ranks, rank)
	}
	sort.Ints(ranks)
	byRank := make([]RankBucketMetric, 0, len(ranks))
	for _, rank := range ranks {
		bucket := buckets[rank]
		wins := 0
		totalPnl := 0.0
		totalProb := 0.0
		probCount := 0
		totalEV := 0.0
		evCount := 0
		for _, trade := range bucket {
			totalPnl += trade.Pnl
			if trade.Pnl > 0 {
				wins++
			}
			if trade.PredictedProbability != nil {
				totalProb += *trade.PredictedProbability
				probCount++
			}
			if trade.PredictedEV != nil {
				totalEV += *trade.PredictedEV
				evCount++
			}
		}
		avgProb := 0.0
		if probCount > 0 {
			avgProb = totalProb / float64(probCount)
		}
		avgEV := 0.0
		if evCount > 0 {
			avgEV = totalEV / float64(evCount)
		}
		byRank = append(byRank, RankBucketMetric{
			Rank:     rank,
			Trades:   len(bucket),
			WinRate:  float64(wins) / float64(len(bucket)),
			AvgPnl:   totalPnl / float64(len(bucket)),
			TotalPnl: totalPnl,
			AvgProb:  avgProb,
			AvgEV:    avgEV,
		})
	}
	return &RankingMetrics{
		ModelVersion: config.ModelArtifact.Version,
		TopK:         config.ModelPolicy.TopK,
		Selected:     selected,
		ByRank:       byRank,
		Diagnostics:  buildRankingDiagnostics(byRank),
	}
}

func determineExitPrice(bar services.OHLCV, decision services.ExitDecision, config BacktestConfig) float64 {
	price := bar.Close
	switch decision.Reason {
	case services.CloseReasonStopLoss, services.CloseReasonATRTrailing, services.CloseReasonTrailingStop:
		price = decision.TriggerPrice
		if bar.Open > 0 && (decision.TriggerPrice <= 0 || bar.Open <= decision.TriggerPrice) {
			price = bar.Open
		}
	case services.CloseReasonTakeProfit:
		price = decision.TriggerPrice
		if bar.Open > 0 && (decision.TriggerPrice <= 0 || bar.Open >= decision.TriggerPrice) {
			price = bar.Open
		}
	case services.CloseReasonSellSignal, services.CloseReasonTimeStop:
		price = bar.Close
	}
	return applySlippage(price, config.SlippageBps, false)
}

func applySlippage(price float64, slippageBps float64, isBuy bool) float64 {
	if slippageBps <= 0 {
		return price
	}
	slippage := slippageBps / 10000
	if isBuy {
		return price * (1 + slippage)
	}
	return price * (1 - slippage)
}

func computeAtr(candles []services.Candle, config BacktestConfig) float64 {
	if config.AtrAnnualizationEnabled {
		return services.CalculateAnnualizedATR(candles, config.AtrPeriod, config.TimeframeMinutes, config.AtrAnnualizationDays)
	}
	return services.CalculateATR(candles, config.AtrPeriod)
}

func buildCandles(series []services.OHLCV, idx int, lookback int) []services.Candle {
	start := idx - lookback + 1
	if start < 0 {
		start = 0
	}
	window := series[start : idx+1]
	return candlesFromOHLCV(window)
}

func candlesFromOHLCV(series []services.OHLCV) []services.Candle {
	candles := make([]services.Candle, len(series))
	for i, c := range series {
		candles[i] = services.Candle{Close: c.Close, High: c.High, Low: c.Low, Volume: c.Volume}
	}
	return candles
}

func filterSeriesWindow(series map[string][]services.OHLCV, start time.Time, end time.Time) map[string][]services.OHLCV {
	result := map[string][]services.OHLCV{}
	for symbol, candles := range series {
		var filtered []services.OHLCV
		for _, candle := range candles {
			openTime := time.UnixMilli(candle.OpenTime)
			if !start.IsZero() && openTime.Before(start) {
				continue
			}
			if !end.IsZero() && openTime.After(end) {
				continue
			}
			filtered = append(filtered, candle)
		}
		if len(filtered) > 0 {
			result[symbol] = filtered
		}
	}
	return result
}

func buildSymbolStates(series map[string][]services.OHLCV) map[string]*symbolState {
	states := make(map[string]*symbolState, len(series))
	for symbol, candles := range series {
		indexByTime := make(map[int64]int, len(candles))
		for i, candle := range candles {
			indexByTime[candle.OpenTime] = i
		}
		states[symbol] = &symbolState{series: candles, indexByTime: indexByTime, lastIndex: -1, currentIndex: -1}
	}
	return states
}

func buildTimeline(series map[string][]services.OHLCV) []int64 {
	unique := map[int64]struct{}{}
	for _, candles := range series {
		for _, candle := range candles {
			unique[candle.OpenTime] = struct{}{}
		}
	}
	timeline := make([]int64, 0, len(unique))
	for ts := range unique {
		timeline = append(timeline, ts)
	}
	sort.Slice(timeline, func(i, j int) bool { return timeline[i] < timeline[j] })
	return timeline
}

func sortedSymbols(series map[string][]services.OHLCV) []string {
	symbols := make([]string, 0, len(series))
	for symbol := range series {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)
	return symbols
}

func computeSignalLookback(config BacktestConfig) int {
	barsPerDay := maxInt(1, (24*60)/maxInt(1, config.TimeframeMinutes))
	lookback := maxInt(200, barsPerDay*7)
	if config.AtrPeriod+1 > lookback {
		lookback = config.AtrPeriod + 1
	}
	return lookback
}

func recentWindow(series []services.OHLCV, idx int, size int) []services.OHLCV {
	start := idx - size + 1
	if start < 0 {
		start = 0
	}
	return series[start : idx+1]
}

func aggregateOHLCVByBars(series []services.OHLCV, barsPerBucket int) []services.OHLCV {
	if len(series) == 0 || barsPerBucket <= 1 {
		copySeries := make([]services.OHLCV, len(series))
		copy(copySeries, series)
		return copySeries
	}
	aggregated := make([]services.OHLCV, 0, (len(series)+barsPerBucket-1)/barsPerBucket)
	for start := 0; start < len(series); start += barsPerBucket {
		end := start + barsPerBucket
		if end > len(series) {
			end = len(series)
		}
		bucket := series[start:end]
		merged := bucket[0]
		merged.High = bucket[0].High
		merged.Low = bucket[0].Low
		merged.Volume = 0
		merged.Close = bucket[len(bucket)-1].Close
		merged.CloseTime = bucket[len(bucket)-1].CloseTime
		for _, candle := range bucket {
			if candle.High > merged.High {
				merged.High = candle.High
			}
			if candle.Low < merged.Low {
				merged.Low = candle.Low
			}
			merged.Volume += candle.Volume
		}
		aggregated = append(aggregated, merged)
	}
	return aggregated
}

func closesFromOHLCV(series []services.OHLCV) []float64 {
	closes := make([]float64, len(series))
	for i, candle := range series {
		closes[i] = candle.Close
	}
	return closes
}

func sumQuoteVolume(series []services.OHLCV) float64 {
	total := 0.0
	for _, candle := range series {
		total += candle.Close * candle.Volume
	}
	return total
}

func backtestRegimeGate(candles []services.Candle, emaFast int, emaSlow int) bool {
	if len(candles) < emaSlow {
		return false
	}
	closes := make([]float64, len(candles))
	for i, candle := range candles {
		closes[i] = candle.Close
	}
	return services.CalculateEMA(closes, emaFast) > services.CalculateEMA(closes, emaSlow)
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a int, b int) int {
	if a > b {
		return a
	}
	return b
}
