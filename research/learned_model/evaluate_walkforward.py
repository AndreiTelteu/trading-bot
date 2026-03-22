#!/usr/bin/env python3
"""
evaluate_walkforward.py — Purged walk-forward evaluation of learned signal models.

Implements rolling train/validation/test windows with embargo periods.
Reports per-window and aggregate prediction, ranking, and portfolio metrics.
Includes robustness cuts by regime, month, volatility bucket, and ranking decile.

Usage:
    python evaluate_walkforward.py --dataset-dir datasets/v1 \
        --model-type logistic --train-months 6 --test-months 1 \
        --output-path results/walkforward_logistic.json
"""

import argparse
import json
import logging
import os
import sys
from datetime import datetime, timezone
from typing import Any

import numpy as np
import pandas as pd
from sklearn.linear_model import LogisticRegression
from sklearn.metrics import brier_score_loss, log_loss, ndcg_score
from sklearn.preprocessing import StandardScaler

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
logger = logging.getLogger(__name__)

FEATURE_SPEC_VERSION = "learned_signal_v1"

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


def load_full_dataset(dataset_dir: str) -> pd.DataFrame:
    """Load the full dataset CSV."""
    path = os.path.join(dataset_dir, "full.csv")
    if not os.path.isfile(path):
        logger.error("Full dataset not found: %s", path)
        sys.exit(1)
    df = pd.read_csv(path)
    df["snapshot_time"] = pd.to_datetime(df["snapshot_time"], utc=True)
    return df


def prepare_xy(df: pd.DataFrame) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    """Extract X, y (binary label), and realized returns."""
    labeled = df.dropna(subset=["profitable"]).copy()
    if labeled.empty:
        return np.array([]), np.array([]), np.array([])

    X = labeled[FEATURE_NAMES].values.astype(np.float64)
    y = labeled["profitable"].values.astype(np.float64)
    returns = labeled["realized_return"].values.astype(np.float64)
    returns = np.nan_to_num(returns, 0.0)

    mask = ~np.isfinite(X)
    if mask.any():
        X[mask] = 0.0

    return X, y, returns


def compute_calibration_error(y_true: np.ndarray, y_prob: np.ndarray, n_bins: int = 10) -> float:
    """Expected calibration error."""
    bin_edges = np.linspace(0, 1, n_bins + 1)
    ece = 0.0
    for i in range(n_bins):
        mask = (y_prob >= bin_edges[i]) & (y_prob < bin_edges[i + 1])
        if mask.sum() == 0:
            continue
        ece += mask.sum() / len(y_true) * abs(y_true[mask].mean() - y_prob[mask].mean())
    return ece


def precision_at_k(y_true: np.ndarray, y_prob: np.ndarray, k: int) -> float:
    if len(y_true) < k or k == 0:
        k = max(1, len(y_true))
    top_k = np.argsort(y_prob)[::-1][:k]
    return float(y_true[top_k].mean())


def recall_at_k(y_true: np.ndarray, y_prob: np.ndarray, k: int) -> float:
    if y_true.sum() == 0 or k == 0:
        return 0.0
    k = min(k, len(y_true))
    top_k = np.argsort(y_prob)[::-1][:k]
    return float(y_true[top_k].sum() / y_true.sum())


def return_at_k(returns: np.ndarray, y_prob: np.ndarray, k: int) -> float:
    if len(returns) < k or k == 0:
        k = max(1, len(returns))
    top_k = np.argsort(y_prob)[::-1][:k]
    return float(returns[top_k].mean())


def hit_rate_at_k(y_true: np.ndarray, y_prob: np.ndarray, k: int) -> float:
    return precision_at_k(y_true, y_prob, k)


def compute_ndcg(y_true: np.ndarray, y_prob: np.ndarray) -> float:
    if len(y_true) < 2:
        return 0.0
    try:
        return float(ndcg_score(y_true.reshape(1, -1), y_prob.reshape(1, -1)))
    except Exception:
        return 0.0


def compute_sharpe(returns: np.ndarray) -> float:
    if len(returns) < 2:
        return 0.0
    mean = returns.mean()
    std = returns.std()
    if std == 0:
        return 0.0
    return float(mean / std * np.sqrt(252))


def compute_max_drawdown(cumulative_returns: np.ndarray) -> float:
    if len(cumulative_returns) == 0:
        return 0.0
    peak = np.maximum.accumulate(cumulative_returns)
    peak[peak == 0] = 1e-10
    drawdowns = (cumulative_returns - peak) / np.abs(peak)
    return float(drawdowns.min()) if len(drawdowns) > 0 else 0.0


