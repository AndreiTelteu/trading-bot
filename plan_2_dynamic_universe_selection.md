# Plan 2 — Dynamic Universe Selection

## Goal

Replace the current top-volume/gainers/losers scan with a production-grade universe pipeline that finds liquid, tradable, regime-appropriate coins and supports dynamic-universe backtesting.

## Why this plan matters

Current discovery is too weak for serious alpha generation:

- live selection is dominated by simple 24h ticker lists,
- the merged candidate set is effectively re-ranked by volume,
- static `backtest_symbols` does not test live discovery quality,
- poor universe selection leaks weak names into the signal engine and creates overtrading.

Better coin discovery is one of the highest-leverage changes available, because it improves every later step:

- cleaner candidate set,
- lower execution drag,
- better model training labels,
- better top-K ranking decisions.

## Chosen universe pipeline

Use a five-stage process:

1. **Eligibility** — only tradable, supported spot pairs
2. **Data quality and liquidity** — remove broken or thin names
3. **Market regime and breadth filter** — suppress risk-on exposure during weak market states
4. **Cross-sectional ranking** — score the remaining universe using trend, liquidity, and relative-strength features
5. **Shortlist analysis** — only then run detailed 15m signal analysis and execution logic

## Exact implementation tasks

## 1. Add symbol metadata and eligibility filtering

### Required features

- [ ] fetch and cache exchange symbol metadata
- [ ] keep only spot-tradable `USDT` pairs
- [ ] exclude obvious non-target instruments:
  - leveraged tokens,
  - stablecoin wrappers,
  - fiat wrappers,
  - delisted or suspended symbols,
  - symbols with insufficient listing age

### Recommended eligibility defaults

- quote asset: `USDT`
- listing age: at least 45 to 60 days
- spot trading required
- margin-only / special token variants excluded by default

### Files to update

- `internal/services/exchange.go`
- `internal/services/trending.go`
- `internal/database/models.go`
- `internal/database/migrations.go`

## 2. Persist universe metadata and snapshots

### New tables/entities

- [ ] `UniverseSymbol`
- [ ] `UniverseSnapshot`
- [ ] `UniverseMember`
- [ ] optional `UniverseFilterResult`

### Minimum fields

For `UniverseSymbol`:

- [ ] symbol
- [ ] base asset
- [ ] quote asset
- [ ] trading status
- [ ] listing start time or first seen time
- [ ] exclusion flags / reason

For `UniverseSnapshot`:

- [ ] timestamp
- [ ] rebalance interval
- [ ] regime state
- [ ] breadth metrics
- [ ] active universe size

For `UniverseMember`:

- [ ] snapshot id
- [ ] symbol
- [ ] liquidity metrics
- [ ] volatility metrics
- [ ] rank score
- [ ] shortlist flag
- [ ] rejection reason if filtered out

### Files to update

- `internal/database/models.go`
- `internal/database/migrations.go`
- `internal/database/database.go`

## 3. Build the liquidity and data-quality filter layer

### Required features

- [ ] minimum median quote volume on daily data
- [ ] minimum median quote volume on intraday data
- [ ] minimum bar coverage / low missing-bar ratio
- [ ] volatility band filter using ATR/price
- [ ] overextension filter to avoid extreme single-day pump names

### Recommended first-pass metrics

- 20d median daily quote volume
- 7d or 14d median 1h quote volume
- missing-bar ratio over the last 30d
- ATR/price on 1h and 4h
- distance from recent high
- 24h and 7d return shock caps

### Files to update

- `internal/services/indicators.go`
- `internal/services/trending.go`
- `internal/backtest/job.go` for replay support

## 4. Add market regime and breadth gating before ranking alts

### Required features

- [ ] compute BTC regime from 4h and 1d trend state
- [ ] compute market breadth from the eligible universe
- [ ] allow different universe behavior in different regimes

### Recommended behavior

- in strong risk-off regimes:
  - either trade nothing,
  - or reduce the universe to the most liquid majors only
- in neutral regimes:
  - tighten shortlist size and risk caps
- in risk-on regimes:
  - allow full shortlist generation

### Files to update

- `internal/services/trending.go`
- `internal/services/indicators.go`
- `internal/backtest/engine.go`

## 5. Add a rule-based cross-sectional ranker before the learned model exists

This is the bridge step between current discovery and the later learned-model stack.

### Required features

- [ ] compute a cross-sectional score from:
  - relative strength vs BTC,
  - relative strength vs the eligible universe,
  - multi-horizon momentum,
  - liquidity score,
  - trend quality,
  - volatility-in-range bonus,
  - overextension penalty
