# Backup, isolated restore, verification, and disaster recovery

Prerequisites are `pg_dump`, `pg_restore`, `psql`, `createdb`, `dropdb`, `sha256sum`, the pinned Go binary, and libpq service entries whose passwords are supplied by a mode-0600 `PGPASSFILE` (or another libpq credential provider). The source service must resolve to a database whose verified name ends in `_test` for the bounded exercise. Do not put database URLs or passwords on the command line.

The output path must be absolute, its parent must exist, and the file must not exist. The verifier creates an unguessable database itself from `template0`, writes and reads a random identity token before restore, and drops that database on exit:

```bash
export PGSERVICEFILE=/absolute/path/to/pg_service.conf
export PGPASSFILE=/absolute/path/to/pgpass
export STAGE08_SOURCE_SERVICE=trading_bot_test_source
export STAGE08_MAINTENANCE_SERVICE=trading_bot_test_maintenance
./scripts/stage08_backup_restore.sh \
  --output /tmp/trading_bot_stage08.dump \
  --principal operations-admin
```

Expected output is bounded JSON containing `status:"verified"`, the dump checksum, and the canonical database digest. Exit 3 means a required tool was unavailable and no verification occurred. Other nonzero exits identify refusal, source mutation, restore failure, or a canonical mismatch.

The script refuses overwrite, uses restrictive permissions, never runs `pg_restore --clean`, reruns migrations/startup integrity/reconciliation in the isolated target, and compares ordered per-row hashes (identity, exact amounts/costs, timestamps, provenance, and audit content) plus counts for every economic and immutable audit table. Equal aggregate balances cannot mask changed row identities. It compares the source before and after the exercise, then persists an immutable `BackupVerification` through `cmd/operations -action record-backup`; status only accepts verification bound to the current flag snapshot and cutover transition.

For disaster recovery, preserve the source, acknowledge the incident, and repeat into a newly created database. Cut traffic only after canonical verification and explicit human approval. Never restore over the source. Keep the legacy binary/path and verified dump through the rollback window.
