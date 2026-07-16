# Stage 00 current-state architecture and contracts

This document freezes the behavior observed before the shared-core and ledger
stages. It is descriptive, including known defects; it is not a profitability or
live/backtest parity claim.

## Current live/paper analysis and entry flow

1. The cron scheduler or `POST /api/trending/analyze` calls
   `services.AnalyzeTrendingCoins`.
2. `BuildUniverseSnapshot` reads settings, fetches current Binance metadata and
   bars, applies eligibility/ranking, persists the universe snapshot, and returns
   a shortlist plus the current BTC regime state.
3. `AnalyzeShortlist` fetches 15m candles per shortlisted symbol, calculates RSI,
   MACD, Bollinger, momentum, and volume ratings, and produces the weighted
   `STRONG_BUY`/`BUY`/`NEUTRAL`/`SELL`/`STRONG_SELL` classification. When a model
   artifact is configured it also builds feature snapshots, predicts, ranks,
   persists prediction logs, and records rollout metadata.
4. `ExecuteShortlistTrades` evaluates entry rejection reasons in this exact
   order: analysis error, auto-trade disabled, universe risk-off, model not
   selected, signal, confidence/model floors, per-symbol regime gate, volatility
   gate, then maximum positions. An existing position is checked only after all
   those gates and is reported as `position_exists`.
5. In rule mode, signal and rating control entry. In model mode, policy selection
   plus probability and expected-value floors control entry. Model mode is used
   only when the rollout policy allows model entries and at least one analysis has
   a model ranking.
6. `executeBuyFromTrendingWithContext` fetches the current ticker. Fixed sizing
   spends `entry_percent` of current wallet cash. Volatility sizing risks
   `risk_per_trade` of marked portfolio value over `ATR * stop_mult`, capped by
   `max_position_value` and cash; it persists an ATR stop, target, and optional
   time stop. The currently persisted `rebuy_percent`, `pyramiding_enabled`,
   `max_pyramid_layers`, and `position_scale_percent` do not alter this sizing.
7. The paper auto-buy transaction creates/reopens/adds to a position, inserts a
   filled order, and subtracts cash. Although the low-level function can add to
   an open row, `ExecuteShortlistTrades` skips an existing open symbol, so normal
   shortlist execution does not pyramid.
8. The analysis decision/history and trade records are persisted. Activity,
   trending, wallet, position, and completion messages are broadcast through the
   global WebSocket broadcaster.

There are also separate paths: `/api/trading/buy|sell` submits exchange requests
and mutates wallet/position/order projections; `/api/positions-trade/open|close`
simulates paper fills; and `/api/positions/:id/close` plus
`DELETE /api/positions/:symbol` directly mutate/remove positions.

## Current exit behavior and precedence

Protective exits are evaluated on price updates and again on bar close. First
match wins:

1. explicit hard stop;
2. explicit take-profit;
3. ATR trailing stop when enabled;
4. percent trailing stop when enabled;
5. fallback percent stop when no explicit stop exists;
6. fallback percent take-profit when no explicit target exists;
7. time stop at bar close;
8. `SELL`/`STRONG_SELL` signal at bar close.

`allow_sell_at_loss=false` suppresses only time and signal exits below entry.
Protective exits remain active. `exit_pending` is claimed transactionally by the
execution coordinator to reduce duplicate runtime closes and is released after a
failed submission. Backtests use the same exit-rule functions, resolve same-bar
stop/take-profit collisions in favor of the stop, and use optional 1m replay for
protective fills. The current backtest engine does **not** liquidate remaining
positions at the configured end date; final open positions remain marked in the
equity curve and produce no closing trade.

## Current backtest flow and known parity differences

`RunBacktestSyncWithOverrides` loads settings and market series, then runs the
baseline and volatility-sizing modes separately. `RunBacktest` filters the
series, constructs a union timeline, computes signals from rolling bars, builds
or replays universe snapshots, evaluates open-position exits, selects entries,
sizes/fills them, and derives equity, trades, metrics, and diagnostics.

Known differences from runtime execution:

- Live analysis fetches the current Binance universe and data; static and
  dynamic-recompute backtests can use the supplied historical symbol set and
  current database metadata. Point-in-time membership is guaranteed only when a
  complete dynamic-replay snapshot set is supplied, and coverage is not yet
  proven.
- Runtime entry uses the latest ticker after a 15m decision. Backtest entry uses
  the decision bar close, or the next available 1m open, then configured
  slippage. Signal-time/fill-time separation is therefore mode/data dependent.
