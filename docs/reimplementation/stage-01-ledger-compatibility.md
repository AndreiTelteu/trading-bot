# Stage 01 ledger compatibility and migration notes

This document records implementation decisions that cannot safely be inferred
from the pre-ledger mutable tables. It does not mark the Stage 01 plan complete.

## Numeric semantics

Ledger, fill, and exact projection columns use PostgreSQL `NUMERIC(38,18)`.
Application arithmetic uses a signed fixed-scale base-10 integer with 18 decimal
places and round-half-away-from-zero division. JSON accepts/returns exact amounts
as decimal strings. Existing `float64` wallet, position, and order fields remain
compatibility/display mirrors; ledger transactions calculate exact values first
and derive those mirrors afterward. A legacy binary float is converted only at
an adapter boundary using Go's shortest round-tripping base-10 representation.

Buy fill cash is `-(quantity * fill_price)`, sell fill cash is positive, asset
quantity is positive for buys and negative for sells, and fees are separate
negative-cash events. Position cost basis excludes fees; realized fill P&L is
gross matched-fill P&L, while fee events provide the single fee deduction.
`Position.RealizedPnLExact` is the net compatibility projection. This avoids
charging either entry or exit fees twice.

## Fresh install and upgrade boundary

When seed data creates a genuinely new wallet, the configured default balance
is recorded as a `capital_deposit` opening event and the account becomes ready.
When a wallet already exists, migration creates no economic events. The account
is `pending_approval`, exact projections remain absent, and economic commands
fail with a migration/projection conflict.

`go run ./cmd/ledger -action backfill` is dry-run by default. Applying requires
both `-dry-run=false`, an operator identity, and the exact approval phrase
`APPROVE_LEDGER_OPENING_BALANCE`. Application records the observed cutover cash
as an opening capital event. Legacy positions and orders remain listed in the
migration state as unresolved; they are not converted into invented fills,
fees, quantities, or cost basis. Such positions must be resolved from external
evidence or an explicit later administrative correction before they can trade.

## Adapter limitations retained explicitly

- The current Binance order response model does not provide commission detail.
  Exchange fills are ledgered with a zero fee and metadata
  `exchange_fee_status=unavailable_from_order_response`; reconciliation against
  captured provider fill/commission samples remains deferred as the plan states.
- A reported partial exchange quantity is representable as an immutable fill and
  leaves the position open. Broader broker ingestion/cumulative-fill orchestration
  belongs to Stage 02.
- Shadow positions are not silently mixed into the primary cash account. Closing
  one currently returns an explicit adapter error until Stage 02 introduces the
  separate execution-account orchestration.
- Mutable mark prices, trailing stops, and `exit_pending` are operational
  projections, not economic postings. Cash, quantities, cost basis, realized
  P&L, orders that accompany fills, fills, and fees are committed only by the
  ledger transaction.
