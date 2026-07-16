# Stage 05 — Benchmarks and Simple Strategy Baselines

## Objective

Establish minimal, understandable baselines before introducing a production candidate or ML. Every complex strategy must beat relevant baselines under identical data, exposure, timing, and cost assumptions.

## Strategy registry

- [ ] Add versioned strategy registration and manifest metadata.
- [ ] Require each strategy to declare required data, benchmark, cadence, warmup, and risk policy.
- [ ] Use the shared `Strategy` and `RiskEngine` contracts from Stage 02.
- [ ] Keep parameters explicit and serialized into the run manifest.

## Required baselines

### Cash

- [ ] Hold starting capital with no trades.
- [ ] Establish zero-risk/zero-turnover reference metrics.

### Benchmark buy-and-hold

- [ ] Buy configured benchmark at first executable timestamp after warmup and hold through the interval.
- [ ] Include the same fill costs and final valuation/liquidation policy as candidates.

### Benchmark trend

- [ ] A simple, documented benchmark trend rule on a slower timeframe.
- [ ] No hidden indicator voting or model dependency.
- [ ] Explicit in/out-of-market semantics.

### Equal-weight liquid universe

- [ ] Select only point-in-time eligible/liquid assets.
- [ ] Rebalance on an explicit cadence.
- [ ] Apply equal exposure and the same turnover/cost model.

### Cross-sectional momentum baseline

- [ ] Rank eligible assets by a single documented lookback return or risk-adjusted momentum measure.
- [ ] Select top N with explicit warmup and rebalance cadence.
- [ ] No ML and no composite indicator score.

## Comparable evaluation

- [ ] Normalize starting capital and maximum gross/net exposure.
- [ ] Use identical universe/data manifests, decision timestamps, fill policy, and costs.
- [ ] Report absolute and benchmark-relative return.
- [ ] Report drawdown, Sharpe/Sortino where sample size permits, profit factor, expectancy, turnover, exposure time, and trade count.
- [ ] Report concentration by symbol, period, and regime.
- [ ] Report whether apparent outperformance comes from leverage or exposure differences.

## Testing instructions

### Synthetic strategy fixtures

- [ ] Monotonically rising benchmark produces expected buy-and-hold behavior.
- [ ] Falling benchmark preserves cash advantage and exercises benchmark trend exits.
- [ ] Rotating leaders produce deterministic cross-sectional ranks/rebalances.
- [ ] Flat assets produce no artificial momentum edge.
- [ ] Costs reduce high-turnover strategies as expected.

### Golden metrics

- [ ] Small hand-calculable fixtures match exact fills, equity, turnover, return, and drawdown.
- [ ] Strategies at equal exposure are comparable.
- [ ] Repeated runs are deterministic.
- [ ] Empty or insufficient warmup fails explicitly.
- [ ] Full `go test ./...` passes.

### Cannot yet be proven

- [ ] Positive historical performance cannot be asserted without a complete Stage 04 dataset.
- [ ] Generalization cannot be asserted until Stage 07 multi-window validation.
- [ ] Live performance cannot be inferred from a single backtest.
- [ ] Order-book impact remains outside OHLCV-only evidence.

## Acceptance criteria

- [ ] Every future candidate is compared to cash and relevant market baselines.
- [ ] Baselines use shared execution/risk/accounting paths.
- [ ] Results explain exposure, cost, turnover, and concentration differences.
- [ ] Optimization/promotion is blocked when the candidate lacks robust baseline-relative value.
- [ ] Reviewer independently reproduces golden metric cases.
