# Plan 3 — Learned Signal Model

## Goal

Replace the current hand-tuned indicator vote and manual probability coefficients with a point-in-time, calibrated, model-driven ranking system that selects the best candidates in the active universe.

## Why this must replace the current approach

The current signal stack is too low-dimensional and too hand-tuned:

- indicator outputs are collapsed into a small rating scale,
- weights are edited manually,
- probability coefficients are exposed as settings instead of being fitted,
- entry logic is mostly threshold-based,
- the system buys many weak setups because the score is not discriminative enough.

That architecture can produce acceptable prototypes, but it tends to plateau quickly. The highest-upside path is to move from **manual thresholds** to **learned ranking with calibration**.

## Core decision

The target system should predict candidate quality from a point-in-time feature vector and rank all eligible symbols at each decision timestamp.

### Preferred progression

1. calibrated logistic baseline,
2. calibrated nonlinear model,
3. cross-sectional top-K ranking policy,
4. portfolio allocation based on predicted edge and risk.

Do not skip straight to a complex model before the dataset, labels, and validation path are correct.

## Model architecture decision

## Stage A — Calibrated logistic model

Use this first because it is:

- fast to train,
- easy to interpret,
- easy to export and run in Go,
- a clean replacement for the current linear manual-beta path.

## Stage B — Gradient-boosted tree model

Use this after the data contract is stable.

Why:

- crypto signals are nonlinear,
- tree models typically capture interactions better than manual scoring,
- they often outperform logistic baselines on tabular cross-sectional data.

## Stage C — Ranking policy

Use the model to rank all eligible candidates and select the best few, rather than buying every name above a threshold.

The ranking policy should become the primary live entry mechanism.

## Exact implementation tasks

## 1. Freeze the prediction contract

Before writing training code, define the prediction target precisely.

### Required decisions

- [ ] decision cadence: start with completed `15m` bars
- [ ] candidate set: active symbols from Plan 2 universe snapshots
- [ ] entry timing: next execution point from Plan 1 execution model
- [ ] exit template: use the real shared exit engine assumptions
- [ ] primary prediction target: recommended `net_trade_return`
- [ ] secondary prediction target: recommended `probability_of_positive_net_trade`

### Required labels to persist

- [ ] realized net return after fees/slippage
- [ ] binary profitable/not profitable label
- [ ] max favorable excursion
- [ ] max adverse excursion
- [ ] bars held / time to exit
- [ ] exit reason reached first

### Important instruction

Do not train the model on a label that ignores the actual execution template. The label must reflect the real entry timing and exit logic, otherwise the model will learn the wrong objective.

## 2. Build a model-ready data schema

### New tables/entities

- [ ] `ModelArtifact`
- [ ] `FeatureSnapshot`
- [ ] `PredictionLog`
- [ ] `TradeLabel`
- [ ] optional `FeatureVersion`
- [ ] optional `DatasetBuild`

### Minimum fields for `ModelArtifact`

- [ ] model id / version
- [ ] model family (`logistic`, `gbdt`, later `ranker`)
- [ ] feature spec version
- [ ] label spec version
- [ ] train / validation / test windows
- [ ] calibration method
- [ ] metrics summary
- [ ] artifact storage path / checksum
- [ ] rollout state (`shadow`, `paper`, `live`, `retired`)

### Minimum fields for `FeatureSnapshot`

- [ ] timestamp
- [ ] symbol
- [ ] universe snapshot id
- [ ] all model features as columns or JSON blob
- [ ] feature spec version
- [ ] source quality flags

### Minimum fields for `PredictionLog`

- [ ] model version
- [ ] timestamp
- [ ] symbol
- [ ] predicted probability
- [ ] predicted expected value
- [ ] rank within timestamp
- [ ] decision result (`selected`, `rejected`, `shadow_only`)

### Minimum fields for `TradeLabel`

- [ ] feature snapshot id
- [ ] realized return
- [ ] profitable flag
- [ ] exit reason
- [ ] hold duration

### Files to update

- `internal/database/models.go`
- `internal/database/migrations.go`
- `internal/database/database.go`

## 3. Create a feature pipeline that is identical in training and live inference

### New code units

- [ ] `internal/services/model_features.go`
- [ ] `internal/services/model_features_test.go`
- [ ] offline dataset builder under a new research or tooling directory

### Feature families to include from day one

## 3.1 Price and trend features

- [ ] 15m, 1h, 4h, 1d returns
- [ ] EMA distance and EMA slope by timeframe
- [ ] breakout distance to rolling highs/lows
- [ ] trend persistence flags

## 3.2 Oscillator and mean-reversion features

- [ ] RSI value and normalized RSI distance from thresholds
- [ ] Bollinger `%B`
- [ ] z-score or normalized distance from rolling mean

