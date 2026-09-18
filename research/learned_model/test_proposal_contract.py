#!/usr/bin/env python3
"""Dependency-light contract checks for the offline research boundary."""

import hashlib
import json
import tempfile
from pathlib import Path


def main() -> None:
    root = Path(tempfile.mkdtemp())
    row = {"schema_version": "research-proposal-row-v1", "decision_id": "a" * 64, "decision_time": "2026-01-01T00:00:00Z", "symbol": "AAAUSDT", "feature_snapshot_id": 1, "universe_snapshot_id": 1, "policy_version": "p", "feature_spec_version": "f", "label_spec_version": "l", "label_horizon_seconds": 1, "cost_model_version": "c", "outcome_return": 0.1, "profitable": True, "values": {"x": 1.0}}
    payload = (json.dumps(row) + "\n").encode()
    (root / "dataset.jsonl").write_bytes(payload)
    manifest = {"schema_version": "research-proposal-dataset-v1", "dataset_manifest_id": "b" * 64, "dataset_manifest_hash": "b" * 64, "feature_spec_version": "f", "label_spec_version": "l", "label_horizon_seconds": 1, "policy_version": "p", "start": "2026-01-01T00:00:00Z", "end": "2026-01-02T00:00:00Z", "feature_names": ["x"], "rows_file": "dataset.jsonl", "rows_sha256": hashlib.sha256(payload).hexdigest(), "row_count": 1, "content_digest": "c" * 64}
    (root / "dataset.manifest.json").write_text(json.dumps(manifest))
    from proposal_dataset import verify_dataset
    rows, loaded = verify_dataset(str(root))
    assert len(rows) == 1 and loaded["rows_sha256"] == manifest["rows_sha256"]
    (root / "dataset.jsonl").write_bytes(payload + b" ")
    try:
        verify_dataset(str(root))
    except ValueError:
        return
    raise AssertionError("tampered dataset was accepted")


if __name__ == "__main__":
    main()
