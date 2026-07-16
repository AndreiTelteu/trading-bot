# Stage 06 — Production Candidate: Trend/Momentum with Explicit Risk

## Objective

Replace opaque indicator voting as the primary candidate with one simple, falsifiable long-only hypothesis: liquid assets exhibiting persistent relative and absolute trend perform better during supportive market regimes, after controlling exposure, turnover, and costs.

This stage implements a research/shadow candidate. It does not authorize paper/live promotion by itself.

## Strategy specification

### Universe and cadence

- [ ] Consume only Stage 04 point-in-time eligible/liquid universe.
- [ ] Use configurable but bounded decision and rebalance cadence, defaulting to slower horizons than the legacy 15-minute churn.
- [ ] Require explicit warmup for every feature.
- [ ] Exclude assets with incomplete inputs rather than imputing future-derived values.

### Regime

- [ ] Define one benchmark-based absolute trend/regime rule.
- [ ] Optionally include market breadth only if it is point-in-time and independently testable.
- [ ] Produce explicit `risk_on`, `neutral`, or `risk_off` observations and reasons.
- [ ] Define target exposure per regime; no hidden gate defaults.

### Ranking and entry

- [ ] Define relative momentum from documented lookbacks.
- [ ] Optionally normalize by realized volatility using past data only.
- [ ] Select top N with deterministic tie-breaking.
- [ ] Require positive absolute trend in addition to relative rank.
- [ ] Store factor components separately; do not collapse them into an unexplained 1–5 rating.

### Sizing and risk

- [ ] Per-position notional cap as a percentage of current equity.
- [ ] Total gross/net exposure cap.
- [ ] Cash reserve and maximum concurrent positions.
- [ ] Volatility-based sizing only inside hard notional/exposure caps.
- [ ] Minimum order/notional and symbol precision handling.
- [ ] Turnover/rebalance budget and skip threshold for immaterial trades.

### Exit semantics

- [ ] Exit on loss of absolute trend, loss of rank at rebalance, regime reduction, or explicit risk stop.
- [ ] Define stop behavior separately from alpha exit.
- [ ] Avoid overlapping TP/SL/trailing rules whose precedence is ambiguous.
- [ ] Record the exact exit reason and decision context.

## Ablations and sensitivity

- [ ] Absolute trend only.
- [ ] Relative momentum only.
- [ ] Combined regime + relative momentum.
- [ ] With/without volatility normalization.
- [ ] Multiple bounded lookback/rebalance variants predefined before evaluation.
- [ ] Equal-exposure comparison against Stage 05 baselines.
- [ ] Report turnover and cost sensitivity, not only raw return.

## Testing instructions

### Unit and scenario tests

- [ ] Warmup and missing-data behavior.
- [ ] Rank ordering and deterministic ties.
- [ ] Regime transitions and target exposure changes.
- [ ] Per-position/portfolio caps under low volatility.
- [ ] Rebalance skip threshold and turnover budget.
- [ ] Simultaneous exit reasons follow documented precedence.
- [ ] Model/shadow observations cannot change rule strategy decisions.

### Integration and parity tests

- [ ] Same fixture yields identical intent/risk decisions in backtest and paper.
- [ ] Live adapter builds the same approved order semantics without external submission.
- [ ] Ledger and costs reconcile after rebalance.
- [ ] Strategy version and factor trace appear in run/order metadata.
- [ ] Full `go test ./...` passes.

### Cannot yet be proven

- [ ] Profitability is not an implementation acceptance criterion.
- [ ] Robust edge requires Stage 07 walk-forward validation on complete Stage 04 data.
- [ ] Capacity and market impact require richer liquidity data than OHLCV.
- [ ] Promotion requires elapsed shadow evidence and human approval.

## Acceptance criteria

- [ ] The hypothesis and every decision component are explainable from recorded inputs.
- [ ] Risk caps prevent low-volatility sizing from consuming the portfolio.
- [ ] The candidate remains research/shadow-only by default.
- [ ] Ablations can determine which component contributes value.
- [ ] Reviewer checks lookahead, parameter leakage, turnover, and cap enforcement.
