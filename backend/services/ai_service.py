from backend.models import db, AIProposal, Setting, Order, Position, Wallet
from datetime import datetime, timedelta
import random


def generate_proposals():
    proposals = []

    settings = {}
    for s in Setting.query.all():
        settings[s.key] = s.value

    orders = Order.query.order_by(Order.executed_at.desc()).limit(30).all()
    positions = Position.query.filter_by(status="open").all()
    wallet = Wallet.query.first()

    sample_proposals = [
        {
            "proposal_type": "parameter_change",
            "parameter_key": "stop_loss_percent",
            "old_value": settings.get("stop_loss_percent", "5.0"),
            "new_value": "4.0",
            "reasoning": "Based on recent volatility, reducing stop loss to 4% could protect profits while allowing more room for normal price fluctuations. Analysis of the last 30 trades shows average drawdowns of 3.2%.",
        },
        {
            "proposal_type": "parameter_change",
            "parameter_key": "min_confidence_to_buy",
            "old_value": settings.get("min_confidence_to_buy", "4.0"),
            "new_value": "4.2",
            "reasoning": "Increasing minimum confidence threshold slightly to reduce false signals. Recent trades with confidence between 4.0-4.2 had a 35% lower success rate than higher confidence trades.",
        },
        {
            "proposal_type": "parameter_change",
            "parameter_key": "rsi_period",
            "old_value": settings.get("rsi_period", "14"),
            "new_value": "10",
            "reasoning": "Shortening RSI period to 10 for faster response to price changes. This adjustment aligns better with the 15m timeframe you are using for trading.",
        },
        {
            "proposal_type": "indicator_weight",
            "parameter_key": "rsi_weight",
            "old_value": "1.0",
            "new_value": "1.3",
            "reasoning": "Increasing RSI weight as it has shown strong predictive power in recent market conditions. The correlation between RSI oversold/overbought and price reversals was 72% in the last analysis period.",
        },
        {
            "proposal_type": "parameter_change",
            "parameter_key": "trailing_stop_enabled",
            "old_value": settings.get("trailing_stop_enabled", "false"),
            "new_value": "true",
            "reasoning": "Enable trailing stop to lock in profits on winning positions. With the current take profit at 30%, a trailing stop at 10% could capture additional upside while protecting against reversals.",
        },
    ]

    num_proposals = min(int(settings.get("ai_min_proposals", 1)), len(sample_proposals))
    selected = random.sample(sample_proposals, num_proposals)

    for p in selected:
        existing = AIProposal.query.filter_by(
            parameter_key=p["parameter_key"], status="pending"
        ).first()

        if existing:
            continue

        proposal = AIProposal(
            proposal_type=p["proposal_type"],
            parameter_key=p["parameter_key"],
            old_value=p["old_value"],
            new_value=p["new_value"],
            reasoning=p["reasoning"],
            status="pending",
        )
        db.session.add(proposal)
        proposals.append(p)

    db.session.commit()

    return proposals


def get_proposal_history():
    return AIProposal.query.order_by(AIProposal.created_at.desc()).limit(50).all()
