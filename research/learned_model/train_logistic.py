#!/usr/bin/env python3
"""
train_logistic.py — Train a regularized logistic regression model.

Loads a dataset produced by build_dataset.py, standardizes features,
fits L2-regularized logistic regression with cross-validated C,
calibrates via Platt scaling, and exports the model artifact.

Usage:
    python train_logistic.py --dataset-dir datasets/v1 \
        --output-path models/logistic_v2.json \
        --train-end 2024-07-01 --val-end 2024-10-01
"""

import argparse
import hashlib
import json
import logging
import os
import sys
from datetime import datetime, timezone

import numpy as np
import pandas as pd
from sklearn.linear_model import LogisticRegression, LogisticRegressionCV
from sklearn.calibration import CalibratedClassifierCV
from sklearn.metrics import brier_score_loss, log_loss
from sklearn.preprocessing import StandardScaler

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
logger = logging.getLogger(__name__)

FEATURE_SPEC_VERSION = "learned_signal_v1"
LABEL_SPEC_VERSION = "net_trade_return_v1"

FEATURE_NAMES = [
    "ret_15m_1", "ret_15m_4", "ret_15m_16", "ret_15m_96",
    "price_vs_ema20", "price_vs_ema50", "ema20_slope",
    "breakout_20", "breakdown_20",
    "rsi_14", "rsi_centered", "bb_percent_b", "price_zscore_20",
    "macd_hist", "macd_hist_slope",
    "momentum_3", "momentum_12",
    "volume_ratio_20", "atr_ratio_14", "realized_vol_20",
    "quote_volume_24h_log", "median_intraday_quote_volume_log",
    "relative_strength_7d",
    "universe_rank_pct", "liquidity_rank_pct", "volatility_rank_pct",
    "regime_score", "breadth_ratio",
    "btc_return_1d", "btc_trend_gap",
    "open_position_count", "exposure_ratio", "already_open_position",
    "volume_acceleration", "overextension_penalty",
    "trend_quality", "breakout_proximity", "gap_ratio",
]


def load_split(dataset_dir: str, split: str) -> pd.DataFrame:
    """Load a dataset split CSV."""
    path = os.path.join(dataset_dir, f"{split}.csv")
    if not os.path.isfile(path):
        logger.error("Split file not found: %s", path)
        sys.exit(1)
    df = pd.read_csv(path)
    df["snapshot_time"] = pd.to_datetime(df["snapshot_time"], utc=True)
    return df


def prepare_xy(df: pd.DataFrame) -> tuple[np.ndarray, np.ndarray]:
    """Extract feature matrix X and binary label y from dataframe."""
    # Only rows with labels
    labeled = df.dropna(subset=["profitable"]).copy()
    if labeled.empty:
        logger.error("No labeled rows in the split.")
        sys.exit(1)

    X = labeled[FEATURE_NAMES].values.astype(np.float64)
    y = labeled["profitable"].values.astype(np.float64)

    # Replace any NaN/Inf in features with 0
    mask = ~np.isfinite(X)
    if mask.any():
        logger.warning("Replacing %d non-finite feature values with 0.", mask.sum())
        X[mask] = 0.0

    return X, y


def compute_calibration_error(y_true: np.ndarray, y_prob: np.ndarray, n_bins: int = 10) -> float:
    """Compute expected calibration error (ECE)."""
    bin_edges = np.linspace(0, 1, n_bins + 1)
    ece = 0.0
    for i in range(n_bins):
        mask = (y_prob >= bin_edges[i]) & (y_prob < bin_edges[i + 1])
        if mask.sum() == 0:
            continue
        bin_acc = y_true[mask].mean()
        bin_conf = y_prob[mask].mean()
        ece += mask.sum() / len(y_true) * abs(bin_acc - bin_conf)
    return ece


def precision_at_k(y_true: np.ndarray, y_prob: np.ndarray, k: int) -> float:
    """Compute precision@K: fraction of top-K predictions that are positive."""
    if len(y_true) < k:
        k = len(y_true)
    if k == 0:
        return 0.0
    top_k_idx = np.argsort(y_prob)[::-1][:k]
    return y_true[top_k_idx].mean()


def fit_platt_scaling(
    raw_scores: np.ndarray, y_true: np.ndarray
) -> tuple[float, float]:
    """Fit Platt scaling parameters A, B such that P = sigmoid(A * score + B)."""
    from sklearn.linear_model import LogisticRegression as LR

    platt = LR(C=1e10, solver="lbfgs", max_iter=1000)
    platt.fit(raw_scores.reshape(-1, 1), y_true)
    a = float(platt.coef_[0][0])
    b = float(platt.intercept_[0])
    return a, b


