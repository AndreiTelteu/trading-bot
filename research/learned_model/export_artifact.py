#!/usr/bin/env python3
"""
export_artifact.py — Export a trained model to a Go-compatible JSON artifact.

Takes a training output (from train_logistic.py or train_gbdt.py) and produces
a JSON artifact matching the Go LogisticModelArtifact schema exactly.

Usage:
    python export_artifact.py --model-path models/logistic_v2.json \
        --output-path ../internal/services/model_artifacts/logistic_v2.json \
        --version logistic_v2
"""

import argparse
import hashlib
import json
import logging
import os
import sys
from datetime import datetime, timezone

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
logger = logging.getLogger(__name__)

FEATURE_SPEC_VERSION = "learned_signal_v1"
LABEL_SPEC_VERSION = "net_trade_return_v1"

# Required top-level keys in the Go LogisticModelArtifact
REQUIRED_KEYS = [
    "version", "model_family", "feature_spec_version", "label_spec_version",
    "calibration_method", "training_window", "validation_window", "test_window",
    "metrics", "avg_gain", "avg_loss", "intercept", "calibration", "features",
]

# Required keys per feature entry
REQUIRED_FEATURE_KEYS = ["name", "mean", "std", "coefficient"]

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


def validate_artifact(artifact: dict) -> list[str]:
    """Validate that the artifact matches the Go schema. Returns list of errors."""
    errors = []

    for key in REQUIRED_KEYS:
        if key not in artifact:
            errors.append(f"Missing required key: {key}")

    if "features" in artifact:
        features = artifact["features"]
        if not isinstance(features, list) or len(features) == 0:
            errors.append("features must be a non-empty list")
        else:
            feature_names_in_artifact = []
            for i, feat in enumerate(features):
                for fk in REQUIRED_FEATURE_KEYS:
                    if fk not in feat:
                        errors.append(f"Feature {i} missing key: {fk}")
                if "name" in feat:
                    feature_names_in_artifact.append(feat["name"])

            # Check feature names match the spec
            expected = set(FEATURE_NAMES)
            actual = set(feature_names_in_artifact)
            missing = expected - actual
            extra = actual - expected
            if missing:
                errors.append(f"Missing features: {sorted(missing)}")
            if extra:
                errors.append(f"Extra features not in spec: {sorted(extra)}")

    if "calibration" in artifact:
        cal = artifact["calibration"]
        if not isinstance(cal, dict):
            errors.append("calibration must be a dict")
        else:
            if "a" not in cal:
                errors.append("calibration missing key: a")
            if "b" not in cal:
                errors.append("calibration missing key: b")

    if "feature_spec_version" in artifact:
        if artifact["feature_spec_version"] != FEATURE_SPEC_VERSION:
            errors.append(
                f"feature_spec_version mismatch: expected {FEATURE_SPEC_VERSION}, "
                f"got {artifact['feature_spec_version']}"
            )

    # Ensure numeric types
    for key in ["avg_gain", "avg_loss", "intercept"]:
        if key in artifact and not isinstance(artifact[key], (int, float)):
            errors.append(f"{key} must be numeric, got {type(artifact[key])}")

    return errors