- Exchange execution can reject or return exchange status. Auto-trend paper
  execution assumes an immediate full fill with no fee/slippage. Backtest assumes
  deterministic full fills and applies configured fee/slippage.
- Runtime and backtest duplicate entry qualification and sizing orchestration;
  sharing indicator and exit helpers does not establish full decision parity.
- Runtime model selection is activated by rollout state and the presence of
  rankings. Backtest model behavior is selected by its explicit config/artifact.
- Runtime portfolio value uses persisted latest marks and current wallet state.
  Backtest uses its in-memory cash and latest replay marks.
- Runtime trailing exits are tick/stream driven with a periodic fallback.
  Backtest uses bar OHLC or optional 1m execution series and deterministic
  same-bar precedence.
- Backtest prevents a second position per symbol and has no rebuy/pyramid path.
  Runtime shortlist execution also skips an open symbol, despite the lower-level
  auto-buy function supporting weighted-average additions.
- Backtest does not perform end-of-period liquidation. Runtime positions remain
  open until an explicit exit path acts.
- Backtest results are in-memory derived evidence; runtime wallet, position,
  order, snapshot, and history tables are mutable projections without an
  immutable accounting source.

Full live/backtest parity remains unproven until Stage 02 routes all modes through
one orchestrator. No-lookahead and historical coverage remain unproven until
Stages 03 and 04.

## Governance semantics

The active model must be non-empty before any model rollout state can control an
entry. Current `ModelSelectionPolicy` semantics are:

| State | Effective entry mode | Current meaning |
| --- | --- | --- |
| `research_only` | `rule_rank` | Model may be evaluated/logged; it does not select runtime entries. |
| `shadow` | `rule_rank` | Model predictions/ranks are persisted as shadow observations; rules select entries. |
| `paper` | `model_rank` | Model selection is permitted by `UseForLiveEntries`; the current name is governance intent, not an execution-mode enforcement boundary. |
| `limited_live` | `model_rank` | Model selection is permitted; no separate exposure limiter is imposed by this method. |
| `full_live` | `model_rank` | Model selection is permitted. |
| `rollback` | `rule_rank` | Model selection is disabled and rule ranking resumes. `rollback_target` is metadata for the target artifact. |

Unknown/empty rollout states do not enable model entries; policy loading defaults
an empty configured state to `shadow`. Promotion is represented by settings,
policy versions, model artifacts, experiments, rollout events, and monitoring
snapshots. Stage 00 does not assert that these records form a complete live
authorization boundary.

## Accounting invariants and evidence boundary

Target invariants for the later immutable ledger are:

- cash after a fill equals prior cash plus signed fill proceeds minus fees plus
  explicit capital adjustments;
- open quantity equals signed filled quantity by account and symbol;
- realized P&L is derived from matched fills and fees, not independently edited;
- every order/fill/fee/capital adjustment has one stable identifier and is
  append-only/idempotent;
- wallet and position projections reconcile exactly to ledger events at a stated
  time;
- portfolio value equals reconciled cash plus positions valued by an identified
  observation; and
- no close or delete API can remove economic state without compensating events.

These invariants are **not currently guaranteed**. Wallet, position, and order
rows are mutable; the direct close endpoint changes status without cash proceeds
or an order, and delete removes the position without an accounting event. Paper
auto-trades omit fees and slippage. Existing portfolio snapshots, realized P&L,
returns, drawdowns, Sharpe ratios, and historical account metrics are therefore
explicitly **unreconciled historical evidence**. They must not support a
profitability claim or promotion decision until Stage 01 reconciliation and the
later data/parity stages are complete.

## Target package and dependency direction

`internal/tradingcore` contains transport- and persistence-free value types plus
the `Clock`, `IDGenerator`, `MarketDataSource`, `UniverseProvider`, `Strategy`,
`RiskEngine`, `Broker`, and `Ledger` contracts. `internal/tradingcore/testkit`
supplies deterministic clocks, IDs, settings, observations, and portfolio state.

The intended dependency direction is:

```text
handlers / cron / commands
          |
          v
future application orchestrator
          |
          v
internal/tradingcore contracts and domain values
          ^
          |
database / exchange / websocket adapters
```

The core package imports only the Go standard library. Fiber handlers, GORM
models, database globals, exchange globals, and the WebSocket broadcaster must
remain outside it. Stage 00 defines this boundary without migrating production
callers; Stage 01 adds the ledger implementation and Stage 02 adds the shared
orchestrator/adapters.
