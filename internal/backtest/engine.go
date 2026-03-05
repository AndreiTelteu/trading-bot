package backtest

import (
	"fmt"
	"math"
	"sort"
	"time"
	"trading-go/internal/services"
)

type positionState struct {
	Symbol         string
	EntryPrice     float64
	Size           float64
	StopPrice      *float64
	TakeProfit     *float64
	TrailingStop   *float64
	HighestPrice   float64
	OpenedIndex    int
	BarsHeld       int
	EntryTime      time.Time
	LastAtr        float64
	EntryFee       float64
}

type barContext struct {
	Rating float64
	Signal string
	Atr    float64
}

func RunBacktest(config BacktestConfig, series map[string][]services.OHLCV) (BacktestResult, error) {
	if config.InitialBalance <= 0 {
		return BacktestResult{}, fmt.Errorf("initial balance must be > 0")
	}
	aligned, err := alignSeries(series, config.Start, config.End)
	if err != nil {
		return BacktestResult{}, err
	}
	if len(aligned) == 0 {
		return BacktestResult{}, fmt.Errorf("no data available")
	}

	symbols := make([]string, 0, len(aligned))
	for symbol := range aligned {
		symbols = append(symbols, symbol)
	}
	sort.Strings(symbols)

	minLen := minSeriesLength(aligned, symbols)
	if minLen < 2 {
		return BacktestResult{}, fmt.Errorf("insufficient data length")
	}

	for _, symbol := range symbols {
		if len(aligned[symbol]) > minLen {
			aligned[symbol] = aligned[symbol][len(aligned[symbol])-minLen:]
		}
	}

	lookback := 200
	if config.AtrPeriod+1 > lookback {
		lookback = config.AtrPeriod + 1
	}
	if lookback >= minLen {
		lookback = minLen - 1
	}

	cash := config.InitialBalance
	positions := map[string]*positionState{}
	var equity []EquityPoint
	equityBySymbol := map[string][]EquityPoint{}
	var trades []Trade

	for _, symbol := range symbols {
		equityBySymbol[symbol] = []EquityPoint{}
	}

	for idx := lookback; idx < minLen; idx++ {
		currentTime := time.UnixMilli(aligned[symbols[0]][idx].OpenTime)
		contexts := map[string]barContext{}

		for _, symbol := range symbols {
			window := buildCandles(aligned[symbol], idx, lookback)
			rating, signal := services.AnalyzeCandles(window)
			atr := computeAtr(window, config)
			contexts[symbol] = barContext{
				Rating: rating,
				Signal: signal,
				Atr:    atr,
			}
		}

		for _, symbol := range symbols {
			pos := positions[symbol]
			if pos == nil {
				continue
			}
			bar := aligned[symbol][idx]
			price := bar.Close
			pos.BarsHeld++
			if price > pos.HighestPrice {
				pos.HighestPrice = price
			}

			if config.AtrTrailingEnabled && config.AtrTrailingMult > 0 {
				atr := contexts[symbol].Atr
				if atr > 0 {
					pos.LastAtr = atr
					candidate := price - (atr * config.AtrTrailingMult)
					if candidate > 0 {
						if pos.TrailingStop == nil || candidate > *pos.TrailingStop {
							pos.TrailingStop = &candidate
						}
					}
				}
			}

			closeReason := ""
			if pos.StopPrice != nil && price <= *pos.StopPrice {
				closeReason = "stop_loss"
			} else if pos.TakeProfit != nil && price >= *pos.TakeProfit {
				closeReason = "take_profit"
			}

			if closeReason == "" && config.AtrTrailingEnabled && pos.TrailingStop != nil {
				if price <= *pos.TrailingStop {
					closeReason = "atr_trailing_stop"
				}
			}

			if closeReason == "" && config.TrailingStopEnabled && pos.HighestPrice > 0 {
				dropFromHigh := ((price - pos.HighestPrice) / pos.HighestPrice) * 100
				if dropFromHigh <= -config.TrailingStopPercent {
					closeReason = "trailing_stop"
				}
			}

			pnlPercent := ((price - pos.EntryPrice) / pos.EntryPrice) * 100
			if closeReason == "" && pos.StopPrice == nil && pos.TakeProfit == nil {
				if pnlPercent <= -config.StopLossPercent {
					closeReason = "stop_loss"
				} else if pnlPercent >= config.TakeProfitPercent {
					closeReason = "take_profit"
				}
			}

			if closeReason == "" && config.TimeStopBars > 0 && pos.BarsHeld >= config.TimeStopBars && pnlPercent <= 0 {
				closeReason = "time_stop"
			}

			if closeReason == "" && config.SellOnSignal {
				ctx := contexts[symbol]
				if (ctx.Signal == "SELL" || ctx.Signal == "STRONG_SELL") && ctx.Rating <= config.MinConfidenceToSell {
					closeReason = "sell_signal"
				}
			}

			if closeReason != "" {
				exitPrice := applySlippage(price, config.SlippageBps, false)
				proceeds := pos.Size * exitPrice
				exitFee := proceeds * (config.FeeBps / 10000)
				cash += proceeds - exitFee
				pnl := (exitPrice-pos.EntryPrice)*pos.Size - pos.EntryFee - exitFee
				trades = append(trades, Trade{
					Symbol:     symbol,
					EntryTime:  pos.EntryTime,
					ExitTime:   currentTime,
					EntryPrice: pos.EntryPrice,
					ExitPrice:  exitPrice,
					Size:       pos.Size,
					Pnl:        pnl,
					PnlPercent: pnl / (pos.EntryPrice * pos.Size) * 100,
					Reason:     closeReason,
				})
				delete(positions, symbol)
			}
		}

		for _, symbol := range symbols {
			if len(positions) >= config.MaxPositions {
				break
			}
			if positions[symbol] != nil {
				continue
			}
			ctx := contexts[symbol]
			if config.BuyOnlyStrong && ctx.Signal != "STRONG_BUY" {
				continue
			}
			if ctx.Signal != "BUY" && ctx.Signal != "STRONG_BUY" {
				continue
			}
			if ctx.Rating < config.MinConfidenceToBuy {
				continue
			}

			price := aligned[symbol][idx].Close
			portfolioValue := cash + currentPositionsValue(positions, aligned, idx)
			entryPrice := applySlippage(price, config.SlippageBps, true)
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
				Symbol:       symbol,
				EntryPrice:   entryPrice,
				Size:         size,
				StopPrice:    stopPrice,
				TakeProfit:   takeProfitPrice,
				TrailingStop: trailingStop,
				HighestPrice: entryPrice,
				OpenedIndex:  idx,
				BarsHeld:     0,
				EntryTime:    currentTime,
				LastAtr:      ctx.Atr,
				EntryFee:     entryFee,
			}

			if amountUsdt <= 0 {
				continue
			}
		}

		totalValue := cash + currentPositionsValue(positions, aligned, idx)
		equity = append(equity, EquityPoint{
			Time:  currentTime,
			Value: totalValue,
		})
		for _, symbol := range symbols {
			value := 0.0
			if pos := positions[symbol]; pos != nil {
				value = pos.Size * aligned[symbol][idx].Close
			}
			equityBySymbol[symbol] = append(equityBySymbol[symbol], EquityPoint{
				Time:  currentTime,
				Value: value,
			})
		}
	}

	metrics := ComputeMetrics(equity, trades, config.TimeframeMinutes, config.AtrAnnualizationDays)
	return BacktestResult{
		Mode:           config.StrategyMode,
		Metrics:        metrics,
		Equity:         equity,
		EquityBySymbol: equityBySymbol,
		Trades:         trades,
	}, nil
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