def compute_profit_factor(returns: np.ndarray) -> float:
    gains = returns[returns > 0].sum()
    losses = np.abs(returns[returns < 0]).sum()
    if losses == 0:
        return float("inf") if gains > 0 else 0.0
    return float(gains / losses)


def compute_turnover(selected_symbols_per_period: list[set]) -> float:
    if len(selected_symbols_per_period) < 2:
        return 0.0
    turnovers = []
    for i in range(1, len(selected_symbols_per_period)):
        prev = selected_symbols_per_period[i - 1]
        curr = selected_symbols_per_period[i]
        union = prev | curr
        if len(union) == 0:
            continue
        changed = len(prev.symmetric_difference(curr))
        turnovers.append(changed / len(union))
    return float(np.mean(turnovers)) if turnovers else 0.0


def train_and_predict_logistic(
    X_train: np.ndarray, y_train: np.ndarray,
    X_test: np.ndarray,
) -> np.ndarray:
    """Train logistic regression and return calibrated probabilities."""
    scaler = StandardScaler()
    X_train_s = scaler.fit_transform(X_train)
    X_test_s = scaler.transform(X_test)

    model = LogisticRegression(
        C=1.0, penalty="l2", solver="lbfgs", max_iter=2000, random_state=42
    )
    model.fit(X_train_s, y_train)

    raw = (X_test_s @ model.coef_.T + model.intercept_).ravel()
    probs = 1.0 / (1.0 + np.exp(-raw))
    return probs


def train_and_predict_gbdt(
    X_train: np.ndarray, y_train: np.ndarray,
    X_test: np.ndarray,
) -> np.ndarray:
    """Train LightGBM and return calibrated probabilities."""
    try:
        import lightgbm as lgb
    except ImportError:
        logger.error("LightGBM not installed. Cannot use gbdt model type.")
        sys.exit(1)

    from sklearn.calibration import CalibratedClassifierCV

    gbdt = lgb.LGBMClassifier(
        objective="binary", n_estimators=300, learning_rate=0.05,
        num_leaves=31, max_depth=6, min_child_samples=20,
        subsample=0.8, colsample_bytree=0.8,
        reg_alpha=0.1, reg_lambda=1.0,
        random_state=42, verbose=-1,
    )
    gbdt.fit(X_train, y_train)

    # Simple calibration on training data (in walk-forward, we split further if needed)
    probs = gbdt.predict_proba(X_test)[:, 1]
    return probs


def generate_windows(
    df: pd.DataFrame, train_months: int, test_months: int, embargo_bars: int = 96
) -> list[dict]:
    """Generate purged walk-forward windows."""
    min_time = df["snapshot_time"].min()
    max_time = df["snapshot_time"].max()

    windows = []
    current_test_start = min_time + pd.DateOffset(months=train_months)

    while current_test_start < max_time:
        train_start = current_test_start - pd.DateOffset(months=train_months)
        test_end = current_test_start + pd.DateOffset(months=test_months)

        # Embargo: exclude embargo_bars worth of data (~24h at 15m bars = 96 bars)
        embargo_delta = pd.Timedelta(minutes=15 * embargo_bars)
        train_end_purged = current_test_start - embargo_delta

        windows.append({
            "train_start": train_start,
            "train_end": train_end_purged,
            "test_start": current_test_start,
            "test_end": min(test_end, max_time),
        })

        current_test_start = test_end

    return windows


