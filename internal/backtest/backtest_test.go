package backtest

import (
	"math"
	"testing"
	"time"
)

func TestComputeMetricsBasic(t *testing.T) {
	now := time.Now()
	equity := []EquityPoint{
		{Time: now, Value: 100},
		{Time: now.Add(time.Minute), Value: 110},
		{Time: now.Add(2 * time.Minute), Value: 100},
	}
	trades := []Trade{
		{Pnl: 10},
		{Pnl: -5},
	}

	metrics := ComputeMetrics(equity, trades, 60, 365)
	if math.Abs(metrics.MaxDrawdown-0.090909) > 0.0005 {
		t.Errorf("MaxDrawdown = %v, want ~0.090909", metrics.MaxDrawdown)
	}
	if math.Abs(metrics.WinRate-0.5) > 0.0001 {
		t.Errorf("WinRate = %v, want 0.5", metrics.WinRate)
	}
	if math.Abs(metrics.ProfitFactor-2.0) > 0.0001 {
		t.Errorf("ProfitFactor = %v, want 2.0", metrics.ProfitFactor)
	}
	if math.Abs(metrics.AvgWin-10.0) > 0.0001 {
		t.Errorf("AvgWin = %v, want 10", metrics.AvgWin)
	}
	if math.Abs(metrics.AvgLoss-(-5.0)) > 0.0001 {
		t.Errorf("AvgLoss = %v, want -5", metrics.AvgLoss)
	}
	if metrics.TradeCount != 2 {
		t.Errorf("TradeCount = %v, want 2", metrics.TradeCount)
	}
}

func TestApplySlippage(t *testing.T) {
	price := 100.0
	slippageBps := 10.0
	buyPrice := applySlippage(price, slippageBps, true)
	sellPrice := applySlippage(price, slippageBps, false)

	if math.Abs(buyPrice-100.1) > 0.0001 {
		t.Errorf("applySlippage buy = %v, want 100.1", buyPrice)
	}
	if math.Abs(sellPrice-99.9) > 0.0001 {
		t.Errorf("applySlippage sell = %v, want 99.9", sellPrice)
	}
}
