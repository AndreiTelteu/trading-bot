# Plan 3 — Remaining Work: Learned Signal Model

## Status: ~70% Complete

The Go-side inference pipeline (feature computation, model loading, prediction, ranking, shadow logging, backtest integration) is fully operational. The major gaps are in the offline training tooling, advanced model types, and transitioning the AI system.

---

## 1. Offline Dataset Pipeline (Task 4)

**Status:** Not Implemented

This is the largest missing piece. No Python research directory exists.

### What is needed

#### Directory structure
```
research/learned_model/
  build_dataset.py
  train_logistic.py
  train_gbdt.py
  evaluate_walkforward.py
  export_artifact.py
  requirements.txt
```

#### `build_dataset.py` responsibilities
- Connect to the SQLite database (or export from backtest)
- Replay `UniverseSnapshot` records from Plan 2
- Compute features at every decision timestamp using the same logic as `model_features.go`
- Attach labels from trade outcomes (realized return, profitable, exit reason, hold duration)
- Write partitioned datasets by time (train/validation/test splits)
- Store dataset metadata and checksums

#### Feature parity requirement
- The Python feature computation must produce identical values to `BuildModelFeatureRow()` in Go
- Need parity tests comparing the same candle inputs in both languages

#### Label requirements
- `net_trade_return` after fees/slippage
- `probability_of_positive_net_trade` (binary)
- Max favorable excursion
- Max adverse excursion
- Bars held / time to exit
- Exit reason reached first

### Files to create
- `research/learned_model/build_dataset.py`
- `research/learned_model/requirements.txt`

---

## 2. Training Code for Logistic Baseline (Task 5)

**Status:** Partial — artifact exists but training code missing

### What exists
- `internal/services/model_artifacts/logistic_baseline_v1.json` — pre-built artifact
- `model_registry.go` loads and caches artifacts
- `model_inference.go` runs predictions with standardization and calibration

### What is missing
- `train_logistic.py`:
  - Standardize numeric features
  - Fit regularized logistic regression (sklearn)
  - Fit a calibration layer on held-out validation data (Platt scaling or isotonic)
  - Measure log loss, Brier score, calibration error, precision@K, and trading metrics
  - Export coefficients, intercept, scaler statistics, calibration parameters
- `export_artifact.py`:
  - Write JSON artifact matching `LogisticModelArtifact` schema
  - Include metadata, feature order, mean/std, coefficients, intercept, calibration parameters
  - Compute and store artifact checksum

### Files to create
- `research/learned_model/train_logistic.py`
- `research/learned_model/export_artifact.py`

---

## 3. Walk-Forward Evaluation Script (Task 5 + 9)

**Status:** Not Implemented

### What is needed
- `evaluate_walkforward.py`:
  - Purged walk-forward train/validation/test windows
  - No hyperparameter tuning on final test windows
  - Embargo around label horizons
  - Final lockbox period kept untouched until promotion review
  - Report: log loss, Brier score, calibration error, precision@K, hit rate@K, return@K
  - Report: Sharpe, max drawdown, profit factor, turnover, exposure concentration
  - Robustness cuts: by regime, by month, by volatility bucket, by symbol cohort, by ranking decile

### Files to create
- `research/learned_model/evaluate_walkforward.py`

---

## 4. Gradient-Boosted Tree Model (Task 8)

**Status:** Not Implemented

### What is needed
- `train_gbdt.py`:
  - Train gradient-boosted tree model (LightGBM or XGBoost) on the same feature contract
  - Calibrate probabilities
  - Compare against logistic baseline over identical walk-forward splits
  - Export artifact in a deployable format

### Serving decision needed
- Go-native inference from exported tree JSON, or
- Local model-serving sidecar
- Recommendation: start with logistic in production, add GBDT only when offline evidence is clearly stronger

### Go-side inference for GBDT
- Would require a tree-walking inference engine in Go, or
- A gRPC/HTTP call to a Python sidecar

### Files to create
- `research/learned_model/train_gbdt.py`
- Potentially `internal/services/model_inference_gbdt.go` (later)

---

## 5. Retire Old Settings and Controls (Task 10)

**Status:** Partial

### What exists
- New model policy settings are added: `active_model_version`, `model_rollout_state`, `selection_policy_top_k`, etc.
- Old settings still present and functional

### What is missing — settings to deprecate/remove
- `prob_model_beta0` through `prob_model_beta6`
- `prob_p_min`, `prob_ev_min`, `prob_avg_gain`, `prob_avg_loss`
- `buy_only_strong`
- `min_confidence_to_buy`
- `min_confidence_to_sell` as the primary entry framework
- Indicator weights as routine live-ops knobs
- Indicator periods as routine live-ops knobs

### Approach
- Don't delete immediately — mark as deprecated in the UI
- Hide from the main settings sections
- Keep functional for rollback scenarios
- Add deprecation warnings in logs when these settings are read

### Files to update
- `frontend/src/components/SettingsPanel.jsx` — hide deprecated settings from primary sections
- `internal/database/database.go` — mark deprecated defaults
- `internal/services/trending.go` — add deprecation logging

---

## 6. Change AI Optimizer Role (Task 11)

**Status:** Not Implemented

### What exists
- AI proposals target indicator weights and manual model betas
- `internal/services/ai.go` builds prompts from analysis history + settings

### What is needed
- Stop proposing indicator weights as primary alpha improvements
- Stop proposing manual model coefficients
- New responsibilities:
  - Propose policy thresholds around a fixed model (top_k, min_prob, min_ev)
  - Propose risk-control changes (stop multipliers, position sizing)
  - Summarize validation results and recommend promotion/rollback
  - Propose universe policy adjustments within safe bounds
- Update AI prompt template to reflect new governance role

### Files to update
- `internal/services/ai.go` — rewrite prompt template, restrict proposal targets
- `internal/handlers/ai.go` — update proposal validation logic

---

## 7. Feature Parity Tests (Validation)

**Status:** Partial

### What exists
- `model_features_test.go` tests Go feature computation
- No cross-language parity tests

### What is missing
- Test that identical candle inputs produce identical feature rows in Python and Go
- This is critical for training/serving consistency
- Could be implemented as a shared test fixture (JSON input -> expected output)

### Files to create
- `research/learned_model/test_feature_parity.py`
- `internal/services/model_features_test.go` — add fixture-based tests

---

## Implementation Priority

1. **Offline dataset pipeline** — prerequisite for everything else in training
2. **Logistic training + export code** — enables retraining and artifact iteration
3. **Walk-forward evaluation** — validates model quality before any live changes
4. **Feature parity tests** — ensures training/serving consistency
5. **Retire old settings** — reduces operator confusion
6. **AI optimizer role change** — aligns AI system with new architecture
7. **GBDT model** — only after logistic baseline is stable and trusted
