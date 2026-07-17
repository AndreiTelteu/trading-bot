# Stage 07 — Validation, Governance, and ML Quarantine

## Objective

Require statistically meaningful, reproducible evidence before a strategy or model can advance. Keep bootstrap/test artifacts incapable of controlling execution and make promotion/rollback decisions explicit and auditable.

## Experiment manifests

- [x] Immutable experiment ID and creation timestamp.
- [x] Code revision, strategy/model version, policy bundle, dataset manifest, universe policy, interval, costs, and seed.
- [x] Predeclared train/validation/test windows and purge/embargo settings.
- [x] Predeclared metrics and promotion/rollback thresholds.
- [x] Links to compact metrics, trades, curves, cohorts, and coverage diagnostics.
- [x] Reproduction command or machine-readable invocation.

## Walk-forward validation

- [x] Multiple chronological train/validation/test windows.
- [x] Purge overlapping labels/features around boundaries.
- [x] Embargo where label horizon creates leakage.
- [x] Fit parameters/models only on each training window.
- [x] Select/tune only from allowed training/validation evidence.
- [x] Aggregate untouched test windows after decisions are frozen.
- [x] Reject validation when windows, observations, trades, or regimes are insufficient.

## Statistical evaluation

- [x] Bootstrap across the correct independent unit, normally windows/blocks rather than a single aggregate.
- [x] Report confidence intervals only when sample requirements are met.
- [x] Report after-cost expectancy, benchmark-relative return, drawdown, turnover, exposure, and concentration.
- [x] Include worst-window, worst-regime, and worst-symbol behavior.
- [x] Detect performance dominated by one trade/symbol/window.
- [x] Correctly label exploratory versus confirmatory results.

## Governance

- [x] Explicit stages: research, shadow, paper, limited live, full live, rollback.
- [x] Promotion requires all predefined gates and human approval.
- [x] Rollback thresholds are defined before deployment.
- [x] Policy/version changes create new experiment context rather than rewriting history.
- [x] Backtest authority follows rollout semantics or explicit research override recorded in manifest.
- [x] No automatic optimizer may mutate live settings directly.

## ML quarantine and evaluation

- [x] Mark bootstrap/contract fixtures with an artifact class that cannot be promoted.
- [x] Validate artifact feature/label specs and training provenance before loading for authority.
- [x] Shadow predictions may be recorded but cannot change rule decisions.
- [x] Compute ROC AUC, Brier score, log loss, calibration buckets, probability/return correlation, and rank monotonicity.
- [x] Compare ML ranking against the Stage 05 non-ML baseline at equal candidate set and exposure.
- [x] Reject promotion for severe overconfidence, near-random discrimination, non-monotonic ranking, or negative after-cost expectancy.

## Testing instructions

### Window/leakage tests

- [x] Purge removes overlapping labels across boundaries.
- [x] Embargo timestamps are respected.
- [x] Future test data cannot affect training/selection.
- [x] One-window validation fails sample requirements.
- [x] Empty/zero-trade windows do not produce neutral passing metrics.

### Governance negative-path tests

- [x] Bootstrap artifact cannot enter paper/live authority.
- [x] Shadow model cannot change selected orders.
- [x] Failed gate blocks promotion.
- [x] Human approval is required even when metrics pass.
- [x] Rollback restores configured fallback without losing audit history.
- [x] Manifest/config mutation creates a new identity or fails integrity validation.

### Reproducibility

- [x] Same manifest and data reproduce metrics.
- [x] Full `go test ./...` passes.
- [x] Research Python tests/feature-parity checks pass when touched.

### Cannot yet be proven

- [ ] A trained model cannot be evaluated if sufficient outcomes/features do not exist.
- [ ] Real shadow/paper stability requires elapsed market time.
- [ ] Statistical significance cannot be manufactured when external history is incomplete.
- [ ] Passing gates reduces risk but does not guarantee future profit.

## Acceptance criteria

- [x] Validation refuses insufficient evidence.
- [x] Promotion is reproducible, gated, and human-approved.
- [x] Bootstrap artifacts are structurally quarantined.
- [x] ML must beat a non-ML baseline, not merely produce probabilities.
- [x] Reviewer independently verifies leakage boundaries and governance bypass resistance.

## Completion evidence

- Initial implementation commit: `a807e77`.
- Independent read-only review verdict: **Reject**, with findings C1–C3, H1–H9, and M1–M4.
- The single allowed feedback pass was resumed in the original implementation session and remediated every finding in commit `2426278`.
- Adversarial coverage includes shadow-order isolation, immutable policy envelopes, ML-baseline promotion gates, fold isolation, trusted Stage 04/05/06 sources, confirmatory approvals, monitoring-evidence rollback, primitive reconciliation, database integrity, strategy authority, roles, idempotency, and historical upgrades.
- Isolated PostgreSQL full suite passed serially with `go test -p 1 -count=1 ./...`.
- Relevant PostgreSQL race suites, including validation and governance, passed serially; `go vet ./...` and `git diff --check` passed.
- Research Python was not touched; its conditional test requirement is therefore not applicable to this implementation.
- The four external claims above remain deliberately unresolved.
