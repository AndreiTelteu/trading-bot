# Stage 07 strategy/model promotion and rollback

Use only the authenticated `/api/validation` workflow. Create a confirmatory experiment from canonical Stage 05/06 job IDs, run it, inspect immutable evidence, then have a different trusted human approval step where organizational policy requires it. Promotion calls are:

```text
POST /api/validation/experiments
POST /api/validation/experiments/:id/run
POST /api/validation/approvals
POST /api/validation/transitions
```

Do not copy request fields from an LLM or mutable settings without matching them to the stored manifest/evidence. Bootstrap and contract-fixture artifacts cannot be promoted. Paper/live authority requires exact implementation, configuration, run-manifest, artifact, policy, dataset/universe, evidence, approval, elapsed-monitoring, and deployment digests. These digest classes are not interchangeable: an implementation digest cannot stand in for governed configuration or a replay manifest.

Before treating runtime model monitoring as promotion evidence, inspect the
decision-cohort label coverage and pending/unavailable counts. Calibration uses
only matured fixed-horizon labels; missing bars or unmatured horizons are
insufficient evidence, never losses. See [model decision cohorts and
fixed-horizon labels](model-decision-cohorts.md).

For new Stage 07 runs, source jobs must expose a `stage07-source-artifact-v2`
replay-settings digest. Older Stage 05 source envelopes are intentionally
rejected: rerun the canonical comparison against the same immutable Stage 04
dataset before creating the validation experiment. A source comparison is
provenance only; each fold performs separate train, validation, and untouched
test replay. Do not treat a prior comparison curve, aggregate cost, or trade
list as fold evidence.

Rollback uses `POST /api/validation/rollback` with the deployed context, predefined rollback evidence, fallback version, stable idempotency key, and an authenticated rollback-capable principal. Then perform the Stage 08 cutover rollback and restore compatible flags. Confirm `/api/operations/status`, ledger reconciliation, and immutable transition history. Rollback never deletes economics or validation history.
