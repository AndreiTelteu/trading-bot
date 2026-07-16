# Stage 07 — Validation, Governance, and ML Quarantine

## Objective

Require statistically meaningful, reproducible evidence before a strategy or model can advance. Keep bootstrap/test artifacts incapable of controlling execution and make promotion/rollback decisions explicit and auditable.

## Experiment manifests

- [ ] Immutable experiment ID and creation timestamp.
- [ ] Code revision, strategy/model version, policy bundle, dataset manifest, universe policy, interval, costs, and seed.
- [ ] Predeclared train/validation/test windows and purge/embargo settings.
- [ ] Predeclared metrics and promotion/rollback thresholds.
- [ ] Links to compact metrics, trades, curves, cohorts, and coverage diagnostics.
- [ ] Reproduction command or machine-readable invocation.

## Walk-forward validation

- [ ] Multiple chronological train/validation/test windows.
- [ ] Purge overlapping labels/features around boundaries.
- [ ] Embargo where label horizon creates leakage.
- [ ] Fit parameters/models only on each training window.
- [ ] Select/tune only from allowed training/validation evidence.
- [ ] Aggregate untouched test windows after decisions are frozen.
- [ ] Reject validation when windows, observations, trades, or regimes are insufficient.

## Statistical evaluation

- [ ] Bootstrap across the correct independent unit, normally windows/blocks rather than a single aggregate.
- [ ] Report confidence intervals only when sample requirements are met.
- [ ] Report after-cost expectancy, benchmark-relative return, drawdown, turnover, exposure, and concentration.
- [ ] Include worst-window, worst-regime, and worst-symbol behavior.
- [ ] Detect performance dominated by one trade/symbol/window.
- [ ] Correctly label exploratory versus confirmatory results.

## Governance

- [ ] Explicit stages: research, shadow, paper, limited live, full live, rollback.
- [ ] Promotion requires all predefined gates and human approval.
- [ ] Rollback thresholds are defined before deployment.
- [ ] Policy/version changes create new experiment context rather than rewriting history.
- [ ] Backtest authority follows rollout semantics or explicit research override recorded in manifest.
- [ ] No automatic optimizer may mutate live settings directly.

## ML quarantine and evaluation

- [ ] Mark bootstrap/contract fixtures with an artifact class that cannot be promoted.
- [ ] Validate artifact feature/label specs and training provenance before loading for authority.
- [ ] Shadow predictions may be recorded but cannot change rule decisions.
- [ ] Compute ROC AUC, Brier score, log loss, calibration buckets, probability/return correlation, and rank monotonicity.
- [ ] Compare ML ranking against the Stage 05 non-ML baseline at equal candidate set and exposure.
- [ ] Reject promotion for severe overconfidence, near-random discrimination, non-monotonic ranking, or negative after-cost expectancy.

## Testing instructions

### Window/leakage tests

- [ ] Purge removes overlapping labels across boundaries.
- [ ] Embargo timestamps are respected.
- [ ] Future test data cannot affect training/selection.
- [ ] One-window validation fails sample requirements.
- [ ] Empty/zero-trade windows do not produce neutral passing metrics.

### Governance negative-path tests

- [ ] Bootstrap artifact cannot enter paper/live authority.
- [ ] Shadow model cannot change selected orders.
- [ ] Failed gate blocks promotion.
- [ ] Human approval is required even when metrics pass.
- [ ] Rollback restores configured fallback without losing audit history.
- [ ] Manifest/config mutation creates a new identity or fails integrity validation.

### Reproducibility

- [ ] Same manifest and data reproduce metrics.
- [ ] Full `go test ./...` passes.
- [ ] Research Python tests/feature-parity checks pass when touched.

### Cannot yet be proven

- [ ] A trained model cannot be evaluated if sufficient outcomes/features do not exist.
- [ ] Real shadow/paper stability requires elapsed market time.
- [ ] Statistical significance cannot be manufactured when external history is incomplete.
- [ ] Passing gates reduces risk but does not guarantee future profit.

## Acceptance criteria

- [ ] Validation refuses insufficient evidence.
- [ ] Promotion is reproducible, gated, and human-approved.
- [ ] Bootstrap artifacts are structurally quarantined.
- [ ] ML must beat a non-ML baseline, not merely produce probabilities.
- [ ] Reviewer independently verifies leakage boundaries and governance bypass resistance.
