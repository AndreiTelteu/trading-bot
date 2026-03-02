Implementation Plan 3.1: Volatility Trailing Stops, Annualized Volatility, and Validation Harness

Goal
Fill the remaining gaps from plan_3_volatility_position_sizing.md by adding ATR-based trailing exits, annualized volatility calculation options, and a full backtest/validation workflow with statistical confidence reporting.

Scope
- Backend:
  - internal/services/trading.go
  - internal/services/trending.go
  - internal/services/indicators.go
  - internal/database/models.go
  - internal/database/database.go
- Settings:
  - defaults in SeedData
  - retrieval via GetAllSettings
- Backtest:
  - new package under internal/backtest
  - CLI entrypoint or command in cmd/backtest
- Validation:
  - bootstrap CI metrics
  - walk-forward split routines

Non-Goals
- Exchange integration changes
- UI visualization of backtest results (CSV/JSON output is sufficient)

Order of Execution
1) Add volatility annualization utilities and settings
2) Implement ATR-based trailing stop logic for open positions
3) Build backtest engine with benchmark strategy
4) Add walk-forward + bootstrap validation harness
5) Add tests for volatility math, trailing stop behavior, and backtest outputs

Requirements
1) Optional trailing ATR-based stop
- Must be disabled by default
- Uses ATR(14) on 15m candles by default
- Trailing stop should only move in favor of the position
- Integrate with existing trailing_stop_enabled without breaking percent-based trailing stop
- If ATR trailing is enabled, it must take precedence over percent trailing when both are configured

2) Backtest and benchmark comparison
- Simulate both:
  - Baseline (entry_percent + percent stops)
  - Volatility sizing (risk_per_trade + ATR exits)
- Must include:
  - fees
  - slippage
  - max_positions
  - time_stop_bars
- Output metrics:
  - Sharpe
  - max drawdown
  - win rate
  - profit factor
  - avg win, avg loss
  - return volatility

3) Validation (walk-forward, bootstrap CI)
- Use rolling windows:
  - Train window: 12 months
  - Test window: 3 months
- Produce bootstrap 95% CI for:
  - Sharpe
  - max drawdown
  - profit factor
- Accept configuration only if CI excludes baseline for at least 2 metrics

4) Annualization factor for volatility
- Provide a setting to choose annualization basis:
  - 365 days by default
- Annualize ATR by:
  - atr_annualized = atr * sqrt(bars_per_year)
  - bars_per_year = (365 * 24 * 60 / timeframe_minutes)
- If annualization is enabled, use annualized ATR for:
  - trailing stop distance
  - risk sizing
  - volatility reporting

Settings (new keys)
- atr_trailing_enabled (bool)
- atr_trailing_mult (float)
- atr_trailing_period (int, default 14)
- atr_annualization_enabled (bool)
- atr_annualization_days (int, default 365)
- backtest_fee_bps (float)
- backtest_slippage_bps (float)
- backtest_start (string, ISO date)
- backtest_end (string, ISO date)
- backtest_symbols (string, comma-separated)

Data Model Changes
- Position:
  - Add TrailingStopPrice *float64
  - Add LastAtrValue *float64
- PortfolioSnapshot:
  - Add VolatilityAnnualized *float64 (optional)

Implementation Details

1) Annualized ATR utilities
- Add helper in indicators.go:
  - CalculateAnnualizedATR(candles, period, timeframeMinutes, annualizationDays) float64
  - Use CalculateATR and scale by sqrt(bars_per_year)
- Add helper in trending.go:
  - getAtrValue(candles, period, annualizeEnabled, timeframeMinutes, annualizationDays) float64
- Integrate into:
  - computePositionSize
  - ATR trailing stop computation
- Ensure annualization is used consistently when enabled and does not alter existing behavior when disabled

2) ATR trailing stop behavior
- On position open (auto-trade only in first pass):
  - Initialize TrailingStopPrice = entryPrice - (atr * atr_trailing_mult)
- On each UpdatePositionsPrices:
  - If atr_trailing_enabled:
    - Recompute ATR on the last N bars for symbol (15m)
    - Compute candidateStop = currentPrice - (atr * atr_trailing_mult)
    - Update TrailingStopPrice only if candidateStop > existing TrailingStopPrice
    - If currentPrice <= TrailingStopPrice => closeReason = "atr_trailing_stop"
- Priority:
  - StopPrice / TakeProfitPrice
  - ATR trailing stop
  - Percent trailing stop
  - Percent stop loss / take profit
  - time_stop
  - sell_signal

3) Backtest architecture
- New package: internal/backtest
- Core types:
  - BacktestConfig {symbols, start, end, timeframe, feeBps, slippageBps, strategyMode}
  - PositionState {entry, size, stop, takeProfit, trailingStop, openedAt, barsHeld}
  - EquityCurve []float64
- Strategy modes:
  - baseline
  - vol_sizing
- Steps per bar:
  - compute indicators on rolling window
  - decide entry/exit based on strategy
  - apply fees and slippage at entry and exit
  - enforce max_positions
- Output:
  - JSON summary file and CSV equity curve per symbol

4) Validation harness
- Add internal/backtest/validation.go:
  - walkForwardSplit(data, trainMonths, testMonths)
  - runBootstrap(metricValues, iterations)
  - compareCI(baseline, candidate)
- Output:
  - metrics_summary.json with CI bounds and pass/fail per metric
- CLI:
  - cmd/backtest/main.go reading config from settings and flags

Tests
- indicators_test.go:
  - TestCalculateAnnualizedATR for known scaling
- trading_test.go:
  - Simulate ATR trailing stop updates and closeReason precedence
- backtest_test.go:
  - Deterministic mock series to validate metrics, fees, slippage
- validation_test.go:
  - Bootstrap CI bounds correctness and stability

Acceptance Criteria
- ATR trailing stop is optional and does not break percent trailing
- Annualization toggles correctly and only when enabled
- Backtest outputs baseline vs vol sizing with required metrics
- Walk-forward + bootstrap CI produces stable, reproducible results
- Total test coverage ≥ 90%

Dependencies
- None external; use existing exchange candle fetching utilities in backtest only if offline data is not required
- If offline data is required, store OHLCV as JSON/CSV under a new data folder (not generated by default)

Rollout
- Feature flags in settings (atr_trailing_enabled, atr_annualization_enabled)
- Start with paper trading only, then enable for auto-trade after validation passes
