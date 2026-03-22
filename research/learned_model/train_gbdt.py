#!/usr/bin/env python3
"""
train_gbdt.py — Train a LightGBM gradient-boosted tree model.

Same interface as train_logistic.py. Trains a LightGBM binary classifier,
calibrates probabilities, compares against the logistic baseline, and exports
an artifact compatible with the Go model registry.

Usage:
    python train_gbdt.py --dataset-dir datasets/v1 \
        --output-path models/gbdt_v1.json \
        --train-end 2024-07-01 --val-end 2024-10-01
"""

import argparse
import hashlib
import json
import logging
import os
import sys
from datetime import datetime, timezone

import lightgbm as lgb
import numpy as np
import pandas as pd
from sklearn.calibration import CalibratedClassifierCV
from sklearn.metrics import brier_score_loss, log_loss
from sklearn.model_selection import StratifiedKFold
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
    """Extract feature matrix X and binary label y."""
    labeled = df.dropna(subset=["profitable"]).copy()
    if labeled.empty:
        logger.error("No labeled rows in the split.")
        sys.exit(1)

    X = labeled[FEATURE_NAMES].values.astype(np.float64)
    y = labeled["profitable"].values.astype(np.float64)

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
    """Compute precision@K."""
    if len(y_true) < k:
        k = len(y_true)
    if k == 0:
        return 0.0
    top_k_idx = np.argsort(y_prob)[::-1][:k]
    return y_true[top_k_idx].mean()


def compute_trading_metrics(
    y_true: np.ndarray, returns: np.ndarray
) -> dict[str, float]:
    """Compute simple trading metrics."""
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
    feature_importances: np.ndarray,
    calibration_a: float,
    calibration_b: float,
    intercept: float,
    metrics: dict,
    avg_gain: float,
    avg_loss: float,
    train_window: str,
    val_window: str,
    test_window: str,
    lgb_params: dict,
) -> dict:
    """Build JSON artifact. Uses logistic-compatible format with GBDT metadata."""
    features = []
    for i, fname in enumerate(FEATURE_NAMES):
        features.append({
            "name": fname,
            "mean": float(scaler.mean_[i]),
            "std": float(scaler.scale_[i]),
            "coefficient": float(feature_importances[i]),
        })

    artifact = {
        "version": version,
        "model_family": "gbdt",
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
            "lgb_params": lgb_params,
            "note": "Coefficients represent normalized feature importances, not linear weights.",
        },
        "avg_gain": avg_gain,
        "avg_loss": avg_loss,
        "intercept": intercept,
        "calibration": {
            "a": calibration_a,
            "b": calibration_b,
        },
        "features": features,
    }
    return artifact


