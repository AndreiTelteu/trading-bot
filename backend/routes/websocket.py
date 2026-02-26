from flask_socketio import emit, join_room, leave_room
from flask import request


def init_websocket(socketio):

    @socketio.on("connect")
    def handle_connect():
        print(f"Client connected: {request.sid}")
        emit("connected", {"sid": request.sid})

    @socketio.on("disconnect")
    def handle_disconnect():
        print(f"Client disconnected: {request.sid}")

    @socketio.on("join")
    def handle_join(data):
        room = data.get("room", "main")
        join_room(room)
        emit("joined", {"room": room}, room=request.sid)

    @socketio.on("leave")
    def handle_leave(data):
        room = data.get("room", "main")
        leave_room(room)
        emit("left", {"room": room}, room=request.sid)

    @socketio.on("subscribe_prices")
    def handle_subscribe_prices(data):
        symbols = data.get("symbols", [])
        for symbol in symbols:
            join_room(f"price_{symbol}")

    @socketio.on("request_update")
    def handle_request_update(data):
        from backend.models import Wallet, Position

        wallet = Wallet.query.first()
        positions = Position.query.filter_by(status="open").all()

        total_value = wallet.balance if wallet else 0
        for p in positions:
            if p.current_price:
                total_value += p.amount * p.current_price

        emit(
            "balance_update",
            {"balance": wallet.balance if wallet else 0, "total_value": total_value},
            room=request.sid,
        )

    return socketio


def emit_position_update(socketio, symbol, price, pnl):
    socketio.emit(
        "position_update",
        {"symbol": symbol, "price": price, "pnl": pnl},
        room=f"price_{symbol}",
    )


def emit_order_executed(socketio, order_data):
    socketio.emit("order_executed", order_data)


def emit_balance_update(socketio, new_balance):
    socketio.emit("balance_update", {"new_balance": new_balance})


def emit_ai_proposal(socketio, proposal_data):
    socketio.emit("ai_proposal", proposal_data)
