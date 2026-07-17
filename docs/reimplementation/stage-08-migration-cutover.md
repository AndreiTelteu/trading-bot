# Stage 08 — Migration, Cutover, and Operations

## Objective

Move from legacy paths to the reimplemented core without silent behavior changes, data loss, or irreversible deployment. Expose enough operational state to detect reconciliation, parity, coverage, and governance failures.

## Migration strategy

### Feature flags and modes

- [x] Separate flags for ledger authority, shared decision engine, new backtest, point-in-time universe, and new strategy.
- [x] Flags have safe defaults and documented dependencies.
- [x] Invalid combinations fail startup/config validation.
- [x] Every runtime observation records active path/version.

### Dual-run/shadow comparison

- [x] Run legacy and new decision paths on the same immutable context without allowing the shadow path to execute.
- [x] Compare action, symbol, side, quantity/notional, rejection reason, exit reason, and factor trace.
- [x] Classify expected versus unexplained divergences.
- [x] Persist compact divergence samples and aggregate rates.
- [x] Define acceptance thresholds before cutover.

### Data migration

- [x] Apply schema migrations in dependency order.
- [x] Backfill opening capital/events with dry-run and explicit approval.
- [x] Preserve unresolved historical gaps as flagged adjustments.
- [x] Verify projections and reconciliation before ledger authority is enabled.
- [x] Provide backup/restore and rollback instructions.

## Operational visibility

- [x] Health/status surfaces expose active engine, strategy, policy, model rollout, and dataset/universe versions.
- [x] Expose ledger reconciliation status and last successful check.
- [x] Expose backtest coverage failures distinctly from strategy zero trades.
- [x] Expose parity divergence counts during dual-run.
- [x] Expose validation/promotion state and failed gates.
- [x] Alert/log on reconciliation breaks, missing benchmark/universe data, governance bypass attempts, and repeated broker idempotency conflicts.

## Cutover sequence

- [ ] Deploy schema/contracts with legacy runtime still authoritative.
- [ ] Enable ledger recording/projection comparison.
- [ ] Enable shared engine in shadow comparison.
- [ ] Resolve unexplained divergences.
- [ ] Enable new paper path with costs and reconciliation.
- [ ] Run controlled shadow/paper observation period.
- [ ] Enable new backtest and validation artifacts for research.
- [ ] Consider limited live only after governance gates and explicit human approval.
- [ ] Deprecate/remove legacy mutation and decision paths only after rollback window and acceptance criteria.

## Documentation/runbooks

- [x] Fresh install and upgrade procedure.
- [x] Ledger reconciliation and incident response.
- [x] Dataset coverage/backfill operation.
- [x] Backtest reproduction.
- [x] Strategy/model promotion and rollback.
- [x] Exchange/broker failure and idempotent recovery.
- [x] Backup/restore verification.

## Testing instructions

### Automated

- [x] Fresh database migration and startup.
- [x] Upgrade from a representative pre-reimplementation fixture.
- [ ] Rollback/restore from backup fixture.
- [x] Invalid feature-flag combinations fail clearly.
- [x] Shadow engine cannot execute orders.
- [x] Divergence comparison is deterministic.
- [x] Frontend status renders new health fields when frontend changes.
- [x] Full `go test ./...` passes.
- [x] Frontend build/typecheck passes.
- [x] `docker compose config` succeeds.

### Controlled runtime verification

- [x] Paper buy/sell round trip reconciles including costs.
- [x] Service restart does not duplicate orders/fills/events.
- [x] Missing benchmark/universe data blocks decisions and raises status.
- [x] Rollout state change follows governance and is auditable.
- [ ] Backup restores equivalent ledger/projections.

### Cannot yet be proven

- [ ] Long-term production stability requires elapsed shadow/paper operation.
- [ ] Live exchange behavior requires testnet or explicitly approved limited-live tests.
- [ ] Profitability remains governed by Stage 07 evidence, not deployment success.
- [ ] Operational alert delivery may depend on external channel availability.

## Acceptance criteria

- [x] Cutover is reversible until legacy removal is explicitly approved.
- [x] New and old paths can be compared without double execution.
- [x] Operators can see reconciliation, coverage, parity, and governance state.
- [ ] Migration and restore procedures are tested, not merely documented.
- [ ] Reviewer confirms no hidden path bypasses feature flags or ledger authority.

## Completion evidence

- Initial implementation commit: `000a496`.
- Independent read-only review verdict: **Reject**, with findings C1, H1–H8, and M1–M2.
- The single allowed feedback pass was resumed in the original implementation session and remediated every reported finding in commit `f7eeaed`.
- Adversarial coverage includes effective-authority reconciliation, stale flag rejection, observational shadow orders, policy-bound parity, evidence-bound cutover prerequisites, rollback envelopes, fail-closed status, incident cooldown/delivery failures, operations capabilities, and complete transition idempotency.
- Isolated PostgreSQL full suite passed serially with `go test -p 1 -count=1 ./...`.
- Relevant PostgreSQL race suites, including `cutover` and `operations`, passed serially; `go vet ./...`, `git diff --check`, backup-script syntax, and Compose rendering passed.
- Frontend was not changed, so frontend build/typecheck was not applicable.
- Pure canonical backup fingerprint/equivalence logic is tested, but an actual dump/restore exercise was blocked because `pg_dump`, `pg_restore`, `psql`, `createdb`, and `dropdb` are unavailable on this host.
- No deployment, runtime cutover, exchange action, controlled observation period, limited-live activation, or legacy removal was performed.
- The unchecked items above are therefore intentionally pending rather than claimed complete.
