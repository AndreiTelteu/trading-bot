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

When server initialization creates a genuinely new wallet, the `Config`
`DefaultBalance` and `DefaultCurrency` values passed into seeding are recorded
as a `capital_deposit` opening event and the account becomes ready.
When a wallet already exists, migration creates no economic events. The account
is `pending_approval`, exact projections remain absent, and economic commands
fail with a migration/projection conflict.

`go run ./cmd/ledger -action backfill` is dry-run by default. Applying requires
both `-dry-run=false`, an operator identity, and the exact approval phrase
`APPROVE_LEDGER_OPENING_BALANCE`. Application records the observed cutover cash
as an opening capital event. Legacy positions and orders remain listed in the
migration state as unresolved; they are not converted into invented fills,
fees, quantities, or cost basis. Such positions must be resolved from external
evidence or the controlled `asset-correction` CLI action before they can trade.
Applying cash backfill with open legacy exposure leaves the account in
`pending_resolution`; it does not become ready until every open position has an
exact reviewed quantity and cost basis. Corrections require the phrase
`APPROVE_LEDGER_ASSET_CORRECTION`, operator identity, reason, idempotency key,
and evidence metadata. They can be compensated by an immutable reversal.

## Adapter limitations retained explicitly

- All exchange order-submission paths are deliberately fail-closed in Stage 01,
  before invoking the exchange executor. Stage 02 must provide durable intent
  reservation, stable provider client IDs, acknowledgement persistence, and
  recovery polling before these paths may be enabled. There is therefore no
  reachable “exchange succeeded, local ledger failed” ambiguity in Stage 01.
- Provider fill ingestion accepts exact decimal strings and requires a genuine
  venue/account/provider-fill identity. It never synthesizes an ID from order
  quantity. Uniqueness is scoped by account and venue. Provider commission
  samples and generalized cumulative-fill polling remain Stage 02 inputs.
- Shadow positions are not silently mixed into the primary cash account. Closing
  one currently returns an explicit adapter error until Stage 02 introduces the
  separate execution-account orchestration.
- Mutable mark prices, trailing stops, and `exit_pending` are operational
  projections, not economic postings. Workers use timestamp-ordered,
  column-scoped updates, and database guards reject non-ledger changes to
  economic wallet/position columns. Cash, quantities, cost basis, realized
  P&L, orders that accompany fills, fills, and fees are committed only by the
  ledger transaction.

Only the `primary` account and its configured settlement currency are supported
in Stage 01. Fees must use that settlement currency; base-asset and third-asset
fees are rejected rather than silently treated as quote cash. Historical
`as_of` projection reconciliation is explicitly unsupported until projections
can be replayed at a common cutoff. Current reconciliation is exact (zero local
tolerance), includes quantity, cash, basis, fees, realized P&L, order/fill/event
links, and returns a nonzero CLI status for actionable imbalance.
