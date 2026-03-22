# Plan 1 — Remaining Work: WebSocket Execution Parity

## Status: ~85% Complete

The core live infrastructure (shared exit engine, streaming, execution coordinator, bar-close exits, cron fallback) is fully operational. The remaining gaps are in backtest execution fidelity and formal verification.

---

## 1. Formal Exit Policy Documentation

**Status:** Not done

- Write a canonical document or code comment block that defines:
  - Close-precedence order (stop_loss > take_profit > atr_trailing > trailing_stop > time_stop > sell_signal)
  - Which exits are evaluated on ticks vs 15m bars
  - How `allow_sell_at_loss` interacts with each exit type
- Currently the policy is implicit in `EvaluateProtectiveExit` and `EvaluateBarCloseExit` but not formally documented

### Files to update
- `internal/services/exit_rules.go` (add doc comments)
- Optionally a `docs/exit_policy.md`

---

## 2. Multi-Timeframe Backtest Input Loading (Task 8.1)

**Status:** Partial

### What exists
- Backtest loads a single timeframe (15m) for both signals and execution

### What is missing
- Load 1m candles for execution replay alongside 15m candles for signal generation
- Align them point-in-time without future leakage
- `BacktestConfig` needs fields for execution timeframe data
- `job.go` needs to fetch and pass both timeframes to the engine

### Files to update
- `internal/backtest/types.go` — add `ExecutionTimeframe` config field
- `internal/backtest/job.go` — fetch 1m candles alongside 15m
- `internal/backtest/engine.go` — accept and iterate over both timeframes

---

## 3. Entry Timing at Next 1m Open (Task 8.2)

**Status:** Partial

### What exists
- Entries apply slippage to the 15m bar close price via `applySlippage(bar.Close, ...)`

### What is missing
- Compute buy/sell decision only after a 15m bar completes
- Fill entries at the next 1m open (or next valid 1m execution mark)
- Apply buy slippage at the 1m execution point, not the 15m signal bar close

### Files to update
- `internal/backtest/engine.go` — change entry fill logic to use 1m open after signal

---

## 4. Intrabar Protective Exit Simulation (Task 8.3)

**Status:** Not Implemented

### What exists
- Backtest evaluates stop-loss and take-profit at 15m bar close only
- `determineExitPrice` has gap-aware fill rules but only for bar-level data

### What is missing
- Evaluate stop-loss from 1m low vs stop price (intrabar)
- Evaluate take-profit from 1m high vs target price (intrabar)
- Apply gap-aware fill rules using 1m data
- Define a deterministic tie-break rule when both TP and stop are hit in the same 1m bar
- This is the single largest execution-parity gap between live and backtest

### Files to update
- `internal/backtest/engine.go` — add intrabar exit loop over 1m candles
- `internal/backtest/types.go` — add tie-break policy config

---

## 5. Integration Tests for Stream Infrastructure (Task 9)

**Status:** Partial

### What exists
- `execution_coordinator_test.go` — duplicate tick burst test
- Unit tests for exit rules and trailing stop ratchets

### What is missing
- Test: reconnect resumes monitoring of open positions
- Test: restart resubscribes to existing open positions
- Test: simulated tick stream triggers exactly one close
- These may require a mock `PriceStream` implementation for deterministic testing

### Files to add
- `internal/services/position_monitor_test.go`
- `internal/services/stream_supervisor_test.go`

---

## 6. Shadow Mode Verification (Task 10)

**Status:** Partial

### What exists
- `stream_exit_enabled` setting exists and is checked
- Cron fallback remains active when stream is unhealthy

### What is missing
- Formal shadow-mode deployment where both cron and stream run in parallel
- Comparison of cron decision timestamps vs stream trigger timestamps
- Verification that no duplicate close orders occur across both paths
- Defined criteria for removing 1m price cron after parity is verified

### Files to update
- `internal/services/stream_supervisor.go` — add shadow-mode logging
- `internal/cron/scheduler.go` — add shadow comparison logging

---

## Implementation Priority

1. **Intrabar protective exits (Task 8.3)** — highest impact on backtest fidelity
2. **Multi-timeframe loading (Task 8.1)** — prerequisite for task 8.3
3. **Entry timing (Task 8.2)** — improves entry fill realism
4. **Integration tests (Task 9)** — safety net before relying on stream exits
5. **Shadow mode verification (Task 10)** — operational safety
6. **Exit policy documentation (Task 1)** — useful but lower urgency
