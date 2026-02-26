import ccxt
from backend.models import db, Wallet, Position, Order, Setting
from datetime import datetime


def get_exchange():
    return ccxt.binance({"enableRateLimit": True})


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


def execute_buy(symbol, amount_usdt=None):
    settings = get_settings()
    wallet = Wallet.query.first()

    if not wallet:
        return {"error": "No wallet found"}

    symbol = symbol.upper().replace("/USDT", "").replace("/", "")
    pair = f"{symbol}/USDT"

    exchange = get_exchange()
    ticker = exchange.fetch_ticker(pair)
    current_price = ticker["last"]

    if amount_usdt is None:
        entry_percent = settings.get("entry_percent", 5.0)
        amount_usdt = wallet.balance * (entry_percent / 100)

    if amount_usdt > wallet.balance:
        return {"error": "Insufficient balance"}

    crypto_amount = amount_usdt / current_price

    position = Position.query.filter_by(symbol=symbol).first()
    if position:
        old_amount = position.amount
        old_avg = position.avg_price
        new_avg = ((old_amount * old_avg) + (crypto_amount * current_price)) / (
            old_amount + crypto_amount
        )
        position.amount += crypto_amount
        position.avg_price = new_avg
    else:
        position = Position(
            symbol=symbol,
            amount=crypto_amount,
            avg_price=current_price,
            entry_price=current_price,
            current_price=current_price,
            status="open",
        )
        db.session.add(position)

    order = Order(
        order_type="buy",
        symbol=symbol,
        amount_crypto=crypto_amount,
        amount_usdt=amount_usdt,
        price=current_price,
    )
    db.session.add(order)

    wallet.balance -= amount_usdt
    db.session.commit()

    return {
        "success": True,
        "order": {
            "type": "buy",
            "symbol": symbol,
            "amount_crypto": crypto_amount,
            "amount_usdt": amount_usdt,
            "price": current_price,
        },
    }


def execute_sell(symbol, amount_crypto=None, close_position=False):
    settings = get_settings()
    wallet = Wallet.query.first()

    symbol = symbol.upper().replace("/USDT", "").replace("/", "")
    pair = f"{symbol}/USDT"

    exchange = get_exchange()
    ticker = exchange.fetch_ticker(pair)
    current_price = ticker["last"]

    position = Position.query.filter_by(symbol=symbol).first()
    if not position:
        return {"error": "No position found"}

    if amount_crypto is None or close_position:
        amount_crypto = position.amount

    if amount_crypto > position.amount:
        return {"error": "Insufficient crypto"}

    usdt_received = amount_crypto * current_price

    allow_sell_at_loss = settings.get("allow_sell_at_loss", False)
    pnl_percent = (current_price - position.avg_price) / position.avg_price * 100

    if not allow_sell_at_loss and pnl_percent < 0:
        return {"error": "Selling at loss is disabled"}

    order = Order(
        order_type="sell",
        symbol=symbol,
        amount_crypto=amount_crypto,
        amount_usdt=usdt_received,
        price=current_price,
    )
    db.session.add(order)

    wallet.balance += usdt_received
    position.amount -= amount_crypto

    if position.amount <= 0.00000001 or close_position:
        position.status = "closed"
        position.closed_at = datetime.utcnow()
        position.close_reason = "manual"

    position.current_price = current_price
    position.pnl = usdt_received - (position.amount * position.avg_price)
    position.pnl_percent = pnl_percent

    db.session.commit()

    return {
        "success": True,
        "order": {
            "type": "sell",
            "symbol": symbol,
            "amount_crypto": amount_crypto,
            "amount_usdt": usdt_received,
            "price": current_price,
        },
    }


def update_positions_prices():
    exchange = get_exchange()
    positions = Position.query.filter_by(status="open").all()

    for position in positions:
        try:
            pair = f"{position.symbol}/USDT"
            ticker = exchange.fetch_ticker(pair)
            current_price = ticker["last"]
            position.current_price = current_price
            position.pnl = (current_price - position.avg_price) * position.amount
            position.pnl_percent = (
                (current_price - position.avg_price) / position.avg_price * 100
                if position.avg_price
                else 0
            )
        except Exception as e:
            print(f"Error updating price for {position.symbol}: {e}")

    db.session.commit()
