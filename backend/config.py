import os


class Config:
    SECRET_KEY = os.environ.get("SECRET_KEY", "dev-secret-key-change-in-production")
    SQLALCHEMY_DATABASE_URI = os.environ.get("DATABASE_URL", "sqlite:///trading.db")
    SQLALCHEMY_TRACK_MODIFICATIONS = False

    BINANCE_API_KEY = os.environ.get("BINANCE_API_KEY", "")
    BINANCE_SECRET = os.environ.get("BINANCE_SECRET", "")

    DEFAULT_BALANCE = 400.0
    DEFAULT_CURRENCY = "USDT"

    WEBSOCKET_PING_INTERVAL = 25
    WEBSOCKET_PING_TIMEOUT = 60
