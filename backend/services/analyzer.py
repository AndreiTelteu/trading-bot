import ccxt
import pandas as pd
from backend.models import Setting, IndicatorWeight

try:
    import pandas_ta as ta
except ImportError:
    ta = None


def get_settings():
    settings = {}
    for s in Setting.query.all():
        val = s.value
        if val.lower() == "true":
            val = True
        elif val.lower() == "false":
            val = False
        elif "." in val:
            try:
                val = float(val)
            except:
                pass
        else:
            try:
                val = int(val)
            except:
                pass
        settings[s.key] = val
    return settings


def get_weights():
    weights = {}
    for w in IndicatorWeight.query.all():
        weights[w.indicator.lower()] = w.weight
    return weights


def fetch_ohlcv(symbol, timeframe, limit=200):
    exchange = ccxt.binance({"enableRateLimit": True})
    ohlcv = exchange.fetch_ohlcv(symbol, timeframe, limit=limit)
    df = pd.DataFrame(
        ohlcv, columns=["timestamp", "open", "high", "low", "close", "volume"]
    )
    df["timestamp"] = pd.to_datetime(df["timestamp"], unit="ms")
    df.set_index("timestamp", inplace=True)
    return df


def calculate_rsi(df, period=14):
    if ta is None:
        return {
            "name": "RSI",
            "value": 50.0,
            "signal": "neutral",
            "rating": 3,
            "description": f"Period: {period} (pandas_ta not available)",
        }
    rsi = df.ta.rsi(length=period)
    current_rsi = rsi.iloc[-1]

    if current_rsi >= 70:
        signal = "sell"
        rating = 5
    elif current_rsi > 60:
        signal = "sell"
        rating = 4
    elif current_rsi >= 40 and current_rsi <= 60:
        signal = "neutral"
        rating = 3
    elif current_rsi < 40:
        signal = "buy"
        rating = 4
    else:
        signal = "buy"
        rating = 5

    return {
        "name": "RSI",
        "value": round(current_rsi, 2),
        "signal": signal,
        "rating": rating,
        "description": f"Period: {period}",
    }


def calculate_macd(df, fast=12, slow=26, signal=9):
    if ta is None:
        return {
            "name": "MACD",
            "value": "MACD: 0.00, Signal: 0.00, Hist: 0.00",
            "signal": "neutral",
            "rating": 3,
            "description": f"Fast: {fast}, Slow: {slow}, Signal: {signal} (pandas_ta not available)",
        }
    macd = df.ta.macd(fast=fast, slow=slow, signal=signal)

    macd_line = macd[f"MACD_{fast}_{slow}_{signal}"]
    signal_line = macd[f"MACDs_{fast}_{slow}_{signal}"]
    histogram = macd[f"MACDh_{fast}_{slow}_{signal}"]

    current_macd = macd_line.iloc[-1]
    current_signal = signal_line.iloc[-1]
    current_hist = histogram.iloc[-1]

    prev_macd = macd_line.iloc[-2]
    prev_signal = signal_line.iloc[-2]

    if current_macd > current_signal and prev_macd <= prev_signal:
        signal_type = "buy"
        rating = 5
    elif current_macd < current_signal and prev_macd >= prev_signal:
        signal_type = "sell"
        rating = 5
    elif current_macd > current_signal:
        signal_type = "buy"
        rating = 4
    elif current_macd < current_signal:
        signal_type = "sell"
        rating = 4
    else:
        signal_type = "neutral"
        rating = 3

    return {
        "name": "MACD",
        "value": f"MACD: {current_macd:.2f}, Signal: {current_signal:.2f}, Hist: {current_hist:.2f}",
        "signal": signal_type,
        "rating": rating,
        "description": f"Fast: {fast}, Slow: {slow}, Signal: {signal}",
    }


