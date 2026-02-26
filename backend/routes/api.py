from flask import Blueprint, jsonify, request
from backend.models import db, Wallet, Position, Order, ActivityLog
from datetime import datetime

api = Blueprint("api", __name__)


@api.route("/wallet", methods=["GET"])
def get_wallet():
    wallet = Wallet.query.first()
    if not wallet:
        wallet = Wallet(balance=400.0, currency="USDT")
        db.session.add(wallet)
        db.session.commit()
    return jsonify(
        {
            "id": wallet.id,
            "balance": wallet.balance,
            "currency": wallet.currency,
            "updated_at": wallet.updated_at.isoformat() if wallet.updated_at else None,
        }
    )


@api.route("/wallet", methods=["PUT"])
def update_wallet():
    data = request.json
    wallet = Wallet.query.first()
    if not wallet:
        wallet = Wallet()
        db.session.add(wallet)

    if "balance" in data:
        wallet.balance = data["balance"]
    if "currency" in data:
        wallet.currency = data["currency"]

    wallet.updated_at = datetime.utcnow()
    db.session.commit()
    return jsonify({"success": True, "balance": wallet.balance})


@api.route("/positions", methods=["GET"])
def get_positions():
    positions = Position.query.all()
    return jsonify(
        [
            {
                "id": p.id,
                "symbol": p.symbol,
                "amount": p.amount,
                "avg_price": p.avg_price,
                "entry_price": p.entry_price,
                "current_price": p.current_price,
                "pnl": p.pnl,
                "pnl_percent": p.pnl_percent,
                "status": p.status,
                "opened_at": p.opened_at.isoformat() if p.opened_at else None,
                "closed_at": p.closed_at.isoformat() if p.closed_at else None,
                "close_reason": p.close_reason,
            }
            for p in positions
        ]
    )


@api.route("/positions", methods=["POST"])
def create_position():
    data = request.json
    symbol = data.get("symbol", "").upper().replace("/", "")

    position = Position.query.filter_by(symbol=symbol).first()
    if position:
        return jsonify({"error": "Position already exists"}), 400

    position = Position(
        symbol=symbol,
        amount=data.get("amount", 0),
        avg_price=data.get("avg_price", 0),
        entry_price=data.get("entry_price"),
        current_price=data.get("current_price"),
        status="open",
    )
    db.session.add(position)
    db.session.commit()

    return jsonify({"id": position.id, "symbol": position.symbol})


@api.route("/positions/<int:position_id>/close", methods=["POST"])
def close_position(position_id):
    data = request.json or {}
    position = Position.query.get(position_id)
    if not position:
        return jsonify({"error": "Position not found"}), 404

    position.status = "closed"
    position.closed_at = datetime.utcnow()
    position.close_reason = data.get("reason", "manual")

    wallet = Wallet.query.first()
    if wallet and position.current_price:
        wallet.balance += position.amount * position.current_price

    db.session.commit()
    return jsonify({"success": True})


@api.route("/positions/<symbol>", methods=["DELETE"])
def delete_position(symbol):
    symbol = symbol.upper().replace("/", "")
    position = Position.query.filter_by(symbol=symbol).first()
    if position:
        db.session.delete(position)
        db.session.commit()
    return jsonify({"success": True})


@api.route("/orders", methods=["GET"])
def get_orders():
    limit = request.args.get("limit", 100, type=int)
    orders = Order.query.order_by(Order.executed_at.desc()).limit(limit).all()
    return jsonify(
        [
            {
                "id": o.id,
                "order_type": o.order_type,
                "symbol": o.symbol,
                "amount_crypto": o.amount_crypto,
                "amount_usdt": o.amount_usdt,
                "price": o.price,
                "fee": o.fee,
                "executed_at": o.executed_at.isoformat() if o.executed_at else None,
            }
            for o in orders
        ]
    )


