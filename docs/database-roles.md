# Database login roles

Four PostgreSQL connections have distinct purposes:

- `MIGRATION_DATABASE_URL`: administrative, used only by `cmd/bootstrap` (and
  isolated restore-target verification), never by the long-lived server.
- `DATABASE_URL`: non-superuser `trading_bot_app_runtime`; inherits
  `trading_bot_runtime` and cannot assume either writer role.
- `LEDGER_DATABASE_URL`: non-superuser `trading_bot_app_ledger`; inherits only
  `trading_bot_ledger_writer` and can explicitly enter that role.
- `PARITY_DATABASE_URL`: non-superuser `trading_bot_app_parity`; inherits only
  `trading_bot_parity_writer` and can explicitly enter that role.

The Compose bootstrap job provisions those three application logins using
passwords read from mounted secret files. The ledger group role has only the
economic tables/sequences and explicit position columns needed by ledger services. It has no parity-role
membership and no settings, governance, cutover, backup, incident, or other
operations DML. Economic wallet/position changes are deferred-checked against
immutable ledger events from the same top-level transaction. Position quantity,
cost basis, accumulated fees, and realized P&L must each equal their ledger-event
totals.

Runtime can read economic state and update only operational position columns,
including the coordinator's `exit_pending` claim/release. It cannot insert,
delete, or update economic wallet/position fields. Parity persistence uses its
own connection and explicitly enters the parity writer role. That role may
insert immutable populations and observations and maintain only the mutable
parity aggregate; it cannot update immutable evidence or write ledger/projection
tables.

For the isolated backup/restore workflow, runtime has no direct `INSERT`,
`SELECT`, `UPDATE`, or `DELETE` authority on `backup_verifications`. It records
evidence only by executing the pinned `SECURITY DEFINER` function
`record_verified_backup_evidence`, which validates and derives the immutable
record server-side. Runtime has read-only access to the authority metadata
needed to validate the current cutover binding. The restore administrator is
never used for that evidence write.

Direct environment variables remain supported outside Compose. `*_URL_FILE`
is preferred so DSNs are not placed in Compose or process configuration files.
