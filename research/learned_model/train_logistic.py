#!/usr/bin/env python3
"""Produce a quarantined logistic research proposal from a Go data export.

This is offline experimentation only. It cannot read a database, run
walk-forward/portfolio evaluation, register a model, or create promotion
evidence. Use the Go Stage 07 workflow for all of those operations.
"""

from __future__ import annotations

import argparse
import hashlib
import json
from datetime import datetime, timezone
from pathlib import Path

import numpy as np
import pandas as pd
from sklearn.linear_model import LogisticRegression
from sklearn.metrics import brier_score_loss, log_loss
from sklearn.preprocessing import StandardScaler

from proposal_dataset import load_verified_dataset


def main() -> None:
    parser = argparse.ArgumentParser(description="Create a non-authoritative logistic research proposal.")
    parser.add_argument("--dataset-dir", required=True, help="immutable directory written by `cmd/marketdata -action export-model-dataset`")
    parser.add_argument("--output-path", required=True, help="new JSON path outside runtime artifact directories")
    parser.add_argument("--train-end", required=True, help="exclusive UTC RFC3339/ISO timestamp")
    parser.add_argument("--validation-end", required=True, help="exclusive UTC RFC3339/ISO timestamp")
    parser.add_argument("--version", required=True, help="proposal identifier; it grants no runtime authority")
    args = parser.parse_args()

    output = Path(args.output_path)
    if "internal/services/model_artifacts" in output.as_posix() or output.exists():
        raise SystemExit("proposal output must be a new file outside runtime model artifacts")
    frame, manifest = load_verified_dataset(args.dataset_dir)
    train_end = _timestamp(args.train_end)
    validation_end = _timestamp(args.validation_end)
    if not validation_end > train_end:
        raise SystemExit("validation-end must be after train-end")
    train = frame[frame["decision_time"] < train_end]
    validation = frame[(frame["decision_time"] >= train_end) & (frame["decision_time"] < validation_end)]
    if train.empty or validation.empty:
        raise SystemExit("chronological train and validation partitions must both contain labeled rows")

    names = manifest["feature_names"]
    train_x, train_y = _matrix(train, names)
    validation_x, validation_y = _matrix(validation, names)
    if len(set(train_y)) != 2 or len(set(validation_y)) != 2:
        raise SystemExit("train and validation partitions must each contain both outcome classes")

    scaler = StandardScaler()
    train_x = scaler.fit_transform(train_x)
    validation_x = scaler.transform(validation_x)
    model = LogisticRegression(C=1.0, penalty="l2", solver="lbfgs", max_iter=2000, random_state=42)
    model.fit(train_x, train_y)
    validation_probabilities = model.predict_proba(validation_x)[:, 1]
    train_returns = train["outcome_return"].to_numpy(dtype=np.float64)
    avg_gain = float(train_returns[train_returns > 0].mean()) if np.any(train_returns > 0) else 0.0
    avg_loss = float(abs(train_returns[train_returns < 0].mean())) if np.any(train_returns < 0) else 0.0

    artifact = {
        "artifact_class": "research_proposal",
        "version": args.version,
        "model_family": "logistic",
        "feature_spec_version": manifest["feature_spec_version"],
        "label_spec_version": manifest["label_spec_version"],
        "calibration_method": "unvalidated_identity",
        "training_window": _window(train),
        "validation_window": _window(validation),
        "test_window": "not_evaluated_by_python",
        "metrics": {
            "exploratory_validation_log_loss": round(float(log_loss(validation_y, validation_probabilities)), 6),
            "exploratory_validation_brier_score": round(float(brier_score_loss(validation_y, validation_probabilities)), 6),
            "train_rows": int(len(train)),
            "validation_rows": int(len(validation)),
        },
        "metadata": {
            "authority": "offline_research_proposal_only",
            "dataset_manifest_id": manifest["dataset_manifest_id"],
            "dataset_content_digest": manifest["content_digest"],
            "dataset_rows_sha256": manifest["rows_sha256"],
            "policy_version": manifest["policy_version"],
            "label_horizon_seconds": manifest["label_horizon_seconds"],
            "trainer": "sklearn.LogisticRegression(C=1.0,random_state=42)",
            "trained_at": datetime.now(timezone.utc).isoformat(),
            "warning": "No Python result is validation, portfolio, cost, calibration, or promotion evidence.",
        },
        "avg_gain": avg_gain,
        "avg_loss": avg_loss,
        "intercept": float(model.intercept_[0]),
        "calibration": {"a": 1.0, "b": 0.0},
        "features": [
            {"name": name, "mean": float(scaler.mean_[index]), "std": float(scaler.scale_[index]), "coefficient": float(model.coef_[0][index])}
            for index, name in enumerate(names)
        ],
    }
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(json.dumps(artifact, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"artifact_sha256": hashlib.sha256(output.read_bytes()).hexdigest(), "artifact_class": artifact["artifact_class"], "warning": artifact["metadata"]["warning"]}))


def _timestamp(value: str) -> pd.Timestamp:
    parsed = pd.Timestamp(value)
    if parsed.tzinfo is None:
        raise SystemExit("split boundaries must include an explicit UTC offset")
    return parsed.tz_convert("UTC")


def _matrix(frame: pd.DataFrame, names: list[str]) -> tuple[np.ndarray, np.ndarray]:
    values = frame[names].to_numpy(dtype=np.float64)
    if not np.isfinite(values).all():
        raise SystemExit("Go-exported dataset contains non-finite features")
    return values, frame["profitable"].astype(int).to_numpy()


def _window(frame: pd.DataFrame) -> str:
    return f"{frame['decision_time'].min().isoformat()} to {frame['decision_time'].max().isoformat()}"


if __name__ == "__main__":
    main()