@api.route("/orders", methods=["POST"])
def create_order():
    data = request.json
    order = Order(
        order_type=data.get("order_type"),
        symbol=data.get("symbol"),
        amount_crypto=data.get("amount_crypto"),
        amount_usdt=data.get("amount_usdt"),
        price=data.get("price"),
        fee=data.get("fee", 0),
    )
    db.session.add(order)

    wallet = Wallet.query.first()
    if wallet:
        if order.order_type == "buy":
            wallet.balance -= order.amount_usdt
        elif order.order_type == "sell":
            wallet.balance += order.amount_usdt

    db.session.commit()
    return jsonify({"id": order.id})


@api.route("/analysis/<symbol>", methods=["GET"])
def get_analysis(symbol):
    from backend.services.analyzer import analyze

    try:
        result = analyze(symbol)
        return jsonify(result)
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@api.route("/analysis", methods=["GET"])
def get_analysis_default():
    from backend.services.analyzer import analyze

    symbol = request.args.get("symbol", "BTC/USDT")
    timeframe = request.args.get("timeframe", "15m")
    try:
        result = analyze(symbol, timeframe)
        return jsonify(result)
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@api.route("/balance", methods=["GET"])
def get_balance():
    wallet = Wallet.query.first()
    positions = Position.query.filter_by(status="open").all()

    total_value = wallet.balance if wallet else 0
    position_values = []

    for p in positions:
        if p.current_price:
            value = p.amount * p.current_price
            total_value += value
            pnl = (
                (p.current_price - p.avg_price) / p.avg_price * 100
                if p.avg_price
                else 0
            )
            position_values.append(
                {"symbol": p.symbol, "value": value, "pnl": p.pnl, "pnl_percent": pnl}
            )

    return jsonify(
        {
            "balance": wallet.balance if wallet else 0,
            "total_value": total_value,
            "positions_value": sum(pv["value"] for pv in position_values),
            "positions": position_values,
        }
    )


@api.route("/activity-logs", methods=["GET"])
def get_activity_logs():
    limit = request.args.get("limit", 50, type=int)
    log_type = request.args.get("type")

    query = ActivityLog.query
    if log_type:
        query = query.filter_by(log_type=log_type)

    logs = query.order_by(ActivityLog.timestamp.desc()).limit(limit).all()
    return jsonify(
        [
            {
                "id": log.id,
                "log_type": log.log_type,
                "message": log.message,
                "details": log.details,
                "timestamp": log.timestamp.isoformat() if log.timestamp else None,
            }
            for log in logs
        ]
    )


@api.route("/activity-logs", methods=["POST"])
def create_activity_log():
    data = request.json
    log = ActivityLog(
        log_type=data.get("log_type", "system"),
        message=data.get("message", ""),
        details=data.get("details"),
    )
    db.session.add(log)
    db.session.commit()
    return jsonify({"id": log.id, "success": True})


@api.route("/positions/update-prices", methods=["POST"])
def update_positions_prices_endpoint():
    from backend.services.trading import update_positions_prices

    try:
        update_positions_prices()
        return jsonify({"success": True, "message": "Prices updated"})
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@api.route("/trending/analyze", methods=["POST"])
def run_trending_analysis():
    from backend.services.trending_service import analyze_trending_coins

    try:
        result = analyze_trending_coins()
        return jsonify(result)
    except Exception as e:
        return jsonify({"error": str(e)}), 500


@api.route("/trending/recent", methods=["GET"])
def get_recent_coins():
    from backend.services.trending_service import (
        get_recent_analyzed_coins,
        analyze_trending_coins,
    )

    try:
        cache = get_recent_analyzed_coins()
        if not cache.get("coins"):
            result = analyze_trending_coins()
            cache = {
                "coins": result.get("analyzed", []),
                "timestamp": result.get("timestamp"),
            }
        return jsonify(cache)
    except Exception as e:
        return jsonify({"error": str(e)}), 500
