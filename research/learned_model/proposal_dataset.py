"""Verify and load an immutable Go-exported research-proposal dataset.

This module intentionally has no database driver, evaluation engine, or
portfolio simulation. PostgreSQL/Go is the sole source of the dataset and the
sole authority for walk-forward evaluation, costs, manifests, and promotion.
"""

from __future__ import annotations

import hashlib
import json
from pathlib import Path
from typing import Any


DATASET_SCHEMA = "research-proposal-dataset-v1"
ROW_SCHEMA = "research-proposal-row-v1"
ROWS_FILENAME = "dataset.jsonl"


def verify_dataset(dataset_dir: str) -> tuple[list[dict[str, Any]], dict[str, Any]]:
    """Verify the Go export without needing optional scientific packages."""
    root = Path(dataset_dir)
    manifest_path = root / "dataset.manifest.json"
    rows_path = root / ROWS_FILENAME
    if not manifest_path.is_file() or not rows_path.is_file():
        raise ValueError("dataset must contain Go-exported dataset.manifest.json and dataset.jsonl")

    manifest = json.loads(manifest_path.read_text(encoding="utf-8"))
    required = {
        "schema_version", "dataset_manifest_id", "dataset_manifest_hash",
        "feature_spec_version", "label_spec_version", "label_horizon_seconds",
        "policy_version", "start", "end", "feature_names", "rows_file",
        "rows_sha256", "row_count", "content_digest",
    }
    if set(manifest) != required or manifest["schema_version"] != DATASET_SCHEMA:
        raise ValueError("dataset manifest schema is unsupported")
    if manifest["dataset_manifest_id"] != manifest["dataset_manifest_hash"]:
        raise ValueError("dataset manifest identity is not hash-bound")
    if manifest["rows_file"] != ROWS_FILENAME or not isinstance(manifest["feature_names"], list):
        raise ValueError("dataset manifest rows file or feature schema is invalid")
    if len(manifest["dataset_manifest_id"]) != 64 or len(manifest["rows_sha256"]) != 64 or len(manifest["content_digest"]) != 64:
        raise ValueError("dataset manifest digest is invalid")

    payload = rows_path.read_bytes()
    if hashlib.sha256(payload).hexdigest() != manifest["rows_sha256"]:
        raise ValueError("dataset rows hash does not match immutable manifest")

    rows: list[dict[str, Any]] = []
    for number, line in enumerate(payload.splitlines(), start=1):
        try:
            row = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(f"invalid JSON at dataset row {number}") from exc
        _validate_row(row, manifest, number)
        values = row.pop("values")
        row.update(values)
        rows.append(row)
    if len(rows) != manifest["row_count"] or not rows:
        raise ValueError("dataset row count is missing, empty, or inconsistent")

    return rows, manifest


def load_verified_dataset(dataset_dir: str):
    """Load only a complete, hash-bound Go export; reject mutable CSV inputs."""
    import pandas as pd

    rows, manifest = verify_dataset(dataset_dir)
    frame = pd.DataFrame(rows)
    frame["decision_time"] = pd.to_datetime(frame["decision_time"], utc=True)
    return frame, manifest


def _validate_row(row: dict[str, Any], manifest: dict[str, Any], number: int) -> None:
    required = {
        "schema_version", "decision_id", "decision_time", "symbol",
        "feature_snapshot_id", "universe_snapshot_id", "policy_version",
        "feature_spec_version", "label_spec_version", "label_horizon_seconds",
        "cost_model_version", "outcome_return", "profitable", "values",
    }
    if set(row) != required or row["schema_version"] != ROW_SCHEMA:
        raise ValueError(f"dataset row {number} schema is unsupported")
    if row["policy_version"] != manifest["policy_version"] or row["feature_spec_version"] != manifest["feature_spec_version"] or row["label_spec_version"] != manifest["label_spec_version"] or row["label_horizon_seconds"] != manifest["label_horizon_seconds"]:
        raise ValueError(f"dataset row {number} does not match its manifest")
    if set(row["values"]) != set(manifest["feature_names"]):
        raise ValueError(f"dataset row {number} feature schema does not match its manifest")
    if not isinstance(row["profitable"], bool) or not isinstance(row["outcome_return"], (int, float)):
        raise ValueError(f"dataset row {number} label is invalid")
