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

Enhanced Plan (Detailed)

Phase 0: Baseline conventions
- ATR inputs default to 15m candles unless a backtest timeframe override is provided
- All new settings are seeded in SeedData and returned by GetAllSettings, preserving behavior when disabled
- Bootstrap and walk-forward use a fixed RNG seed for reproducibility

Phase 1: Data model and settings
- Position model: add TrailingStopPrice *float64 and LastAtrValue *float64
- PortfolioSnapshot model: add VolatilityAnnualized *float64
- New settings defaults:
  - atr_trailing_enabled: false
  - atr_trailing_mult: 1.0
  - atr_trailing_period: 14
  - atr_annualization_enabled: false
  - atr_annualization_days: 365
  - backtest_fee_bps: 10
  - backtest_slippage_bps: 5
  - backtest_start: empty
  - backtest_end: empty
  - backtest_symbols: empty
- Ensure GetAllSettings parses typed values with fallbacks

Phase 2: Annualized ATR utilities
- indicators.go: CalculateAnnualizedATR(candles, period, timeframeMinutes, annualizationDays)
- trending.go: getAtrValue(candles, period, annualizeEnabled, timeframeMinutes, annualizationDays)
- Use annualized ATR consistently for sizing, trailing stop distance, and volatility reporting when enabled

Phase 3: ATR trailing stop behavior
- On open: TrailingStopPrice = entryPrice - (atr * atr_trailing_mult)
- On each UpdatePositionsPrices:
  - Recompute ATR on last N bars for symbol
  - candidateStop = currentPrice - (atr * atr_trailing_mult)
  - Update only if candidateStop > TrailingStopPrice
  - If currentPrice <= TrailingStopPrice, closeReason = atr_trailing_stop
- Exit precedence:
  - StopPrice / TakeProfitPrice
  - ATR trailing stop
  - Percent trailing stop
  - Percent stop loss / take profit
  - time_stop
  - sell_signal

Phase 4: Backtest engine
- internal/backtest package with deterministic bar iteration
- BacktestConfig: symbols, start, end, timeframe, feeBps, slippageBps, maxPositions, timeStopBars, strategyMode
- Strategy modes:
  - baseline: entry_percent + percent stops
  - vol_sizing: risk_per_trade + ATR exits
- Execution per bar:
  - Compute indicators on rolling window
  - Evaluate entry signal
  - Enforce max_positions
  - Apply fees and slippage at entry and exit
  - Apply exit precedence with time_stop
- Output:
  - JSON summary per run
  - CSV equity curve per symbol and portfolio

Phase 5: Validation harness
- Rolling walk-forward split:
  - Train window 12 months, test window 3 months
  - Slide by test window length
- Bootstrap CI:
  - Compute metrics per window
  - 95% CI with fixed seed
- Acceptance rule:
  - Candidate must beat baseline CI for at least 2 metrics

Phase 6: Background execution and progress reporting
- Create a backtest job record with status, progress, started_at, finished_at, error
- Start endpoint returns job_id immediately
- Run backtest/validation in a goroutine, updating progress and status
- Broadcast progress via WebSocket events:
  - backtest_progress: {job_id, status, progress, message}
  - backtest_complete: {job_id, status, summary}
- Log milestones to ActivityLog for persistence and UI display

Phase 7: UI progress bar and results visibility
- Add a backtest run action in the UI that calls the start endpoint
- Subscribe to backtest_progress and backtest_complete over WebSocket
- Show a progress bar with status text and percent
- Persist the latest job state in UI state so reconnects can recover using a status endpoint

Metric definitions
- Sharpe: mean(returns) / std(returns) * sqrt(bars_per_year)
- Max drawdown: max peak-to-trough equity decline
- Win rate: wins / total trades
- Profit factor: gross profit / gross loss
- Avg win/loss: mean PnL of winning/losing trades
- Return volatility: std(returns)

Data requirements
- Default to existing candle fetching utilities for backtest runs
- Optional offline CSV/JSON under a data folder if needed later

Implementation sequencing (refined)
- Step 1: Settings and models
- Step 2: Annualized ATR helpers and integration
- Step 3: ATR trailing stop logic with precedence
- Step 4: Backtest core and outputs
- Step 5: Validation harness
- Step 6: Background job execution and progress events
- Step 7: UI progress bar and job status display
- Step 8: Tests and verification
