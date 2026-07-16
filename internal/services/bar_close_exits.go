package services

import (
	"time"
	"trading-go/internal/database"
)

func EvaluateOpenPositionsOnBarClose() (int, error) {
	settings := GetAllSettings()
	policy := BuildExitPolicy(settings)
	atrTrailingPeriod := getSettingInt(settings, "atr_trailing_period", 14)
	atrAnnualizationEnabled := getSettingBool(settings, "atr_annualization_enabled", false)
	atrAnnualizationDays := getSettingInt(settings, "atr_annualization_days", 365)

	var positions []database.Position
	if err := database.DB.Where("status = ?", "open").Find(&positions).Error; err != nil {
		return 0, err
	}
	var wallet database.Wallet
	if err := database.DB.First(&wallet).Error; err != nil {
		return 0, err
	}

	closedCount := 0
	coordinator := GetExecutionCoordinator()
	for _, position := range positions {
		if position.ExitPending {
			continue
		}

		pairSymbol := PositionPairSymbol(position.Symbol, wallet.Currency)
		candles, err := fetchCandles(pairSymbol, DecisionTimeframeDefault, 200)
		if err != nil || len(candles) == 0 {
			continue
		}

		now := time.Now()
		currentPrice := candles[len(candles)-1].Close
		markPositionPrice(&position, currentPrice, now)

		entryPrice := position.AvgPrice
		if position.EntryPrice != nil && *position.EntryPrice > 0 {
			entryPrice = *position.EntryPrice
		}

		if policy.ATRTrailingEnabled {
			atr := getAtrValue(candles, atrTrailingPeriod, atrAnnualizationEnabled, 15, atrAnnualizationDays)
			if atr > 0 {
				position.LastAtrValue = &atr
				position.TrailingStopPrice = RatchetATRTrailingStop(position.TrailingStopPrice, currentPrice, entryPrice, atr, policy.ATRTrailingMult)
			}
		} else if policy.TrailingStopEnabled {
			position.TrailingStopPrice = RatchetPercentTrailingStop(position.TrailingStopPrice, currentPrice, entryPrice, policy.TrailingStopPercent)
		}

		values := map[string]interface{}{"current_price": currentPrice, "last_mark_price": currentPrice, "trailing_stop_price": position.TrailingStopPrice, "last_atr_value": position.LastAtrValue}
		updated, err := updatePositionOperational(position.ID, now, values)
		if err != nil {
			return closedCount, err
		}
		if !updated {
			continue
		}

		analysis := analyzeSymbolFromCandles(pairSymbol, DecisionTimeframeDefault, candles)
		barsHeld := int(time.Since(position.OpenedAt) / (15 * time.Minute))
		decision := EvaluateBarCloseExit(ExitEvaluationInput{
			CurrentPrice:      currentPrice,
			HighPrice:         currentPrice,
			LowPrice:          currentPrice,
			EntryPrice:        entryPrice,
			StopPrice:         position.StopPrice,
			TakeProfitPrice:   position.TakeProfitPrice,
			TrailingStopPrice: position.TrailingStopPrice,
			BarsHeld:          barsHeld,
			MaxBarsHeld:       position.MaxBarsHeld,
			Signal:            analysis.Signal,
			SignalRating:      analysis.Rating,
			ObservedAt:        now,
			ExecutionMode:     position.ExecutionMode,
		}, policy)
		if decision.Reason == "" {
			continue
		}

		result, err := coordinator.RequestClose(CloseRequest{
			PositionID:     position.ID,
			Reason:         decision.Reason,
			RequestedPrice: decision.TriggerPrice,
			TriggeredAt:    now,
			Source:         "bar_close_plane",
		})
		if err != nil {
			return closedCount, err
		}
		if result != nil && result.Closed {
			closedCount++
		}
	}

	return closedCount, nil
}