def compute_trading_metrics(
    y_true: np.ndarray, returns: np.ndarray
) -> dict[str, float]:
    """Compute simple trading metrics from realized returns."""
    winning = returns[y_true == 1]
    losing = returns[y_true == 0]
    avg_gain = float(winning.mean()) if len(winning) > 0 else 0.0
    avg_loss = float(np.abs(losing).mean()) if len(losing) > 0 else 0.0
    win_rate = float(y_true.mean()) if len(y_true) > 0 else 0.0
    profit_factor = avg_gain / avg_loss if avg_loss > 0 else 0.0

    return {
        "avg_gain": avg_gain,
        "avg_loss": avg_loss,
        "win_rate": win_rate,
        "profit_factor": profit_factor,
    }


def build_artifact(
    version: str,
    scaler: StandardScaler,
    model: LogisticRegression,
    calibration_a: float,
    calibration_b: float,
    metrics: dict,
    avg_gain: float,
    avg_loss: float,
    train_window: str,
    val_window: str,
    test_window: str,
) -> dict:
    """Build JSON artifact matching Go LogisticModelArtifact schema."""
    features = []
    for i, fname in enumerate(FEATURE_NAMES):
        features.append({
            "name": fname,
            "mean": float(scaler.mean_[i]),
            "std": float(scaler.scale_[i]),
            "coefficient": float(model.coef_[0][i]),
        })

    artifact = {
        "version": version,
        "model_family": "logistic",
        "feature_spec_version": FEATURE_SPEC_VERSION,
        "label_spec_version": LABEL_SPEC_VERSION,
        "calibration_method": "platt",
        "training_window": train_window,
        "validation_window": val_window,
        "test_window": test_window,
        "metrics": metrics,
        "metadata": {
            "bootstrap": False,
            "decision_cadence": "15m_close",
            "selection_objective": "expected_value",
            "trained_at": datetime.now(timezone.utc).isoformat(),
            "sklearn_C": float(model.C),
        },
        "avg_gain": avg_gain,
        "avg_loss": avg_loss,
        "intercept": float(model.intercept_[0]),
        "calibration": {
            "a": calibration_a,
            "b": calibration_b,
        },
        "features": features,
    }
    return artifact


