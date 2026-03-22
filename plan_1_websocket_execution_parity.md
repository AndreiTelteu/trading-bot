# Plan 1 — Websocket Execution Parity

## Goal

Replace cron-polled price exits with event-driven monitoring, then rebuild backtest execution so it simulates the same live behavior as closely as possible.

## Why this plan comes first

Current optimization is happening on top of a live/backtest mismatch:

- live protective exits are checked on a 1-minute cron,
- backtest exits are evaluated from bar closes,
- live and backtest do not share one canonical exit engine,
- auto-closes mutate local DB state directly instead of going through a strict execution coordinator.

Until this is fixed, every later optimization result is partly contaminated by execution-model noise.

## Target execution model

Use a hybrid architecture:

- **Realtime plane**: websocket-driven price monitoring for `stop_loss`, `take_profit`, `trailing_stop`, and `atr_trailing_stop`
- **Bar-close plane**: 15m decision loop for `sell_signal`, `time_stop`, ATR refresh, and new entries
- **Reconciliation plane**: order/fill synchronization and fallback reconciliation

## Core design rules

1. Protective exits must be event-driven.
2. Signal exits should remain bar-close based.
3. Live and backtest must call the same exit-rule functions.
4. Every close must be idempotent.
5. Paper and exchange-backed positions must be explicit and not inferred indirectly.

## Exact implementation tasks

## 1. Freeze and document the exit policy

- [ ] Define one canonical close-precedence order.
- [ ] Decide how `allow_sell_at_loss` works.
- [ ] Recommended policy:
  - protective exits (`stop_loss`, `take_profit`, `trailing_stop`, `atr_trailing_stop`) always execute,
  - discretionary exits (`sell_signal`, `time_stop`) may respect `allow_sell_at_loss`.
- [ ] Document which exits are evaluated on ticks vs 15m bars.

### Files to update

- `internal/services/trading.go`
- `internal/backtest/engine.go`
- `roadmap.md` if the policy changes materially

## 2. Add explicit execution-state fields

### New `Position` fields

- [ ] `execution_mode` — `paper`, `exchange`, `shadow`
- [ ] `entry_source` — `manual`, `auto_trend`, `backfill`, `paper_test`
- [ ] `exit_pending` — guards duplicate closes
- [ ] `last_mark_price`
- [ ] `last_mark_at`
- [ ] `exchange_position_ref` or `client_position_id`
- [ ] `decision_timeframe` (start with `15m`)

### New `Order` fields

- [ ] `exchange_order_id`
- [ ] `client_order_id`
- [ ] `status`
- [ ] `execution_mode`
- [ ] `trigger_reason`
- [ ] `requested_price`
- [ ] `fill_price`
- [ ] `executed_qty`
- [ ] `exchange_fee`
- [ ] `submitted_at`
- [ ] `filled_at`

### Files to update

- `internal/database/models.go`
- `internal/database/migrations.go`
- `internal/database/database.go`

## 3. Extract a shared exit engine

Create one shared module that both live execution and backtest call.

### New code units

- [ ] `internal/services/exit_rules.go`
- [ ] `internal/services/exit_rules_test.go`

### Responsibilities

- [ ] compute trailing-stop ratchets
- [ ] evaluate stop / TP / trailing / ATR trailing
- [ ] evaluate time-stop eligibility
- [ ] evaluate discretionary loss policy
- [ ] return one close reason with deterministic precedence

### Important instruction

Do not leave separate copies of close logic in `trading.go` and `backtest/engine.go`. Both must delegate into the same pure rule helpers.

## 4. Build market-data streaming for open positions

### New code units

- [ ] `internal/services/exchange_stream.go`
- [ ] `internal/services/position_monitor.go`
- [ ] `internal/services/stream_supervisor.go`

### Required features

- [ ] subscribe only to symbols with open positions
- [ ] dynamically subscribe/unsubscribe on position open/close
- [ ] keep one serialized worker per symbol
- [ ] store latest mark/bid/ask timestamp
- [ ] reconnect with backoff
- [ ] mark streams stale if no update arrives inside timeout

### Recommended stream choice

- Start with Binance websocket market data for tracked symbols.
- Prefer an executable mark source like best bid/ask over slow 24h ticker data.

### Files to update

- `internal/services/exchange.go`
- `cmd/server/main.go`
- `internal/websocket/broadcaster.go` if new client messages are added

## 5. Replace cron-driven protective exits with an execution coordinator

### New code unit

- [ ] `internal/services/execution_coordinator.go`

### Required behavior

