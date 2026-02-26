from backend.models import db, Wallet, Setting, IndicatorWeight, LLMConfig


def init_db(app):
    with app.app_context():
        db.create_all()
        _init_default_data()


def _init_default_data():
    if Wallet.query.count() == 0:
        wallet = Wallet(balance=400.0, currency="USDT")
        db.session.add(wallet)

    default_settings = {
        "entry_percent": ("5.0", "trading"),
        "stop_loss_percent": ("5.0", "trading"),
        "take_profit_percent": ("30.0", "trading"),
        "rebuy_percent": ("2.5", "trading"),
        "max_positions": ("5", "trading"),
        "max_open_positions": ("5", "trading"),
        "buy_only_strong": ("true", "trading"),
        "sell_on_signal": ("true", "trading"),
        "min_confidence_to_buy": ("4.0", "trading"),
        "min_confidence_to_sell": ("3.5", "trading"),
        "allow_sell_at_loss": ("false", "trading"),
        "trailing_stop_enabled": ("false", "trading"),
        "trailing_stop_percent": ("10.0", "trading"),
        "pyramiding_enabled": ("false", "trading"),
        "max_pyramid_layers": ("3", "trading"),
        "position_scale_percent": ("50.0", "trading"),
        "auto_trade_enabled": ("false", "trading"),
        "trending_coins_to_analyze": ("5", "trading"),
        "ai_analysis_interval": ("24", "ai"),
        "ai_lookback_days": ("30", "ai"),
        "ai_min_proposals": ("1", "ai"),
        "ai_auto_apply_days": ("0", "ai"),
        "macd_fast_period": ("12", "indicators"),
        "macd_slow_period": ("26", "indicators"),
        "macd_signal_period": ("9", "indicators"),
        "rsi_period": ("14", "indicators"),
        "rsi_oversold": ("30", "indicators"),
        "rsi_overbought": ("70", "indicators"),
        "bb_period": ("20", "indicators"),
        "bb_std": ("2.0", "indicators"),
        "volume_ma_period": ("20", "indicators"),
        "momentum_period": ("10", "indicators"),
    }

    for key, (value, category) in default_settings.items():
        if not Setting.query.get(key):
            setting = Setting(key=key, value=value, category=category)
            db.session.add(setting)

    default_weights = {
        "macd": 1.0,
        "rsi": 1.0,
        "bollinger": 1.0,
        "volume": 0.5,
        "momentum": 1.0,
    }

    for indicator, weight in default_weights.items():
        if not IndicatorWeight.query.get(indicator):
            iw = IndicatorWeight(indicator=indicator, weight=weight)
            db.session.add(iw)

    if LLMConfig.query.count() == 0:
        llm = LLMConfig(
            provider="openrouter",
            base_url="https://openrouter.ai/api/v1",
            api_key="",
            model="google/gemini-2.0-flash-001",
        )
        db.session.add(llm)

    db.session.commit()
