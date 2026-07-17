#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: TEST_DATABASE_URL=postgres://.../name_test $0 --target postgres://.../name_restore_test --output /absolute/path/backup.dump" >&2
  exit 2
}

source_url="${TEST_DATABASE_URL:-}"
target_url=""
output=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --target) target_url="${2:-}"; shift 2 ;;
    --output) output="${2:-}"; shift 2 ;;
    *) usage ;;
  esac
done

[[ -n "$source_url" && -n "$target_url" && -n "$output" ]] || usage
[[ "$source_url" != "$target_url" ]] || { echo "source and restore target must differ" >&2; exit 2; }
for tool in pg_dump pg_restore psql sha256sum; do
  command -v "$tool" >/dev/null || { echo "$tool is required; backup verification was not performed" >&2; exit 3; }
done

source_db="$(psql "$source_url" -Atqc 'select current_database()')"
target_db="$(psql "$target_url" -Atqc 'select current_database()')"
[[ "$source_db" == *_test ]] || { echo "refusing non-test source database: $source_db" >&2; exit 4; }
[[ "$target_db" == *_restore_test ]] || { echo "refusing target without _restore_test suffix: $target_db" >&2; exit 4; }

canonical_sql="SELECT jsonb_build_object(
  'wallets',(SELECT count(*) FROM wallets),
  'positions',(SELECT count(*) FROM positions),
  'orders',(SELECT count(*) FROM orders),
  'fills',(SELECT count(*) FROM fills),
  'ledger_events',(SELECT count(*) FROM ledger_events),
  'ledger_batches',(SELECT count(*) FROM ledger_batches),
  'cash',COALESCE((SELECT sum(cash_delta)::text FROM ledger_events),'0'),
  'assets',COALESCE((SELECT jsonb_object_agg(symbol,total ORDER BY symbol) FROM (SELECT symbol,sum(asset_delta)::text total FROM ledger_events WHERE symbol<>'' GROUP BY symbol) s),'{}'::jsonb)
)::text;"

source_before="$(psql "$source_url" -Atqc "$canonical_sql")"
pg_dump --format=custom --no-owner --no-privileges --file="$output" "$source_url"
dump_checksum="$(sha256sum "$output" | awk '{print $1}')"
pg_restore --clean --if-exists --no-owner --no-privileges --exit-on-error --dbname="$target_url" "$output"
go_bin="${STAGE08_GO_BIN:-/home/andrei/.local/opt/go-v1.26.1/bin/go}"
[[ -x "$go_bin" ]] || { echo "Go binary is required to rerun migrations/integrity/reconciliation: $go_bin" >&2; exit 3; }
GOCACHE="${STAGE08_GOCACHE:-/tmp/trading-bot-stage08-go-cache}" DATABASE_URL="$target_url" STAGE08_LEDGER_AUTHORITY=legacy STAGE08_SHARED_ENGINE=off STAGE08_NEW_BACKTEST=off STAGE08_POINT_IN_TIME_UNIVERSE=off STAGE08_CANDIDATE_STRATEGY=off STAGE08_DUAL_RUN=off \
  "$go_bin" run ./cmd/operations -action verify >/dev/null
target_after="$(psql "$target_url" -Atqc "$canonical_sql")"
source_after="$(psql "$source_url" -Atqc "$canonical_sql")"
[[ "$source_before" == "$source_after" ]] || { echo "source changed during backup/restore verification" >&2; exit 5; }
[[ "$source_before" == "$target_after" ]] || { echo "canonical ledger/projection digest mismatch" >&2; exit 6; }

printf '{"schema_version":"stage08-backup-verification-v1","source_database":"%s","target_database":"%s","dump_sha256":"%s","canonical":%s,"status":"verified"}\n' \
  "$source_db" "$target_db" "$dump_checksum" "$source_before"
