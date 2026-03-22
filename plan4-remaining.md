# Plan 4 — Remaining Work: Validation, Rollout, and Cleanup

## Status: ~80% Complete

The governance framework (policy configs, experiment registry, promotion gates, monitoring, rollout events, rollback procedures) is fully operational. The remaining gaps are in backtest modes, AI workflow, and legacy cleanup.

---

## 1. Paper Replay Backtest Mode (Task 1)

**Status:** Not Implemented

### What exists
- `legacy_static`, `dynamic_universe_rule_rank`, `dynamic_universe_model_rank` modes
- Walk-forward validation with train/test windows

### What is missing
- `paper_replay` mode — replay using the live execution template
- This mode would simulate the exact live decision flow:
  - Use the real shared exit engine
  - Use the real universe snapshot pipeline
  - Use the real model inference pipeline
  - Apply the real execution coordinator logic (minus actual exchange calls)
- Useful for validating the complete live pipeline in a safe environment

### Implementation approach
- Add `BacktestModePaperReplay` constant
- In paper replay mode, the backtest should call the same high-level functions as the live trending analysis loop
- Use persisted universe snapshots and model artifacts, not recomputed ones

### Files to update
- `internal/backtest/types.go` — add constant
- `internal/backtest/job.go` — add paper replay branch
- `internal/backtest/engine.go` — wire paper replay execution

---

## 2. Regime-Sliced and Symbol-Cohort Validation Modes (Task 1)

**Status:** Partial

### What exists
- `buildRegimeSliceMetrics()` and `buildSymbolCohortMetrics()` compute these metrics
- `StrategyDiagnostics` includes regime slices and symbol cohorts
- Walk-forward validation runs and reports per-window metrics

### What is missing
- Explicit validation modes that surface regime-sliced and symbol-cohort results prominently
- Decile/top-K ranking validation as a first-class report
- The data is computed but not structured as a standalone validation mode the operator can request

### Implementation approach
- Extend `ValidationSummary` to include regime-sliced and cohort summaries as top-level sections
- Add decile-based ranking validation (group trades by predicted probability decile)
- Surface these in backtest job summary output

### Files to update
- `internal/backtest/validation.go` — add decile ranking validation
- `internal/backtest/metrics.go` — add probability-decile bucketing

---

## 3. Change AI Proposal Workflow (Task 8)

**Status:** Not Implemented

### What exists
- AI proposals target indicator weights and manual coefficients
- `OptimizeBacktest` handler uses AI to suggest parameter changes based on backtest results

### What is missing

#### Phase out
- Proposing indicator weights as primary alpha improvements
- Proposing manual model coefficients (`prob_model_beta*`)

#### New behavior to implement
- **Summarize experiment results**: AI should read `ExperimentRun` + `ValidationSummary` and produce a human-readable summary
- **Propose safe policy changes**: limit proposals to `selection_policy_top_k`, `selection_policy_min_prob`, `selection_policy_min_ev`, risk parameters, universe policy thresholds
- **Promotion/rollback recommendations**: AI should recommend whether to promote or rollback based on validation evidence
- **Explain drift**: AI should interpret `MonitoringSnapshot` data (feature drift, calibration drift, regime changes)

### Implementation approach
- Rewrite the AI prompt template in `ai.go` to:
  - Include governance context, experiment results, monitoring snapshots
  - Restrict proposal targets to the approved policy parameter set
  - Ask for promotion/rollback recommendations explicitly
- Add a new proposal type: `governance_recommendation` alongside existing `parameter_change`
- Validate that proposals don't target deprecated settings

### Files to update
- `internal/services/ai.go` — rewrite prompt template, add governance context to prompt
- `internal/handlers/ai.go` — add governance recommendation endpoint
- `internal/database/models.go` — extend `AIProposal` if new proposal types needed

---

## 4. Simplify Settings UI — Legacy Removal (Task 4)

**Status:** Partial

### What exists
- New sections: Execution & Risk, Universe Selection, Model & Policy, Backtest & Validation, AI Governance
- Legacy sections have a deprecation notice banner
- Old indicator weights and probability betas still visible

### What is missing
- Remove indicator weights from the primary UI (keep as hidden/debug only)
- Remove manual probability betas from all UI paths
- Remove indicator-period tuning from live controls
- Remove confidence thresholds as the primary strategy knobs
- The `weights` tab and `indicators`/`atr` sections should be removed or moved to an "Advanced/Legacy" section

### Implementation approach
- Remove `indicators`, `atr`, `weights` from `SETTINGS_SECTIONS` in the frontend
- Move them to a collapsible "Legacy/Debug" section at the bottom
- Or gate them behind a `show_legacy_settings` toggle

### Files to update
- `frontend/src/components/SettingsPanel.jsx` — restructure sections

---

## 5. Backtest Job Output Enrichment (Task 6)

**Status:** Partial

### What exists
- `BacktestResult` includes `ModelVersion`, `PolicyVersion`, `RolloutState`
- `RankingMetrics` and `StrategyDiagnostics` are computed
- Regime slices and symbol cohorts included

### What is missing
- Persist the universe mode used alongside the result
- Persist rank-bucket performance tables explicitly in job summary
- Persist regime-sliced and symbol-cohort tables in structured format
- Turnover and exposure diagnostics are computed but could be more prominent

### Implementation approach
- Ensure `BacktestResult` serialization includes all diagnostic tables
- Add `universe_mode` to the compact summary JSON
- Verify the frontend displays all available diagnostic data

### Files to update
- `internal/backtest/job.go` — ensure full diagnostic serialization
- `internal/backtest/status.go` — include diagnostics in API response

---

## 6. Promotion Review Checklist Automation (Validation)

**Status:** Partial

### What exists
- `evaluatePromotionReadiness()` checks 5 gates automatically
- `PromotionReadiness` is included in validation results

### What is missing from the recommended checklist
- "Was the candidate tested on dynamic-universe walk-forward data?" — checked
- "Does the candidate outperform the currently active version after realistic costs?" — not automated (needs baseline comparison)
- "Is the improvement present across multiple regime slices?" — partially checked (counts slices, doesn't verify each is positive)
- "Are top-ranked candidates measurably better than lower-ranked candidates?" — checked via ranking spread
- "Are calibration and drift metrics acceptable?" — not checked in promotion gates
- "Is there a tested rollback path?" — not automated

### Implementation approach
- Add a gate for "outperforms active version" by comparing against the last promoted experiment
- Add a gate for "calibration acceptable" using monitoring snapshot data
- Add a gate for "rollback tested" as a manual checkbox or automated check

### Files to update
- `internal/backtest/validation.go` — extend `evaluatePromotionReadiness()`

---

## Implementation Priority

1. **AI proposal workflow change** — aligns the AI system with the new architecture (blocks full Plan 3/4 completion)
2. **Paper replay backtest mode** — completes the backtest mode matrix
3. **Legacy settings UI cleanup** — reduces operator confusion
4. **Regime/cohort validation modes** — improves validation rigor
5. **Backtest output enrichment** — operational completeness
6. **Promotion checklist automation** — adds safety to the promotion pipeline
