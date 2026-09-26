package backtest

import (
	"math"
	"testing"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/validation"
)

func TestStage07ResidualPositionFromPartialCloseAndFinalMark(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	buyAt, sellAt, end := start.Add(time.Minute), start.Add(2*time.Minute), start.Add(3*time.Minute)
	candidate := Stage05StrategyResult{
		Metrics: ComparableMetrics{StartingCapital: "100", EndingEquity: "102.68", AverageGrossExposure: availableMetric(.1), AverageNetExposure: availableMetric(.1), Turnover: "32", TurnoverRatio: availableMetric(.32), FeeCosts: ".32", SlippageCosts: "0", FillCount: 2, TradeCount: 1},
		Equity:  []EquityPoint{{Time: start, Value: 100}, {Time: end, Value: 102.68}},
		Trades:  []Trade{{Symbol: "AAA", EntryTime: buyAt, ExitTime: sellAt, EntryPrice: 10, ExitPrice: 12, Size: 1, Pnl: 1.78, RegimeState: "risk_on"}},
		Artifacts: BacktestArtifacts{
			Fills: []FillArtifact{
				{FillID: "buy", FillAt: buyAt.Format(time.RFC3339Nano), Symbol: "AAA", Side: "buy", Quantity: "2", Price: "10", Fee: ".2", ExecutionReferencePrice: "10"},
				{FillID: "sell", FillAt: sellAt.Format(time.RFC3339Nano), Symbol: "AAA", Side: "sell", Quantity: "1", Price: "12", Fee: ".12", ExecutionReferencePrice: "12"},
			},
			Ledger:   []LedgerArtifact{{CashAfter: "79.8"}, {CashAfter: "91.68"}},
			Exposure: []ExposureArtifact{{At: end.Format(time.RFC3339Nano), Symbol: "AAA", Quantity: "1", MarkPrice: "11", Value: "11", Status: "marked_unliquidated_no_executable_bar"}},
		},
	}
	decision := map[string][]services.OHLCV{"AAA": {{OpenTime: start.UnixMilli(), CloseTime: end.Add(-time.Minute).UnixMilli(), Close: 11}}}
	execution := map[string][]services.OHLCV{"AAA": {{OpenTime: buyAt.UnixMilli(), Volume: 100}, {OpenTime: sellAt.UnixMilli(), Volume: 100}}}
	baseline := Stage05StrategyResult{Metrics: ComparableMetrics{AverageGrossExposure: availableMetric(.1), TurnoverRatio: availableMetric(.32)}, Equity: []EquityPoint{{Time: start, Value: 100}, {Time: end, Value: 100}}}
	p, err := stage07Primitives(candidate, baseline, 0, 2, decision, execution)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.ResidualPositions) != 1 || math.Abs(p.ResidualPositions[0].CostBasis-10) > 1e-12 || math.Abs(p.ResidualPositions[0].EntryFee-.1) > 1e-12 || math.Abs(p.ResidualPositions[0].UnrealizedPnL-.9) > 1e-12 {
		t.Fatalf("residual inventory was not derived from fills and final mark: %+v", p.ResidualPositions)
	}
	metrics, err := validation.DeriveFoldMetrics(p)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Trades != 1 || math.Abs(metrics.AfterCostReturn-.0268) > 1e-12 || math.Abs(metrics.AfterCostExpectancy-.0178) > 1e-12 {
		t.Fatalf("open PnL was misclassified: %+v", metrics)
	}
	// Stage 05's exposure snapshot may include the bar closing exactly at end;
	// fold equity deliberately values only bars known before end.
	laterExposure := candidate
	laterExposure.Artifacts.Exposure = append([]ExposureArtifact(nil), candidate.Artifacts.Exposure...)
	laterExposure.Artifacts.Exposure[0].MarkPrice = "12"
	laterExposure.Artifacts.Exposure[0].Value = "12"
	withBoundaryBar := map[string][]services.OHLCV{"AAA": append(append([]services.OHLCV(nil), decision["AAA"]...), services.OHLCV{OpenTime: end.Add(-time.Minute).UnixMilli(), CloseTime: end.UnixMilli(), Close: 12})}
	laterPrimitives, err := stage07Primitives(laterExposure, baseline, 0, 2, withBoundaryBar, execution)
	if err != nil || len(laterPrimitives.ResidualPositions) != 1 || laterPrimitives.ResidualPositions[0].MarkPrice != 11 {
		t.Fatalf("artifact and final-equity marks were conflated: positions=%+v err=%v", laterPrimitives.ResidualPositions, err)
	}
	for name, mutate := range map[string]func(*Stage05StrategyResult){
		"exposure quantity": func(v *Stage05StrategyResult) { v.Artifacts.Exposure[0].Quantity = "2" },
		"exposure mark":     func(v *Stage05StrategyResult) { v.Artifacts.Exposure[0].MarkPrice = "12" },
		"exposure value":    func(v *Stage05StrategyResult) { v.Artifacts.Exposure[0].Value = "12" },
		"future exposure": func(v *Stage05StrategyResult) {
			v.Artifacts.Exposure[0].At = end.Add(time.Minute).Format(time.RFC3339Nano)
		},
		"ledger cash":      func(v *Stage05StrategyResult) { v.Artifacts.Ledger[1].CashAfter = "92" },
		"ending metric":    func(v *Stage05StrategyResult) { v.Metrics.EndingEquity = "103" },
		"missing exposure": func(v *Stage05StrategyResult) { v.Artifacts.Exposure = nil },
	} {
		t.Run(name, func(t *testing.T) {
			bad := candidate
			bad.Artifacts.Exposure = append([]ExposureArtifact(nil), candidate.Artifacts.Exposure...)
			bad.Artifacts.Ledger = append([]LedgerArtifact(nil), candidate.Artifacts.Ledger...)
			mutate(&bad)
			if _, err := stage07Primitives(bad, baseline, 0, 2, decision, execution); err == nil {
				t.Fatal("inconsistent final inventory was accepted")
			}
		})
	}
}
