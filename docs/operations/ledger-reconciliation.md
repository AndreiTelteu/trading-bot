# Ledger reconciliation and incident response

Run the read-only check against the intended database:

```bash
export DATABASE_URL='postgres://USER:PASSWORD@HOST:5432/trading_bot?sslmode=require'
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/ledger -action reconcile -json
```

`balanced:true` and exit 0 are required before ledger authority. Exit 2 means inspect `actionable_issues`, orphan IDs, differences, and unresolved migration gaps. The operational monitor persists a deduplicated `reconciliation_break` incident; it does not repair data.

For legacy opening capital, use the authenticated Stage 08 plan flow. `POST /api/operations/backfill/plans` generates/persists the server-derived dry-run digest. A governance approver posts that exact digest to `/plans/:id/approve`; a transition-capable principal then posts the returned approval digest to `/plans/:id/apply`. Replanning is required if source state changes. Open legacy exposure remains explicitly unresolved; reconstruct only from trusted evidence with `cmd/ledger -action asset-correction` and its required approval phrase, actor, reason, and idempotency key.

After remediation rerun reconciliation. Acknowledge or resolve the incident only with a reason via `POST /api/operations/incidents/:id/transition`. Do not edit ledger/fill rows, projection economics, or migration state directly.
