#!/usr/bin/env python3
"""
build_dataset.py — Build training dataset from the trading-bot SQLite database.

Loads feature snapshots, trade labels, and universe membership to produce a
partitioned dataset suitable for model training and evaluation.

Usage:
    python build_dataset.py --db-path trading.db --output-dir datasets/v1 \
        --start 2024-01-01 --end 2025-01-01
"""

import argparse
import hashlib
import json
import logging
import math
import os
import sqlite3
import sys
from datetime import datetime, timezone

import numpy as np
import pandas as pd

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
logger = logging.getLogger(__name__)

FEATURE_SPEC_VERSION = "learned_signal_v1"

# Canonical ordered feature list — must match Go BuildModelFeatureRow output
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


def connect_db(db_path: str) -> sqlite3.Connection:
    """Open a read-only connection to the SQLite database."""
    if not os.path.isfile(db_path):
        logger.error("Database file not found: %s", db_path)
        sys.exit(1)
    conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    return conn


def load_feature_snapshots(conn: sqlite3.Connection, start: str, end: str) -> pd.DataFrame:
    """Load feature snapshots from the database within a time range."""
    query = """
        SELECT
            fs.id,
            fs.snapshot_time,
            fs.symbol,
            fs.last_price,
            fs.regime_state,
            fs.breadth_ratio,
            fs.rank_score,
            fs.feature_spec_version,
            fs.features_json,
            fs.quality_flags_json,
            fs.universe_snapshot_id
        FROM feature_snapshots fs
        WHERE fs.snapshot_time >= ? AND fs.snapshot_time < ?
          AND fs.feature_spec_version = ?
        ORDER BY fs.snapshot_time, fs.symbol
    """
    logger.info("Loading feature snapshots from %s to %s ...", start, end)
    rows = conn.execute(query, (start, end, FEATURE_SPEC_VERSION)).fetchall()
    if not rows:
        logger.warning("No feature snapshots found in the specified range.")
        return pd.DataFrame()

    records = []
    for row in rows:
        features_json = row["features_json"]
        quality_flags_json = row["quality_flags_json"]

        try:
            features = json.loads(features_json) if features_json else {}
        except json.JSONDecodeError:
            logger.warning("Invalid features JSON for snapshot %d, skipping.", row["id"])
            continue

        # Skip rows with quality flags (invalid data)
        quality_flags = []
        if quality_flags_json:
            try:
                quality_flags = json.loads(quality_flags_json)
            except json.JSONDecodeError:
                pass
        if quality_flags:
            continue

        # Verify all expected features are present
        missing = [f for f in FEATURE_NAMES if f not in features]
        if missing:
            logger.debug(
                "Snapshot %d missing features: %s, skipping.",
                row["id"], missing[:5],
            )
            continue

        record = {
            "snapshot_id": row["id"],
            "snapshot_time": row["snapshot_time"],
            "symbol": row["symbol"],
            "last_price": row["last_price"],
            "regime_state": row["regime_state"],
            "breadth_ratio": row["breadth_ratio"],
            "rank_score": row["rank_score"],
            "universe_snapshot_id": row["universe_snapshot_id"],
        }
        for fname in FEATURE_NAMES:
            record[fname] = features.get(fname, np.nan)

        records.append(record)

    df = pd.DataFrame(records)
    if not df.empty:
        df["snapshot_time"] = pd.to_datetime(df["snapshot_time"], utc=True)
    logger.info("Loaded %d valid feature snapshots.", len(df))
    return df


def load_trade_labels(conn: sqlite3.Connection, start: str, end: str) -> pd.DataFrame:
    """Load trade labels from the database."""
    query = """
        SELECT
            tl.id,
            tl.feature_snapshot_id,
            tl.symbol,
            tl.realized_return,
            tl.profitable,
            tl.exit_reason,
            tl.hold_bars,
            tl.created_at
        FROM trade_labels tl
        WHERE tl.created_at >= ? AND tl.created_at < ?
        ORDER BY tl.created_at
    """
    logger.info("Loading trade labels from %s to %s ...", start, end)
    rows = conn.execute(query, (start, end)).fetchall()
    if not rows:
        logger.warning("No trade labels found in the specified range.")
        return pd.DataFrame()

    df = pd.DataFrame([dict(r) for r in rows])
    df["created_at"] = pd.to_datetime(df["created_at"], utc=True)
    logger.info("Loaded %d trade labels.", len(df))
    return df


def load_universe_membership(conn: sqlite3.Connection, start: str, end: str) -> pd.DataFrame:
    """Load universe membership info for filtering."""
    query = """
        SELECT
            um.symbol,
            um.stage,
            um.shortlisted,
            us.snapshot_time,
            us.regime_state
        FROM universe_members um
        JOIN universe_snapshots us ON um.universe_snapshot_id = us.id
        WHERE us.snapshot_time >= ? AND us.snapshot_time < ?
        ORDER BY us.snapshot_time, um.symbol
    """
    logger.info("Loading universe membership from %s to %s ...", start, end)
    rows = conn.execute(query, (start, end)).fetchall()
    if not rows:
        logger.warning("No universe membership found in the specified range.")
        return pd.DataFrame()

    df = pd.DataFrame([dict(r) for r in rows])
    df["snapshot_time"] = pd.to_datetime(df["snapshot_time"], utc=True)
    logger.info("Loaded %d universe membership records.", len(df))
    return df


