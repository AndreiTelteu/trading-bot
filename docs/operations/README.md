# Operations runbooks

These runbooks describe the implemented Stage 08 controls. They do not authorize a deployment or live exchange submission. Run every database command with an explicit `DATABASE_URL`; validate the database name before approving a mutation. The production server requires `AUTH_USERNAME` and `AUTH_PASSWORD`; governance actions additionally require the logged-in user in `GOVERNANCE_ADMIN_USERS`.

- [Install and upgrades](fresh-install-and-upgrades.md)
- [Feature flags, rollout, and rollback](feature-flags-and-cutover.md)
- [Ledger reconciliation](ledger-reconciliation.md)
- [Stage 04 data coverage](dataset-coverage.md)
- [Stage 05/06 backtest reproduction](backtest-reproduction.md)
- [Stage 07 promotion and rollback](strategy-model-promotion.md)
- [Broker and idempotency recovery](broker-idempotency-recovery.md)
- [Backup, isolated restore, and disaster recovery](backup-restore.md)

`GET /api/health` and `GET /api/operations/status` are authenticated and return HTTP 503 whenever required evidence is missing/stale or inconsistent. A fresh legacy-authoritative installation is therefore expected to be operationally `degraded` until Stage 04/backtest evidence exists; it is never reported green by default.
