# Feature flags, staged rollout, and rollback

The effective authority is the integrity-verified persisted cutover state plus its exact immutable flag snapshot. Environment flags must match that snapshot at startup; they cannot independently grant authority:

| Flag | Values | Safe default |
| --- | --- | --- |
| `STAGE08_LEDGER_AUTHORITY` | `legacy`, `compare`, `authoritative` | `legacy` |
| `STAGE08_SHARED_ENGINE` | `off`, `shadow`, `paper`, `limited_live`, `full_live` | `off` |
| `STAGE08_NEW_BACKTEST` | `off`, `research` | `off` |
| `STAGE08_POINT_IN_TIME_UNIVERSE` | `off`, `research`, `authoritative` | `off` |
| `STAGE08_CANDIDATE_STRATEGY` | `off`, `research`, `shadow`, `paper`, `limited_live`, `full_live` | `off` |
| `STAGE08_DUAL_RUN` | `off`, `observe` | `off` |

Dependencies are enforced before startup. Dual run requires the shared engine; candidate shadow/capital requires shared engine plus PIT universe; new paper requires authoritative ledger; live engine/candidate modes must match and require authoritative ledger/PIT plus an exact verified `STAGE08_STAGE07_CONTEXT`. Dual run never grants execution.

Before the first observation, a governance approver must declare immutable parity tolerances/thresholds. The service rejects declaration after samples exist and never accepts a caller label as an expected mismatch:

```bash
curl -b cookie.jar -H 'Content-Type: application/json' \
  -d '{"name":"legacy-shared-cutover-v1","minimum_samples":1000,"minimum_coverage_bps":9500,"max_action_rate_bps":10,"max_quantity_rate_bps":10,"max_reason_rate_bps":20,"max_version_rate_bps":0,"quantity_tolerance_bps":1,"notional_tolerance_bps":1,"expected_reasons":[]}' \
  http://127.0.0.1:5001/api/operations/parity/policies
```

The legal state sequence is `schema_legacy → ledger_compare → shared_shadow → parity_accepted → new_paper → paper_observation → research_validation → limited_live`. No metric auto-advances it. First POST the proposed complete envelope to `/api/operations/flags/snapshots`; preparing a content-addressed snapshot grants no authority. Record each completed bounded prerequisite at `/api/operations/cutover/evidence`. Then an authenticated operations/governance administrator posts the transition with the target snapshot, exact parity policy/population, Stage 07 context, and evidence IDs. Caller summaries and parity denominators are rejected:

```bash
curl -b cookie.jar -H 'Content-Type: application/json' \
  -d '{"idempotency_key":"cutover-ledger-compare-001","to_stage":"ledger_compare","reason":"approved change CHG-001","flag_snapshot_id":"DIGEST","evidence_ids":[]}' \
  http://127.0.0.1:5001/api/operations/cutover/transitions
```

Rollback uses the same route with `"rollback":true` and an earlier stage. It atomically changes authority and appends immutable history; it does not delete fills/events. Restore the matching safe environment flags and restart after the audited transition. `legacy_removal_eligible` is deliberately rejected: legacy removal needs a separate future irreversible approval after the rollback window.
