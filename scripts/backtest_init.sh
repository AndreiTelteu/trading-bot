#!/usr/bin/env bash
# Initialize a manifest-backed, point-in-time research backtest dataset.
#
# This script is intentionally resumable and fail-closed. It never enables
# Stage 08 authority, invents metadata, or replaces coverage failures with an
# empty backtest. Enable/persist Stage 04 research authority first.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GO_BIN="${GO_BIN:-/home/andrei/.local/opt/go-v1.26.1/bin/go}"
START="${BACKTEST_INIT_START:-2024-07-01T00:00:00Z}"
END="${BACKTEST_INIT_END:-2026-01-01T00:00:00Z}"
DATASET_VERSION="${BACKTEST_INIT_DATASET_VERSION:-binance-spot-15m-2024h2-2025-v1}"
POLICY_VERSION="${BACKTEST_INIT_POLICY_VERSION:-research-universe-v1}"
SYMBOLS_CSV="${BACKTEST_INIT_SYMBOLS:-BTCUSDT,ETHUSDT,SOLUSDT,BNBUSDT}"
TIMEFRAME="${BACKTEST_INIT_TIMEFRAME:-15m}"
RATE_LIMIT="${BACKTEST_INIT_RATE_LIMIT:-300ms}"
WARMUP_DAYS="${BACKTEST_INIT_WARMUP_DAYS:-35}"
WARMUP_START="$(python3 - "$START" "$WARMUP_DAYS" <<'PY'
from datetime import datetime, timedelta, timezone
import sys
start = datetime.fromisoformat(sys.argv[1].replace('Z', '+00:00'))
print((start - timedelta(days=int(sys.argv[2]))).astimezone(timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ'))
PY
)"

RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)"
RUN_DIR="${BACKTEST_INIT_RUN_DIR:-$ROOT/instance/backtest-init/$RUN_ID}"
mkdir -p "$RUN_DIR"
LOG="$RUN_DIR/backtest_init.log"
exec > >(tee -a "$LOG") 2>&1

status() { printf '[%s] %s\n' "$(date -u +%FT%TZ)" "$*"; }
die() { status "ERROR: $*"; exit 1; }

on_error() {
  local code=$?
  status "FAILED (exit=$code). Resume with the same BACKTEST_INIT_RUN_DIR=$RUN_DIR after fixing the reported cause."
  exit "$code"
}
trap on_error ERR

if [[ ! -x "$GO_BIN" ]]; then
  die "Go toolchain not found at $GO_BIN; set GO_BIN explicitly"
fi

# Read a single dotenv value without sourcing credentials into this shell.
dotenv_value() {
  local key="$1"
  python3 - "$ROOT/.env" "$key" <<'PY'
from pathlib import Path
import sys
path, wanted = Path(sys.argv[1]), sys.argv[2]
if not path.exists():
    raise SystemExit(0)
for raw in path.read_text().splitlines():
    line = raw.strip()
    if not line or line.startswith('#') or '=' not in line:
        continue
    key, value = line.split('=', 1)
    if key == wanted:
        print(value.strip().strip('"').strip("'"))
        break
PY
}

activate_research_ingestion_authority() {
  local api_url="${BACKTEST_INIT_API_URL:-http://127.0.0.1:5001}"
  local audit_file="$RUN_DIR/research_ingestion_transition.json"
  status "Declaring and applying the audited research-ingestion authority"
  python3 - "$ROOT/.env" "$api_url" "$audit_file" "$RUN_ID" <<'PY'
from pathlib import Path
import http.cookiejar, json, sys, urllib.error, urllib.request

env_path, base, audit_path, run_id = map(str, sys.argv[1:])
values = {}
for raw in Path(env_path).read_text().splitlines():
    line = raw.strip()
    if not line or line.startswith('#') or '=' not in line:
        continue
    key, value = line.split('=', 1)
    values[key] = value.strip().strip('"').strip("'")
password = values.get('AUTH_PASSWORD', '')
if not password and values.get('AUTH_PASSWORD_FILE'):
    password = Path(values['AUTH_PASSWORD_FILE']).read_text().strip()
if not values.get('AUTH_USERNAME') or not password:
    raise SystemExit('AUTH_USERNAME plus AUTH_PASSWORD or AUTH_PASSWORD_FILE are required for the audited transition')
jar = http.cookiejar.CookieJar()
opener = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))
def request(path, body):
    data = json.dumps(body, separators=(',', ':')).encode()
    req = urllib.request.Request(base + path, data=data, method='POST', headers={'Content-Type': 'application/json'})
    try:
        with opener.open(req, timeout=30) as response:
            return response.status, json.loads(response.read().decode())
    except urllib.error.HTTPError as exc:
        raise SystemExit(f'{path}: HTTP {exc.code}: {exc.read().decode()[:1000]}')
