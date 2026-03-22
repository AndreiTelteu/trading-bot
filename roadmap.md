# Trading Bot Profitability Roadmap

## Thesis

The current system is no longer bottlenecked by parameter tuning. The next large improvement will come from changing the architecture in four places:

1. make live execution and backtest behavior match much more closely,
2. replace static/top-volume coin discovery with a real dynamic universe pipeline,
3. replace hand-tuned indicator voting with a learned, calibrated ranking model,
4. tighten the research, validation, and rollout loop so new ideas are promoted only when they survive realistic walk-forward tests.

## Direct answer on websocket-based TP/SL

Moving live TP/SL handling from cron polling to websocket-based monitoring is the right direction.

That will make backtest results closer to live performance only if the backtest is also upgraded to:

- keep signal generation on completed bars,
- replay execution on lower-timeframe bars,
- use the same exit-rule engine as live,
- simulate trigger timing, gap risk, and slippage under the same execution assumptions.

Websocket exits improve live execution by themselves. They improve backtest realism only when the backtest is rebuilt around the same event model.

## What to stop doing

- Stop spending cycles on small weight tweaks and threshold sweeps.
- Stop treating profit factor improvements below realistic execution costs as meaningful.
- Stop relying on static `backtest_symbols` as the main research harness.
- Stop exposing model coefficients and indicator weights as primary live controls.

## Strategic direction

The target architecture should be:

- **Discovery layer**: build a clean, liquid, regime-aware universe.
- **Model layer**: rank candidates with learned, calibrated probabilities or expected value.
- **Execution layer**: use event-driven price monitoring for protective exits and bar-close logic for discretionary exits.
- **Research layer**: evaluate the exact same decision path with point-in-time data and walk-forward validation.

## Roadmap phases

## Phase 1 — Execution parity first

Goal: reduce live/backtest mismatch before more optimization work.

Primary outcomes:

- websocket-driven TP/SL/trailing/ATR trailing,
- shared exit-rule engine used by live and backtest,
- 15m strategy decisions plus 1m execution replay in backtest,
- removal of cron-based price polling as the primary exit path.

Detailed plan: [`plan_1_websocket_execution_parity.md`](./plan_1_websocket_execution_parity.md)

## Phase 2 — Replace the coin scanner with a real universe pipeline

Goal: stop using static symbols and weak “top volume/gainers/losers” discovery.

Primary outcomes:

- dynamic USDT tradable universe,
- liquidity, listing-age, data-quality, and regime filters,
- cross-sectional shortlist generation,
- dynamic-universe backtesting.

Detailed plan: [`plan_2_dynamic_universe_selection.md`](./plan_2_dynamic_universe_selection.md)

## Phase 3 — Replace hand-tuned voting with a learned model

Goal: move from indicator weighting to point-in-time prediction and ranking.

Primary outcomes:

- model-ready dataset and feature store,
- calibrated logistic baseline,
- nonlinear model upgrade path,
- top-K ranking instead of threshold-only buys,
- retirement of manual weights and manual probability betas.

Detailed plan: [`plan_3_learned_signal_model.md`](./plan_3_learned_signal_model.md)

## Phase 4 — Tighten validation, promotion, and operator controls

Goal: promote only robust improvements and simplify what operators can change live.

Primary outcomes:

- purged walk-forward model promotion,
- shadow → paper → live rollout pipeline,
- drift/calibration monitoring,
- cleaner settings surface focused on risk, universe, execution, and model policy.

Detailed plan: [`plan_4_validation_rollout_and_cleanup.md`](./plan_4_validation_rollout_and_cleanup.md)

## Recommended implementation order

1. complete Phase 1,
2. start Phase 2 data capture and dynamic universe replay,
3. begin Phase 3 with a logistic baseline only after Phase 1 and the Phase 2 data contract are stable,
4. complete Phase 4 before trusting live capital increases.

## Short-term success criteria

Before any new optimization loop is considered valid, the system should achieve all of the following:

- live exits no longer depend on a 1-minute price cron,
- backtests support 15m signal bars and 1m execution bars,
- backtests can replay a dynamic universe instead of only static symbols,
- candidate ranking can be analyzed by decile or top-K bucket,
- promotion decisions use walk-forward results, not one aggregate run.

## End-state operator experience

The settings UI should eventually focus on only four categories:

- **Execution & Risk**
- **Universe Selection**
- **Model & Policy**
- **Backtest & Validation**

The following should be retired from day-to-day live control once the new stack is stable:

- indicator weights,
- manual indicator tuning as live knobs,
- manual probability betas,
- `buy_only_strong` / `min_confidence_to_buy` as the primary entry logic.

## Deliverables created with this roadmap

- [`plan_1_websocket_execution_parity.md`](./plan_1_websocket_execution_parity.md)
- [`plan_2_dynamic_universe_selection.md`](./plan_2_dynamic_universe_selection.md)
- [`plan_3_learned_signal_model.md`](./plan_3_learned_signal_model.md)
- [`plan_4_validation_rollout_and_cleanup.md`](./plan_4_validation_rollout_and_cleanup.md)
