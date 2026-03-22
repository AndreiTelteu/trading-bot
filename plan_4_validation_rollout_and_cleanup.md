# Plan 4 — Validation, Rollout, and Cleanup

## Goal

Turn the strategy process into a controlled promotion pipeline where only robust improvements reach live trading, while the operator UI and AI optimizer are simplified around the new architecture.

## Why this plan matters

Once Plans 1 to 3 exist, the biggest risk becomes false confidence:

- overfitting to a backtest period,
- promoting changes that help one aggregate run but fail in regime splits,
- keeping too many legacy controls alive and creating accidental configuration drift.

This plan makes the new system operationally safe.

## Exact implementation tasks

## 1. Redesign the backtest and validation contract

### Required backtest modes

- [ ] `legacy_static` for backward comparison
- [ ] `dynamic_universe_rule_rank`
- [ ] `dynamic_universe_model_rank`
- [ ] `paper_replay` using the live execution template

### Required validation modes

- [ ] aggregate full-period summary
- [ ] purged walk-forward validation
- [ ] regime-sliced validation
- [ ] symbol-cohort validation
- [ ] decile / top-K ranking validation

### Files to update

- `internal/backtest/types.go`
- `internal/backtest/job.go`
- `internal/backtest/validation.go`
- `internal/backtest/metrics.go`

## 2. Define hard promotion gates

### Promotion stages

- [ ] `research_only`
- [ ] `shadow`
- [ ] `paper`
- [ ] `limited_live`
- [ ] `full_live`
- [ ] `rollback`

### Suggested mandatory gates

Before moving from `research_only` to `shadow`:

- [ ] complete walk-forward metrics available
- [ ] dynamic-universe backtest included
- [ ] execution-parity assumptions documented

Before moving from `shadow` to `paper`:

- [ ] prediction logging stable
- [ ] rank buckets show monotonic quality
- [ ] calibration sanity checks pass

Before moving from `paper` to `limited_live`:

- [ ] paper profit factor > 1 after realistic costs
- [ ] no execution-state bugs detected
- [ ] no unexplained drift between live predictions and replayed predictions

Before moving from `limited_live` to `full_live`:

- [ ] limited-live drawdown acceptable
- [ ] model performance still above baseline over a meaningful sample
- [ ] rollback procedure tested

### Files to update

- `internal/database/models.go`
- `internal/services/model_registry.go`
- `internal/services/trending.go`

## 3. Add live monitoring and drift detection

### Required dashboards / reports

- [ ] live prediction count by model version
- [ ] selection rate by rank bucket
- [ ] realized outcomes vs predicted probability bucket
- [ ] drift in feature distributions
- [ ] drift in rank concentration
- [ ] regime and breadth state over time

### Required persistence

- [ ] store live feature snapshots or compact summaries
- [ ] store live predictions and realized outcomes
- [ ] store model version and policy version on every trade decision

### Files to update

- `internal/database/models.go`
- `internal/services/trending.go`
- `frontend/src/components/SettingsPanel.jsx`
- add new frontend monitoring views if needed

## 4. Simplify the settings surface

## 4.1 Keep these categories

- [ ] Execution & Risk
- [ ] Universe Selection
- [ ] Model & Policy
- [ ] Backtest & Validation
- [ ] AI Governance

## 4.2 Remove or de-emphasize these categories

- [ ] manual indicator weights
- [ ] manual probability betas
- [ ] indicator-period tuning as live controls
- [ ] confidence thresholds as the primary strategy knobs

### Files to update

- `frontend/src/components/SettingsPanel.jsx`
- `internal/database/database.go`
- `internal/handlers/settings.go`

## 5. Add structured policy configs instead of loose setting sprawl

### New policy groups to support

- [ ] execution policy
- [ ] universe policy
- [ ] model selection policy
- [ ] entry selection policy
- [ ] portfolio risk policy
- [ ] rollout policy

### Recommended approach

- keep DB-backed settings for small scalar controls,
- introduce versioned JSON policy payloads for grouped strategy state,
- persist the active policy version on every backtest and live prediction.

### Files to update

- `internal/database/models.go`
- `internal/handlers/settings.go`
- `internal/services/ai.go`

## 6. Upgrade the backtest job outputs

### New output artifacts to persist

- [ ] model version used
- [ ] universe mode used
- [ ] policy version used
- [ ] rank-bucket performance tables
- [ ] regime-sliced performance tables
- [ ] symbol-cohort performance tables
- [ ] turnover and exposure diagnostics

### Files to update

- `internal/backtest/job.go`
- `internal/backtest/metrics.go`
- `internal/backtest/validation.go`

## 7. Add a formal experiment registry

### Required features

- [ ] experiment id
- [ ] feature spec version
- [ ] label spec version
- [ ] model artifact version
- [ ] universe policy version
- [ ] execution policy version
- [ ] validation summary
- [ ] promotion decision and reviewer notes

### Why this matters

Without an experiment registry, it becomes too easy to lose track of which combination of universe, label, feature, and execution assumptions produced a given result.

### Files to update

- `internal/database/models.go`
- optional new admin handlers

## 8. Change the AI proposal workflow

### Old behavior to phase out

- [ ] proposing indicator weights as primary alpha improvements
- [ ] proposing manual model coefficients

### New behavior to support

- [ ] summarize experiment results
- [ ] propose safe policy changes within defined bounds
- [ ] generate promotion/rollback recommendations
- [ ] explain model drift and validation gaps

### Files to update

- `internal/services/ai.go`
- AI proposal endpoints if schema changes

## 9. Add explicit rollback procedures

### Required features

- [ ] switch active model version quickly
- [ ] switch back to rule-based ranker if model degrades
- [ ] disable live auto-trading without losing monitoring
- [ ] preserve prediction logging during rollback

### Files to update

- `internal/services/model_registry.go`
- `internal/services/trending.go`
- `frontend/src/components/SettingsPanel.jsx`

## Detailed instructions for implementation order

1. Extend validation outputs before changing rollout rules.
2. Add model and policy version persistence everywhere before live promotion.
3. Build monitoring and drift views before expanding live use.
4. Simplify the settings UI only after the new policy objects and model registry exist.
5. Keep rollback paths tested and visible at all times.

## Recommended pass/fail review checklist for every promotion

- [ ] Was the candidate tested on dynamic-universe walk-forward data?
- [ ] Does the candidate outperform the currently active version after realistic costs?
- [ ] Is the improvement present across multiple regime slices?
- [ ] Are top-ranked candidates measurably better than lower-ranked candidates?
- [ ] Are calibration and drift metrics acceptable?
- [ ] Is there a tested rollback path?

If any answer is no, do not promote.

## Validation tasks

- [ ] `go test -v ./...`
- [ ] verify backtest job summary includes model/policy/universe identifiers
- [ ] verify prediction logs persist the active model and policy version
- [ ] verify rollback changes active state without corrupting stored predictions

## Success criteria

- every live decision can be traced to a model version, policy version, universe snapshot, and execution template
- promotion decisions rely on structured evidence rather than one backtest number
- the operator UI becomes smaller and safer even while strategy sophistication increases
- the AI optimizer becomes a governance and policy assistant instead of a weight tweaker

## Final end state

When this plan is complete, the repo should operate like a disciplined trading research system:

- execution is event-driven and realistic,
- universe selection is dynamic and inspectable,
- model selection is versioned and calibrated,
- promotion is gated by walk-forward evidence,
- rollback is fast and safe,
- operators manage policy, not raw model coefficients.
