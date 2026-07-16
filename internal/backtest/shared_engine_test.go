package backtest

import (
	"testing"
	"time"

	"trading-go/internal/services"
)

func TestRunBacktestSharedEngineRoutesEntriesThroughCommonBroker(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	series := map[string][]services.OHLCV{"BTCUSDT": buildBacktestSeries(start, 800, 100, .01, 20), "AAAUSDT": buildBacktestSeries(start, 800, 50, .08, 30)}
	config := BacktestConfig{EngineMode: EngineShared, Symbols: []string{"BTCUSDT", "AAAUSDT"}, UniverseMode: UniverseStatic, UniversePolicy: services.UniversePolicy{TopK: 2, AnalyzeTopN: 2}, Start: start, End: start.Add(800 * 15 * time.Minute), IndicatorConfig: services.DefaultIndicatorConfig(), IndicatorWeights: map[string]float64{"rsi": 1, "macd": 1, "bollinger": 1, "volume": .5, "momentum": 1}, Timeframe: "15m", TimeframeMinutes: 15, InitialBalance: 1000, FeeBps: 10, SlippageBps: 5, MaxPositions: 1, StrategyMode: StrategyBaseline, EntryPercent: 20, BuyOnlyStrong: false, MinConfidenceToBuy: 0, TimeStopBars: 4, AllowSellAtLoss: true}
	result, err := RunBacktest(config, series)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Trades) == 0 {
		t.Fatal("shared engine backtest produced no closed trades")
	}
	if result.SharedEngineRuns == 0 || result.SharedLedgerEvents == 0 || result.SharedEngineRuns < result.SharedLedgerEvents {
		t.Fatalf("backtest bypassed orchestrator/ledger: runs=%d events=%d", result.SharedEngineRuns, result.SharedLedgerEvents)
	}
	if result.SharedLedgerEvents < len(result.Trades)*2 {
		t.Fatalf("entries/exits were not both ledger-applied: events=%d trades=%d", result.SharedLedgerEvents, len(result.Trades))
	}
	for _, trade := range result.Trades {
		if trade.EntryPrice <= 0 || trade.Size <= 0 {
			t.Fatalf("invalid broker-derived trade: %+v", trade)
		}
	}
}

func TestSharedBacktestRespectsNonUSDTSettlementAndVenue(t *testing.T) {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	series := map[string][]services.OHLCV{"BTCEUR": buildBacktestSeries(start, 800, 100, .01, 20), "AAAEUR": buildBacktestSeries(start, 800, 50, .08, 30)}
	config := BacktestConfig{EngineMode: EngineShared, AccountID: "research-eur", SettlementCurrency: "EUR", VenueID: "kraken", Symbols: []string{"BTCEUR", "AAAEUR"}, UniverseMode: UniverseStatic, UniversePolicy: services.UniversePolicy{TopK: 2, AnalyzeTopN: 2}, Start: start, End: start.Add(800 * 15 * time.Minute), IndicatorConfig: services.DefaultIndicatorConfig(), IndicatorWeights: map[string]float64{"rsi": 1, "macd": 1, "bollinger": 1, "volume": .5, "momentum": 1}, Timeframe: "15m", TimeframeMinutes: 15, InitialBalance: 1000, FeeBps: 10, SlippageBps: 5, MaxPositions: 1, StrategyMode: StrategyBaseline, EntryPercent: 20, MinConfidenceToBuy: 0, TimeStopBars: 4, AllowSellAtLoss: true}
	result, err := RunBacktest(config, series)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Trades) == 0 || result.SharedLedgerEvents < len(result.Trades)*2 {
		t.Fatalf("non-USDT shared fixture did not traverse ledger: %+v", result)
	}
	instrument, err := backtestInstrument(config, "BTCEUR")
	if err != nil || instrument.QuoteAsset.String() != "EUR" || instrument.Venue.String() != "kraken" || instrument.VenueSymbol != "BTCEUR" {
		t.Fatalf("configured instrument dimensions = %+v err=%v", instrument, err)
	}
}

func TestBacktestUnknownEngineModeFailsClosed(t *testing.T) {
	_, err := RunBacktest(BacktestConfig{EngineMode: "shared_typo", InitialBalance: 1000}, map[string][]services.OHLCV{})
	if err == nil || err.Error() != `unknown backtest engine mode "shared_typo"` {
		t.Fatalf("unknown mode error = %v", err)
	}
}