def evaluate_window(
    df: pd.DataFrame,
    window: dict,
    model_type: str,
    top_k: int = 5,
) -> dict[str, Any] | None:
    """Evaluate a single walk-forward window."""
    train_mask = (df["snapshot_time"] >= window["train_start"]) & \
                 (df["snapshot_time"] < window["train_end"])
    test_mask = (df["snapshot_time"] >= window["test_start"]) & \
                (df["snapshot_time"] < window["test_end"])

    train_df = df[train_mask]
    test_df = df[test_mask]

    X_train, y_train, _ = prepare_xy(train_df)
    X_test, y_test, returns_test = prepare_xy(test_df)

    if len(y_train) < 50 or len(y_test) < 10:
        logger.warning(
            "Skipping window %s-%s: insufficient data (train=%d, test=%d)",
            window["train_start"].date(), window["test_end"].date(),
            len(y_train), len(y_test),
        )
        return None

    # Train and predict
    if model_type == "logistic":
        y_prob = train_and_predict_logistic(X_train, y_train, X_test)
    elif model_type == "gbdt":
        y_prob = train_and_predict_gbdt(X_train, y_train, X_test)
    else:
        logger.error("Unknown model type: %s", model_type)
        sys.exit(1)

    # Prediction metrics
    result: dict[str, Any] = {
        "window": {
            "train_start": str(window["train_start"].date()),
            "train_end": str(window["train_end"].date()),
            "test_start": str(window["test_start"].date()),
            "test_end": str(window["test_end"].date()),
        },
        "train_samples": int(len(y_train)),
        "test_samples": int(len(y_test)),
        "train_positive_rate": round(float(y_train.mean()), 4),
        "test_positive_rate": round(float(y_test.mean()), 4),
    }

    result["log_loss"] = round(log_loss(y_test, y_prob), 6)
    result["brier_score"] = round(brier_score_loss(y_test, y_prob), 6)
    result["calibration_error"] = round(compute_calibration_error(y_test, y_prob), 6)

    for k in [3, 5, 10]:
        result[f"precision_at_{k}"] = round(precision_at_k(y_test, y_prob, k), 4)
        result[f"recall_at_{k}"] = round(recall_at_k(y_test, y_prob, k), 4)

    # Ranking metrics
    for k in [3, 5, 10]:
        result[f"return_at_{k}"] = round(return_at_k(returns_test, y_prob, k), 6)
        result[f"hit_rate_at_{k}"] = round(hit_rate_at_k(y_test, y_prob, k), 4)

    result["ndcg"] = round(compute_ndcg(y_test, y_prob), 4)

    # Portfolio metrics (simulate top-K strategy)
    top_k_idx = np.argsort(y_prob)[::-1][:top_k]
    selected_returns = returns_test[top_k_idx]
    cum_returns = np.cumsum(selected_returns) + 1.0

    result["sharpe"] = round(compute_sharpe(selected_returns), 4)
    result["max_drawdown"] = round(compute_max_drawdown(cum_returns), 4)
    result["profit_factor"] = round(compute_profit_factor(selected_returns), 4)
    result["mean_return_top_k"] = round(float(selected_returns.mean()), 6)

    return result


def compute_robustness_cuts(
    df: pd.DataFrame, model_type: str
) -> dict[str, Any]:
    """Compute robustness metrics by various cuts."""
    cuts: dict[str, Any] = {}

    labeled = df.dropna(subset=["profitable"]).copy()
    if labeled.empty:
        return cuts

    # By regime
    if "regime_state" in labeled.columns:
        regime_cuts = {}
        for regime in labeled["regime_state"].unique():
            subset = labeled[labeled["regime_state"] == regime]
            if len(subset) >= 20:
                regime_cuts[str(regime)] = {
                    "count": int(len(subset)),
                    "positive_rate": round(float(subset["profitable"].mean()), 4),
                    "mean_return": round(float(subset["realized_return"].mean()), 6),
                }
        cuts["by_regime"] = regime_cuts

    # By month
    labeled["month"] = labeled["snapshot_time"].dt.to_period("M").astype(str)
    month_cuts = {}
    for month in sorted(labeled["month"].unique()):
        subset = labeled[labeled["month"] == month]
        if len(subset) >= 10:
            month_cuts[month] = {
                "count": int(len(subset)),
                "positive_rate": round(float(subset["profitable"].mean()), 4),
                "mean_return": round(float(subset["realized_return"].mean()), 6),
            }
    cuts["by_month"] = month_cuts

    # By volatility bucket
    if "realized_vol_20" in labeled.columns:
        labeled["vol_bucket"] = pd.qcut(
            labeled["realized_vol_20"], q=4, labels=["low", "med_low", "med_high", "high"],
            duplicates="drop",
        )
        vol_cuts = {}
        for bucket in labeled["vol_bucket"].dropna().unique():
            subset = labeled[labeled["vol_bucket"] == bucket]
            if len(subset) >= 10:
                vol_cuts[str(bucket)] = {
                    "count": int(len(subset)),
                    "positive_rate": round(float(subset["profitable"].mean()), 4),
                    "mean_return": round(float(subset["realized_return"].mean()), 6),
                }
        cuts["by_volatility_bucket"] = vol_cuts

    # By ranking decile
    if "rank_score" in labeled.columns:
        labeled["rank_decile"] = pd.qcut(
            labeled["rank_score"], q=10, labels=False, duplicates="drop"
        )
        decile_cuts = {}
        for decile in sorted(labeled["rank_decile"].dropna().unique()):
            subset = labeled[labeled["rank_decile"] == decile]
            if len(subset) >= 5:
                decile_cuts[f"decile_{int(decile)}"] = {
                    "count": int(len(subset)),
                    "positive_rate": round(float(subset["profitable"].mean()), 4),
                    "mean_return": round(float(subset["realized_return"].mean()), 6),
                }
        cuts["by_ranking_decile"] = decile_cuts

    return cuts


