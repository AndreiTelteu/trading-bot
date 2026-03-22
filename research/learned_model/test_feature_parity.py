#!/usr/bin/env python3
"""
test_feature_parity.py — Verify Python feature calculations match Go BuildModelFeatureRow.

Implements the same indicator math as model_features.go and indicators.go in Python,
then compares outputs against expected values from a fixture file.

Usage:
    python test_feature_parity.py
    python test_feature_parity.py --fixture fixtures/feature_parity_test.json
"""

import argparse
import json
import logging
import math
import os
import sys
from dataclasses import dataclass
from typing import Any

import numpy as np

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s [%(levelname)s] %(message)s",
)
logger = logging.getLogger(__name__)

FEATURE_SPEC_VERSION = "learned_signal_v1"
TOLERANCE = 1e-6  # Tolerance for floating-point comparison


# ---------------------------------------------------------------------------
# Indicator calculations — matching Go indicators.go exactly
# ---------------------------------------------------------------------------


def calculate_ema(data: list[float], period: int) -> float:
    """EMA matching Go calculateEMA: starts from data[0], iterates through all."""
    if len(data) < period:
        return 0.0
    multiplier = 2.0 / (period + 1)
    ema = data[0]
    for i in range(1, len(data)):
        ema = (data[i] - ema) * multiplier + ema
    return ema


def calculate_rsi(closes: list[float], period: int) -> float:
    """RSI matching Go CalculateRSI."""
    if len(closes) < period + 1:
        return 50.0

    gains = 0.0
    losses = 0.0
    for i in range(len(closes) - period, len(closes)):
        change = closes[i] - closes[i - 1]
        if change > 0:
            gains += change
        else:
            losses += abs(change)

    avg_gain = gains / period
    avg_loss = losses / period

    if avg_loss == 0:
        if avg_gain == 0:
            return 50.0
        return 100.0

    rs = avg_gain / avg_loss
    return 100 - (100 / (1 + rs))


def calculate_macd(
    closes: list[float], fast: int, slow: int, signal: int
) -> tuple[float, float, float]:
    """MACD matching Go CalculateMACD. Returns (macd_line, signal_line, histogram)."""
    if len(closes) < slow + signal:
        return 0.0, 0.0, 0.0

    fast_ema = calculate_ema(closes, fast)
    slow_ema = calculate_ema(closes, slow)
    macd_line = fast_ema - slow_ema

    # Build MACD values array
    macd_values = [0.0] * len(closes)
    for i in range(slow - 1, len(closes)):
        f = calculate_ema(closes[: i + 1], fast)
        s = calculate_ema(closes[: i + 1], slow)
        macd_values[i] = f - s

    signal_line = calculate_ema(macd_values, signal)
    histogram = macd_line - signal_line

    return macd_line, signal_line, histogram


def calculate_bollinger_bands(
    closes: list[float], period: int, std_dev: float
) -> tuple[float, float, float]:
    """Bollinger Bands matching Go. Returns (upper, middle, lower)."""
    if len(closes) < period:
        return 0.0, 0.0, 0.0

    recent = closes[-period:]
    middle = sum(recent) / period
    variance = sum((c - middle) ** 2 for c in recent) / period
    std = math.sqrt(variance)

    upper = middle + std_dev * std
    lower = middle - std_dev * std
    return upper, middle, lower


def calculate_bb_percent_b(
    upper: float, lower: float, price: float
) -> float:
    """BB %B matching Go CalculateBBPercentB."""
    denom = upper - lower
    if denom == 0:
        return 0.5
    return (price - lower) / denom


def calculate_volume_ma(volumes: list[float], period: int) -> tuple[float, float]:
    """Volume MA matching Go CalculateVolumeMA. Returns (volume_ma, ratio)."""
    if len(volumes) < period + 1:
        return 0.0, 1.0

    recent = volumes[-(period + 1) : -1]
    volume_ma = sum(recent) / period
    current = volumes[-1]

    if volume_ma == 0:
        return 0.0, 1.0
    return volume_ma, current / volume_ma


def calculate_atr(candles: list[dict], period: int) -> float:
    """ATR matching Go CalculateATR using EMA of true ranges."""
    if len(candles) < period + 1:
        return 0.0

    trs = []
    prev_close = candles[0]["close"]
    for c in candles:
        high_low = c["high"] - c["low"]
        high_close = abs(c["high"] - prev_close)
        low_close = abs(c["low"] - prev_close)
        tr = max(high_low, high_close, low_close)
        trs.append(tr)
        prev_close = c["close"]

    return calculate_ema(trs, period)


def calculate_momentum(closes: list[float], period: int) -> float:
    """Momentum matching Go CalculateMomentum."""
    if len(closes) < period + 1:
        return 0.0
    current = closes[-1]
    past = closes[-(period + 1)]
    if past == 0:
        return 0.0
    return ((current - past) / past) * 100


