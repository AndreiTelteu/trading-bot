Implementation Plan 1: Regime-Aware Multi-Timeframe Gating

Goal
Add a trend-regime and volatility gate so 15m BUY signals are only actionable when higher-timeframe conditions agree and volatility is within a controlled band.

Order of Execution
1) Data access and indicator extensions
2) Regime gate computation
3) Buy decision integration
4) Settings exposure
5) Backtest harness updates
6) Validation and rollout

Scope
- Backend: services/trending.go, services/indicators.go, services/analyzer.go, database seed settings, handlers/settings if new keys are needed.
- Frontend: Settings panel for new gating parameters.
- Backtest: External harness or new internal module to compare baseline vs gated logic.

Technical Design

1) Add higher-timeframe OHLCV access
- Function: FetchOHLCV already supports interval; reuse it with "1h" or "4h".
- Implement helper function in services/trending.go:
  - fetchCandles(symbol, timeframe, limit) []Candle
  - input: symbol string, timeframe string, limit int
  - output: []Candle
  - reuse exchange.FetchOHLCV and map to Candle

2) Add regime indicators
- Extend services/indicators.go to include EMA and ATR (if ATR not present).
  - Function: CalculateEMA(series []float64, period int) float64
  - Function: CalculateATR(candles []Candle, period int) float64
  - Use typical ATR = EMA of true range.
- In services/trending.go:
  - build features for 1h candles:
    - ema50 = CalculateEMA(closes, 50)
    - ema200 = CalculateEMA(closes, 200)
    - trend_ok = ema50 > ema200
  - build volatility gate on 15m:
    - atr14 = CalculateATR(candles15m, 14)
    - vol_ratio = atr14 / currentPrice
    - vol_ok = vol_ratio between [vol_min, vol_max]

3) Gate the buy decision
- In AnalyzeTrendingCoins:
  - After analysis := analyzeSymbolForTrending(symbol, "15m"), compute gating conditions.
  - Require: signalQualifies && confidenceQualifies && trend_ok && vol_ok.
  - If gating fails, skip trade, still store analysis history.
- Ensure gating outcome is logged:
  - Extend TrendAnalysisHistory to include gate flags if needed, or add logActivity details.

4) Settings and configuration
- Add settings keys in database.SeedData:
  - regime_timeframe = "1h"
  - regime_ema_fast = "50"
  - regime_ema_slow = "200"
  - vol_atr_period = "14"
  - vol_ratio_min = "0.002"
  - vol_ratio_max = "0.02"
  - regime_gate_enabled = "true"
- Add retrieval helpers with getSettingFloat/getSettingInt in trending.go.
- Frontend SettingsPanel.jsx:
  - Add new fields under trading settings or create a new section.

5) Backtesting methodology integration
- Add a backtest configuration file or struct:
  - Inputs: symbol list, start/end dates, timeframe, fee, slippage.
  - Add parameters for regime gate thresholds.
- For each symbol and bar:
  - compute 15m analysis
  - compute 1h regime and 15m volatility gate
  - run baseline and gated strategies side-by-side for benchmark output.
- Metrics:
  - Sharpe, max drawdown, win rate, profit factor
  - Additional: exposure time, trade count, avg holding time

6) Validation and rollout
- Walk-forward splits: 12 months train + 3 months test, rolling.
- Out-of-sample final 3–6 months.
- Statistical testing: bootstrap 95% CI on Sharpe and profit factor.
- Feature flag: regime_gate_enabled to allow quick rollback.

Function-Level Changes

services/trending.go
- Add fetchCandles(symbol, timeframe, limit)
- Add computeRegimeGate(candles1h []Candle, settings map[string]string) (trendOK bool)
- Add computeVolGate(candles15m []Candle, price float64, settings map[string]string) (volOK bool)
- Modify AnalyzeTrendingCoins to apply gating logic before executeBuyFromTrending

services/indicators.go
- Add CalculateEMA
- Add CalculateATR

database/database.go
- Add default settings for regime and volatility gate

frontend/src/components/SettingsPanel.jsx
- Add configuration inputs for regime and volatility gate

Risks and Mitigations
- Risk: Reduced trade frequency
  - Mitigation: thresholds configurable and tested via backtest
- Risk: Noisy EMA cross on low-liquidity assets
  - Mitigation: enforce minimum volume filter before analysis

Milestones
1) Implement EMA/ATR and gating helpers
2) Add settings and UI
3) Integrate gating into trade decision
4) Backtest and tune parameters
5) Rollout with feature flag and monitoring
