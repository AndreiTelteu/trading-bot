#!/usr/bin/env python3
"""Login to Trading Bot and print a compact status snapshot.

Default credentials come from the repo conventions (admin / qwe321), but you can
override them with AUTH_USERNAME / AUTH_PASSWORD or CLI flags.

Examples:
  ./scripts/trading_status.py
  ./scripts/trading_status.py balance
  ./scripts/trading_status.py status --base-url http://localhost:5001
  ./scripts/trading_status.py transactions --transactions-endpoint activity
  ./scripts/trading_status.py backtest
  ./scripts/trading_status.py proposals
  ./scripts/trading_status.py settings
  ./scripts/trading_status.py settings --settings-key regime_gate_enabled
  ./scripts/trading_status.py settings --setting regime_gate_enabled=false
  ./scripts/trading_status.py optimize-backtest
  ./scripts/trading_status.py optimize-backtest --backtest-id 9
  ./scripts/trading_status.py approve 12
  ./scripts/trading_status.py deny 12
"""

from __future__ import annotations

import argparse
import http.cookiejar
import json
import os
import sys
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Any

DEFAULT_USERNAME = "admin"
DEFAULT_PASSWORD = "qwe321"
DEFAULT_BASE_URL = "http://localhost:5001"


@dataclass
class ApiClient:
    base_url: str
    opener: urllib.request.OpenerDirector

    def request(
        self,
        method: str,
        path: str,
        payload: Any | None = None,
        timeout: int = 15,
    ) -> Any:
        url = self.base_url + path
        headers = {"Accept": "application/json"}
        data = None
        if payload is not None:
            headers["Content-Type"] = "application/json"
            data = json.dumps(payload).encode("utf-8")

        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with self.opener.open(req, timeout=timeout) as resp:
                body = resp.read().decode("utf-8")
                return json.loads(body) if body else None
        except urllib.error.HTTPError as exc:
            body = exc.read().decode("utf-8", errors="replace")
            try:
                details = json.loads(body)
            except json.JSONDecodeError:
                details = body.strip()
            raise RuntimeError(f"HTTP {exc.code} {method} {path}: {details}") from exc
        except urllib.error.URLError as exc:
            raise RuntimeError(f"Cannot reach {url}: {exc.reason}") from exc


def build_client(base_url: str) -> ApiClient:
    base_url = base_url.rstrip("/")
    jar = http.cookiejar.CookieJar()
    opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
    return ApiClient(base_url=base_url, opener=opener)


def login(client: ApiClient, username: str, password: str) -> dict[str, Any]:
    data = client.request("POST", "/api/auth/login", {"username": username, "password": password})
    if not isinstance(data, dict) or not data.get("success"):
        raise RuntimeError(f"Unexpected login response: {data!r}")
    return data


def fetch_wallet(client: ApiClient) -> dict[str, Any]:
    data = client.request("GET", "/api/wallet")
    if not isinstance(data, dict):
        raise RuntimeError(f"Unexpected wallet response: {data!r}")
    return data


def fetch_positions(client: ApiClient) -> list[dict[str, Any]]:
    data = client.request("GET", "/api/positions")
    if not isinstance(data, list):
        raise RuntimeError(f"Unexpected positions response: {data!r}")
    return data


def fetch_orders(client: ApiClient) -> list[dict[str, Any]]:
    data = client.request("GET", "/api/orders")
    if not isinstance(data, list):
        raise RuntimeError(f"Unexpected orders response: {data!r}")
    return data


def fetch_activity_logs(client: ApiClient, limit: int) -> list[dict[str, Any]]:
    data = client.request("GET", f"/api/activity-logs?limit={limit}")
    if not isinstance(data, list):
        raise RuntimeError(f"Unexpected activity logs response: {data!r}")
    return data


def fetch_latest_backtest(client: ApiClient) -> dict[str, Any] | None:
    try:
        data = client.request("GET", "/api/backtest/latest")
    except RuntimeError as exc:
        if "HTTP 404" in str(exc):
            return None
        raise
    if not isinstance(data, dict):
        raise RuntimeError(f"Unexpected backtest response: {data!r}")
    return data


def fetch_backtest_job(client: ApiClient, job_id: int) -> dict[str, Any]:
    data = client.request("GET", f"/api/backtest/status/{job_id}")
    if not isinstance(data, dict):
        raise RuntimeError(f"Unexpected backtest response: {data!r}")
    return data


def fetch_session(client: ApiClient) -> dict[str, Any]:
    data = client.request("GET", "/api/auth/session")
    if not isinstance(data, dict):
        raise RuntimeError(f"Unexpected session response: {data!r}")
    return data


