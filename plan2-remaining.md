# Plan 2 — Remaining Work: Dynamic Universe Selection

## Status: ~90% Complete

The universe pipeline (metadata, eligibility, liquidity filters, regime gating, cross-sectional ranking, snapshot persistence, settings) is fully operational. The remaining gaps are in backtest replay and operator visibility.

---

## 1. Dynamic Replay Backtest Mode (Task 7)

**Status:** Not Implemented

### What exists
- `UniverseStatic` mode — uses `backtest_symbols` list
- `UniverseDynamicRecompute` mode — rebuilds universe from historical candles during backtest
- Both modes are functional in `engine.go`

### What is missing
- `dynamic_replay` mode — replay persisted historical `UniverseSnapshot` records during backtest
- This mode would use stored snapshots instead of recomputing from candles, giving faster and more reproducible backtests
- Requires loading `UniverseSnapshot` + `UniverseMember` records by timestamp
- Needs point-in-time lookup: at each backtest bar, find the most recent snapshot before that timestamp

### Implementation approach
- Add `UniverseDynamicReplay` constant to `backtest/types.go`
- Add snapshot loader function in `services/universe.go` or `backtest/engine.go`
- In `RunBacktest`, when mode is `dynamic_replay`:
  - Load all snapshots within the backtest window
  - At each decision point, use the latest snapshot that precedes the current timestamp
  - Derive `backtestUniverseSelection` from the snapshot members

### Files to update
- `internal/backtest/types.go` — add `UniverseDynamicReplay` constant
- `internal/backtest/engine.go` — add replay branch in universe selection logic
- `internal/services/universe.go` — add `LoadUniverseSnapshotsForReplay(start, end)` function

---

## 2. Dedicated Universe Snapshot UI (Task 9)

**Status:** Partial

### What exists
- Governance overview panel shows regime state, breadth ratio, and monitoring data
- Settings panel has universe policy controls
- Backend persists full snapshot data including rejection reasons

### What is missing
- A dedicated UI view or API endpoint that shows:
  - The active universe snapshot with all members
  - Shortlist rank and component score breakdown per symbol
  - Why specific symbols were filtered out (rejection reasons)
  - Historical comparison of universe composition over time
  - Side-by-side comparison of current vs previous snapshot
- An API endpoint like `GET /api/universe/latest` returning full snapshot detail
- An API endpoint like `GET /api/universe/snapshots` for historical browsing

### Implementation approach
- Add handler in `internal/handlers/universe.go` (new file)
- Wire routes in `cmd/server/main.go`
- Add frontend component showing snapshot table with rejection reasons

### Files to add/update
- `internal/handlers/universe.go` — new handler file
- `cmd/server/main.go` — wire universe routes
- `frontend/src/components/UniverseViewer.jsx` — new component (optional)

---

## 3. Retire Legacy Scanner Settings (Task 8)

**Status:** Partial

### What exists
- `trending_coins_to_analyze` is still referenced as a fallback for `universe_analyze_top_n`
- The new universe settings are all in place and functional

### What is missing
- Formally deprecate `trending_coins_to_analyze` once the universe pipeline is confirmed stable
- Remove or hide the setting from the UI
- Currently it silently falls back — should log a deprecation warning

### Files to update
- `internal/services/universe.go` — add deprecation log
- `frontend/src/components/SettingsPanel.jsx` — remove from displayed settings

---

## 4. Validation Tasks

**Status:** Partial

### What exists
- `universe_test.go` covers hard filter, ranking, and risk-off tightening
- Dynamic universe backtest tests exist

### What is missing
- Compare forward returns of ranked buckets (live validation)
- Compare old scanner vs new scanner over the same periods
- Compare turnover and win rate after liquidity filtering
- Verify that dynamic-universe backtests materially differ from static-symbol runs
- Inspect symbol rejection reasons for sanity (manual or automated)

These are operational validation steps rather than code tasks, but some could be automated as test scripts.

---

## Implementation Priority

1. **Dynamic replay backtest mode** — enables faster reproducible backtests using stored snapshots
2. **Universe snapshot UI/API** — operator visibility for debugging and tuning
3. **Legacy setting deprecation** — cleanup
4. **Validation comparisons** — operational quality assurance