status, _ = request('/api/auth/login', {'username': values['AUTH_USERNAME'], 'password': password})
if status != 200:
    raise SystemExit(f'login failed with HTTP {status}')
flags = {
    'schema_version': 'stage08-flags-v1', 'ledger_authority': 'legacy',
    'shared_engine': 'off', 'new_backtest': 'research',
    'point_in_time_universe': 'research', 'candidate_strategy': 'off',
    'dual_run': 'off',
}
_, snapshot = request('/api/operations/flags/snapshots', flags)
snapshot_id = snapshot.get('id')
if not isinstance(snapshot_id, str) or len(snapshot_id) != 64:
    raise SystemExit('snapshot response lacks canonical id')
_, transition = request('/api/operations/cutover/transitions', {
    'idempotency_key': 'backtest-init-research-ingestion-' + run_id,
    'to_stage': 'research_ingestion',
    'reason': 'approved manifest-backed point-in-time research ingestion',
    'flag_snapshot_id': snapshot_id,
    'evidence_ids': [],
})
if transition.get('to_stage') != 'research_ingestion' or transition.get('to_authority') != 'legacy':
    raise SystemExit('research transition did not preserve legacy capital authority')
Path(audit_path).write_text(json.dumps({'snapshot': snapshot, 'transition': transition}, indent=2, sort_keys=True) + '\n')
print(snapshot_id)
PY
  python3 - "$ROOT/.env" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
updates = {
    'STAGE08_FLAG_SCHEMA_VERSION': 'stage08-flags-v1',
    'STAGE08_LEDGER_AUTHORITY': 'legacy',
    'STAGE08_SHARED_ENGINE': 'off',
    'STAGE08_NEW_BACKTEST': 'research',
    'STAGE08_POINT_IN_TIME_UNIVERSE': 'research',
    'STAGE08_CANDIDATE_STRATEGY': 'off',
    'STAGE08_DUAL_RUN': 'off',
    'STAGE08_STAGE07_CONTEXT': '',
}
lines = path.read_text().splitlines() if path.exists() else []
seen = set()
out = []
for raw in lines:
    stripped = raw.strip()
    if stripped and not stripped.startswith('#') and '=' in raw:
        key = raw.split('=', 1)[0].strip()
        if key in updates:
            out.append(f'{key}={updates[key]}')
            seen.add(key)
            continue
    out.append(raw)
for key, value in updates.items():
    if key not in seen:
        out.append(f'{key}={value}')
path.write_text('\n'.join(out) + '\n')
PY
  status "Restarting app with the persisted research-only flag envelope"
  docker compose up -d --force-recreate app
  for _ in $(seq 1 30); do
    if curl -fsS "$api_url/login" >/dev/null; then
      return 0
    fi
    sleep 1
  done
  die "app did not become reachable after the research-authority transition"
}

PIT_AUTHORITY="${STAGE08_POINT_IN_TIME_UNIVERSE:-$(dotenv_value STAGE08_POINT_IN_TIME_UNIVERSE)}"
PIT_AUTHORITY="${PIT_AUTHORITY:-off}"
if [[ "$PIT_AUTHORITY" == "off" ]]; then
  if [[ "${BACKTEST_INIT_ACTIVATE_RESEARCH_AUTHORITY:-1}" != "1" ]]; then
    status "BLOCKED: Stage 04 ingestion requires persisted STAGE08_POINT_IN_TIME_UNIVERSE=research or authoritative."
    exit 78
  fi
  activate_research_ingestion_authority
  PIT_AUTHORITY="research"