def fetch_settings(client: ApiClient) -> list[dict[str, Any]]:
    data = client.request("GET", "/api/settings")
    if not isinstance(data, list):
        raise RuntimeError(f"Unexpected settings response: {data!r}")
    return data


def update_settings(client: ApiClient, updates: list[dict[str, str]]) -> list[dict[str, Any]]:
    data = client.request("PUT", "/api/settings", updates)
    if not isinstance(data, list):
        raise RuntimeError(f"Unexpected update settings response: {data!r}")
    return data


def fetch_proposals(client: ApiClient) -> list[dict[str, Any]]:
    data = client.request("GET", "/api/ai/proposals")
    if not isinstance(data, list):
        raise RuntimeError(f"Unexpected proposals response: {data!r}")
    return data


def resolve_proposal_id(raw_id: str | None) -> int:
    if raw_id is None or not str(raw_id).strip():
        raise ValueError("proposal ID is required")
    try:
        proposal_id = int(str(raw_id).strip())
    except ValueError as exc:
        raise ValueError(f"invalid proposal ID: {raw_id!r}") from exc
    if proposal_id <= 0:
        raise ValueError(f"invalid proposal ID: {raw_id!r}")
    return proposal_id


def parse_setting_pair(raw_pair: str) -> dict[str, str]:
    if "=" not in raw_pair:
        raise ValueError(f"invalid setting update {raw_pair!r}; expected KEY=VALUE")
    key, value = raw_pair.split("=", 1)
    key = key.strip()
    if not key:
        raise ValueError(f"invalid setting update {raw_pair!r}; key is required")
    return {"key": key, "value": value.strip()}


def approve_or_deny_proposal(client: ApiClient, proposal_id: int, action: str) -> dict[str, Any]:
    if action not in {"approve", "deny"}:
        raise ValueError(f"unsupported action: {action}")
    data = client.request("POST", f"/api/ai/proposals/{proposal_id}/{action}")
    if not isinstance(data, dict):
        raise RuntimeError(f"Unexpected {action} response: {data!r}")
    return data


def optimize_backtest(client: ApiClient, job_id: int) -> dict[str, Any]:
    data = client.request("POST", "/api/ai/optimize-backtest", {"job_id": job_id}, timeout=600)
    if not isinstance(data, dict):
        raise RuntimeError(f"Unexpected optimize-backtest response: {data!r}")
    return data


def fmt_money(value: Any, currency: str) -> str:
    try:
        return f"{float(value):,.2f} {currency}"
    except (TypeError, ValueError):
        return f"{value} {currency}"


def fmt_float(value: Any, digits: int = 4) -> str:
    try:
        return f"{float(value):,.{digits}f}"
    except (TypeError, ValueError):
        return str(value)


def compact_message(value: Any) -> str:
    if value is None:
        return ""
    if isinstance(value, str):
        return value.strip().replace("\n", " ")
    return str(value).strip().replace("\n", " ")


def get_position_mark_price(position: dict[str, Any]) -> float:
    for field in ("current_price", "last_mark_price", "avg_price", "entry_price"):
        value = position.get(field)
        try:
            if value is not None:
                return float(value)
        except (TypeError, ValueError):
            continue
    return 0.0


def compute_portfolio_snapshot(wallet: dict[str, Any], positions: list[dict[str, Any]]) -> dict[str, Any]:
    active = [p for p in positions if str(p.get("status", "")).lower() == "open"]
    cash_balance = float(wallet.get("balance") or 0.0)
    open_positions_value = 0.0
    unrealized_pnl = 0.0
    for pos in active:
        amount = float(pos.get("amount") or 0.0)
        mark_price = get_position_mark_price(pos)
        avg_price = float(pos.get("avg_price") or pos.get("entry_price") or mark_price)
        open_positions_value += amount * mark_price
        unrealized_pnl += amount * (mark_price - avg_price)
    total_asset_value = cash_balance + open_positions_value
    return {
        "active_positions": active,
        "cash_balance": cash_balance,
        "open_positions_value": open_positions_value,
        "total_asset_value": total_asset_value,
        "unrealized_pnl": unrealized_pnl,
    }


def print_wallet(wallet: dict[str, Any], positions: list[dict[str, Any]]) -> None:
    currency = wallet.get("currency", "USDT")
    snapshot = compute_portfolio_snapshot(wallet, positions)
    print(f"Total asset value: {fmt_money(snapshot['total_asset_value'], currency)}")
    print(f"Cash balance: {fmt_money(snapshot['cash_balance'], currency)}")
    print(f"Open positions value: {fmt_money(snapshot['open_positions_value'], currency)}")
    print(f"Unrealized PnL: {fmt_money(snapshot['unrealized_pnl'], currency)}")
    if wallet.get("updated_at"):
        print(f"Updated: {wallet['updated_at']}")


