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
- [x] Immutable experiment-family and per-candidate attempt records, including
  failed/unfinished outcomes, so the multiple-testing population cannot be
  reduced by deleting an unfavorable tuning run.
- [x] One locked, single-use confirmatory holdout per family. Its exact
  point-in-time dataset digest and interval are bound before execution; a
  second confirmatory manifest fails closed.

## Walk-forward validation

- [x] Multiple chronological train/validation/test windows.
- [x] Purge overlapping labels/features around boundaries.
- [x] Embargo where label horizon creates leakage.
- [x] Fit parameters/models only on each training window.
- [x] Select/tune only from allowed training/validation evidence.
- [x] Aggregate untouched test windows after decisions are frozen.
- [x] Reject validation when windows, observations, trades, or regimes are insufficient.
- [x] Resolve and preflight the effective walk-forward policy before launching either replay lane; the governed default remains 12 training months plus 3 test months, and warmup does not count toward the validation interval.
- [x] Persist the effective train/test/bootstrap policy in the versioned backtest run manifest.
- [x] Require the independent `research-readiness-v1` preflight for Stage 05/06
  source comparisons. Its policy snapshot is included in the Stage 07 replay
  settings envelope and generic settings cannot weaken it.

### Causally isolated fold execution

Stage 07 consumes a `stage07-source-artifact-v2` envelope only as immutable
strategy/configuration provenance. Legacy v1 envelopes are not validation
evidence and fail closed. The v2 envelope includes a digest-bound, non-secret
replay-settings snapshot; it does not donate its previously computed trades,
curves, or metrics to a fold.

For every fold, the source rebuilds manifest-pinned Stage 04 bars, benchmark
availability, universe regime snapshots, quality/coverage, and forward-label
availability. `FitAndSelect` validates those supplied train/validation samples,
runs fresh chronological train and validation simulations for every
predeclared configuration, and freezes the validation-selected configuration,
partition digests, data digest, and selection rationale. `Test` validates the
frozen artifact and supplied untouched test partition, truncates market data at
the test boundary, and performs fresh candidate and baseline simulations.
It never returns a Stage 05 primitive cached in a source job.

Trade costs are reconstructed from the fills belonging to that trade: fee plus
the observed fill-vs-market-price slippage. Missing fill attribution fails the
fold; aggregate Stage 05 costs are never divided across trades.

## Statistical evaluation

- [x] Bootstrap across the correct independent unit, normally windows/blocks rather than a single aggregate.
- [x] Report confidence intervals only when sample requirements are met.
- [x] Report after-cost expectancy, benchmark-relative return, drawdown, turnover, exposure, and concentration.
- [x] Include worst-window, worst-regime, and worst-symbol behavior.
- [x] Detect performance dominated by one trade/symbol/window.
- [x] Correctly label exploratory versus confirmatory results.
- [x] Report deterministic downside deviation and 95% expected shortfall from
  chronological equity changes, maximum entry-liquidity participation, and a
  conservative capacity/impact-stressed after-cost return.
- [x] Require source-baseline exposure and turnover matching; comparability is
  rejected rather than adjusted after the fact.
- [x] Report a conservative deflated-Sharpe-style multiple-testing adjustment
  based on the predeclared tuning-space size. It is a screening gate, not a
  claim of a fully specified probability of backtest overfitting.

## Governance

- [x] Explicit stages: research, shadow, paper, limited live, full live, rollback.
- [x] Promotion requires all predefined gates and human approval.
- [x] Rollback thresholds are defined before deployment.
- [x] Policy/version changes create new experiment context rather than rewriting history.
- [x] Backtest authority follows rollout semantics or explicit research override recorded in manifest.
- [x] No automatic optimizer may mutate live settings directly.

### Typed identities

Promotion records distinguish `implementation_digest`, `config_digest`,
`run_manifest_digest`, `dataset_digest`, and `evidence_digest`. An
implementation digest identifies executable shared strategy code; a config
digest identifies the governed strategy configuration; a run-manifest digest
identifies one replay invocation; a dataset digest identifies the Stage 04
dataset; and an evidence digest identifies immutable validation output. The
Stage 07 source adapter rejects a comparison artifact if any identity is
missing, substituted, or conflated. Deployment additionally binds the approved
implementation digest to the local registry; the separately persisted
authority-policy digest binds the complete runtime policy envelope.

Operational status verifies the immutable manifest/evidence pair for the
active Stage 08 snapshot and names every failing validation metric or
diagnostic. It does not infer readiness from a mutable asynchronous job
summary. Rule-only candidates record model version `none` in that snapshot;
the absence of a learned model is explicit provenance, not an error or an
implicit authority bypass.

## ML quarantine and evaluation

- [x] Mark bootstrap/contract fixtures with an artifact class that cannot be promoted.
- [x] Validate artifact feature/label specs and training provenance before loading for authority.
- [x] Shadow predictions may be recorded but cannot change rule decisions.
- [x] Compute ROC AUC, Brier score, log loss, calibration buckets, probability/return correlation, and rank monotonicity.
- [x] Compare ML ranking against the Stage 05 non-ML baseline at equal candidate set and exposure.
- [x] Reject promotion for severe overconfidence, near-random discrimination, non-monotonic ranking, or negative after-cost expectancy.
- [x] Persist every model-eligible decision (accepted and rejected) under a deterministic identity bound to decision time, symbol, horizon, policy, artifact, and cost identities.
- [x] Label the complete cohort only from point-in-time stored bars at a fixed horizon; pending/unavailable labels are explicit, retried, and excluded from calibration denominators.
- [x] Monitor cohort coverage separately from performance, and rank drift by absolute deviation so adverse negative shifts cannot be hidden.
- [x] Quarantine offline Python to hash-verified PostgreSQL-derived proposal datasets; Go rejects proposal artifacts and remains authoritative for evaluation and promotion evidence.

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
- [x] Rejected opportunities, delayed labels, duplicate retries, unavailable label sources, and both signs of feature drift have regression coverage.
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
- [ ] Runtime labels require a complete, timely ingestion of the decision-timeframe bar series. Missing bars remain unavailable evidence and cannot be converted into losses or passing calibration.
- [ ] Real shadow/paper stability requires elapsed market time.
- [ ] Statistical significance cannot be manufactured when external history is incomplete.
- [ ] Passing gates reduces risk but does not guarantee future profit.
- [ ] The deterministic impact stress is a transparent scenario based on
  point-in-time bar liquidity. It cannot establish actual exchange queue
  position, hidden liquidity, or future market impact; an operator must retain
  conservative participation limits and paper/shadow evidence.
- [ ] The deflated-Sharpe approximation does not replace an independently
  designed PBO study when candidate correlations and selection paths warrant
  one. The immutable family denominator makes that later analysis auditable.

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
- The former SQLite/Python evaluator was removed in ADR 0006. The retained optional Python proposal utility verifies a Go-exported immutable dataset and has a tamper regression check; it cannot emit runtime authority or Stage 07 evidence.
- The four external claims above remain deliberately unresolved.