fi
if [[ "$PIT_AUTHORITY" != "research" && "$PIT_AUTHORITY" != "authoritative" ]]; then
  die "unknown STAGE08_POINT_IN_TIME_UNIVERSE=$PIT_AUTHORITY"
fi

status "Run directory: $RUN_DIR"
status "Dataset=$DATASET_VERSION evaluation=[$START,$END) warmup=[$WARMUP_START,$START) timeframe=$TIMEFRAME symbols=$SYMBOLS_CSV"

METADATA="$RUN_DIR/binance_metadata.json"
CONTAINER_METADATA="/app/${METADATA#"$ROOT"/}"
status "Fetching current Binance exchange metadata and producing immutable import envelope"
python3 - "$METADATA" "$WARMUP_START" "$SYMBOLS_CSV" <<'PY'
from datetime import datetime, timezone
from pathlib import Path
import json, sys, urllib.parse, urllib.request

out, start, symbols_csv = sys.argv[1:]
symbols = [s.strip().upper() for s in symbols_csv.split(',') if s.strip()]
url = 'https://api.binance.com/api/v3/exchangeInfo?' + urllib.parse.urlencode({'symbols': json.dumps(symbols, separators=(',', ':'))})
with urllib.request.urlopen(url, timeout=30) as response:
    payload = json.load(response)
rows = {row['symbol']: row for row in payload.get('symbols', [])}
missing = sorted(set(symbols) - set(rows))
if missing:
    raise SystemExit('Binance exchangeInfo missing symbols: ' + ','.join(missing))
retrieved = datetime.now(timezone.utc).isoformat().replace('+00:00', 'Z')
start_at = start
assets = {}
exchange_symbols = []
tradability = []
constraints = []
for ticker in symbols:
    row = rows[ticker]
    if row.get('status') != 'TRADING':
        raise SystemExit(f'{ticker} is not currently TRADING')
    base, quote = row['baseAsset'], row['quoteAsset']
    asset_id = f'asset-{base.lower()}'
    base_id, quote_id = asset_id, f'asset-{quote.lower()}'
    for code, identifier in ((base, base_id), (quote, quote_id)):
        assets[identifier] = {
            'id': identifier, 'canonical_code': code, 'name': code,
            'source': 'binance-exchangeInfo',
            'provenance': json.dumps({'endpoint': 'api/v3/exchangeInfo', 'symbol': ticker}, separators=(',', ':')),
            'available_at': start_at, 'retrieved_at': retrieved,
        }
    symbol_id = f'binance-{ticker.lower()}-v1'
    filters = {item['filterType']: item for item in row.get('filters', [])}
    lot = filters.get('LOT_SIZE') or filters.get('MARKET_LOT_SIZE')
    price = filters.get('PRICE_FILTER')
    notional = filters.get('NOTIONAL') or filters.get('MIN_NOTIONAL') or {}
    if not lot or not price:
        raise SystemExit(f'{ticker} has no LOT_SIZE or PRICE_FILTER')
    min_notional = notional.get('minNotional', notional.get('notional', '0'))
    provenance = json.dumps({'endpoint': 'api/v3/exchangeInfo', 'symbol': ticker, 'filters': filters}, sort_keys=True, separators=(',', ':'))
    exchange_symbols.append({
        'id': symbol_id, 'venue_id': 'binance', 'ticker': ticker,
        'asset_id': asset_id, 'base_asset_id': base_id, 'quote_asset_id': quote_id,
        # ExchangeInfo does not provide trustworthy historical listing dates.
        # Use the requested research interval boundary and preserve provenance;
        # the subsequent manifest makes this explicit rather than pretending
        # the asset existed before the requested study window.
        'listed_at': start_at, 'version': 1, 'source': 'binance-exchangeInfo',
        'provenance': provenance, 'available_at': start_at, 'retrieved_at': retrieved,
    })
    tradability.append({
        'exchange_symbol_id': symbol_id, 'effective_from': start_at,
        'spot_tradable': True, 'status': row.get('status', 'TRADING'),
        'source': 'binance-exchangeInfo', 'provenance': provenance,
        'available_at': start_at, 'retrieved_at': retrieved,
    })
    constraints.append({
        'exchange_symbol_id': symbol_id, 'effective_from': start_at,
        'quantity_step': lot['stepSize'], 'price_tick': price['tickSize'],
        'min_quantity': lot['minQty'], 'min_notional': min_notional,
        'source': 'binance-exchangeInfo', 'provenance': provenance,
        'available_at': start_at, 'retrieved_at': retrieved,
    })