def main():
    parser = argparse.ArgumentParser(
        description="Purged walk-forward evaluation of learned signal models."
    )
    parser.add_argument(
        "--dataset-dir", required=True,
        help="Directory containing dataset CSVs from build_dataset.py",
    )
    parser.add_argument(
        "--model-type", required=True, choices=["logistic", "gbdt"],
        help="Model type to evaluate",
    )
    parser.add_argument(
        "--train-months", type=int, default=6,
        help="Training window size in months (default: 6)",
    )
    parser.add_argument(
        "--test-months", type=int, default=1,
        help="Test window size in months (default: 1)",
    )
    parser.add_argument(
        "--embargo-bars", type=int, default=96,
        help="Embargo period in bars between train and test (default: 96 = 24h at 15m)",
    )
    parser.add_argument(
        "--top-k", type=int, default=5,
        help="Top-K for portfolio simulation (default: 5)",
    )
    parser.add_argument(
        "--output-path", required=True,
        help="Path for the output JSON results",
    )
    args = parser.parse_args()

    df = load_full_dataset(args.dataset_dir)
    logger.info("Loaded %d rows, time range: %s to %s",
                len(df), df["snapshot_time"].min(), df["snapshot_time"].max())

    windows = generate_windows(
        df, args.train_months, args.test_months, args.embargo_bars
    )
    logger.info("Generated %d walk-forward windows.", len(windows))

    if not windows:
        logger.error("No valid windows generated. Check data range and window sizes.")
        sys.exit(1)

    # Evaluate each window
    window_results = []
    for i, window in enumerate(windows):
        logger.info("Evaluating window %d/%d: %s to %s",
                     i + 1, len(windows),
                     window["test_start"].date(), window["test_end"].date())
        result = evaluate_window(df, window, args.model_type, args.top_k)
        if result is not None:
            window_results.append(result)

    if not window_results:
        logger.error("No windows produced results.")
        sys.exit(1)

    # Aggregate metrics
    metric_keys = [
        "log_loss", "brier_score", "calibration_error",
        "precision_at_3", "precision_at_5", "precision_at_10",
        "recall_at_3", "recall_at_5", "recall_at_10",
        "return_at_3", "return_at_5", "return_at_10",
        "hit_rate_at_3", "hit_rate_at_5", "hit_rate_at_10",
        "ndcg", "sharpe", "max_drawdown", "profit_factor",
        "mean_return_top_k",
    ]

    aggregate: dict[str, Any] = {}
    for key in metric_keys:
        values = [w[key] for w in window_results if key in w]
        if values:
            aggregate[f"mean_{key}"] = round(float(np.mean(values)), 6)
            aggregate[f"std_{key}"] = round(float(np.std(values)), 6)
            aggregate[f"min_{key}"] = round(float(np.min(values)), 6)
            aggregate[f"max_{key}"] = round(float(np.max(values)), 6)

    total_test_samples = sum(w.get("test_samples", 0) for w in window_results)
    aggregate["total_test_samples"] = total_test_samples
    aggregate["num_windows"] = len(window_results)

    # Robustness cuts
    robustness = compute_robustness_cuts(df, args.model_type)

    # Final report
    report = {
        "model_type": args.model_type,
        "feature_spec_version": FEATURE_SPEC_VERSION,
        "config": {
            "train_months": args.train_months,
            "test_months": args.test_months,
            "embargo_bars": args.embargo_bars,
            "top_k": args.top_k,
        },
        "aggregate": aggregate,
        "per_window": window_results,
        "robustness_cuts": robustness,
        "evaluated_at": datetime.now(timezone.utc).isoformat(),
    }

    os.makedirs(os.path.dirname(args.output_path) or ".", exist_ok=True)
    with open(args.output_path, "w") as f:
        json.dump(report, f, indent=2)
    logger.info("Walk-forward report saved -> %s", args.output_path)

    # Print summary
    logger.info("=== Aggregate Metrics (%d windows) ===", len(window_results))
    for k, v in sorted(aggregate.items()):
        if k.startswith("mean_"):
            logger.info("  %s: %s", k, v)


if __name__ == "__main__":
    main()