## 3.3 Momentum quality features

- [ ] MACD histogram
- [ ] MACD histogram slope
- [ ] momentum over 3, 7, 14, 30 bars
- [ ] acceleration / deceleration of returns

## 3.4 Volume and liquidity features

- [ ] current volume / rolling volume ratios
- [ ] 24h quote volume
- [ ] 7d median intraday quote volume
- [ ] notional turnover proxies
- [ ] spread proxy if available later

## 3.5 Volatility and execution-cost features

- [ ] ATR / price on 15m, 1h, 4h
- [ ] realized volatility
- [ ] gap frequency
- [ ] expected slippage bucket

## 3.6 Cross-sectional features

- [ ] rank of return vs active universe
- [ ] rank of liquidity vs active universe
- [ ] rank of volatility vs active universe
- [ ] relative strength vs BTC
- [ ] relative strength vs universe median

## 3.7 Regime and breadth features

- [ ] BTC trend state
- [ ] BTC volatility state
- [ ] market breadth
- [ ] risk-on / risk-off regime bucket

## 3.8 Portfolio-context features

- [ ] open position count
- [ ] current exposure
- [ ] whether the symbol is highly correlated with current holdings
- [ ] concentration risk bucket

### Important instructions

- Keep features numeric and point-in-time safe.
- Version the feature spec.
- Write parity tests so the same candle inputs produce the same feature row in research and live code.

## 4. Build the offline dataset pipeline

### Recommended tooling decision

Use Python for offline training and dataset assembly, while keeping live inference in Go.

Why this is the best practical choice:

- Python has the best mature ecosystem for tabular modeling,
- offline training benefits from richer experimentation tooling,
- Go remains the online execution engine.

### Recommended directory layout

- [ ] `research/learned_model/README_internal.md` if needed later
- [ ] `research/learned_model/build_dataset.py`
- [ ] `research/learned_model/train_logistic.py`
- [ ] `research/learned_model/train_gbdt.py`
- [ ] `research/learned_model/evaluate_walkforward.py`
- [ ] `research/learned_model/export_artifact.py`

### Dataset builder responsibilities

- [ ] replay universe snapshots from Plan 2
- [ ] compute features at every decision timestamp
- [ ] attach labels from Plan 1 execution logic
- [ ] write partitioned datasets by time
- [ ] store dataset metadata and checksums

### Files to update

- add new research scripts/directories
- `internal/backtest/job.go` if dataset build hooks are surfaced via Go later

## 5. Train the logistic baseline and export a Go-friendly artifact

### Training tasks

- [ ] standardize numeric features
- [ ] fit regularized logistic regression
- [ ] fit a calibration layer on held-out validation data
- [ ] measure log loss, Brier score, calibration error, precision@K, and trading metrics
- [ ] export coefficients, intercept, scaler statistics, and calibration parameters

### Artifact format

Prefer a simple JSON artifact for the logistic baseline:

- [ ] metadata block
- [ ] feature order
- [ ] mean/std per feature
- [ ] coefficients
- [ ] intercept
- [ ] calibration parameters
- [ ] training window metadata
- [ ] metrics summary

### New code units in Go

- [ ] `internal/services/model_registry.go`
- [ ] `internal/services/model_inference.go`
- [ ] `internal/services/model_inference_test.go`

### Important instruction

Do not read model coefficients from `settings`. Load one immutable artifact by version.

## 6. Add live shadow inference before changing trading decisions

### Required behavior

- [ ] compute model predictions for each shortlisted candidate
- [ ] store them in `PredictionLog`
- [ ] do not trade on them yet
- [ ] show side-by-side comparison with the current rule-based decision

### Files to update

- `internal/services/trending.go`
- `internal/database/models.go`
- `frontend/src/components/SettingsPanel.jsx`

### Success gate before promotion

- [ ] top-ranked shadow candidates outperform low-ranked candidates out of sample
- [ ] probability calibration looks sane
- [ ] predictions are stable across restarts and repeated runs

## 7. Replace threshold-based entry logic with a ranked policy

### Required changes

- [ ] stop buying every symbol that passes `buy_only_strong` and confidence thresholds
- [ ] score all active candidates
- [ ] sort by predicted expected value or calibrated probability
- [ ] select only the top `K` symbols that pass risk and execution constraints

### Recommended first policy

- top 3 to 5 candidates per decision cycle
- minimum calibrated probability floor
- minimum expected value floor
- per-symbol and portfolio caps still enforced

### Files to update

- `internal/services/trending.go`
- `internal/backtest/engine.go`
- `internal/backtest/validation.go`

## 8. Add the nonlinear model after the logistic baseline is stable