def print_positions(positions: list[dict[str, Any]]) -> None:
    active = [p for p in positions if str(p.get("status", "")).lower() == "open"]
    print(f"Active positions: {len(active)}")
    if not active:
        return

    for pos in active:
        symbol = pos.get("symbol", "?")
        amount = fmt_float(pos.get("amount"), 6)
        avg_price = fmt_float(pos.get("avg_price"), 4)
        pnl = fmt_float(pos.get("pnl"), 2)
        pnl_pct = fmt_float(pos.get("pnl_percent"), 2)
        opened_at = pos.get("opened_at", "-")
        print(f"  - {symbol}: amount={amount}, avg={avg_price}, pnl={pnl} ({pnl_pct}%), opened={opened_at}")


def print_orders(orders: list[dict[str, Any]], limit: int = 5) -> None:
    recent = orders[:limit]
    print(f"Last {len(recent)} transactions (orders):")
    if not recent:
        print("  - none")
        return

    for order in recent:
        symbol = order.get("symbol", "?")
        order_type = order.get("order_type", "?")
        status = order.get("status", "?")
        price = fmt_float(order.get("price"), 4)
        executed_at = order.get("executed_at", "-")
        qty = order.get("amount_crypto")
        usdt = order.get("amount_usdt")
        qty_text = f"qty={fmt_float(qty, 6)}" if qty is not None else "qty=?"
        usdt_text = f", usdt={fmt_float(usdt, 2)}" if usdt is not None else ""
        print(f"  - {executed_at} | {symbol} | {order_type} | {status} | price={price} | {qty_text}{usdt_text}")


def print_activity_logs(logs: list[dict[str, Any]], limit: int = 5) -> None:
    recent = logs[:limit]
    print(f"Last {len(recent)} activity logs:")
    if not recent:
        print("  - none")
        return

    for log in recent:
        timestamp = log.get("timestamp", "-")
        log_type = log.get("log_type", "?")
        message = log.get("message", "")
        print(f"  - {timestamp} | {log_type} | {message}")


def print_backtest(backtest: dict[str, Any] | None) -> None:
    if not backtest:
        print("Backtest: no jobs yet")
        return

    summary = backtest.get("summary") or {}
    validation = summary.get("validation") or {}
    message = compact_message(backtest.get("message"))
    parts = [f"#{backtest.get('id', '?')}", str(backtest.get("status", "?"))]
    progress = backtest.get("progress")
    if progress is not None:
        parts.append(f"{fmt_float(float(progress) * 100, 1)}%")
    if validation.get("passed") is not None:
        parts.append("validation=pass" if validation.get("passed") else "validation=fail")
    if validation.get("recommended_stage"):
        parts.append(f"stage={validation.get('recommended_stage')}")
    if summary.get("rollout_state"):
        parts.append(f"rollout={summary.get('rollout_state')}")
    if message:
        parts.append(message)
    print("Backtest: " + " | ".join(parts))


def print_optimization_result(result: dict[str, Any], job_id: int) -> None:
    count = result.get("count")
    message = compact_message(result.get("message"))
    parts = [f"job #{job_id}", f"count={count if count is not None else '?'}"]
    if result.get("success") is not None:
        parts.insert(0, "success" if result.get("success") else "failed")
    if result.get("attempt_mode"):
        parts.append(f"mode={result.get('attempt_mode')}")
    if result.get("used_fallback") is not None:
        parts.append(f"fallback={str(bool(result.get('used_fallback'))).lower()}")
    if result.get("governance_recommendation"):
        parts.append(f"governance={compact_message(result.get('governance_recommendation'))}")
    if message:
        parts.append(message)
    print("Optimize backtest: " + " | ".join(parts))
    proposals = result.get("proposals") or []
    if proposals:
        print(f"AI proposals: {len(proposals)}")
        for proposal in proposals:
            print(f"  - {format_proposal_line(proposal)}")


def format_proposal_line(proposal: dict[str, Any]) -> str:
    status = proposal.get("status", "?")
    proposal_type = proposal.get("proposal_type", "?")
    parameter_key = proposal.get("parameter_key") or "?"
    old_value = proposal.get("old_value") or "?"
    new_value = proposal.get("new_value") or "?"
    reasoning = compact_message(proposal.get("reasoning"))
    return (
        f"#{proposal.get('id', '?')} | {status} | {proposal_type} | "
        f"{parameter_key}: {old_value} -> {new_value}"
        + (f" | {reasoning}" if reasoning else "")
    )


