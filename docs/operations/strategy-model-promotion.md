# Stage 07 strategy/model promotion and rollback

Use only the authenticated `/api/validation` workflow. Create a confirmatory experiment from canonical Stage 05/06 job IDs, run it, inspect immutable evidence, then have a different trusted human approval step where organizational policy requires it. Promotion calls are:

```text
POST /api/validation/experiments
POST /api/validation/experiments/:id/run
POST /api/validation/approvals
POST /api/validation/transitions
```

Do not copy request fields from an LLM or mutable settings without matching them to the stored manifest/evidence. Bootstrap and contract-fixture artifacts cannot be promoted. Paper/live authority requires exact artifact, policy, dataset/universe, evidence, approval, elapsed monitoring, and deployment digests.

Rollback uses `POST /api/validation/rollback` with the deployed context, predefined rollback evidence, fallback version, stable idempotency key, and an authenticated rollback-capable principal. Then perform the Stage 08 cutover rollback and restore compatible flags. Confirm `/api/operations/status`, ledger reconciliation, and immutable transition history. Rollback never deletes economics or validation history.