### Training tasks

- [ ] train a gradient-boosted tree model on the same feature contract
- [ ] calibrate its probabilities
- [ ] compare against the logistic baseline over identical walk-forward splits
- [ ] export an artifact in a deployable format

### Serving decision

Choose one of these implementation paths and stick to it:

- [ ] Go-native inference from an exported tree artifact, or
- [ ] a tightly controlled local model-serving sidecar

### Recommendation

Start with logistic production inference first. Only add the nonlinear model when the offline evidence is clearly stronger and the serving path is operationally safe.

## 9. Add ranking-aware backtesting and analytics

### Required features

- [ ] evaluate realized return by rank bucket
- [ ] evaluate top-K vs threshold policies
- [ ] report precision@K / return@K / turnover@K
- [ ] compare model versions head-to-head

### Files to update

- `internal/backtest/engine.go`
- `internal/backtest/validation.go`
- `internal/backtest/metrics.go`

## 10. Retire the old settings and UI controls

### Remove or de-emphasize

- [ ] indicator weights
- [ ] `prob_model_beta0` to `prob_model_beta6`
- [ ] `prob_p_min`, `prob_ev_min`, `prob_avg_gain`, `prob_avg_loss`
- [ ] `buy_only_strong`
- [ ] `min_confidence_to_buy`
- [ ] `min_confidence_to_sell` as the primary entry framework
- [ ] indicator periods as routine live-ops knobs

### Replace with

- [ ] `active_model_version`
- [ ] `model_rollout_state`
- [ ] `selection_policy_top_k`
- [ ] `selection_policy_min_prob`
- [ ] `selection_policy_min_ev`
- [ ] `selection_policy_max_turnover`
- [ ] model diagnostics and drift views

### Files to update

- `frontend/src/components/SettingsPanel.jsx`
- `internal/database/database.go`
- `internal/handlers/settings.go`
- `internal/services/ai.go`

## 11. Change the AI optimizer's role

The AI proposal system should stop proposing indicator weights and manual model betas as primary strategy changes.

### New AI responsibilities

- [ ] propose policy thresholds around a fixed model
- [ ] propose risk-control changes
- [ ] summarize validation results and recommend promotion/rollback
- [ ] propose universe policy adjustments only within safe bounds

### Files to update

- `internal/services/ai.go`
- any AI-related settings UI or handlers

## Detailed instructions for the implementation order

1. Finish Plan 1 execution parity first.
2. Finish the Plan 2 universe contract before finalizing labels.
3. Build schema and feature pipeline before any training code.
4. Produce a complete offline dataset before choosing the first model.
5. Ship the calibrated logistic baseline in shadow mode before any live decision change.
6. Replace threshold entry with top-K ranking only after shadow evidence is convincing.
7. Add the nonlinear model only after the logistic baseline is trustworthy.
8. Retire old settings only after the new model policy is live and monitored.

## Required evaluation methodology

## Time splitting

- [ ] purged walk-forward train / validation / test windows
- [ ] no hyperparameter tuning on final test windows
- [ ] embargo around label horizons when needed
- [ ] final lockbox period kept untouched until promotion review

## Metrics to require

### Prediction metrics

- [ ] log loss
- [ ] Brier score
- [ ] calibration error
- [ ] precision and recall at chosen policy thresholds

### Ranking metrics

- [ ] precision@K
- [ ] hit rate@K
- [ ] realized return@K
- [ ] NDCG or similar ranking metric if needed

### Portfolio metrics

- [ ] Sharpe
- [ ] max drawdown
- [ ] profit factor
- [ ] turnover
- [ ] exposure concentration
- [ ] performance after fees/slippage

## Robustness cuts

- [ ] by regime
- [ ] by month or quarter
- [ ] by volatility bucket
- [ ] by symbol cohort
- [ ] by ranking decile

## Validation tasks

- [ ] feature parity tests between research and Go inference
- [ ] artifact load and inference tests in Go
- [ ] backtest comparison of old signal vs logistic baseline
- [ ] shadow-mode live logging sanity checks
- [ ] promotion review with explicit pass/fail criteria

## Success criteria

- the top-ranked candidates materially outperform lower-ranked candidates out of sample
- model calibration remains acceptable in shadow and paper modes
- profit factor improves in dynamic-universe walk-forward tests, not just one aggregate run
- the system trades fewer, higher-quality setups instead of buying many marginal names

## Practical end state

When this plan is complete, the main live loop should work like this:

1. get the active universe from Plan 2,
2. compute the model feature row for each candidate,
3. load the active model artifact,
4. predict probability and expected value,
5. rank all candidates,
6. take only the top few that satisfy policy and risk rules,
7. manage exits with the Plan 1 execution engine.