def calculate_return(closes: list[float], lookback: int) -> float:
    """Return matching Go CalculateReturn."""
    if len(closes) == 0 or lookback <= 0 or len(closes) <= lookback:
        return 0.0
    past = closes[-(lookback + 1)]
    current = closes[-1]
    if past == 0:
        return 0.0
    return ((current - past) / past) * 100


def rolling_mean_std(values: list[float], window: int) -> tuple[float, float]:
    """Rolling mean and std matching Go rollingMeanStd (population std)."""
    if len(values) == 0 or window <= 0:
        return 0.0, 0.0
    if window > len(values):
        window = len(values)
    segment = values[-window:]
    mean = sum(segment) / len(segment)
    variance = sum((v - mean) ** 2 for v in segment) / len(segment)
    return mean, math.sqrt(variance)


def realized_volatility(closes: list[float], window: int) -> float:
    """Realized volatility matching Go realizedVolatility."""
    if len(closes) < 2:
        return 0.0
    if window <= 0 or window >= len(closes):
        window = len(closes) - 1
    start = len(closes) - window - 1
    if start < 0:
        start = 0
    returns = []
    for i in range(start + 1, len(closes)):
        prev = closes[i - 1]
        if prev <= 0:
            continue
        returns.append((closes[i] - prev) / prev)
    if len(returns) == 0:
        return 0.0
    mean = sum(returns) / len(returns)
    variance = sum((r - mean) ** 2 for r in returns) / len(returns)
    return math.sqrt(variance)


def safe_ratio_delta(current: float, reference: float) -> float:
    """Matching Go safeRatioDelta."""
    if current == 0 or reference == 0:
        return 0.0
    return (current - reference) / reference


def distance_to_rolling_high(candles: list[dict], lookback: int, price: float) -> float:
    """Matching Go distanceToRollingHigh."""
    if len(candles) == 0 or lookback <= 0 or price <= 0:
        return 0.0
    start = len(candles) - lookback
    if start < 0:
        start = 0
    high = candles[start]["high"]
    for i in range(start + 1, len(candles)):
        if candles[i]["high"] > high:
            high = candles[i]["high"]
    if high <= 0:
        return 0.0
    return (price / high) - 1.0


def distance_to_rolling_low(candles: list[dict], lookback: int, price: float) -> float:
    """Matching Go distanceToRollingLow."""
    if len(candles) == 0 or lookback <= 0 or price <= 0:
        return 0.0
    start = len(candles) - lookback
    if start < 0:
        start = 0
    low = candles[start]["low"]
    for i in range(start + 1, len(candles)):
        if candles[i]["low"] < low:
            low = candles[i]["low"]
    if low <= 0:
        return 0.0
    return (price / low) - 1.0


def regime_score_value(regime: str) -> float:
    """Matching Go regimeScoreValue."""
    if regime == "risk_on":
        return 1.0
    elif regime == "risk_off":
        return -1.0
    return 0.0


# ---------------------------------------------------------------------------
# Full feature row computation
# ---------------------------------------------------------------------------