Path(out).write_text(json.dumps({
    'assets': list(assets.values()), 'symbols': exchange_symbols,
    'tradability_intervals': tradability, 'constraints': constraints,
}, indent=2, sort_keys=True) + '\n')
print(out)
PY

run_marketdata() {
  # Mutating data commands need the migration/admin pool. The long-lived app
  # intentionally does not receive that secret, so use the restricted one-shot
  # bootstrap job rather than expanding app authority.
  docker compose run --rm --no-deps bootstrap -c "go run ./cmd/marketdata $*"
}

status "Importing metadata (skipped if already present)"
ASSET_COUNT="$(docker compose exec -T postgres psql -U postgres -d trading_bot -Atqc "SELECT count(*) FROM assets;")"
if [[ "$ASSET_COUNT" -ge 1 ]]; then
  status "Metadata already present ($ASSET_COUNT assets), skipping import"
else
  run_marketdata -action import-metadata -metadata-file "$CONTAINER_METADATA" -start "$WARMUP_START" -end "$END" -dry-run=false
fi

symbol_id() { printf 'binance-%s-v1\n' "$(tr '[:upper:]' '[:lower:]' <<<"$1")"; }

# Expected closed bars for [WARMUP_START, END). Used only as a resume shortcut:
# if the series already has at least this many rows, skip the slow per-bar walk.
expected_bar_count() {
  local timeframe="$1"
  python3 - "$WARMUP_START" "$END" "$timeframe" <<'PY'
from datetime import datetime, timezone
import sys
start = datetime.fromisoformat(sys.argv[1].replace('Z', '+00:00')).astimezone(timezone.utc)
end = datetime.fromisoformat(sys.argv[2].replace('Z', '+00:00')).astimezone(timezone.utc)
tf = sys.argv[3]
seconds = {'15m': 15 * 60, '1h': 60 * 60, '1d': 24 * 60 * 60}[tf]
print(max(0, int((end - start).total_seconds() // seconds)))
PY
}

bar_count() {
  local symbol_id="$1" timeframe="$2" role="$3"
  docker compose exec -T postgres psql -U postgres -d trading_bot -Atqc \
    "SELECT count(*) FROM historical_bars WHERE dataset_version='$DATASET_VERSION' AND exchange_symbol_id='$symbol_id' AND timeframe='$timeframe' AND role='$role';"
}

ensure_bars() {
  local label="$1" symbol="$2" symbol_id="$3" timeframe="$4" role="$5"
  local have need
  need="$(expected_bar_count "$timeframe")"
  have="$(bar_count "$symbol_id" "$timeframe" "$role")"
  if [[ "${have:-0}" -ge "$need" && "$need" -gt 0 ]]; then
    status "Bars already present for $label ($have >= $need), skipping ingest"
    return 0
  fi
  status "Ingesting $label (have=${have:-0} need=$need)"
  run_marketdata -action ingest -dataset-version "$DATASET_VERSION" -symbol-id "$symbol_id" -symbol "$symbol" -timeframe "$timeframe" -role "$role" -start "$WARMUP_START" -end "$END" -source binance-public -dry-run=false -rate-limit "$RATE_LIMIT"
}

IFS=',' read -r -a SYMBOLS <<< "$SYMBOLS_CSV"
for symbol in "${SYMBOLS[@]}"; do
  symbol="$(tr -d '[:space:]' <<<"$symbol")"
  [[ -n "$symbol" ]] || continue
  ensure_bars "decision bars for $symbol ($TIMEFRAME)" "$symbol" "$(symbol_id "$symbol")" "$TIMEFRAME" decision
done

ensure_bars "independent BTCUSDT benchmark bars ($TIMEFRAME)" BTCUSDT "$(symbol_id BTCUSDT)" "$TIMEFRAME" benchmark

# Universe selection requires 1d (decision/trend) and 1h (liquidity/volume) bars
# for the benchmark and all decision symbols.
for tf in 1d 1h; do
  ensure_bars "BTCUSDT benchmark bars ($tf)" BTCUSDT "$(symbol_id BTCUSDT)" "$tf" benchmark
  for symbol in "${SYMBOLS[@]}"; do
    symbol="$(tr -d '[:space:]' <<<"$symbol")"
    [[ -n "$symbol" ]] || continue
    ensure_bars "decision bars for $symbol ($tf)" "$symbol" "$(symbol_id "$symbol")" "$tf" decision
  done
done

status "Building deterministic dataset manifest"
MANIFEST_JSON="$RUN_DIR/manifest.json"
MANIFEST_ID_FILE="$RUN_DIR/manifest.id"
KNOWLEDGE_CUTOFF_FILE="$RUN_DIR/knowledge_cutoff.txt"
SYMBOL_IDS="$(for symbol in "${SYMBOLS[@]}"; do symbol="$(tr -d '[:space:]' <<<"$symbol")"; [[ -n "$symbol" ]] && symbol_id "$symbol"; done | paste -sd, -)"

reuse_manifest=false
if [[ -f "$MANIFEST_JSON" && -f "$MANIFEST_ID_FILE" ]]; then
  MANIFEST_ID="$(tr -d '[:space:]' <"$MANIFEST_ID_FILE")"
  if [[ "$MANIFEST_ID" =~ ^[a-f0-9]{64}$ ]] && python3 - "$MANIFEST_JSON" "$MANIFEST_ID" <<'PY'
import json, sys
payload = json.load(open(sys.argv[1]))
ok = payload.get('id') == sys.argv[2] and len(payload.get('series') or []) > 0 and all(s.get('complete') for s in payload.get('series') or [])
raise SystemExit(0 if ok else 1)
PY
  then
    status "Reusing existing complete manifest $MANIFEST_ID"
    reuse_manifest=true
  fi
fi

if [[ "$reuse_manifest" != true ]]; then
  if [[ -f "$KNOWLEDGE_CUTOFF_FILE" ]]; then
    KNOWLEDGE_CUTOFF="$(tr -d '[:space:]' <"$KNOWLEDGE_CUTOFF_FILE")"
  else
    KNOWLEDGE_CUTOFF="$(date -u +%FT%TZ)"
    printf '%s\n' "$KNOWLEDGE_CUTOFF" >"$KNOWLEDGE_CUTOFF_FILE"
  fi
  # GORM logs pollute stdout inside the container; extract only the JSON line.
  run_marketdata -action build-manifest -dataset-version "$DATASET_VERSION" -symbols "$SYMBOL_IDS" -start "$WARMUP_START" -end "$END" -knowledge-cutoff "$KNOWLEDGE_CUTOFF" -source binance-public 2>&1 | tee "$MANIFEST_JSON.raw"
  grep '^{' "$MANIFEST_JSON.raw" | tail -1 > "$MANIFEST_JSON"
  rm -f "$MANIFEST_JSON.raw"
  MANIFEST_ID="$(python3 - "$MANIFEST_JSON" <<'PY'
import json, sys
payload = json.load(open(sys.argv[1]))
print(payload['id'])
PY
)"
  [[ "$MANIFEST_ID" =~ ^[a-f0-9]{64}$ ]] || die "manifest response did not contain a canonical manifest id"
  printf '%s\n' "$MANIFEST_ID" >"$MANIFEST_ID_FILE"
fi
status "Manifest: $MANIFEST_ID"

# Skip already-complete universe rebuilds; otherwise resume from checkpoint for
# the same manifest/policy/step identity.
UNIVERSE_STEP="${BACKTEST_INIT_UNIVERSE_STEP:-24h}"
# Go's time.Duration string for 24h is "24h0m0s"; normalize common short forms.
UNIVERSE_INTERVAL_LABEL="$(python3 - "$UNIVERSE_STEP" <<'PY'
import sys
from datetime import timedelta
raw=sys.argv[1].strip().lower()
units={'s':1,'m':60,'h':3600,'d':86400}
total=0
num=''
for ch in raw:
    if ch.isdigit() or ch=='.':
        num += ch
        continue
    if ch not in units or not num:
        raise SystemExit(f'unsupported step {raw!r}')
    total += float(num)*units[ch]
    num=''
if num:
    raise SystemExit(f'unsupported step {raw!r}')
td=timedelta(seconds=total)
# Match Go duration string style used by interval_label.
hours, rem = divmod(int(td.total_seconds()), 3600)
mins, secs = divmod(rem, 60)
print(f'{hours}h{mins}m{secs}s')
PY
)"
UNIVERSE_DONE=false
CP_STATUS="$(docker compose exec -T postgres psql -U postgres -d trading_bot -Atqc "SELECT status FROM universe_build_checkpoints WHERE dataset_manifest_id='$MANIFEST_ID' AND policy_version='$POLICY_VERSION' AND interval_label='$UNIVERSE_INTERVAL_LABEL';" || true)"
if [[ "$CP_STATUS" == "complete" ]]; then
  status "Universe snapshots already complete for $MANIFEST_ID"
  UNIVERSE_DONE=true
fi
if [[ "$UNIVERSE_DONE" != true ]]; then
  status "Building point-in-time universe snapshots (resumable, step=$UNIVERSE_STEP)"
  run_marketdata -action build-universe-range -manifest-id "$MANIFEST_ID" -policy-version "$POLICY_VERSION" -benchmark-symbol-id "$(symbol_id BTCUSDT)" -benchmark-asset-id asset-btc -start "$START" -end "$END" -step "$UNIVERSE_STEP" -dry-run=false
fi

status "Validating exact coverage"
run_marketdata -action coverage -manifest-id "$MANIFEST_ID" -symbols "$(IFS=,; echo "${SYMBOLS[*]}")" -start "$START" -end "$END" -timeframe "$TIMEFRAME" -role decision

# The command-line backtest reads this value from persisted settings. This is a
# local Compose operator script; use the bootstrap admin inside postgres only
# after coverage passes, and retain the manifest ID in the run directory.
status "Binding covered manifest to the local research backtest configuration"
# Universe snapshots are built at BACKTEST_INIT_UNIVERSE_STEP (default 24h). The
# runtime rebalance setting must match that cadence, otherwise dynamic_replay
# coverage fails closed with replay_internal_gap (settings default is 1h).
docker compose exec -T postgres psql -U postgres -d trading_bot -v ON_ERROR_STOP=1 <<SQL
INSERT INTO settings(key,value,category,updated_at) VALUES
  ('backtest_dataset_manifest_id','$MANIFEST_ID','backtest',CURRENT_TIMESTAMP),
  ('universe_rebalance_interval','$UNIVERSE_STEP','universe',CURRENT_TIMESTAMP)
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value, updated_at=CURRENT_TIMESTAMP;
UPDATE universe_snapshots
SET rebalance_interval='$UNIVERSE_STEP', updated_at=CURRENT_TIMESTAMP
WHERE dataset_manifest_id='$MANIFEST_ID';
SQL

status "Launching deterministic backtest"
BACKTEST_JSON="$RUN_DIR/backtest.json"
# `go run` inside the bootstrap image does not embed VCS build metadata, and
# the backtest job fails closed without an explicit code revision. Prefer an
# operator override, then the local git HEAD for this checkout.
CODE_REVISION="${BACKTEST_CODE_REVISION:-}"
if [[ -z "$CODE_REVISION" ]]; then
  CODE_REVISION="$(git -C "$ROOT" rev-parse HEAD 2>/dev/null || true)"
fi
[[ -n "$CODE_REVISION" ]] || die "BACKTEST_CODE_REVISION unavailable; set it or run from a git checkout"
status "Backtest code revision: $CODE_REVISION"
docker compose run --rm --no-deps \
  -e "BACKTEST_CODE_REVISION=$CODE_REVISION" \
  -e "GORM_LOG_LEVEL=silent" \
  bootstrap -c "go run ./cmd/backtest" 2>&1 | tee "$BACKTEST_JSON.raw"
grep '^{' "$BACKTEST_JSON.raw" | tail -1 > "$BACKTEST_JSON"
rm -f "$BACKTEST_JSON.raw"

status "SUCCESS: manifest-backed backtest initialized"
status "Manifest=$MANIFEST_ID"
status "Artifacts: $RUN_DIR"
