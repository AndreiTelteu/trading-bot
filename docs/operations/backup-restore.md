# Backup, isolated restore, verification, and disaster recovery

The verifier refuses a source database without `_test`, a target without `_restore_test`, equal source/target URLs, or missing PostgreSQL tools. For the mandated isolated exercise, pre-create an empty target database and run:

```bash
export TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:55433/trading_bot_test?sslmode=disable'
createdb -h 127.0.0.1 -p 55433 -U postgres trading_bot_restore_test
./scripts/stage08_backup_restore.sh \
  --target 'postgres://postgres:postgres@127.0.0.1:55433/trading_bot_restore_test?sslmode=disable' \
  --output /tmp/trading_bot_stage08.dump
dropdb -h 127.0.0.1 -p 55433 -U postgres trading_bot_restore_test
```

Expected output is bounded JSON with `status:"verified"`, dump SHA-256, database identities, and canonical ledger/projection counts/digest. The script uses `pg_dump`, cleans only the already-isolated target, runs `cmd/operations -action verify` there to apply current migrations and perform integrity/reconciliation checks, verifies canonical equality, and proves the source economic fingerprint did not change. If any required tool is unavailable it exits 3 and makes no verification claim.

For an actual disaster, first preserve the damaged source, obtain explicit incident/change approval, create a new isolated recovery database, restore there, run the current binary migrations, `cmd/ledger -action reconcile -json`, Stage 04 manifest verification, and canonical comparison. Cut application traffic over only after human approval. Never restore over the source. Keep the legacy binary/path and verified dump through the rollback window.
