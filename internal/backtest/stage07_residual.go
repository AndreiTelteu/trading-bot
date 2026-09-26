package backtest

import (
	"math"
	"strconv"
	"time"

	"trading-go/internal/services"
	"trading-go/internal/validation"
)

// stage07ResidualPrimitives values verified end inventory at the same as-of
// boundary used by Stage 05 final equity. Stage 05 exposure artifacts identify
// remaining quantities, while immutable decision bars provide the final mark.
func stage07ResidualPrimitives(result Stage05StrategyResult, inventory stage07EndInventory, series map[string][]services.OHLCV, capital float64) ([]validation.ResidualPositionPrimitive, error) {
	invalid := func(details string) ([]validation.ResidualPositionPrimitive, error) {
		return nil, &validation.DiagnosticError{Code: validation.DiagnosticManifestIntegrity, Field: "residual_positions", Details: details}
	}
	if len(result.Equity) == 0 || !stage07Nonnegative(inventory.Cash) || inventory.Cash < 0 || len(result.Artifacts.Ledger) != len(result.Artifacts.Fills) {
		return invalid("end inventory, equity, or ledger evidence is incomplete")
	}
	end := result.Equity[len(result.Equity)-1]
	if end.Time.IsZero() || !stage07Positive(end.Value) {
		return invalid("final equity is unavailable")
	}
	ledgerCash := capital
	if len(result.Artifacts.Ledger) > 0 {
		var err error
		ledgerCash, err = strconv.ParseFloat(result.Artifacts.Ledger[len(result.Artifacts.Ledger)-1].CashAfter, 64)
		if err != nil || !stage07Nonnegative(ledgerCash) {
			return invalid("last immutable ledger cash is invalid")
		}
	}
	if !stage07EquityNear(ledgerCash, inventory.Cash, capital) {
		return invalid("fill-derived cash differs from immutable ledger cash")
	}
	exposures := make(map[string]ExposureArtifact, len(result.Artifacts.Exposure))
	for _, exposure := range result.Artifacts.Exposure {
		if exposure.Symbol == "" || exposure.Status != "marked_unliquidated_no_executable_bar" {
			return invalid("final exposure has an invalid identity or status")
		}
		if _, duplicate := exposures[exposure.Symbol]; duplicate {
			return invalid("duplicate final exposure")
		}
		exposures[exposure.Symbol] = exposure
	}
	if len(exposures) != len(inventory.Positions) {
		return invalid("final exposure count differs from fill-derived inventory")
	}
	marks := marksAsOf(series, end.Time.Add(-time.Nanosecond))
	positions := make([]validation.ResidualPositionPrimitive, 0, len(inventory.Positions))
	markedValue := 0.0
	lastSymbol := ""
	for _, position := range inventory.Positions {
		if position.Symbol == "" || position.Symbol <= lastSymbol || !stage07Positive(position.Quantity) || !stage07Positive(position.EntryPrice) || !stage07Nonnegative(position.EntryFee) {
			return invalid("fill-derived final inventory is invalid or unsorted")
		}
		lastSymbol = position.Symbol
		exposure, ok := exposures[position.Symbol]
		if !ok {
			return invalid("fill-derived position has no Stage 05 exposure")
		}
		quantity, err := strconv.ParseFloat(exposure.Quantity, 64)
		if err != nil || !stage07EquityNear(quantity, position.Quantity, math.Max(1, position.Quantity)) {
			return invalid("Stage 05 exposure quantity differs from verified fills")
		}
		exposureAt, timeErr := time.Parse(time.RFC3339Nano, exposure.At)
		exposureMark, markErr := strconv.ParseFloat(exposure.MarkPrice, 64)
		exposureValue, valueErr := strconv.ParseFloat(exposure.Value, 64)
		if timeErr != nil || exposureAt.IsZero() || exposureAt.After(end.Time) || exposureAt.Before(result.Equity[0].Time) || markErr != nil || valueErr != nil || !stage07Positive(exposureMark) || !stage07Positive(exposureValue) {
			return invalid("Stage 05 exposure time, mark, or value is invalid")
		}
		artifactMark := marksAsOf(series, exposureAt)[position.Symbol]
		if !stage07Positive(artifactMark) || !stage07EquityNear(exposureMark, artifactMark, capital) || !stage07EquityNear(exposureValue, quantity*exposureMark, capital) {
			return invalid("Stage 05 exposure mark/value differs from its point-in-time bar")
		}
		mark := marks[position.Symbol]
		if !stage07Positive(mark) {
			return invalid("final point-in-time mark is missing")
		}
		value := position.Quantity * mark
		basis := position.Quantity * position.EntryPrice
		pnl := value - basis - position.EntryFee
		if !stage07Positive(value) || !stage07Positive(basis) || !stage07Nonnegative(math.Abs(pnl)) {
			return invalid("final marked position has nonfinite economics")
		}
		positions = append(positions, validation.ResidualPositionPrimitive{Symbol: position.Symbol, MarkedAt: end.Time.UTC(), Quantity: position.Quantity, CostBasis: basis, EntryFee: position.EntryFee, MarkPrice: mark, MarkValue: value, UnrealizedPnL: pnl})
		markedValue += value
	}
	metricEnd, err := strconv.ParseFloat(result.Metrics.EndingEquity, 64)
	if err != nil || !stage07EquityNear(inventory.Cash+markedValue, end.Value, capital) || !stage07EquityNear(end.Value, metricEnd, capital) {
		return invalid("cash plus final marked inventory does not reconcile to the equity curve and Stage 05 metric")
	}
	return positions, nil
}

func stage07EquityNear(a, b, capital float64) bool {
	return !math.IsNaN(a) && !math.IsInf(a, 0) && !math.IsNaN(b) && !math.IsInf(b, 0) && math.Abs(a-b) <= math.Max(1e-8, math.Abs(capital)*1e-10)
}