def merge_features_and_labels(
    features_df: pd.DataFrame, labels_df: pd.DataFrame
) -> pd.DataFrame:
    """Merge feature snapshots with trade labels."""
    if features_df.empty or labels_df.empty:
        logger.warning("Cannot merge: features or labels is empty.")
        return pd.DataFrame()

    # Primary join: on feature_snapshot_id
    labels_with_snap = labels_df[labels_df["feature_snapshot_id"].notna()].copy()
    labels_with_snap["feature_snapshot_id"] = labels_with_snap["feature_snapshot_id"].astype(int)

    merged = features_df.merge(
        labels_with_snap[["feature_snapshot_id", "realized_return", "profitable",
                          "exit_reason", "hold_bars"]],
        left_on="snapshot_id",
        right_on="feature_snapshot_id",
        how="inner",
    )

    logger.info(
        "Merged dataset: %d rows (%d features x %d labels -> %d matched).",
        len(merged), len(features_df), len(labels_df), len(merged),
    )
    return merged


def split_by_time(
    df: pd.DataFrame, train_end: str, val_end: str
) -> tuple[pd.DataFrame, pd.DataFrame, pd.DataFrame]:
    """Split dataset into train / validation / test by time."""
    train_end_dt = pd.Timestamp(train_end, tz="UTC")
    val_end_dt = pd.Timestamp(val_end, tz="UTC")

    train = df[df["snapshot_time"] < train_end_dt].copy()
    val = df[(df["snapshot_time"] >= train_end_dt) & (df["snapshot_time"] < val_end_dt)].copy()
    test = df[df["snapshot_time"] >= val_end_dt].copy()

    logger.info(
        "Split: train=%d, val=%d, test=%d", len(train), len(val), len(test)
    )
    return train, val, test


def compute_checksum(df: pd.DataFrame) -> str:
    """Compute a SHA-256 checksum of the dataset content."""
    csv_bytes = df.to_csv(index=False).encode("utf-8")
    return hashlib.sha256(csv_bytes).hexdigest()


def save_dataset(
    df: pd.DataFrame,
    output_dir: str,
    split_name: str,
) -> str:
    """Save a dataset split as CSV."""
    os.makedirs(output_dir, exist_ok=True)
    path = os.path.join(output_dir, f"{split_name}.csv")
    df.to_csv(path, index=False)
    logger.info("Saved %s: %d rows -> %s", split_name, len(df), path)
    return path


def save_metadata(
    output_dir: str,
    total_rows: int,
    time_range: tuple[str, str],
    feature_count: int,
    splits: dict[str, int],
    checksum: str,
) -> None:
    """Save dataset metadata."""
    meta = {
        "feature_spec_version": FEATURE_SPEC_VERSION,
        "total_rows": total_rows,
        "time_range_start": time_range[0],
        "time_range_end": time_range[1],
        "feature_count": feature_count,
        "feature_names": FEATURE_NAMES,
        "splits": splits,
        "checksum": checksum,
        "created_at": datetime.now(timezone.utc).isoformat(),
    }
    path = os.path.join(output_dir, "metadata.json")
    with open(path, "w") as f:
        json.dump(meta, f, indent=2)
    logger.info("Saved metadata -> %s", path)


def main():
    parser = argparse.ArgumentParser(
        description="Build training dataset from the trading-bot SQLite database."
    )
    parser.add_argument(
        "--db-path", default=os.environ.get("DATABASE_PATH", "trading.db"),
        help="Path to the SQLite database (default: $DATABASE_PATH or trading.db)",
    )
    parser.add_argument(
        "--output-dir", required=True,
        help="Directory to write dataset files",
    )
    parser.add_argument(
        "--start", required=True,
        help="Start date for data extraction (YYYY-MM-DD)",
    )
    parser.add_argument(
        "--end", required=True,
        help="End date for data extraction (YYYY-MM-DD)",
    )
    parser.add_argument(
        "--train-end", default=None,
        help="End of training period (YYYY-MM-DD). Default: 70%% of range.",
    )
    parser.add_argument(
        "--val-end", default=None,
        help="End of validation period (YYYY-MM-DD). Default: 85%% of range.",
    )
    args = parser.parse_args()

    conn = connect_db(args.db_path)
    try:
        features_df = load_feature_snapshots(conn, args.start, args.end)
        labels_df = load_trade_labels(conn, args.start, args.end)

        if features_df.empty:
            logger.error("No feature snapshots found. Cannot build dataset.")
            sys.exit(1)

        merged = merge_features_and_labels(features_df, labels_df)
        if merged.empty:
            logger.warning(
                "No label matches. Saving feature-only dataset for unsupervised analysis."
            )
            merged = features_df.copy()
            merged["realized_return"] = np.nan
            merged["profitable"] = np.nan
            merged["exit_reason"] = np.nan
            merged["hold_bars"] = np.nan

        # Determine split boundaries
        min_time = merged["snapshot_time"].min()
        max_time = merged["snapshot_time"].max()
        total_range = max_time - min_time

        if args.train_end:
            train_end = args.train_end
        else:
            train_end = (min_time + total_range * 0.7).strftime("%Y-%m-%d")

        if args.val_end:
            val_end = args.val_end
        else:
            val_end = (min_time + total_range * 0.85).strftime("%Y-%m-%d")

        train, val, test = split_by_time(merged, train_end, val_end)

        # Save splits
        save_dataset(train, args.output_dir, "train")
        save_dataset(val, args.output_dir, "validation")
        save_dataset(test, args.output_dir, "test")

        # Full dataset for walk-forward
        full_path = save_dataset(merged, args.output_dir, "full")
        checksum = compute_checksum(merged)

        save_metadata(
            args.output_dir,
            total_rows=len(merged),
            time_range=(args.start, args.end),
            feature_count=len(FEATURE_NAMES),
            splits={
                "train": len(train),
                "validation": len(val),
                "test": len(test),
            },
            checksum=checksum,
        )

        logger.info("Dataset build complete. Output: %s", args.output_dir)
    finally:
        conn.close()


if __name__ == "__main__":
    main()