- [ ] normalize features cross-sectionally
- [ ] keep top `K` names in the active shortlist

### Recommended feature families

- 1d / 3d / 7d / 30d returns
- return residual vs BTC
- 4h and 1d EMA slope
- distance to 20d high / breakout proximity
- 24h volume acceleration vs 20d baseline
- ATR/price target-band bonus

### Files to update

- `internal/services/trending.go`
- `internal/services/analyzer.go`
- `internal/services/indicators.go`

## 6. Split discovery from analysis and execution

### Required refactor

Break `AnalyzeTrendingCoins()` into distinct stages:

- [ ] `RefreshUniverseMetadata()`
- [ ] `BuildUniverseSnapshot()`
- [ ] `RankUniverse()`
- [ ] `AnalyzeShortlist()`
- [ ] `ExecuteShortlistTrades()`

### Important instruction

Do not keep one giant function that discovers, scores, gates, trades, and logs everything in one pass. The discovery stage must become independently testable and replayable.

### Files to update

- `internal/services/trending.go`
- `internal/cron/scheduler.go`
- `internal/handlers/analyzer.go`

## 7. Add dynamic-universe backtest support

### Backtest modes to support

- [ ] `static` — current `backtest_symbols`
- [ ] `dynamic_recompute` — rebuild the universe from historical candles during backtest
- [ ] `dynamic_replay` — replay persisted historical universe snapshots

### Required rules

- [ ] universe membership must use only information known at time `t`
- [ ] rebalance universe on a slower cadence than execution
- [ ] new entries may only come from the active universe
- [ ] existing positions may remain open after a symbol drops from the universe
- [ ] newly listed symbols need warmup bars before eligibility

### Files to update

- `internal/backtest/types.go`
- `internal/backtest/job.go`
- `internal/backtest/engine.go`
- `internal/backtest/validation.go`

## 8. Add settings for universe policy

### New settings to add

- [ ] `universe_mode`
- [ ] `universe_rebalance_interval`
- [ ] `universe_min_listing_days`
- [ ] `universe_min_daily_quote_volume`
- [ ] `universe_min_intraday_quote_volume`
- [ ] `universe_max_gap_ratio`
- [ ] `universe_vol_ratio_min`
- [ ] `universe_vol_ratio_max`
- [ ] `universe_max_24h_move`
- [ ] `universe_top_k`
- [ ] `universe_analyze_top_n`

### Settings to eventually retire

- [ ] `trending_coins_to_analyze`
- [ ] direct reliance on raw top gainers / losers as the primary scanner

### Files to update

- `internal/database/database.go`
- `frontend/src/components/SettingsPanel.jsx`
- `internal/handlers/settings.go`

## 9. Add operator visibility

### Required UI/API features

- [ ] show the active universe snapshot
- [ ] show shortlist rank and component scores
- [ ] show why symbols were filtered out
- [ ] compare current live selector vs new selector during rollout

### Files to update

- `frontend/src/components/SettingsPanel.jsx`
- `internal/handlers/analyzer.go`
- `internal/services/trending.go`

## Detailed instructions for implementation order

1. Implement metadata and snapshot tables first.
2. Add eligibility filtering before liquidity ranking.
3. Add rule-based ranking before learned-model ranking.
4. Split discovery from trade execution before changing backtests.
5. Only after live snapshot generation is stable, add `dynamic_replay` backtests.

## Recommended initial ranking policy

Before the learned model is ready, use this interim policy:

- build the full eligible universe every 1h,
- compute a rule-based cross-sectional score,
- keep the top 20 names,
- run detailed 15m analysis only on the top 5 to 10,
- allow trades only if regime and risk constraints remain satisfied.

This gives a better discovery layer immediately without waiting for the full model stack.

## Validation tasks

- [ ] compare forward returns of ranked buckets
- [ ] compare old scanner vs new scanner over the same periods
- [ ] compare turnover and win rate after liquidity filtering
- [ ] verify that dynamic-universe backtests materially differ from static-symbol runs
- [ ] inspect symbol rejection reasons for sanity

## Success criteria

- the system no longer depends on a static symbol list to measure strategy quality
- low-liquidity and poor-quality names are consistently excluded
- top-ranked candidates outperform lower-ranked candidates in out-of-sample tests
- live candidate selection becomes inspectable and replayable

## Dependencies on later plans

- Plan 3 should train on this universe contract, not on the old top-volume scanner.
- Plan 4 should use dynamic-universe walk-forward evaluation as the promotion standard.
