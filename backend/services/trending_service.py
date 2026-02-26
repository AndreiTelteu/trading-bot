import ccxt
from backend.models import db, ActivityLog, Position, Setting
from backend.services.analyzer import analyze
from backend.services.trading import execute_buy
from datetime import datetime, timezone


recent_coins_cache = {"coins": [], "timestamp": None}


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


def log_activity(log_type, message, details=None):
    log = ActivityLog(
        log_type=log_type,
        message=message,
        details=details,
    )
    db.session.add(log)
    db.session.commit()
    return log


def get_binance_trending(limit_volume=15, limit_gainers=10, limit_losers=10):
    binance = ccxt.binance({"enableRateLimit": True})
    tickers = binance.fetch_tickers()

    volume_data = []
    for symbol, data in tickers.items():
        if "/USDT" in symbol and "BTC" not in symbol:
            quote_volume = data.get("quoteVolume", 0)
            change_pct = data.get("percentage", 0) or 0
            volume_data.append(
                {
                    "symbol": symbol,
                    "price": data["last"],
                    "change_24h": change_pct,
                    "volume_24h": quote_volume,
                }
            )

    by_volume = sorted(volume_data, key=lambda x: x["volume_24h"], reverse=True)[
        :limit_volume
    ]
    gainers = sorted(volume_data, key=lambda x: x["change_24h"], reverse=True)[
        :limit_gainers
    ]
    losers = sorted(volume_data, key=lambda x: x["change_24h"])[:limit_losers]

    return {
        "source": "Binance",
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "top_volume": by_volume,
        "top_gainers": gainers,
        "top_losers": losers,
    }


def analyze_trending_coins():
    settings = get_settings()

    auto_trade_enabled = settings.get("auto_trade_enabled", False)
    # Use 'max_positions' (canonical key from PLAN / SettingsPanel)
    max_positions = settings.get("max_positions", 5)
    top_n_to_analyze = settings.get("trending_coins_to_analyze", 5)
    buy_only_strong = settings.get("buy_only_strong", True)
    min_confidence_to_buy = float(settings.get("min_confidence_to_buy", 4.0))

    log_activity(
        "system",
        "Starting trending coins analysis",
        f"Auto-trade: {auto_trade_enabled}, buy_only_strong: {buy_only_strong}, min_confidence: {min_confidence_to_buy}",
    )

    trending_data = get_binance_trending(
        limit_volume=20, limit_gainers=10, limit_losers=10
    )

    coins_to_analyze = trending_data["top_gainers"][:top_n_to_analyze]

    results = []
    trades_opened = 0

    # Count already open positions so we don't breach max_positions
    current_open_count = Position.query.filter_by(status="open").count()

    for coin in coins_to_analyze:
        symbol = coin["symbol"]
        try:
            analysis = analyze(symbol, "15m")

            signal = analysis.get("final", {}).get("final_signal", "NEUTRAL")
            rating = analysis.get("final", {}).get("final_rating", 0)
            current_price = analysis.get("current_price")

            result_entry = {
                "symbol": symbol,
                "price": current_price,
                "change_24h": coin.get("change_24h"),
                "signal": signal,
                "rating": rating,
            }

            log_activity(
                "analysis",
                f"Analyzed {symbol}",
                f"Signal: {signal}, Rating: {rating}",
            )

            # Determine whether the signal qualifies for a buy:
            # - buy_only_strong=True  → only STRONG_BUY signals allowed
            # - buy_only_strong=False → BUY or STRONG_BUY are both acceptable
            # Additionally the final rating must meet min_confidence_to_buy.
            signal_qualifies = (
                signal == "STRONG_BUY"
                if buy_only_strong
                else signal in ("BUY", "STRONG_BUY")
            )
            confidence_qualifies = rating >= min_confidence_to_buy

            if (
                auto_trade_enabled
                and signal_qualifies
                and confidence_qualifies
                and (current_open_count + trades_opened) < max_positions
            ):
                clean_symbol = symbol.replace("/USDT", "")
                existing_position = Position.query.filter_by(
                    symbol=clean_symbol
                ).first()

                if not existing_position:
                    trade_result = execute_buy(symbol)

                    if trade_result.get("success"):
                        trades_opened += 1
                        log_activity(
                            "trade",
                            f"Bought {symbol}",
                            f"Amount: {trade_result['order']['amount_crypto']:.4f} at ${current_price:.2f}",
                        )
                        result_entry["trade_executed"] = True
                    else:
                        log_activity(
                            "trade",
                            f"Failed to buy {symbol}",
                            trade_result.get("error", "Unknown error"),
                        )
                        result_entry["trade_executed"] = False
                else:
                    log_activity(
                        "trade",
                        f"Skipped {symbol} - position already exists",
                        "",
                    )

            results.append(result_entry)

        except Exception as e:
            log_activity("error", f"Error analyzing {symbol}", str(e))
            results.append({"symbol": symbol, "error": str(e)})

    log_activity(
        "system",
        "Trending analysis complete",
        f"Analyzed {len(results)} coins, opened {trades_opened} trades",
    )

    global recent_coins_cache
    recent_coins_cache = {
        "coins": results,
        "timestamp": datetime.now(timezone.utc).isoformat(),
    }

    return {
        "timestamp": datetime.now(timezone.utc).isoformat(),
        "trending": trending_data,
        "analyzed": results,
        "trades_opened": trades_opened,
    }


def get_recent_analyzed_coins():
    return recent_coins_cache