func currentPositionsValue(positions map[string]*positionState, series map[string][]services.OHLCV, idx int) float64 {
	var total float64
	for symbol, pos := range positions {
		if idx < len(series[symbol]) {
			total += pos.Size * series[symbol][idx].Close
		}
	}
	return total
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
	candles := make([]services.Candle, len(window))
	for i, c := range window {
		candles[i] = services.Candle{
			Close:  c.Close,
			High:   c.High,
			Low:    c.Low,
			Volume: c.Volume,
		}
	}
	return candles
}

func minSeriesLength(series map[string][]services.OHLCV, symbols []string) int {
	minLen := math.MaxInt32
	for _, symbol := range symbols {
		if len(series[symbol]) < minLen {
			minLen = len(series[symbol])
		}
	}
	if minLen == math.MaxInt32 {
		return 0
	}
	return minLen
}

func alignSeries(series map[string][]services.OHLCV, start time.Time, end time.Time) (map[string][]services.OHLCV, error) {
	aligned := map[string][]services.OHLCV{}
	var latestStart int64
	var earliestEnd int64

	for symbol, candles := range series {
		if len(candles) == 0 {
			continue
		}
		first := candles[0].OpenTime
		last := candles[len(candles)-1].OpenTime
		if latestStart == 0 || first > latestStart {
			latestStart = first
		}
		if earliestEnd == 0 || last < earliestEnd {
			earliestEnd = last
		}
		aligned[symbol] = candles
	}
	if len(aligned) == 0 {
		return aligned, nil
	}
	if !start.IsZero() && start.UnixMilli() > latestStart {
		latestStart = start.UnixMilli()
	}
	if !end.IsZero() && end.UnixMilli() < earliestEnd {
		earliestEnd = end.UnixMilli()
	}
	if earliestEnd <= latestStart {
		return nil, fmt.Errorf("no overlapping range")
	}

	for symbol, candles := range aligned {
		var filtered []services.OHLCV
		for _, c := range candles {
			if c.OpenTime >= latestStart && c.OpenTime <= earliestEnd {
				filtered = append(filtered, c)
			}
		}
		aligned[symbol] = filtered
	}
	return aligned, nil
}
