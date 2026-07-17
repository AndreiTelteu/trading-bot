# Stage 06 — Production Candidate: Trend/Momentum with Explicit Risk

## Objective

Replace opaque indicator voting as the primary candidate with one simple, falsifiable long-only hypothesis: liquid assets exhibiting persistent relative and absolute trend perform better during supportive market regimes, after controlling exposure, turnover, and costs.

This stage implements a research/shadow candidate. It does not authorize paper/live promotion by itself.

## Strategy specification

### Universe and cadence

- [x] Consume only Stage 04 point-in-time eligible/liquid universe.
- [x] Use configurable but bounded decision and rebalance cadence, defaulting to slower horizons than the legacy 15-minute churn.
- [x] Require explicit warmup for every feature.
- [x] Exclude assets with incomplete inputs rather than imputing future-derived values.

### Regime

- [x] Define one benchmark-based absolute trend/regime rule.
- [x] Optionally include market breadth only if it is point-in-time and independently testable.
- [x] Produce explicit `risk_on`, `neutral`, or `risk_off` observations and reasons.
- [x] Define target exposure per regime; no hidden gate defaults.

### Ranking and entry

- [x] Define relative momentum from documented lookbacks.
- [x] Optionally normalize by realized volatility using past data only.
- [x] Select top N with deterministic tie-breaking.
- [x] Require positive absolute trend in addition to relative rank.
- [x] Store factor components separately; do not collapse them into an unexplained 1–5 rating.

### Sizing and risk

- [x] Per-position notional cap as a percentage of current equity.
- [x] Total gross/net exposure cap.
- [x] Cash reserve and maximum concurrent positions.
- [x] Volatility-based sizing only inside hard notional/exposure caps.
- [x] Minimum order/notional and symbol precision handling.
- [x] Turnover/rebalance budget and skip threshold for immaterial trades.

### Exit semantics

- [x] Exit on loss of absolute trend, loss of rank at rebalance, regime reduction, or explicit risk stop.
- [x] Define stop behavior separately from alpha exit.
- [x] Avoid overlapping TP/SL/trailing rules whose precedence is ambiguous.
- [x] Record the exact exit reason and decision context.

## Ablations and sensitivity

- [x] Absolute trend only.
- [x] Relative momentum only.
- [x] Combined regime + relative momentum.
- [x] With/without volatility normalization.
- [x] Multiple bounded lookback/rebalance variants predefined before evaluation.
- [x] Equal-exposure comparison against Stage 05 baselines.
- [x] Report turnover and cost sensitivity, not only raw return.

## Testing instructions

### Unit and scenario tests

- [x] Warmup and missing-data behavior.
- [x] Rank ordering and deterministic ties.
- [x] Regime transitions and target exposure changes.
- [x] Per-position/portfolio caps under low volatility.
- [x] Rebalance skip threshold and turnover budget.
- [x] Simultaneous exit reasons follow documented precedence.
- [x] Model/shadow observations cannot change rule strategy decisions.

### Integration and parity tests

- [x] Same fixture yields identical intent/risk decisions in backtest and paper.
- [x] Live adapter builds the same approved order semantics without external submission.
- [x] Ledger and costs reconcile after rebalance.
- [x] Strategy version and factor trace appear in run/order metadata.
- [x] Full `go test ./...` passes.

### Cannot yet be proven

- [ ] Profitability is not an implementation acceptance criterion.
- [ ] Robust edge requires Stage 07 walk-forward validation on complete Stage 04 data.
- [ ] Capacity and market impact require richer liquidity data than OHLCV.
- [ ] Promotion requires elapsed shadow evidence and human approval.

## Acceptance criteria

- [x] The hypothesis and every decision component are explainable from recorded inputs.
- [x] Risk caps prevent low-volatility sizing from consuming the portfolio.
- [x] The candidate remains research/shadow-only by default.
- [x] Ablations can determine which component contributes value.
- [x] Reviewer checks lookahead, parameter leakage, turnover, and cap enforcement.

## Completion evidence

- Initial implementation commit: `d1a45a6`.
- Independent read-only review verdict: **Reject**, with findings H1–H5, M1–M4, and L1.
- The single allowed feedback pass was resumed in the original implementation session and remediated every finding in commit `1ea0d8d`.
- Causal decision/fill clocks, mandatory reductions, independent parity adapters, explicit execution modes, component ablations, bounded sensitivity, stable identities, and structured reason propagation have counterexample coverage.
- Isolated PostgreSQL full suite passed serially with `go test -p 1 -count=1 ./...`.
- Relevant PostgreSQL race suites passed serially; `go vet ./...` and `git diff --check` passed.
- The four external claims above remain deliberately unresolved and are deferred to Stage 07, richer liquidity evidence, elapsed shadow operation, and human approval.