def main():
    parser = argparse.ArgumentParser(
        description="Train LightGBM gradient-boosted tree model."
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
        help="Override training period end (YYYY-MM-DD).",
    )
    parser.add_argument(
        "--val-end", default=None,
        help="Override validation period end (YYYY-MM-DD).",
    )
    parser.add_argument(
        "--version", default=None,
        help="Model version string (default: auto-generated)",
    )
    parser.add_argument(
        "--baseline-path", default=None,
        help="Path to logistic baseline artifact for comparison",
    )
    args = parser.parse_args()

    # Load data
    if args.train_end and args.val_end:
        full = load_split(args.dataset_dir, "full")
        train_end = pd.Timestamp(args.train_end, tz="UTC")
        val_end = pd.Timestamp(args.val_end, tz="UTC")
        train_df = full[full["snapshot_time"] < train_end]
        val_df = full[(full["snapshot_time"] >= train_end) & (full["snapshot_time"] < val_end)]
        test_df = full[full["snapshot_time"] >= val_end]
    else:
        train_df = load_split(args.dataset_dir, "train")
        val_df = load_split(args.dataset_dir, "validation")
        test_df = load_split(args.dataset_dir, "test")

    X_train, y_train = prepare_xy(train_df)
    X_val, y_val = prepare_xy(val_df)

    logger.info("Train: %d samples (%.1f%% positive)", len(y_train), y_train.mean() * 100)
    logger.info("Val:   %d samples (%.1f%% positive)", len(y_val), y_val.mean() * 100)

    # Standardize features (for artifact compatibility; GBDT doesn't need it but
    # we store scaler stats for the artifact format)
    scaler = StandardScaler()
    X_train_s = scaler.fit_transform(X_train)
    X_val_s = scaler.transform(X_val)

    # LightGBM parameters
    lgb_params = {
        "objective": "binary",
        "metric": "binary_logloss",
        "learning_rate": 0.05,
        "num_leaves": 31,
        "max_depth": 6,
        "min_child_samples": 20,
        "subsample": 0.8,
        "colsample_bytree": 0.8,
        "reg_alpha": 0.1,
        "reg_lambda": 1.0,
        "n_estimators": 500,
        "random_state": 42,
        "verbose": -1,
    }

    # Train LightGBM (on raw features, not standardized)
    logger.info("Training LightGBM model ...")
    gbdt = lgb.LGBMClassifier(**lgb_params)
    gbdt.fit(
        X_train, y_train,
        eval_set=[(X_val, y_val)],
        callbacks=[
            lgb.early_stopping(50, verbose=True),
            lgb.log_evaluation(50),
        ],
    )
    logger.info("Best iteration: %d", gbdt.best_iteration_)

    # Calibrate using CalibratedClassifierCV on validation set
    logger.info("Calibrating probabilities via CalibratedClassifierCV (sigmoid) ...")
    calibrated = CalibratedClassifierCV(gbdt, method="sigmoid", cv="prefit")
    calibrated.fit(X_val, y_val)

    # Raw and calibrated predictions on validation
    y_prob_raw = gbdt.predict_proba(X_val)[:, 1]
    y_prob_cal = calibrated.predict_proba(X_val)[:, 1]

    # Compute Platt-like A, B from the calibrator for artifact export
    # CalibratedClassifierCV with sigmoid uses a LogisticRegression internally
    cal_clf = calibrated.calibrated_classifiers_[0].calibrators[0]
    # sigmoid calibrator: P = 1 / (1 + exp(-(A*f + B)))
    cal_a = float(cal_clf.coef_[0][0]) if hasattr(cal_clf, "coef_") else 1.0
    cal_b = float(cal_clf.intercept_[0]) if hasattr(cal_clf, "intercept_") else 0.0
    logger.info("Calibration: a=%.6f, b=%.6f", cal_a, cal_b)

    # Metrics
    metrics = {
        "val_log_loss_raw": round(log_loss(y_val, y_prob_raw), 6),
        "val_log_loss_calibrated": round(log_loss(y_val, y_prob_cal), 6),
        "val_brier_raw": round(brier_score_loss(y_val, y_prob_raw), 6),
        "val_brier_calibrated": round(brier_score_loss(y_val, y_prob_cal), 6),
        "val_ece_raw": round(compute_calibration_error(y_val, y_prob_raw), 6),
        "val_ece_calibrated": round(compute_calibration_error(y_val, y_prob_cal), 6),
        "val_precision_at_3": round(precision_at_k(y_val, y_prob_cal, 3), 4),
        "val_precision_at_5": round(precision_at_k(y_val, y_prob_cal, 5), 4),
        "val_precision_at_10": round(precision_at_k(y_val, y_prob_cal, 10), 4),
        "best_iteration": int(gbdt.best_iteration_),
        "train_samples": int(len(y_train)),
        "val_samples": int(len(y_val)),
        "train_positive_rate": round(float(y_train.mean()), 4),
        "val_positive_rate": round(float(y_val.mean()), 4),
    }

    # Trading metrics
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

    # Test set evaluation
    try:
        X_test, y_test = prepare_xy(test_df)
        y_prob_test = calibrated.predict_proba(X_test)[:, 1]
        metrics["test_log_loss"] = round(log_loss(y_test, y_prob_test), 6)
        metrics["test_brier_score"] = round(brier_score_loss(y_test, y_prob_test), 6)
        metrics["test_ece"] = round(compute_calibration_error(y_test, y_prob_test), 6)
        metrics["test_precision_at_3"] = round(precision_at_k(y_test, y_prob_test, 3), 4)
        metrics["test_precision_at_5"] = round(precision_at_k(y_test, y_prob_test, 5), 4)
        metrics["test_precision_at_10"] = round(precision_at_k(y_test, y_prob_test, 10), 4)
        metrics["test_samples"] = int(len(y_test))
        logger.info("Test log_loss=%.4f, Brier=%.4f, ECE=%.4f",
                     metrics["test_log_loss"], metrics["test_brier_score"],
                     metrics["test_ece"])
    except SystemExit:
        logger.warning("No labeled test data for evaluation.")

    # Compare with logistic baseline
    if args.baseline_path and os.path.isfile(args.baseline_path):
        with open(args.baseline_path) as f:
            baseline = json.load(f)
        baseline_metrics = baseline.get("metrics", {})
        logger.info("=== Comparison vs Logistic Baseline ===")
        for key in ["val_log_loss", "val_brier_score", "val_calibration_error"]:
            bl_val = baseline_metrics.get(key)
            gb_key = key.replace("val_log_loss", "val_log_loss_calibrated") \
                        .replace("val_brier_score", "val_brier_calibrated") \
                        .replace("val_calibration_error", "val_ece_calibrated")
            gb_val = metrics.get(gb_key)
            if bl_val is not None and gb_val is not None:
                diff = gb_val - bl_val
                better = "BETTER" if diff < 0 else "WORSE"
                logger.info("  %s: baseline=%.6f, gbdt=%.6f (%s, diff=%.6f)",
                            key, bl_val, gb_val, better, diff)

    # Feature importances (normalized to sum=1 for coefficient-like storage)
    raw_importances = gbdt.feature_importances_.astype(np.float64)
    total = raw_importances.sum()
    if total > 0:
        norm_importances = raw_importances / total
    else:
        norm_importances = raw_importances

    # Print summary
    logger.info("=== Validation Metrics ===")
    for k, v in sorted(metrics.items()):
        if k.startswith("val_"):
            logger.info("  %s: %s", k, v)

    logger.info("=== Top 10 Feature Importances ===")
    importance_pairs = sorted(
        zip(FEATURE_NAMES, raw_importances), key=lambda x: -x[1]
    )
    for fname, imp in importance_pairs[:10]:
        logger.info("  %s: %.0f", fname, imp)

    # Windows
    train_window = f"{train_df['snapshot_time'].min().date()} to {train_df['snapshot_time'].max().date()}"
    val_window = f"{val_df['snapshot_time'].min().date()} to {val_df['snapshot_time'].max().date()}"
    test_window = "none"
    if not test_df.empty:
        test_window = f"{test_df['snapshot_time'].min().date()} to {test_df['snapshot_time'].max().date()}"

    version = args.version or f"gbdt_trained_{datetime.now(timezone.utc).strftime('%Y%m%d_%H%M%S')}"

    artifact = build_artifact(
        version=version,
        scaler=scaler,
        feature_importances=norm_importances,
        calibration_a=cal_a,
        calibration_b=cal_b,
        intercept=0.0,  # GBDT has no linear intercept
        metrics=metrics,
        avg_gain=avg_gain,
        avg_loss=avg_loss,
        train_window=train_window,
        val_window=val_window,
        test_window=test_window,
        lgb_params=lgb_params,
    )

    os.makedirs(os.path.dirname(args.output_path) or ".", exist_ok=True)
    with open(args.output_path, "w") as f:
        json.dump(artifact, f, indent=2)
    logger.info("Artifact saved -> %s", args.output_path)

    with open(args.output_path, "rb") as f:
        checksum = hashlib.sha256(f.read()).hexdigest()
    logger.info("Artifact SHA-256: %s", checksum)


if __name__ == "__main__":
    main()
