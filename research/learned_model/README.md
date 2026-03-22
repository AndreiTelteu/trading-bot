# Learned Signal Model — Offline Training Pipeline

Python research tools for training, evaluating, and exporting learned signal models for the trading bot.

## Prerequisites

- Python 3.10+
- `pip install -r requirements.txt`

## Scripts

| Script | Purpose |
|--------|---------|
| `build_dataset.py` | Extract features and labels from the SQLite database into partitioned CSV files |
| `train_logistic.py` | Train a regularized logistic regression with Platt-calibrated probabilities |
| `train_gbdt.py` | Train a LightGBM gradient-boosted tree classifier |
| `evaluate_walkforward.py` | Purged walk-forward evaluation with per-window and aggregate metrics |
| `export_artifact.py` | Export a trained model to the Go-compatible JSON artifact format |
| `test_feature_parity.py` | Verify Python indicator calculations match the Go `BuildModelFeatureRow` |

## Workflow

```
1. Build dataset from database
   python build_dataset.py --db-path ../../trading.db --output-dir datasets/v1 \
       --start 2024-01-01 --end 2025-01-01

2. Train logistic baseline
   python train_logistic.py --dataset-dir datasets/v1 \
       --output-path models/logistic_v2.json

3. (Optional) Train GBDT and compare
   python train_gbdt.py --dataset-dir datasets/v1 \
       --output-path models/gbdt_v1.json \
       --baseline-path models/logistic_v2.json

4. Walk-forward evaluation
   python evaluate_walkforward.py --dataset-dir datasets/v1 \
       --model-type logistic --train-months 6 --test-months 1 \
       --output-path results/walkforward_logistic.json

5. Export artifact for Go deployment
   python export_artifact.py --model-path models/logistic_v2.json \
       --output-path ../../internal/services/model_artifacts/logistic_v2.json \
       --version logistic_v2

6. Verify feature parity
   python test_feature_parity.py
```

## Feature Spec

All models use feature spec version `learned_signal_v1` with 38 features computed by `BuildModelFeatureRow` in Go (`internal/services/model_features.go`). The Python implementations in `test_feature_parity.py` mirror the Go math exactly.

## Artifact Format

The exported JSON artifact matches the Go `LogisticModelArtifact` struct in `internal/services/model_inference.go`. Key fields:

- `version`: unique model identifier
- `features[]`: ordered list with `name`, `mean`, `std`, `coefficient`
- `intercept`: logistic regression bias term
- `calibration.a`, `calibration.b`: Platt scaling parameters
- `avg_gain`, `avg_loss`: for expected-value computation
- `metrics`: training/validation/test metrics

See `internal/services/model_artifacts/logistic_baseline_v1.json` for the reference format.
