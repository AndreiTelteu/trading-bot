# Exchange/broker failure and idempotent recovery

Live exchange submission remains fenced in this repository unless exact governed limited/full-live authority is present; Stage 08 does not claim testnet/live behavior. For a broker timeout or indeterminate result:

1. Stop new capital-path requests by rolling Stage 08 authority back to the prior reversible state.
2. Preserve the original client order/idempotency key. Never submit a replacement with a new key merely because the response was lost.
3. Inspect persisted order, broker outcome ingestion, fills, and ledger events. Re-run the same recovery/poll operation with the same key only after provider identity is known.
4. Run `go run ./cmd/ledger -action reconcile -json`.
5. Treat a same-key/different-payload conflict as critical. The monitor persists `repeated_broker_idempotency_conflict`; manual row edits are forbidden.
6. Resume only after the outcome is terminal, reconciliation is balanced, and the incident has an acknowledged/resolved audit reason.

External exchange state and fee samples require provider/testnet evidence. An HTTP error alone does not prove an order was not accepted.