def main():
    parser = argparse.ArgumentParser(
        description="Train regularized logistic regression model."
    )
    parser.add_argument(
        "--dataset-dir", required=True,
        help="Directory containing dataset CSVs from build_dataset.py",
    )
    parser.add_argument(
        "--output-path", required=True,
        help="Path for the output JSON artifact",
    )
    parser.add_argument(
        "--train-end", default=None,
        help="Override training period end (YYYY-MM-DD). Uses pre-split data by default.",
    )
    parser.add_argument(
        "--val-end", default=None,
        help="Override validation period end (YYYY-MM-DD). Uses pre-split data by default.",
    )
    parser.add_argument(
        "--version", default=None,
        help="Model version string (default: auto-generated)",
    )
    args = parser.parse_args()

    # Load pre-split data or full dataset
    if args.train_end and args.val_end:
        logger.info("Using full dataset with custom time splits.")
        full = load_split(args.dataset_dir, "full")
        train_end = pd.Timestamp(args.train_end, tz="UTC")
        val_end = pd.Timestamp(args.val_end, tz="UTC")
        train_df = full[full["snapshot_time"] < train_end]
        val_df = full[(full["snapshot_time"] >= train_end) & (full["snapshot_time"] < val_end)]
        test_df = full[full["snapshot_time"] >= val_end]
    else:
        logger.info("Using pre-split dataset files.")
        train_df = load_split(args.dataset_dir, "train")
        val_df = load_split(args.dataset_dir, "validation")
        test_df = load_split(args.dataset_dir, "test")

    X_train, y_train = prepare_xy(train_df)
    X_val, y_val = prepare_xy(val_df)

    logger.info("Train: %d samples (%.1f%% positive)", len(y_train), y_train.mean() * 100)
    logger.info("Val:   %d samples (%.1f%% positive)", len(y_val), y_val.mean() * 100)

    # Standardize features
    scaler = StandardScaler()
    X_train_s = scaler.fit_transform(X_train)
    X_val_s = scaler.transform(X_val)

    # Fit logistic regression with CV to select C
    logger.info("Fitting LogisticRegressionCV with L2 penalty ...")
    model_cv = LogisticRegressionCV(
        Cs=10,
        penalty="l2",
        solver="lbfgs",
        cv=5,
        scoring="neg_log_loss",
        max_iter=2000,
        random_state=42,
    )
    model_cv.fit(X_train_s, y_train)
    best_C = float(model_cv.C_[0])
    logger.info("Best C from CV: %.6f", best_C)

    # Refit with best C for clean coefficient extraction
    model = LogisticRegression(
        C=best_C, penalty="l2", solver="lbfgs", max_iter=2000, random_state=42
    )
    model.fit(X_train_s, y_train)

    # Raw predictions on validation set
    raw_scores_val = X_val_s @ model.coef_.T + model.intercept_
    raw_scores_val = raw_scores_val.ravel()

    # Platt scaling on validation data
    logger.info("Fitting Platt scaling on validation set ...")
    cal_a, cal_b = fit_platt_scaling(raw_scores_val, y_val)
    logger.info("Calibration: a=%.6f, b=%.6f", cal_a, cal_b)

    # Calibrated probabilities
    calibrated_logits = cal_a * raw_scores_val + cal_b
    y_prob_val = 1.0 / (1.0 + np.exp(-calibrated_logits))

    # Metrics on validation
    val_log_loss = log_loss(y_val, y_prob_val)
    val_brier = brier_score_loss(y_val, y_prob_val)
    val_ece = compute_calibration_error(y_val, y_prob_val)

    metrics = {
        "val_log_loss": round(val_log_loss, 6),
        "val_brier_score": round(val_brier, 6),
        "val_calibration_error": round(val_ece, 6),
        "val_precision_at_3": round(precision_at_k(y_val, y_prob_val, 3), 4),
        "val_precision_at_5": round(precision_at_k(y_val, y_prob_val, 5), 4),
        "val_precision_at_10": round(precision_at_k(y_val, y_prob_val, 10), 4),
        "best_C": best_C,
        "train_samples": int(len(y_train)),
        "val_samples": int(len(y_val)),
        "train_positive_rate": round(float(y_train.mean()), 4),
        "val_positive_rate": round(float(y_val.mean()), 4),
    }

    # Trading metrics from validation returns
    val_labeled = val_df.dropna(subset=["profitable"]).copy()
    if "realized_return" in val_labeled.columns:
        returns_val = val_labeled["realized_return"].values.astype(np.float64)
        returns_val = np.nan_to_num(returns_val, 0.0)
        trading_metrics = compute_trading_metrics(y_val, returns_val)
        metrics.update({f"val_{k}": round(v, 6) for k, v in trading_metrics.items()})
        avg_gain = trading_metrics["avg_gain"]
        avg_loss = trading_metrics["avg_loss"]
    else:
        avg_gain = 0.028
        avg_loss = 0.015

    # Evaluate on test set if available
    try:
        if args.train_end and args.val_end:
            X_test, y_test = prepare_xy(test_df)
        else:
            X_test, y_test = prepare_xy(test_df)
        X_test_s = scaler.transform(X_test)
        raw_scores_test = (X_test_s @ model.coef_.T + model.intercept_).ravel()
        calibrated_test = cal_a * raw_scores_test + cal_b
        y_prob_test = 1.0 / (1.0 + np.exp(-calibrated_test))
        metrics["test_log_loss"] = round(log_loss(y_test, y_prob_test), 6)
        metrics["test_brier_score"] = round(brier_score_loss(y_test, y_prob_test), 6)
        metrics["test_calibration_error"] = round(
            compute_calibration_error(y_test, y_prob_test), 6
        )
        metrics["test_precision_at_3"] = round(precision_at_k(y_test, y_prob_test, 3), 4)
        metrics["test_precision_at_5"] = round(precision_at_k(y_test, y_prob_test, 5), 4)
        metrics["test_precision_at_10"] = round(precision_at_k(y_test, y_prob_test, 10), 4)
        metrics["test_samples"] = int(len(y_test))
        logger.info("Test log_loss=%.4f, Brier=%.4f, ECE=%.4f",
                     metrics["test_log_loss"], metrics["test_brier_score"],
                     metrics["test_calibration_error"])
    except SystemExit:
        logger.warning("No labeled test data available for evaluation.")

    # Print summary
    logger.info("=== Validation Metrics ===")
    for k, v in sorted(metrics.items()):
        if k.startswith("val_"):
            logger.info("  %s: %s", k, v)

    # Determine windows
    train_window = f"{train_df['snapshot_time'].min().date()} to {train_df['snapshot_time'].max().date()}"
    val_window = f"{val_df['snapshot_time'].min().date()} to {val_df['snapshot_time'].max().date()}"
    test_window = "none"
    if not test_df.empty:
        test_window = f"{test_df['snapshot_time'].min().date()} to {test_df['snapshot_time'].max().date()}"

    # Version
    version = args.version or f"logistic_trained_{datetime.now(timezone.utc).strftime('%Y%m%d_%H%M%S')}"

    artifact = build_artifact(
        version=version,
        scaler=scaler,
        model=model,
        calibration_a=cal_a,
        calibration_b=cal_b,
        metrics=metrics,
        avg_gain=avg_gain,
        avg_loss=avg_loss,
        train_window=train_window,
        val_window=val_window,
        test_window=test_window,
    )

    # Save artifact
    os.makedirs(os.path.dirname(args.output_path) or ".", exist_ok=True)
    with open(args.output_path, "w") as f:
        json.dump(artifact, f, indent=2)
    logger.info("Artifact saved -> %s", args.output_path)

    # Checksum
    with open(args.output_path, "rb") as f:
        checksum = hashlib.sha256(f.read()).hexdigest()
    logger.info("Artifact SHA-256: %s", checksum)


if __name__ == "__main__":
    main()