def calculate_bollinger(df, period=20, std=2.0):
    if ta is None:
        return {
            "name": "Bollinger",
            "value": "Upper: 0.00, Middle: 0.00, Lower: 0.00",
            "signal": "neutral",
            "rating": 3,
            "description": f"Period: {period}, Std: {std} (pandas_ta not available)",
        }
    bb = df.ta.bbands(length=period, std=std)
    current_price = df["close"].iloc[-1]

    upper = bb[f"BBU_{period}_{std}"].iloc[-1]
    middle = bb[f"BBM_{period}_{std}"].iloc[-1]
    lower = bb[f"BBL_{period}_{std}"].iloc[-1]

    if current_price > upper:
        signal = "sell"
        rating = 4
    elif current_price < lower:
        signal = "buy"
        rating = 4
    elif current_price > middle:
        signal = "buy"
        rating = 3
    elif current_price < middle:
        signal = "sell"
        rating = 3
    else:
        signal = "neutral"
        rating = 3

    return {
        "name": "Bollinger",
        "value": f"Upper: {upper:.2f}, Middle: {middle:.2f}, Lower: {lower:.2f}",
        "signal": signal,
        "rating": rating,
        "description": f"Period: {period}, Std: {std}",
    }


def calculate_momentum(df, period=10):
    if ta is None:
        return {
            "name": "Momentum",
            "value": 0.0,
            "signal": "neutral",
            "rating": 3,
            "description": f"Period: {period} (pandas_ta not available)",
        }
    mom = df.ta.mom(length=period)
    current_mom = mom.iloc[-1]
    current_price = df["close"].iloc[-1]

    if current_mom > 0:
        signal = "buy"
        rating = 4
    elif current_mom < 0:
        signal = "sell"
        rating = 4
    else:
        signal = "neutral"
        rating = 3

    return {
        "name": "Momentum",
        "value": round(current_mom, 2),
        "signal": signal,
        "rating": rating,
        "description": f"Period: {period}",
    }


def calculate_volume(df, period=20):
    volume = df["volume"]
    volume_ma = volume.rolling(window=period).mean()
    current_volume = volume.iloc[-1]
    current_ma = volume_ma.iloc[-1]

    if current_volume > current_ma * 1.5:
        signal = "buy"
        rating = 4
    elif current_volume < current_ma * 0.5:
        signal = "sell"
        rating = 3
    else:
        signal = "neutral"
        rating = 3

    return {
        "name": "Volume",
        "value": f"Current: {current_volume:.0f}, MA{period}: {current_ma:.0f}",
        "signal": signal,
        "rating": rating,
        "description": f"MA Period: {period}",
    }


def calculate_final_score(indicators, weights):
    total_weight = 0
    weighted_sum = 0

    for ind in indicators:
        name = ind["name"].lower()
        weight = weights.get(name, 1.0)

        if ind["signal"] == "buy":
            score = ind["rating"]
        elif ind["signal"] == "sell":
            score = -ind["rating"]
        else:
            score = 0

        weighted_sum += score * weight
        total_weight += weight

    final_rating = (weighted_sum / 5) * 5 + 3
    final_rating = max(1, min(5, final_rating))

    if final_rating >= 4:
        final_signal = "STRONG_BUY" if final_rating >= 4.5 else "BUY"
    elif final_rating <= 2:
        final_signal = "STRONG_SELL" if final_rating <= 1.5 else "SELL"
    else:
        final_signal = "NEUTRAL"

    return {
        "final_rating": round(final_rating, 2),
        "final_signal": final_signal,
        "weighted_score": round(weighted_sum, 2),
    }


def analyze(symbol="BTC/USDT", timeframe="15m"):
    settings = get_settings()
    weights = get_weights()

    df = fetch_ohlcv(symbol, timeframe)

    rsi_period = settings.get("rsi_period", 14)
    macd_fast = settings.get("macd_fast_period", 12)
    macd_slow = settings.get("macd_slow_period", 26)
    macd_signal = settings.get("macd_signal_period", 9)
    bb_period = settings.get("bb_period", 20)
    bb_std = settings.get("bb_std", 2.0)
    momentum_period = settings.get("momentum_period", 10)
    volume_period = settings.get("volume_ma_period", 20)

    indicators = [
        calculate_rsi(df, rsi_period),
        calculate_macd(df, macd_fast, macd_slow, macd_signal),
        calculate_bollinger(df, bb_period, bb_std),
        calculate_momentum(df, momentum_period),
        calculate_volume(df, volume_period),
    ]

    final = calculate_final_score(indicators, weights)

    return {
        "symbol": symbol,
        "timeframe": timeframe,
        "timestamp": df.index[-1].isoformat(),
        "current_price": round(df["close"].iloc[-1], 2),
        "indicators": indicators,
        "final": final,
    }
