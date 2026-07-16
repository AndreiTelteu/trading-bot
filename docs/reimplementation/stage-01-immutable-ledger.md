# Stage 01 — Immutable Ledger and Reconciliation

## Objective

Make every change in cash, holdings, fees, and capital attributable to immutable events. Prevent API or service paths from changing portfolio state without a corresponding auditable event.

## Domain model

Introduce a typed append-only ledger. Exact naming may adapt to repository conventions, but the model must distinguish:

- [x] capital deposit and withdrawal;
- [x] buy and sell fills;
- [x] trading fee and exchange fee;
- [x] funding/interest adjustment, even if unused initially;
- [x] explicit administrative correction with reason and actor;
- [x] realized PnL attribution metadata;
- [x] reversal/correction event that references the original event rather than editing it.

Every event needs:

- [x] immutable ID and timestamp;
- [x] account/currency/symbol dimensions;
- [x] signed cash and asset quantities;
- [x] order/fill/position correlation IDs;
- [x] execution mode and strategy/policy versions;
- [x] idempotency key;
- [x] metadata/provenance without hidden balance mutation.

## Implementation work

### Persistence and transactions

- [x] Add database migrations and uniqueness constraints.
- [x] Implement atomic append operations that create order/fill/ledger events and update projections in one transaction.
- [x] Lock or otherwise serialize conflicting position/account updates.
- [x] Ensure retries are idempotent.
- [x] Keep existing wallet/position tables as projections during migration, not independent sources of truth.

### Execution integration

- [x] Route paper buy/sell through the ledger service.
- [x] Route exchange fill ingestion through the same ledger abstraction.
- [x] Persist fees and slippage assumptions for paper fills.
- [x] Ensure partial fills can be represented even if the initial broker policy uses all-or-none simulation.
- [x] Ensure close operations consume actual position quantity and preserve cost basis.

### Unsafe APIs

- [x] Replace direct position close with an execution request or explicit administrative adjustment.
- [x] Reject direct delete when economic exposure exists.
- [x] If administrative deletion remains, require a reason and append balancing/correction events.
- [x] Replace direct arbitrary wallet mutation with deposit/withdrawal/adjustment commands.
- [x] Return clear conflict/validation responses to clients.

### Reconciliation and migration

- [x] Add a read-only reconciliation service/CLI for cash, asset quantities, orders, fills, positions, and fees.
- [x] Report unmatched/orphaned records rather than silently fabricating events.
- [x] Add a controlled backfill command that supports dry-run and explicit cutover approval.
- [x] Record opening balance as a capital event.
- [x] Keep legacy historical gaps marked as unresolved adjustments unless evidence can reconstruct them.

## Testing instructions

### Unit and property-style invariants

- [x] Sum of signed cash ledger entries equals projected cash.
- [x] Sum of signed asset entries per symbol equals projected quantity.
- [x] A round trip realizes PnL exactly once.
- [x] Fees reduce cash/PnL exactly once.
- [x] Replaying the same idempotency key does not duplicate economic effects.
- [x] A reversal balances the original event without mutating it.

### Transaction and concurrency tests

- [x] Failure between order, fill, ledger, and projection writes rolls back everything.
- [x] Concurrent closes cannot sell more than available quantity.
- [x] Concurrent buys respect available cash and risk approval.
- [x] Retry after ambiguous failure is idempotent.

### API and migration tests

- [x] Close/delete/wallet endpoints cannot bypass ledger rules.
- [x] Fresh-schema migration succeeds.
- [x] Upgrade fixture from pre-ledger schema succeeds.
- [x] Backfill dry-run does not mutate data.
- [x] Reconciliation reports known inconsistent fixture data accurately.
- [x] Full `go test ./...` passes.

### Cannot yet be proven

- [ ] Real exchange fee reconciliation requires actual exchange fill samples or fixtures captured from the provider.
- [ ] Historical portfolio return remains unreliable until legacy gaps are classified/backfilled.
- [ ] Identical behavior across brokers depends on Stage 02.
- [ ] End-to-end operational dashboards depend on Stage 08.

## Acceptance criteria

- [x] No normal API/service path mutates cash or position economics outside the ledger transaction.
- [x] Paper fills include configured costs.
- [x] Reconciliation is machine-readable and human-readable.
- [x] Legacy inconsistencies are visible, not hidden.
- [x] Reviewer validates transaction boundaries, idempotency, signs, and quantity precision.
