#!/usr/bin/env bash
set -euo pipefail
umask 077

usage() {
  echo "usage: STAGE08_SOURCE_SERVICE=<pg_service> STAGE08_RESTORE_SERVICE=<pg_service> STAGE08_RECORD_SERVICE=<pg_service> $0 --output /new/path/backup.dump --principal <trusted-operator>" >&2
  echo "       STAGE08_RECORD_SERVICE and --principal may be omitted only with --test-mode" >&2
  exit 2
}

source_service="${STAGE08_SOURCE_SERVICE:-}"
restore_service="${STAGE08_RESTORE_SERVICE:-}"
record_service="${STAGE08_RECORD_SERVICE:-}"
output=""
principal=""
test_mode=0
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output="${2:-}"; shift 2 ;;
    --principal) principal="${2:-}"; shift 2 ;;
    --test-mode) test_mode=1; shift ;;
    *) usage ;;
  esac
done
[[ -n "$source_service" && -n "$restore_service" && -n "$output" ]] || usage
for service in "$source_service" "$restore_service"; do
  [[ "$service" =~ ^[A-Za-z0-9_-]+$ ]] || { echo "unsafe PostgreSQL service name" >&2; exit 4; }
done
[[ "$source_service" != "$restore_service" ]] || { echo "source and restore services must be distinct" >&2; exit 4; }
if [[ "$test_mode" == 0 ]]; then
  [[ -n "$record_service" && -n "$principal" ]] || usage
  [[ "$record_service" =~ ^[A-Za-z0-9_-]+$ ]] || { echo "unsafe PostgreSQL service name" >&2; exit 4; }
  [[ "$record_service" != "$source_service" && "$record_service" != "$restore_service" ]] || { echo "source, restore, and record services must be distinct" >&2; exit 4; }