- [ ] accept a triggered close request from the position monitor
- [ ] lock the position row before acting
- [ ] set `exit_pending=true`
- [ ] reject duplicate close attempts
- [ ] route by `execution_mode`
  - `exchange` → place sell order
  - `paper` → local simulated close
- [ ] persist order lifecycle updates
- [ ] clear `exit_pending` only on terminal state

### Important instruction

Do not let websocket tick handlers directly mutate wallet balances or close positions. They should only ask the coordinator to close.

## 6. Rework `UpdatePositionsPrices()` into a fallback reconcile path

### Required changes

- [ ] stop using `UpdatePositionsPrices()` as the primary TP/SL path
- [ ] keep it as an admin fallback / manual reconcile tool
- [ ] keep snapshot generation separate from protective exit handling
- [ ] stop fetching ATR from REST on every exit check

### Files to update

- `internal/services/trading.go`
- `internal/handlers/trading.go`
- `internal/cron/scheduler.go`

## 7. Keep signal exits on the 15m bar-close plane

### Required behavior

- [ ] keep `sell_signal` checks on completed 15m bars
- [ ] keep `time_stop` checks on completed 15m bars
- [ ] refresh ATR on completed 15m bars
- [ ] do not compute full strategy analysis on every tick

### Files to update

- `internal/services/trading.go`
- `internal/services/trending.go`
- `internal/cron/scheduler.go`

## 8. Rebuild backtest execution for parity

## 8.1 Add multi-timeframe input loading

- [ ] load 15m candles for signal generation
- [ ] load 1m candles for execution replay
- [ ] align them point-in-time without future leakage

### Files to update

- `internal/backtest/job.go`
- `internal/backtest/types.go`

## 8.2 Change entry timing

- [ ] compute the buy/sell decision only after a 15m bar completes
- [ ] fill entries at the next 1m open or next valid 1m execution mark
- [ ] apply buy slippage at execution time, not the signal bar close

## 8.3 Change protective exits

- [ ] evaluate stop-loss from 1m low vs stop price
- [ ] evaluate take-profit from 1m high vs target price
- [ ] apply gap-aware fill rules
- [ ] define a deterministic tie-break rule when both TP and stop are hit in the same 1m bar

## 8.4 Keep discretionary exits on 15m bars

- [ ] `sell_signal` stays on 15m close
- [ ] `time_stop` stays on 15m close
- [ ] ATR refresh stays on 15m close

## 8.5 Use the shared exit engine

- [ ] backtest must call the same exit-rule helpers introduced earlier

### Files to update

- `internal/backtest/engine.go`
- `internal/backtest/validation.go`

## 9. Add tests before rollout

### Unit tests

- [ ] stop-loss precedence
- [ ] take-profit precedence
- [ ] trailing-stop ratchet
- [ ] ATR trailing update behavior
- [ ] same-bar TP/SL tie-break
- [ ] `allow_sell_at_loss` behavior

### Integration tests

- [ ] simulated tick stream triggers one close only
- [ ] duplicate tick bursts do not double-sell
- [ ] reconnect resumes monitoring of open positions
- [ ] restart resubscribes to existing open positions

### Files to add/update

- `internal/services/exit_rules_test.go`
- `internal/services/trading_test.go`
- new stream monitor tests
- backtest tests under `internal/backtest`

## 10. Rollout steps

- [ ] add feature flag `stream_exit_enabled`
- [ ] deploy in shadow mode first
- [ ] compare cron decision timestamps vs stream trigger timestamps
- [ ] verify no duplicate close orders
- [ ] keep cron reconcile as fallback for one rollout cycle
- [ ] remove 1m price cron only after parity is verified

## Detailed instructions for the implementation sequence

1. Add schema and migrations first.
2. Extract exit rules before adding streams.
3. Add the position monitor and coordinator before changing schedulers.
4. Keep the old cron path available as fallback until stream-driven exits are stable.
5. Upgrade backtest only after live exit logic is frozen.
6. Do not start model work until this plan is materially complete.

## Validation commands

- [ ] `go test -v ./...`
- [ ] manual test of position open → websocket monitoring → single close
- [ ] manual restart test with open positions

## Success criteria

- protective exits no longer depend on the 1-minute cron
- one position cannot be auto-closed twice
- live and backtest use the same close-precedence rules
- backtest fills differ meaningfully from current close-only behavior
- the performance gap between paper live and backtest becomes explainable mainly by real market frictions, not architecture mismatch

## Dependencies on later plans

- Plan 2 depends on the new execution assumptions for dynamic-universe replay quality.
- Plan 3 depends on this plan because model labels must reflect the real execution template.