def print_settings(settings: list[dict[str, Any]], key_filter: str | None = None, limit: int = 50) -> None:
    items = settings
    if key_filter:
        normalized = key_filter.strip().lower()
        items = [s for s in items if str(s.get("key", "")).lower() == normalized]
    print(f"Settings: {len(items)}" + (f" (key={key_filter})" if key_filter else ""))
    if not items:
        print("  - none")
        return
    for setting in items[:limit]:
        key = setting.get("key", "?")
        value = setting.get("value", "")
        category = setting.get("category")
        description = compact_message(setting.get("description"))
        suffix = f" [{category}]" if category else ""
        if description:
            suffix += f" - {description}"
        print(f"  - {key} = {value}{suffix}")


def print_proposals(proposals: list[dict[str, Any]], status_filter: str | None = "pending", limit: int = 10) -> None:
    items = proposals
    if status_filter and status_filter != "all":
        items = [p for p in items if str(p.get("status", "")).lower() == status_filter.lower()]
    print(f"AI proposals: {len(items)}" + (f" (status={status_filter})" if status_filter else ""))
    if not items:
        print("  - none")
        return
    for proposal in items[:limit]:
        print(f"  - {format_proposal_line(proposal)}")



def print_compact_summary(username: str, wallet: dict[str, Any], positions: list[dict[str, Any]], backtest: dict[str, Any] | None) -> None:
    currency = wallet.get("currency", "USDT")
    snapshot = compute_portfolio_snapshot(wallet, positions)
    parts = [
        f"User: {username}",
        "Session: authenticated",
        f"Total asset value: {fmt_money(snapshot['total_asset_value'], currency)}",
        f"Cash: {fmt_money(snapshot['cash_balance'], currency)}",
        f"Active positions: {len(snapshot['active_positions'])}",
    ]
    if backtest:
        summary = backtest.get("summary") or {}
        validation = summary.get("validation") or {}
        backtest_bits = [f"#{backtest.get('id', '?')}", str(backtest.get("status", "?"))]
        progress = backtest.get("progress")
        if progress is not None:
            backtest_bits.append(f"{fmt_float(float(progress) * 100, 1)}%")
        if validation.get("passed") is not None:
            backtest_bits.append("validation=pass" if validation.get("passed") else "validation=fail")
        if validation.get("recommended_stage"):
            backtest_bits.append(f"stage={validation.get('recommended_stage')}")
        if summary.get("rollout_state"):
            backtest_bits.append(f"rollout={summary.get('rollout_state')}")
        parts.append("Backtest: " + " ".join(backtest_bits))
    else:
        parts.append("Backtest: no jobs yet")
    print(" | ".join(parts))


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Login and print Trading Bot status",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Commands:\n"
            "  status              compact full summary (default)\n"
            "  balance             wallet only\n"
            "  positions           active positions only\n"
            "  transactions        recent orders or activity logs\n"
            "  logs                activity logs only\n"
            "  backtest            latest backtest status\n"
            "  proposals           list AI proposals\n"
            "  optimize-backtest   generate proposals from the latest backtest\n"
            "  approve <id>        approve proposal by ID\n"
            "  deny <id>           deny proposal by ID\n"
            "  settings            list or update settings\n"
        ),
    )
    parser.add_argument(
        "command",
        nargs="?",
        choices=["status", "balance", "positions", "transactions", "logs", "backtest", "proposals", "optimize-backtest", "approve", "deny", "settings"],
        default=None,
    )
    parser.add_argument("target", nargs="?", help="Proposal ID for approve/deny")
    parser.add_argument("--base-url", default=os.getenv("TRADING_BOT_URL", DEFAULT_BASE_URL), help="API base URL (default: %(default)s)")
    parser.add_argument("--username", default=os.getenv("AUTH_USERNAME", DEFAULT_USERNAME), help="Login username")
    parser.add_argument("--password", default=os.getenv("AUTH_PASSWORD", DEFAULT_PASSWORD), help="Login password")
    parser.add_argument("--limit", type=int, default=5, help="How many recent items to show")
    parser.add_argument(
        "--transactions-endpoint",
        choices=["orders", "activity"],
        default="orders",
        help="What to use for 'transactions' (default: orders)",
    )
    parser.add_argument(
        "--proposal-status",
        choices=["pending", "approved", "denied", "all"],
        default="pending",
        help="Which proposals to show for the proposals command (default: pending)",
    )
    parser.add_argument(
        "--backtest-id",
        type=int,
        default=0,
        help="Backtest job ID for optimize-backtest (default: latest backtest)",
    )
    parser.add_argument(
        "--settings-key",
        default="",
        help="Show only one setting when using the settings command",
    )
    parser.add_argument(
        "--setting",
        action="append",
        default=[],
        metavar="KEY=VALUE",
        help="Update one setting when using the settings command (repeatable)",
    )
    return parser


