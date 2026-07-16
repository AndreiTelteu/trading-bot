# Stage 01 — Immutable Ledger and Reconciliation

## Objective

Make every change in cash, holdings, fees, and capital attributable to immutable events. Prevent API or service paths from changing portfolio state without a corresponding auditable event.

## Domain model

Introduce a typed append-only ledger. Exact naming may adapt to repository conventions, but the model must distinguish:

- [ ] capital deposit and withdrawal;
- [ ] buy and sell fills;
- [ ] trading fee and exchange fee;
- [ ] funding/interest adjustment, even if unused initially;
- [ ] explicit administrative correction with reason and actor;
- [ ] realized PnL attribution metadata;
- [ ] reversal/correction event that references the original event rather than editing it.

Every event needs:

- [ ] immutable ID and timestamp;
- [ ] account/currency/symbol dimensions;
- [ ] signed cash and asset quantities;
- [ ] order/fill/position correlation IDs;
- [ ] execution mode and strategy/policy versions;
- [ ] idempotency key;
- [ ] metadata/provenance without hidden balance mutation.

## Implementation work

### Persistence and transactions

- [ ] Add database migrations and uniqueness constraints.
- [ ] Implement atomic append operations that create order/fill/ledger events and update projections in one transaction.
- [ ] Lock or otherwise serialize conflicting position/account updates.
- [ ] Ensure retries are idempotent.
- [ ] Keep existing wallet/position tables as projections during migration, not independent sources of truth.

### Execution integration

- [ ] Route paper buy/sell through the ledger service.
- [ ] Route exchange fill ingestion through the same ledger abstraction.
- [ ] Persist fees and slippage assumptions for paper fills.
- [ ] Ensure partial fills can be represented even if the initial broker policy uses all-or-none simulation.
- [ ] Ensure close operations consume actual position quantity and preserve cost basis.

### Unsafe APIs

- [ ] Replace direct position close with an execution request or explicit administrative adjustment.
- [ ] Reject direct delete when economic exposure exists.
- [ ] If administrative deletion remains, require a reason and append balancing/correction events.
- [ ] Replace direct arbitrary wallet mutation with deposit/withdrawal/adjustment commands.
- [ ] Return clear conflict/validation responses to clients.

### Reconciliation and migration

- [ ] Add a read-only reconciliation service/CLI for cash, asset quantities, orders, fills, positions, and fees.
- [ ] Report unmatched/orphaned records rather than silently fabricating events.
- [ ] Add a controlled backfill command that supports dry-run and explicit cutover approval.
- [ ] Record opening balance as a capital event.
- [ ] Keep legacy historical gaps marked as unresolved adjustments unless evidence can reconstruct them.

## Testing instructions

### Unit and property-style invariants

- [ ] Sum of signed cash ledger entries equals projected cash.
- [ ] Sum of signed asset entries per symbol equals projected quantity.
- [ ] A round trip realizes PnL exactly once.
- [ ] Fees reduce cash/PnL exactly once.
- [ ] Replaying the same idempotency key does not duplicate economic effects.
- [ ] A reversal balances the original event without mutating it.

### Transaction and concurrency tests

- [ ] Failure between order, fill, ledger, and projection writes rolls back everything.
- [ ] Concurrent closes cannot sell more than available quantity.
- [ ] Concurrent buys respect available cash and risk approval.
- [ ] Retry after ambiguous failure is idempotent.

### API and migration tests

- [ ] Close/delete/wallet endpoints cannot bypass ledger rules.
- [ ] Fresh-schema migration succeeds.
- [ ] Upgrade fixture from pre-ledger schema succeeds.
- [ ] Backfill dry-run does not mutate data.
- [ ] Reconciliation reports known inconsistent fixture data accurately.
- [ ] Full `go test ./...` passes.

### Cannot yet be proven

- [ ] Real exchange fee reconciliation requires actual exchange fill samples or fixtures captured from the provider.
- [ ] Historical portfolio return remains unreliable until legacy gaps are classified/backfilled.
- [ ] Identical behavior across brokers depends on Stage 02.
- [ ] End-to-end operational dashboards depend on Stage 08.

## Acceptance criteria

- [ ] No normal API/service path mutates cash or position economics outside the ledger transaction.
- [ ] Paper fills include configured costs.
- [ ] Reconciliation is machine-readable and human-readable.
- [ ] Legacy inconsistencies are visible, not hidden.
- [ ] Reviewer validates transaction boundaries, idempotency, signs, and quantity precision.
