# Stage 08 — Migration, Cutover, and Operations

## Objective

Move from legacy paths to the reimplemented core without silent behavior changes, data loss, or irreversible deployment. Expose enough operational state to detect reconciliation, parity, coverage, and governance failures.

## Migration strategy

### Feature flags and modes

- [ ] Separate flags for ledger authority, shared decision engine, new backtest, point-in-time universe, and new strategy.
- [ ] Flags have safe defaults and documented dependencies.
- [ ] Invalid combinations fail startup/config validation.
- [ ] Every runtime observation records active path/version.

### Dual-run/shadow comparison

- [ ] Run legacy and new decision paths on the same immutable context without allowing the shadow path to execute.
- [ ] Compare action, symbol, side, quantity/notional, rejection reason, exit reason, and factor trace.
- [ ] Classify expected versus unexplained divergences.
- [ ] Persist compact divergence samples and aggregate rates.
- [ ] Define acceptance thresholds before cutover.

### Data migration

- [ ] Apply schema migrations in dependency order.
- [ ] Backfill opening capital/events with dry-run and explicit approval.
- [ ] Preserve unresolved historical gaps as flagged adjustments.
- [ ] Verify projections and reconciliation before ledger authority is enabled.
- [ ] Provide backup/restore and rollback instructions.

## Operational visibility

- [ ] Health/status surfaces expose active engine, strategy, policy, model rollout, and dataset/universe versions.
- [ ] Expose ledger reconciliation status and last successful check.
- [ ] Expose backtest coverage failures distinctly from strategy zero trades.
- [ ] Expose parity divergence counts during dual-run.
- [ ] Expose validation/promotion state and failed gates.
- [ ] Alert/log on reconciliation breaks, missing benchmark/universe data, governance bypass attempts, and repeated broker idempotency conflicts.

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

- [ ] Fresh install and upgrade procedure.
- [ ] Ledger reconciliation and incident response.
- [ ] Dataset coverage/backfill operation.
- [ ] Backtest reproduction.
- [ ] Strategy/model promotion and rollback.
- [ ] Exchange/broker failure and idempotent recovery.
- [ ] Backup/restore verification.

## Testing instructions

### Automated

- [ ] Fresh database migration and startup.
- [ ] Upgrade from a representative pre-reimplementation fixture.
- [ ] Rollback/restore from backup fixture.
- [ ] Invalid feature-flag combinations fail clearly.
- [ ] Shadow engine cannot execute orders.
- [ ] Divergence comparison is deterministic.
- [ ] Frontend status renders new health fields when frontend changes.
- [ ] Full `go test ./...` passes.
- [ ] Frontend build/typecheck passes.
- [ ] `docker compose config` succeeds.

### Controlled runtime verification

- [ ] Paper buy/sell round trip reconciles including costs.
- [ ] Service restart does not duplicate orders/fills/events.
- [ ] Missing benchmark/universe data blocks decisions and raises status.
- [ ] Rollout state change follows governance and is auditable.
- [ ] Backup restores equivalent ledger/projections.

### Cannot yet be proven

- [ ] Long-term production stability requires elapsed shadow/paper operation.
- [ ] Live exchange behavior requires testnet or explicitly approved limited-live tests.
- [ ] Profitability remains governed by Stage 07 evidence, not deployment success.
- [ ] Operational alert delivery may depend on external channel availability.

## Acceptance criteria

- [ ] Cutover is reversible until legacy removal is explicitly approved.
- [ ] New and old paths can be compared without double execution.
- [ ] Operators can see reconciliation, coverage, parity, and governance state.
- [ ] Migration and restore procedures are tested, not merely documented.
- [ ] Reviewer confirms no hidden path bypasses feature flags or ledger authority.
