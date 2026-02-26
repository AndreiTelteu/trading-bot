from flask_sqlalchemy import SQLAlchemy
from datetime import datetime

db = SQLAlchemy()


class Wallet(db.Model):
    __tablename__ = "wallet"

    id = db.Column(db.Integer, primary_key=True)
    balance = db.Column(db.Float, nullable=False, default=400.0)
    currency = db.Column(db.String(20), default="USDT")
    created_at = db.Column(db.DateTime, default=datetime.utcnow)
    updated_at = db.Column(
        db.DateTime, default=datetime.utcnow, onupdate=datetime.utcnow
    )


class Position(db.Model):
    __tablename__ = "positions"

    id = db.Column(db.Integer, primary_key=True, autoincrement=True)
    symbol = db.Column(db.String(20), nullable=False, unique=True)
    amount = db.Column(db.Float, nullable=False)
    avg_price = db.Column(db.Float, nullable=False)
    entry_price = db.Column(db.Float)
    current_price = db.Column(db.Float)
    pnl = db.Column(db.Float, default=0)
    pnl_percent = db.Column(db.Float, default=0)
    status = db.Column(db.String(20), default="open")
    opened_at = db.Column(db.DateTime, default=datetime.utcnow)
    closed_at = db.Column(db.DateTime)
    close_reason = db.Column(db.String(50))


class Order(db.Model):
    __tablename__ = "orders"

    id = db.Column(db.Integer, primary_key=True, autoincrement=True)
    order_type = db.Column(db.String(10), nullable=False)
    symbol = db.Column(db.String(20), nullable=False)
    amount_crypto = db.Column(db.Float, nullable=False)
    amount_usdt = db.Column(db.Float, nullable=False)
    price = db.Column(db.Float, nullable=False)
    fee = db.Column(db.Float, default=0)
    executed_at = db.Column(db.DateTime, default=datetime.utcnow)


class Setting(db.Model):
    __tablename__ = "settings"

    key = db.Column(db.String(50), primary_key=True)
    value = db.Column(db.String(500), nullable=False)
    category = db.Column(db.String(20))
    updated_at = db.Column(
        db.DateTime, default=datetime.utcnow, onupdate=datetime.utcnow
    )


class AIProposal(db.Model):
    __tablename__ = "ai_proposals"

    id = db.Column(db.Integer, primary_key=True, autoincrement=True)
    proposal_type = db.Column(db.String(50), nullable=False)
    parameter_key = db.Column(db.String(50))
    old_value = db.Column(db.String(200))
    new_value = db.Column(db.String(200))
    reasoning = db.Column(db.Text)
    status = db.Column(db.String(20), default="pending")
    created_at = db.Column(db.DateTime, default=datetime.utcnow)
    resolved_at = db.Column(db.DateTime)
    previous_proposal_id = db.Column(db.Integer, db.ForeignKey("ai_proposals.id"))


class IndicatorWeight(db.Model):
    __tablename__ = "indicator_weights"

    indicator = db.Column(db.String(20), primary_key=True)
    weight = db.Column(db.Float, nullable=False, default=1.0)


class LLMConfig(db.Model):
    __tablename__ = "llm_config"

    id = db.Column(db.Integer, primary_key=True)
    provider = db.Column(db.String(20), default="openrouter")
    base_url = db.Column(db.String(200), default="https://openrouter.ai/api/v1")
    api_key = db.Column(db.String(200))
    model = db.Column(db.String(50), default="google/gemini-2.0-flash-001")
    updated_at = db.Column(
        db.DateTime, default=datetime.utcnow, onupdate=datetime.utcnow
    )


class ActivityLog(db.Model):
    __tablename__ = "activity_logs"

    id = db.Column(db.Integer, primary_key=True, autoincrement=True)
    log_type = db.Column(db.String(20), nullable=False)
    message = db.Column(db.String(500), nullable=False)
    details = db.Column(db.Text)
    timestamp = db.Column(db.DateTime, default=datetime.utcnow)


class TrendAnalysisHistory(db.Model):
    __tablename__ = "trend_analysis_history"

    id = db.Column(db.Integer, primary_key=True, autoincrement=True)
    symbol = db.Column(db.String(20), nullable=False, index=True)
    timeframe = db.Column(db.String(10), nullable=False, default="15m")
    current_price = db.Column(db.Float)
    change_24h = db.Column(db.Float)
    final_signal = db.Column(db.String(20))
    final_rating = db.Column(db.Float)
    indicators_json = db.Column(db.Text, nullable=False)
    analyzed_at = db.Column(db.DateTime, default=datetime.utcnow, index=True)