fi
[[ "$output" = /* ]] || { echo "output must be an absolute path" >&2; exit 2; }
[[ ! -e "$output" ]] || { echo "refusing to overwrite existing backup output" >&2; exit 4; }
[[ -d "$(dirname "$output")" ]] || { echo "backup output parent does not exist" >&2; exit 4; }
for tool in pg_dump pg_restore psql sha256sum; do
  command -v "$tool" >/dev/null || { echo "$tool is required; backup verification was not performed" >&2; exit 3; }
done
go_bin="${STAGE08_GO_BIN:-/home/andrei/.local/opt/go-v1.26.1/bin/go}"
[[ -x "$go_bin" ]] || { echo "Go binary is required: $go_bin" >&2; exit 3; }

work_dir="$(mktemp -d)"
target_token="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
verification_succeeded=0
output_reserved=0
cleanup() {
  if [[ "$output_reserved" == 1 && "$verification_succeeded" != 1 ]]; then rm -f -- "$output"; fi
  rm -rf -- "$work_dir"
}
trap cleanup EXIT INT TERM
set -o noclobber
: >"$output"
output_reserved=1
set +o noclobber
chmod 600 "$output"

source_conn="service=$source_service"
target_conn="service=$restore_service"
database_identity() {
  psql "$1" -X -v ON_ERROR_STOP=1 -Atqc "SELECT current_database() || '|' || (SELECT oid::text FROM pg_catalog.pg_database WHERE datname=current_database()) || '|' || COALESCE(inet_server_addr()::text, 'local') || '|' || inet_server_port()"
}
service_user() {
  psql "$1" -X -v ON_ERROR_STOP=1 -Atqc 'SELECT current_user'
}
libpq_quote() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\'/\\\'}"
  printf "'%s'" "$value"
}
source_identity="$(database_identity "$source_conn")"
target_identity="$(database_identity "$target_conn")"
source_db="${source_identity%%|*}"
target_db="${target_identity%%|*}"
[[ -n "$source_db" && -n "$target_db" && "$source_identity" == *'|'* && "$target_identity" == *'|'* ]] || { echo "source and restore database identities are required" >&2; exit 4; }
[[ "$source_identity" != "$target_identity" ]] || { echo "restore target must be a different database identity from source '$source_db'" >&2; exit 4; }
if [[ "$test_mode" == 0 ]]; then
  record_conn="service=$record_service"
  record_identity="$(database_identity "$record_conn")"
  restore_user="$(service_user "$target_conn")"
  record_user="$(service_user "$record_conn")"
  [[ "$record_identity" == "$target_identity" ]] || { echo "record service must resolve to the restore target database identity" >&2; exit 4; }
  [[ -n "$restore_user" && -n "$record_user" && "$record_user" != "$restore_user" ]] || { echo "record service must use a distinct constrained runtime principal" >&2; exit 4; }
  record_runtime_conn="$record_conn user=$(libpq_quote "$record_user")"
fi
target_user_relations="$(psql "$target_conn" -X -Atqc "SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('pg_catalog','information_schema') AND n.nspname !~ '^pg_toast' AND c.relkind IN ('r','p','v','m','S','f')")"
[[ "$target_user_relations" == "0" ]] || { echo "restore target '$target_db' is not empty; refusing to overwrite it" >&2; exit 4; }

fingerprint() {
  DATABASE_URL="$1" MIGRATION_DATABASE_URL= GOCACHE="${STAGE08_GOCACHE:-/tmp/trading-bot-stage08-go-cache}" "$go_bin" run ./cmd/operations -action fingerprint
}
source_before_json="$(fingerprint "$source_conn")"
source_before="$(printf '%s' "$source_before_json" | sed -n 's/.*"digest":"\([0-9a-f]\{64\}\)".*/\1/p' | tail -1)"
[[ ${#source_before} == 64 ]] || { echo "source canonical fingerprint unavailable" >&2; exit 5; }

pg_dump --dbname="$source_conn" --format=custom --file="$output"
dump_checksum="$(sha256sum "$output" | awk '{print $1}')"
pg_restore --dbname="$target_conn" --exit-on-error "$output"
DATABASE_URL="$target_conn" MIGRATION_DATABASE_URL="$target_conn" GOCACHE="${STAGE08_GOCACHE:-/tmp/trading-bot-stage08-go-cache}" "$go_bin" run ./cmd/operations -action restore-verify >/dev/null

verified_target="$(psql "$target_conn" -X -v ON_ERROR_STOP=1 -Atqc "CREATE TEMP TABLE stage08_restore_identity(token text PRIMARY KEY CHECK (length(token)>=32)); INSERT INTO stage08_restore_identity(token) VALUES ('$target_token'); SELECT current_database() || '|' || token FROM stage08_restore_identity")"
[[ "$verified_target" == "$target_db|$target_token" ]] || { echo "explicit restore target identity verification failed" >&2; exit 5; }
target_json="$(fingerprint "$target_conn")"
target_fingerprint="$(printf '%s' "$target_json" | sed -n 's/.*"digest":"\([0-9a-f]\{64\}\)".*/\1/p' | tail -1)"
source_after_json="$(fingerprint "$source_conn")"
source_after="$(printf '%s' "$source_after_json" | sed -n 's/.*"digest":"\([0-9a-f]\{64\}\)".*/\1/p' | tail -1)"
[[ "$source_before" == "$source_after" ]] || { echo "source changed during dump/isolated restore verification" >&2; exit 6; }
[[ "$source_before" == "$target_fingerprint" ]] || { echo "canonical per-row database fingerprint mismatch" >&2; exit 7; }

manifest="$work_dir/verification.json"
verified_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
pg_dump_version="$(pg_dump --version | tr -d '\n')"
pg_restore_version="$(pg_restore --version | tr -d '\n')"
manifest_checksum="$(printf '%s|%s|%s|%s' "$source_before" "$dump_checksum" "$target_token" "$verified_at" | sha256sum | awk '{print $1}')"
printf '{"SchemaVersion":"stage08-backup-verification-v2","SourceBefore":"%s","SourceAfter":"%s","TargetFingerprint":"%s","DumpChecksum":"%s","ManifestChecksum":"%s","TargetIdentityToken":"%s","ToolVersions":{"pg_dump":"%s","pg_restore":"%s"},"VerifiedAt":"%s"}\n' "$source_before" "$source_after" "$target_fingerprint" "$dump_checksum" "$manifest_checksum" "$target_token" "$pg_dump_version" "$pg_restore_version" "$verified_at" >"$manifest"
if [[ "$test_mode" == 0 ]]; then
  DATABASE_URL="$record_runtime_conn" MIGRATION_DATABASE_URL= GOCACHE="${STAGE08_GOCACHE:-/tmp/trading-bot-stage08-go-cache}" "$go_bin" run ./cmd/operations -action record-backup -manifest "$manifest" -principal "$principal" >/dev/null
fi
verification_succeeded=1
printf '{"status":"verified","test_mode":%s,"source_database":"%s","target_database":"%s","target_retained":true,"evidence_destination":"%s","dump_checksum":"%s","canonical_digest":"%s"}\n' "$test_mode" "$source_db" "$target_db" "$target_db" "$dump_checksum" "$source_before"