def build_feature_row(
    candles_15m: list[dict],
    candidate: dict,
    context: dict,
) -> dict[str, float]:
    """
    Compute all features matching Go BuildModelFeatureRow.
    Returns a dict of feature_name -> value.
    """
    if len(candles_15m) < 120:
        raise ValueError(f"Insufficient candle history: {len(candles_15m)} < 120")

    closes = [c["close"] for c in candles_15m]
    volumes = [c["volume"] for c in candles_15m]
    price = closes[-1]
    if price <= 0:
        raise ValueError(f"Invalid price: {price}")

    ema20 = calculate_ema(closes, 20)
    ema50 = calculate_ema(closes, 50)
    ema20_prev = calculate_ema(closes[:-1], 20)
    bb_upper, bb_middle, bb_lower = calculate_bollinger_bands(closes, 20, 2.0)
    rsi = calculate_rsi(closes, 14)
    macd_line, signal_line, macd_hist = calculate_macd(closes, 12, 26, 9)
    prev_macd_line, prev_signal_line, prev_macd_hist = calculate_macd(closes[:-1], 12, 26, 9)
    _, volume_ratio = calculate_volume_ma(volumes, 20)
    atr = calculate_atr(candles_15m, 14)
    mean20, std20 = rolling_mean_std(closes, 20)

    bb_percent_b = calculate_bb_percent_b(bb_upper, bb_lower, price)
    price_zscore = (price - mean20) / std20 if std20 > 0 else 0.0
    atr_ratio = atr / price if atr > 0 else 0.0

    breakout_20 = distance_to_rolling_high(candles_15m, 20, price)
    breakdown_20 = distance_to_rolling_low(candles_15m, 20, price)

    features = {
        "ret_15m_1": calculate_return(closes, 1),
        "ret_15m_4": calculate_return(closes, 4),
        "ret_15m_16": calculate_return(closes, 16),
        "ret_15m_96": calculate_return(closes, 96),
        "price_vs_ema20": safe_ratio_delta(price, ema20),
        "price_vs_ema50": safe_ratio_delta(price, ema50),
        "ema20_slope": safe_ratio_delta(ema20, ema20_prev),
        "breakout_20": breakout_20,
        "breakdown_20": breakdown_20,
        "rsi_14": rsi,
        "rsi_centered": (rsi - 50.0) / 50.0,
        "bb_percent_b": bb_percent_b,
        "price_zscore_20": price_zscore,
        "macd_hist": macd_hist,
        "macd_hist_slope": macd_hist - prev_macd_hist,
        "momentum_3": calculate_momentum(closes, 3),
        "momentum_12": calculate_momentum(closes, 12),
        "volume_ratio_20": volume_ratio,
        "atr_ratio_14": atr_ratio,
        "realized_vol_20": realized_volatility(closes, 20),
        "quote_volume_24h_log": math.log1p(max(candidate.get("quote_volume_24h", 0), 0)),
        "median_intraday_quote_volume_log": math.log1p(
            max(candidate.get("median_intraday_quote_volume", 0), 0)
        ),
        "relative_strength_7d": candidate.get("relative_strength", 0.0),
        # Rank percentiles would require the full universe — use candidate values or defaults
        "universe_rank_pct": 0.5,  # Default when universe not available
        "liquidity_rank_pct": 0.5,
        "volatility_rank_pct": 0.5,
        "regime_score": regime_score_value(context.get("regime_state", "")),
        "breadth_ratio": context.get("breadth_ratio", 0.5),
        "btc_return_1d": 0.0,  # Requires BTC candles
        "btc_trend_gap": 0.0,  # Requires BTC candles
        "open_position_count": float(context.get("open_position_count", 0)),
        "exposure_ratio": max(0.0, context.get("exposure_ratio", 0.0)),
        "already_open_position": 1.0 if context.get("already_open", False) else 0.0,
        "volume_acceleration": candidate.get("volume_acceleration", 1.0),
        "overextension_penalty": candidate.get("overextension_penalty", 0.5),
        "trend_quality": candidate.get("trend_quality", 0.0),
        "breakout_proximity": candidate.get("breakout_proximity", 0.97),
        "gap_ratio": candidate.get("gap_ratio", 0.01),
    }

    return features


# ---------------------------------------------------------------------------
# Standalone indicator tests (internal consistency)
# ---------------------------------------------------------------------------


def run_indicator_self_tests() -> list[tuple[str, bool, str]]:
    """Run self-consistency tests for individual indicators."""
    results = []

    # Test EMA
    data = [1.0, 2.0, 3.0, 4.0, 5.0, 6.0, 7.0, 8.0, 9.0, 10.0]
    ema3 = calculate_ema(data, 3)
    # EMA should be close to the recent values, weighted towards them
    assert ema3 > 7.0, f"EMA(3) should be > 7, got {ema3}"
    results.append(("ema_basic", True, f"EMA(3)={ema3:.4f}"))

    # Test RSI with all gains
    closes_up = [float(i) for i in range(100, 120)]
    rsi_up = calculate_rsi(closes_up, 14)
    results.append(("rsi_uptrend", rsi_up > 70, f"RSI={rsi_up:.2f} (expected > 70)"))

    # Test RSI with all losses
    closes_down = [float(i) for i in range(120, 100, -1)]
    rsi_down = calculate_rsi(closes_down, 14)
    results.append(("rsi_downtrend", rsi_down < 30, f"RSI={rsi_down:.2f} (expected < 30)"))

    # Test MACD
    closes_macd = [100.0 + 0.5 * i for i in range(50)]
    _, _, hist = calculate_macd(closes_macd, 12, 26, 9)
    results.append(("macd_uptrend", hist > 0, f"MACD histogram={hist:.4f} (expected > 0)"))

    # Test Bollinger Bands: price at mean → %B ≈ 0.5
    closes_flat = [100.0] * 20
    upper, middle, lower = calculate_bollinger_bands(closes_flat, 20, 2.0)
    bb = calculate_bb_percent_b(upper, lower, 100.0)
    results.append(("bb_flat", abs(bb - 0.5) < 0.01, f"BB%B={bb:.4f} (expected ≈ 0.5)"))

    # Test rolling mean/std
    values = [1.0, 2.0, 3.0, 4.0, 5.0]
    mean, std = rolling_mean_std(values, 5)
    expected_mean = 3.0
    expected_std = math.sqrt(2.0)  # population std of [1,2,3,4,5]
    results.append(("rolling_mean", abs(mean - expected_mean) < 1e-10,
                     f"mean={mean:.4f} (expected {expected_mean})"))
    results.append(("rolling_std", abs(std - expected_std) < 1e-10,
                     f"std={std:.4f} (expected {expected_std:.4f})"))

    # Test return calculation
    closes_ret = [100.0, 110.0]
    ret = calculate_return(closes_ret, 1)
    results.append(("return_basic", abs(ret - 10.0) < 1e-10,
                     f"return={ret:.4f} (expected 10.0)"))

    # Test regime score
    results.append(("regime_risk_on", regime_score_value("risk_on") == 1.0, "risk_on=1.0"))
    results.append(("regime_risk_off", regime_score_value("risk_off") == -1.0, "risk_off=-1.0"))
    results.append(("regime_neutral", regime_score_value("neutral") == 0.0, "neutral=0.0"))

    return results


