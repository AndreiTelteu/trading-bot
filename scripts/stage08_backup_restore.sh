#!/usr/bin/env bash
set -euo pipefail
umask 077

usage() {
  echo "usage: STAGE08_SOURCE_SERVICE=<pg_service> STAGE08_MAINTENANCE_SERVICE=<pg_service> $0 --output /new/path/backup.dump --principal <trusted-operator>" >&2
  exit 2
}

source_service="${STAGE08_SOURCE_SERVICE:-}"
maintenance_service="${STAGE08_MAINTENANCE_SERVICE:-}"
output=""
principal=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --output) output="${2:-}"; shift 2 ;;
    --principal) principal="${2:-}"; shift 2 ;;
    *) usage ;;
  esac
done
[[ -n "$source_service" && -n "$maintenance_service" && -n "$output" && -n "$principal" ]] || usage
[[ "$output" = /* ]] || { echo "output must be an absolute path" >&2; exit 2; }
[[ ! -e "$output" ]] || { echo "refusing to overwrite existing backup output" >&2; exit 4; }
[[ -d "$(dirname "$output")" ]] || { echo "backup output parent does not exist" >&2; exit 4; }
for tool in pg_dump pg_restore psql createdb dropdb sha256sum; do
  command -v "$tool" >/dev/null || { echo "$tool is required; backup verification was not performed" >&2; exit 3; }
done
go_bin="${STAGE08_GO_BIN:-/home/andrei/.local/opt/go-v1.26.1/bin/go}"
[[ -x "$go_bin" ]] || { echo "Go binary is required: $go_bin" >&2; exit 3; }

work_dir="$(mktemp -d)"
target_token="$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')"
target_db="trading_bot_restore_${target_token}"
target_created=0
verification_succeeded=0
output_reserved=0
cleanup() {
  if [[ "$target_created" == 1 ]]; then dropdb --if-exists --maintenance-db="service=$maintenance_service" "$target_db" >/dev/null 2>&1 || true; fi
  if [[ "$output_reserved" == 1 && "$verification_succeeded" != 1 ]]; then rm -f -- "$output"; fi
  rm -rf -- "$work_dir"
}
trap cleanup EXIT INT TERM
set -o noclobber
: >"$output"
output_reserved=1
set +o noclobber
chmod 600 "$output"

source_db="$(psql "service=$source_service" -X -Atqc 'select current_database()')"
[[ "$source_db" == *_test ]] || { echo "refusing a source database whose verified identity is not *_test" >&2; exit 4; }
createdb --maintenance-db="service=$maintenance_service" --template=template0 "$target_db"
target_created=1
target_conn="service=$maintenance_service dbname=$target_db"
psql "$target_conn" -X -v ON_ERROR_STOP=1 -qc "CREATE TABLE stage08_restore_identity(token text PRIMARY KEY CHECK (length(token)>=32)); INSERT INTO stage08_restore_identity(token) VALUES ('$target_token');"
verified_token="$(psql "$target_conn" -X -Atqc 'select token from stage08_restore_identity')"
[[ "$verified_token" == "$target_token" ]] || { echo "isolated restore identity verification failed" >&2; exit 5; }

fingerprint() {
  DATABASE_URL="$1" GOCACHE="${STAGE08_GOCACHE:-/tmp/trading-bot-stage08-go-cache}" "$go_bin" run ./cmd/operations -action fingerprint
}
source_before_json="$(fingerprint "service=$source_service")"
# Extract without jq while keeping credentials out of process arguments.
source_before="$(printf '%s' "$source_before_json" | sed -n 's/.*"digest":"\([0-9a-f]\{64\}\)".*/\1/p' | tail -1)"
[[ ${#source_before} == 64 ]] || { echo "source canonical fingerprint unavailable" >&2; exit 5; }

pg_dump --dbname="service=$source_service" --format=custom --no-owner --no-privileges --file="$output"
dump_checksum="$(sha256sum "$output" | awk '{print $1}')"
pg_restore --dbname="$target_conn" --no-owner --no-privileges --exit-on-error "$output"
DATABASE_URL="$target_conn" GOCACHE="${STAGE08_GOCACHE:-/tmp/trading-bot-stage08-go-cache}" "$go_bin" run ./cmd/operations -action restore-verify >/dev/null

target_json="$(fingerprint "$target_conn")"
target_fingerprint="$(printf '%s' "$target_json" | sed -n 's/.*"digest":"\([0-9a-f]\{64\}\)".*/\1/p' | tail -1)"
source_after_json="$(fingerprint "service=$source_service")"
source_after="$(printf '%s' "$source_after_json" | sed -n 's/.*"digest":"\([0-9a-f]\{64\}\)".*/\1/p' | tail -1)"
[[ "$source_before" == "$source_after" ]] || { echo "source changed during dump/isolated restore verification" >&2; exit 6; }
[[ "$source_before" == "$target_fingerprint" ]] || { echo "canonical per-row database fingerprint mismatch" >&2; exit 7; }

manifest="$work_dir/verification.json"
verified_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
pg_dump_version="$(pg_dump --version | tr -d '\n')"
pg_restore_version="$(pg_restore --version | tr -d '\n')"
manifest_checksum="$(printf '%s|%s|%s|%s' "$source_before" "$dump_checksum" "$target_token" "$verified_at" | sha256sum | awk '{print $1}')"
printf '{"SchemaVersion":"stage08-backup-verification-v2","SourceBefore":"%s","SourceAfter":"%s","TargetFingerprint":"%s","DumpChecksum":"%s","ManifestChecksum":"%s","TargetIdentityToken":"%s","ToolVersions":{"pg_dump":"%s","pg_restore":"%s"},"VerifiedAt":"%s"}\n' "$source_before" "$source_after" "$target_fingerprint" "$dump_checksum" "$manifest_checksum" "$target_token" "$pg_dump_version" "$pg_restore_version" "$verified_at" >"$manifest"
DATABASE_URL="service=$source_service" GOCACHE="${STAGE08_GOCACHE:-/tmp/trading-bot-stage08-go-cache}" "$go_bin" run ./cmd/operations -action record-backup -manifest "$manifest" -principal "$principal" >/dev/null
verification_succeeded=1
printf '{"status":"verified","dump_checksum":"%s","canonical_digest":"%s"}\n' "$dump_checksum" "$source_before"