def main() -> int:
    args = build_parser().parse_args()
    client = build_client(args.base_url)

    session = login(client, args.username, args.password)
    session_info = fetch_session(client)
    username = session.get("username") or session_info.get("username") or args.username

    wallet = fetch_wallet(client)
    positions = fetch_positions(client)
    orders = fetch_orders(client)
    logs = fetch_activity_logs(client, args.limit)
    backtest = fetch_latest_backtest(client)
    settings = fetch_settings(client)
    proposals = fetch_proposals(client)

    if args.command is None:
        print_compact_summary(username, wallet, positions, backtest)
        return 0

    if args.command == "balance":
        print(f"User: {username} | Session: authenticated")
        print_wallet(wallet, positions)
        return 0

    if args.command == "positions":
        print(f"User: {username} | Session: authenticated")
        print_positions(positions)
        return 0

    if args.command == "transactions":
        print(f"User: {username} | Session: authenticated")
        if args.transactions_endpoint == "activity":
            print_activity_logs(logs, args.limit)
        else:
            print_orders(orders, args.limit)
        return 0

    if args.command == "logs":
        print(f"User: {username} | Session: authenticated")
        print_activity_logs(logs, args.limit)
        return 0

    if args.command == "backtest":
        print(f"User: {username} | Session: authenticated")
        print_backtest(backtest)
        return 0

    if args.command == "settings":
        print(f"User: {username} | Session: authenticated")
        if args.setting:
            updates = [parse_setting_pair(item) for item in args.setting]
            updated_settings = update_settings(client, updates)
            print(f"Updated settings: {len(updates)}")
            print_settings(updated_settings, args.settings_key or None, max(len(updated_settings), 1))
            return 0
        print_settings(settings, args.settings_key or None, max(len(settings), 1))
        return 0

    if args.command == "optimize-backtest":
        print(f"User: {username} | Session: authenticated")
        optimize_job = args.backtest_id
        if optimize_job <= 0:
            if not backtest:
                raise RuntimeError("No backtest jobs found")
            optimize_job = int(backtest.get("id") or 0)
        if optimize_job <= 0:
            raise RuntimeError("No backtest job available to optimize")
        current_backtest_id = int(backtest.get("id") or 0) if backtest else 0
        selected_backtest = backtest if current_backtest_id == optimize_job else fetch_backtest_job(client, optimize_job)
        if not selected_backtest:
            raise RuntimeError(f"Backtest job #{optimize_job} not found")
        result = optimize_backtest(client, optimize_job)
        print_backtest(selected_backtest)
        print_optimization_result(result, optimize_job)
        return 0

    if args.command == "proposals":
        print(f"User: {username} | Session: authenticated")
        print_proposals(proposals, args.proposal_status, args.limit)
        return 0

    if args.command in {"approve", "deny"}:
        print(f"User: {username} | Session: authenticated")
        proposal_id = resolve_proposal_id(args.target)
        result = approve_or_deny_proposal(client, proposal_id, args.command)
        status = result.get("status", args.command)
        message = result.get("message") or result.get("result") or "done"
        print(f"Proposal #{proposal_id}: {status} | {compact_message(message)}")
        return 0

    # status
    print(f"User: {username} | Session: authenticated")
    print_wallet(wallet, positions)
    print_positions(positions)
    if args.transactions_endpoint == "activity":
        print_activity_logs(logs, args.limit)
    else:
        print_orders(orders, args.limit)
    print_backtest(backtest)

    pending_proposals = [p for p in proposals if str(p.get("status", "")).lower() == "pending"]
    print(f"AI proposals pending: {len(pending_proposals)}")

    open_positions = [p for p in positions if str(p.get("status", "")).lower() == "open"]
    print(
        "Status summary: "
        f"{len(open_positions)} active positions, "
        f"{len(orders)} total orders, "
        f"{len(logs)} recent activity logs fetched, "
        f"{len(pending_proposals)} pending AI proposals"
    )
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except Exception as exc:  # pragma: no cover - user-facing CLI
        print(f"Error: {exc}", file=sys.stderr)
        raise SystemExit(1)