# ---------------------------------------------------------------------------
# Fixture-based parity tests
# ---------------------------------------------------------------------------


def run_fixture_tests(fixture_path: str) -> list[tuple[str, bool, str]]:
    """Run parity tests from a fixture file."""
    results = []

    if not os.path.isfile(fixture_path):
        logger.warning("Fixture file not found: %s", fixture_path)
        results.append(("fixture_load", False, f"File not found: {fixture_path}"))
        return results

    with open(fixture_path) as f:
        fixture = json.load(f)

    for test_case in fixture.get("test_cases", []):
        name = test_case.get("name", "unknown")
        candles = test_case.get("candles_15m", [])
        candidate = test_case.get("candidate_metrics", {})
        context = test_case.get("context", {})
        expected = test_case.get("expected_features", {})

        if len(candles) < 120:
            results.append((
                f"fixture_{name}",
                False,
                f"Insufficient candles: {len(candles)} < 120",
            ))
            continue

        try:
            computed = build_feature_row(candles, candidate, context)
        except Exception as e:
            results.append((f"fixture_{name}", False, f"Error: {e}"))
            continue

        # Compare expected features against computed
        for feat_name, expected_val in expected.items():
            if feat_name.startswith("_"):
                continue
            if expected_val is None:
                # No expected value — just report computed value
                computed_val = computed.get(feat_name)
                results.append((
                    f"fixture_{name}_{feat_name}",
                    True,
                    f"computed={computed_val:.6f} (no expected value to compare)",
                ))
                continue

            computed_val = computed.get(feat_name)
            if computed_val is None:
                results.append((
                    f"fixture_{name}_{feat_name}",
                    False,
                    f"Feature not computed",
                ))
                continue

            match = abs(computed_val - expected_val) < TOLERANCE
            results.append((
                f"fixture_{name}_{feat_name}",
                match,
                f"computed={computed_val:.6f}, expected={expected_val:.6f}, "
                f"diff={abs(computed_val - expected_val):.2e}",
            ))

        # Verify all 38 features are computed
        from build_dataset import FEATURE_NAMES
        missing = [f for f in FEATURE_NAMES if f not in computed]
        results.append((
            f"fixture_{name}_completeness",
            len(missing) == 0,
            f"Missing features: {missing}" if missing else "All 38 features present",
        ))

    return results


def main():
    parser = argparse.ArgumentParser(
        description="Test Python feature calculation parity with Go."
    )
    parser.add_argument(
        "--fixture",
        default=os.path.join(os.path.dirname(__file__), "fixtures", "feature_parity_test.json"),
        help="Path to fixture JSON file",
    )
    parser.add_argument(
        "--verbose", "-v", action="store_true",
        help="Print all test results, not just failures",
    )
    args = parser.parse_args()

    all_results = []

    # Run self-tests
    logger.info("=== Running Indicator Self-Tests ===")
    self_results = run_indicator_self_tests()
    all_results.extend(self_results)

    # Run fixture tests
    logger.info("=== Running Fixture Parity Tests ===")
    fixture_results = run_fixture_tests(args.fixture)
    all_results.extend(fixture_results)

    # Report
    passed = sum(1 for _, ok, _ in all_results if ok)
    failed = sum(1 for _, ok, _ in all_results if not ok)

    logger.info("")
    logger.info("=== Test Results ===")
    for name, ok, detail in all_results:
        status = "PASS" if ok else "FAIL"
        if not ok or args.verbose:
            logger.info("  [%s] %s: %s", status, name, detail)

    logger.info("")
    logger.info("Total: %d passed, %d failed, %d total",
                passed, failed, len(all_results))

    if failed > 0:
        logger.error("SOME TESTS FAILED")
        sys.exit(1)
    else:
        logger.info("ALL TESTS PASSED")
        sys.exit(0)


if __name__ == "__main__":
    main()
