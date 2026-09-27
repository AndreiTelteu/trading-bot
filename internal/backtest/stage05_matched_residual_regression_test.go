package backtest

import (
	"testing"
	"time"

	"trading-go/internal/tradingcore"
)

func runMandatoryExitDustFixture(t *testing.T, id, version string) (*backtestMemoryLedger, error) {
	t.Helper()
	config, series := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10}}, []float64{100, 100, 100}, 0, 0)
	config.StrategyID, config.StrategyVersion = id, version
	config.StrategyParameters = map[string]string{"target_gross": "0.75", "max_gross": "0.75", "risk_off_gross": "0", "skip_delta": "0.015", "allocation_tolerance": "0.02"}
	config.ExecutionPolicy.Version = "backtest-execution-v3"
	config.ExecutionPolicy.Constraints["AAAUSDT"] = SymbolConstraints{QuantityStep: .001, PriceTick: .01, MinQuantity: .001, MinNotional: 5}
	config.ExecutionSeries = series
	ledger := newBacktestMemoryLedger(config)
	ledger.positions["AAAUSDT"] = &positionState{Symbol: "AAAUSDT", EntryPrice: 10, Size: .5005, EntryTime: config.Start}
	signalAt := stage05CloseAt(config.Start, 0)
	fillAt := time.UnixMilli(series["AAAUSDT"][1].OpenTime)
	err := rebalanceStage05(ledger, config, tradingcore.TargetAllocationStrategy{}, nil, map[string]float64{}, nil,
		map[string]ExitReasonTrace{"AAAUSDT": {Primary: "regime_risk_off"}},
		map[string]float64{"AAAUSDT": 10}, map[string]float64{"AAAUSDT": 10},
		signalAt, fillAt, config.StrategyParameters, "risk_off")
	return ledger, err
}

func TestMatchedMomentumMandatoryExitRecordsPostFillExchangeDust(t *testing.T) {
	for _, tt := range []struct{ name, id, version string }{
		{"matched baseline", StrategyMatchedMomentumID, "1.0.0"},
		{"corresponding candidate", StrategyTrendMomentumCandidate, "1.1.0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ledger, err := runMandatoryExitDustFixture(t, tt.id, tt.version)
			if err != nil {
				t.Fatalf("evidenced below-minimum post-fill residual should stay bounded: %v", err)
			}
			remaining := ledger.positions["AAAUSDT"]
			if remaining == nil || !stage05QuantityBelowExecutionMinimum(remaining.Size, 10, ".001", "5", 1e-9) {
				t.Fatalf("fixture did not retain below-minimum inventory: %+v", remaining)
			}
			if stage05RetainedResidualSlots(ledger) != 1 {
				t.Fatalf("mandatory sell left dust without correlated residual evidence: %+v", ledger.allocationDiagnostics)
			}
			if len(ledger.noFills) != 0 || len(ledger.events) != 1 || ledger.events[0].Side != "sell" {
				t.Fatalf("post-fill residual was confused with no-fill or no actual sell: events=%+v no_fills=%+v", ledger.events, ledger.noFills)
			}
		})
	}
}

func TestCandidateV10MandatoryExitKeepsStrictZeroTarget(t *testing.T) {
	ledger, err := runMandatoryExitDustFixture(t, StrategyTrendMomentumCandidate, "1.0.0")
	if !IsStrategyDiagnostic(err, DiagnosticAchievedAllocation) {
		t.Fatalf("candidate v1.0 must retain its strict zero-target result: %v", err)
	}
	if stage05RetainedResidualSlots(ledger) != 0 {
		t.Fatalf("candidate v1.0 gained new residual evidence: %+v", ledger.allocationDiagnostics)
	}
}

func TestMandatoryExitResidualVersionBoundary(t *testing.T) {
	tests := []struct {
		name, id, version string
		want              bool
	}{
		{"matched baseline", StrategyMatchedMomentumID, "1.0.0", true},
		{"candidate v1.1", StrategyTrendMomentumCandidate, "1.1.0", true},
		{"candidate v1.0", StrategyTrendMomentumCandidate, "1.0.0", false},
		{"ordinary momentum", StrategyMomentumID, "1.0.0", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := usesMandatoryExitResidualEvidence(tt.id, tt.version); got != tt.want {
				t.Fatalf("mandatory exit residual evidence=%t want %t", got, tt.want)
			}
		})
	}
	if strategyImplementationDigest(StrategyMatchedMomentumID, "1.0.0") != strategyImplementationDigest(StrategyTrendMomentumCandidate, "1.1.0") {
		t.Fatal("matched and candidate mandatory-exit semantics have different implementation identity")
	}
	if strategyImplementationDigest(StrategyTrendMomentumCandidate, "1.0.0") == strategyImplementationDigest(StrategyMatchedMomentumID, "1.0.0") {
		t.Fatal("candidate v1.0 identity incorrectly claims mandatory-exit residual semantics")
	}
}

func TestMatchedMomentumMandatoryExitRejectsExecutableResidual(t *testing.T) {
	config, _ := stage05Fixture(map[string][]float64{"AAAUSDT": {10, 10, 10}}, []float64{100, 100, 100}, 0, 0)
	config.StrategyID, config.StrategyVersion = StrategyMatchedMomentumID, "1.0.0"
	config.ExecutionPolicy.Constraints["AAAUSDT"] = SymbolConstraints{QuantityStep: .001, PriceTick: .01, MinQuantity: .001, MinNotional: 5}
	ledger := newBacktestMemoryLedger(config)
	ledger.positions["AAAUSDT"] = &positionState{Symbol: "AAAUSDT", EntryPrice: 10, Size: .6, EntryTime: config.Start}
	if err := reconcileMandatoryExitResidual(ledger, config, "AAAUSDT", 1, 10, config.Start); !IsStrategyDiagnostic(err, DiagnosticAchievedAllocation) {
		t.Fatalf("executable post-fill residual must fail: %v", err)
	}
	if stage05RetainedResidualSlots(ledger) != 0 {
		t.Fatalf("executable residual gained dust evidence: %+v", ledger.allocationDiagnostics)
	}
}
