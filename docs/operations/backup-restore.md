# Backup, isolated restore, verification, and disaster recovery

Prerequisites are `pg_dump`, `pg_restore`, `psql`, `sha256sum`, the pinned Go binary, and libpq service entries whose passwords are supplied by a mode-0600 `PGPASSFILE` (or another libpq credential provider). The source may be the production database. The restore service must name an explicitly provisioned, empty database with a different database identity from the source; the script never creates, drops, cleans, or overwrites a database. Do not put database URLs or passwords on the command line.

The output path must be absolute, its parent must exist, and the file must not exist. For a disposable exercise, provision a dedicated empty test database first and point the restore service at it:

```bash
export PGSERVICEFILE=/absolute/path/to/pg_service.conf
export PGPASSFILE=/absolute/path/to/pgpass
export STAGE08_SOURCE_SERVICE=trading_bot_test_source
export STAGE08_RESTORE_SERVICE=trading_bot_restore_test
./scripts/stage08_backup_restore.sh \
  --output /tmp/trading_bot_stage08.dump \
  --test-mode
```

Expected output is bounded JSON containing `status:"verified"`, the dump checksum, and the canonical database digest. Exit 3 means a required tool was unavailable and no verification occurred. Other nonzero exits identify refusal, source mutation, restore failure, or a canonical mismatch.

The script refuses overwrite, uses restrictive permissions, never runs `pg_restore --clean`, and sets both `DATABASE_URL` and `MIGRATION_DATABASE_URL` to the explicit restore target before target migration/reconciliation. Source access is limited to `pg_dump` and the open-only fingerprint command; no migration or evidence command is ever pointed at the source. The v4 fingerprint compares every public-table row plus schemas, owners, raw ACLs, role membership, default privileges, RLS flags/policies, columns, constraints, indexes, triggers, functions, views, and sequence definitions/state. The dump preserves ownership/ACL metadata (the target cluster must contain the referenced roles).

`--test-mode` verifies the restored database without persisting `BackupVerification`, so it does not require a record service. Outside test mode, `STAGE08_RECORD_SERVICE` and `--principal` are required. The record service must be a distinct libpq service entry for the same target database identity as `STAGE08_RESTORE_SERVICE`, but authenticated as the production-shaped constrained runtime login. The script rejects unsafe or reused service names, a different record target, and a record login that is the restore login. It uses the restore administrator only for emptiness, restore, migration/reconciliation, and fingerprint work; it uses the runtime service only for `cmd/operations -action record-backup`. Restore verification and evidence recording derive their Stage 08 flags solely from the restored cutover state, immutable flag snapshot, authority envelope, and transition binding; local flag environment values are not used. The runtime group has no direct `INSERT`, `SELECT`, `UPDATE`, or `DELETE` authority on `backup_verifications`; it can only execute the pinned `SECURITY DEFINER` function `record_verified_backup_evidence`, which validates and derives the immutable evidence server-side. Runtime retains only the read access needed for validating the immutable authority metadata. This deliberately changes only the retained target after verification; it never writes evidence back to the production source. Preserve or explicitly decommission that target under the normal database change process.

For disaster recovery, preserve the source, acknowledge the incident, explicitly provision a new empty restore database, configure a separate runtime-principal record service for that same target, and run the same flow without `--test-mode`. Cut traffic only after canonical verification, recorded target evidence, and explicit human approval. Never restore over the source. Keep the legacy binary/path and verified dump through the rollback window.
