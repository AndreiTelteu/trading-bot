Implementation Plan 3: Volatility-Adjusted Position Sizing and Dynamic Exits

Goal
Replace fixed entry_percent sizing with risk-based sizing and volatility-scaled exits to optimize risk-adjusted returns and reduce drawdowns.

Order of Execution
1) Add ATR and volatility features
2) Risk-based sizing logic
3) Dynamic stop-loss/take-profit
4) Optional time-based exits
5) Backtesting and benchmark comparison
6) Validation and rollout

Scope
- Backend: services/indicators.go, services/trending.go, services/trading.go
- Settings: database seed defaults and retrieval
- Frontend: add settings inputs for risk sizing and dynamic exits
- Backtest: incorporate sizing and exit logic in simulations

Technical Design

1) Volatility feature
- Use ATR(14) on 15m candles:
  - atr = CalculateATR(candles, 14)
  - vol_stop = atr * k_stop
  - vol_tp = atr * k_tp

2) Risk-based position sizing
- Define risk budget as percentage of portfolio:
  - risk_budget = portfolio_value * risk_per_trade
  - position_size = risk_budget / vol_stop
- Cap position size by max_position_value and available balance.

3) Dynamic exits
- Stop-loss price: entry - vol_stop
- Take-profit price: entry + vol_tp
- Optional trailing: use ATR-based trailing to lock gains
- Optional time stop: exit after N bars if no profit

Pseudocode
risk_budget = portfolio_value * risk_per_trade
vol_stop = ATR(15m, 14) * stop_mult
position_size = min(risk_budget / vol_stop, max_position_value / price)

stop_price = entry_price - vol_stop
tp_price = entry_price + ATR(15m, 14) * tp_mult

exit if price <= stop_price or price >= tp_price or time_in_trade >= max_bars

Settings
- risk_per_trade (e.g., 0.5%)
- stop_mult (e.g., 1.5)
- tp_mult (e.g., 3.0)
- max_position_value (optional cap)
- time_stop_bars (optional)
- vol_sizing_enabled

Implementation Notes
- Portfolio value uses wallet balance plus open positions valued at current price when available.
- ATR-based sizing validates risk_per_trade, stop_mult, tp_mult, and max_position_value before ordering.
- StopPrice and TakeProfitPrice are stored per position and used for exit checks before percent-based exits.
- Time stop exits after time_stop_bars 15m bars only when PnL is not positive.
- Existing positions without stored stop/tp continue to use percent-based stop_loss_percent and take_profit_percent.

Function-Level Changes

services/trending.go
- Modify executeBuyFromTrending to accept size calculation inputs
- Add computePositionSize(atr, price, balance, settings) float64
- Use position_size instead of entry_percent when vol_sizing_enabled is true

services/trading.go
- Extend UpdatePositionsPrices:
  - Use per-position stored stop/tp prices if added to Position model
  - Or compute dynamic exits on the fly based on ATR
- Add optional time-stop check if tracking position open duration

database/models.go
- Add optional fields to Position:
  - StopPrice, TakeProfitPrice, MaxBarsHeld
- Include migration in database.AutoMigrate

frontend/src/components/SettingsPanel.jsx
- Add UI controls for risk_per_trade, stop_mult, tp_mult, max_position_value, time_stop_bars

Backtesting Methodology
- Simulate fixed entry_percent vs ATR sizing across 24+ months
- Include fees and slippage
- Compare metrics: Sharpe, max drawdown, win rate, profit factor
- Additional: average loss size, average win size, return volatility

Validation
- Walk-forward splits and out-of-sample holdout
- 95% confidence via bootstrap on drawdown and Sharpe improvement
- Minimum sample size per symbol to accept configuration

Risks and Mitigations
- Risk: Under-sizing in low volatility regimes
  - Mitigation: minimum position size floor
- Risk: Over-sizing in high volatility spikes
  - Mitigation: max_position_value and max_daily_exposure

Milestones
1) Add ATR-based sizing and settings
2) Integrate dynamic exits in trading loop
3) Update Position model and migration
4) Backtest and tune stop/tp multipliers
5) Release behind vol_sizing_enabled flag
