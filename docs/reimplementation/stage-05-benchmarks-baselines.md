# Stage 05 — Benchmarks and Simple Strategy Baselines

## Objective

Establish minimal, understandable baselines before introducing a production candidate or ML. Every complex strategy must beat relevant baselines under identical data, exposure, timing, and cost assumptions.

## Strategy registry

- [x] Add versioned strategy registration and manifest metadata.
- [x] Require each strategy to declare required data, benchmark, cadence, warmup, and risk policy.
- [x] Use the shared `Strategy` and `RiskEngine` contracts from Stage 02.
- [x] Keep parameters explicit and serialized into the run manifest.

## Required baselines

### Cash

- [x] Hold starting capital with no trades.
- [x] Establish zero-risk/zero-turnover reference metrics.

### Benchmark buy-and-hold

- [x] Buy configured benchmark at first executable timestamp after warmup and hold through the interval.
- [x] Include the same fill costs and final valuation/liquidation policy as candidates.

### Benchmark trend

- [x] A simple, documented benchmark trend rule on a slower timeframe.
- [x] No hidden indicator voting or model dependency.
- [x] Explicit in/out-of-market semantics.

### Equal-weight liquid universe

- [x] Select only point-in-time eligible/liquid assets.
- [x] Rebalance on an explicit cadence.
- [x] Apply equal exposure and the same turnover/cost model.

### Cross-sectional momentum baseline

- [x] Rank eligible assets by a single documented lookback return or risk-adjusted momentum measure.
- [x] Select top N with explicit warmup and rebalance cadence.
- [x] No ML and no composite indicator score.

## Comparable evaluation

- [x] Normalize starting capital and maximum gross/net exposure.
- [x] Use identical universe/data manifests, decision timestamps, fill policy, and costs.
- [x] Report absolute and benchmark-relative return.
- [x] Report drawdown, Sharpe/Sortino where sample size permits, profit factor, expectancy, turnover, exposure time, and trade count.
- [x] Report concentration by symbol, period, and regime.
- [x] Report whether apparent outperformance comes from leverage or exposure differences.

## Testing instructions

### Synthetic strategy fixtures

- [x] Monotonically rising benchmark produces expected buy-and-hold behavior.
- [x] Falling benchmark preserves cash advantage and exercises benchmark trend exits.
- [x] Rotating leaders produce deterministic cross-sectional ranks/rebalances.
- [x] Flat assets produce no artificial momentum edge.
- [x] Costs reduce high-turnover strategies as expected.

### Golden metrics

- [x] Small hand-calculable fixtures match exact fills, equity, turnover, return, and drawdown.
- [x] Strategies at equal exposure are comparable.
- [x] Repeated runs are deterministic.
- [x] Empty or insufficient warmup fails explicitly.
- [x] Full `go test ./...` passes.

### Cannot yet be proven

- [ ] Positive historical performance cannot be asserted without a complete Stage 04 dataset.
- [ ] Generalization cannot be asserted until Stage 07 multi-window validation.
- [ ] Live performance cannot be inferred from a single backtest.
- [ ] Order-book impact remains outside OHLCV-only evidence.

## Acceptance criteria

- [x] Every future candidate is compared to cash and relevant market baselines.
- [x] Baselines use shared execution/risk/accounting paths.
- [x] Results explain exposure, cost, turnover, and concentration differences.
- [x] Optimization/promotion is blocked when the candidate lacks robust baseline-relative value.
- [x] Reviewer independently reproduces golden metric cases.

## Completion evidence

- Initial implementation: `1b7a6a0`.
- Independent read-only review: session `019f6e0b-092a-7652-8e5b-53311db04fa7`, verdict `Reject` before remediation.
- Single feedback pass resumed in original implementation session `019f6de6-a0ce-7bd2-b666-40a305c41764` (1/1 consumed).
- Review remediation: `330f8ef`.
- Verified after remediation against isolated PostgreSQL 16: full serial suite, serial race suite, `go vet ./...`, and `git diff --check` pass.
- Stage 05 optimization can proceed only from canonical job-bound evidence; production promotion remains fenced pending Stage 07.