def normalize_artifact(artifact: dict, version: str | None = None) -> dict:
    """Normalize artifact to match Go schema exactly."""
    output = {}

    output["version"] = version or artifact.get("version", "unknown")
    output["model_family"] = artifact.get("model_family", "logistic")
    output["feature_spec_version"] = FEATURE_SPEC_VERSION
    output["label_spec_version"] = artifact.get("label_spec_version", LABEL_SPEC_VERSION)
    output["calibration_method"] = artifact.get("calibration_method", "platt")
    output["training_window"] = artifact.get("training_window", "")
    output["validation_window"] = artifact.get("validation_window", "")
    output["test_window"] = artifact.get("test_window", "")

    # Metrics — ensure all values are JSON-serializable numbers
    raw_metrics = artifact.get("metrics", {})
    output["metrics"] = {
        k: round(float(v), 6) if isinstance(v, (int, float)) else v
        for k, v in raw_metrics.items()
    }

    # Metadata (optional in Go struct but present in example)
    if "metadata" in artifact:
        output["metadata"] = artifact["metadata"]

    output["avg_gain"] = float(artifact.get("avg_gain", 0.028))
    output["avg_loss"] = float(artifact.get("avg_loss", 0.015))
    output["intercept"] = float(artifact.get("intercept", 0.0))

    # Calibration
    cal = artifact.get("calibration", {})
    output["calibration"] = {
        "a": float(cal.get("a", 1.0)),
        "b": float(cal.get("b", 0.0)),
    }

    # Features — ensure exact order matching FEATURE_NAMES
    raw_features = artifact.get("features", [])
    feature_map = {f["name"]: f for f in raw_features if "name" in f}

    ordered_features = []
    for fname in FEATURE_NAMES:
        if fname in feature_map:
            f = feature_map[fname]
            ordered_features.append({
                "name": fname,
                "mean": round(float(f.get("mean", 0.0)), 6),
                "std": round(float(f.get("std", 1.0)), 6),
                "coefficient": round(float(f.get("coefficient", 0.0)), 6),
            })
        else:
            logger.warning("Feature %s not found in model, using defaults.", fname)
            ordered_features.append({
                "name": fname,
                "mean": 0.0,
                "std": 1.0,
                "coefficient": 0.0,
            })

    output["features"] = ordered_features
    return output


def main():
    parser = argparse.ArgumentParser(
        description="Export trained model to Go-compatible JSON artifact."
    )
    parser.add_argument(
        "--model-path", required=True,
        help="Path to the trained model JSON (from train_logistic.py or train_gbdt.py)",
    )
    parser.add_argument(
        "--output-path", required=True,
        help="Path for the output Go-compatible artifact JSON",
    )
    parser.add_argument(
        "--version", default=None,
        help="Override the model version string",
    )
    args = parser.parse_args()

    if not os.path.isfile(args.model_path):
        logger.error("Model file not found: %s", args.model_path)
        sys.exit(1)

    with open(args.model_path) as f:
        raw_artifact = json.load(f)

    logger.info("Loaded model artifact: %s", args.model_path)
    logger.info("Model family: %s, version: %s",
                raw_artifact.get("model_family", "unknown"),
                raw_artifact.get("version", "unknown"))

    # Validate raw artifact
    errors = validate_artifact(raw_artifact)
    if errors:
        logger.warning("Validation warnings on input artifact:")
        for e in errors:
            logger.warning("  - %s", e)

    # Normalize to exact Go schema
    artifact = normalize_artifact(raw_artifact, version=args.version)

    # Final validation
    final_errors = validate_artifact(artifact)
    if final_errors:
        logger.error("Output artifact has validation errors:")
        for e in final_errors:
            logger.error("  - %s", e)
        sys.exit(1)

    # Write artifact
    os.makedirs(os.path.dirname(args.output_path) or ".", exist_ok=True)
    with open(args.output_path, "w") as f:
        json.dump(artifact, f, indent=2)
    logger.info("Artifact written -> %s", args.output_path)

    # Compute and log checksum
    with open(args.output_path, "rb") as f:
        checksum = hashlib.sha256(f.read()).hexdigest()
    logger.info("SHA-256 checksum: %s", checksum)

    # Summary
    logger.info("=== Artifact Summary ===")
    logger.info("  Version: %s", artifact["version"])
    logger.info("  Model family: %s", artifact["model_family"])
    logger.info("  Feature spec: %s", artifact["feature_spec_version"])
    logger.info("  Features: %d", len(artifact["features"]))
    logger.info("  Intercept: %.6f", artifact["intercept"])
    logger.info("  Calibration: a=%.6f, b=%.6f",
                artifact["calibration"]["a"], artifact["calibration"]["b"])
    logger.info("  Avg gain: %.6f, Avg loss: %.6f",
                artifact["avg_gain"], artifact["avg_loss"])
    logger.info("  Checksum: %s", checksum)


if __name__ == "__main__":
    main()
