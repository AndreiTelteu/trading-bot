# Stage 05 v4 capacity checkpoint

## Predeclared question and policy

The v3 fold audit found one AVAX sell of 8.42 units against 5.19 units of
reported base volume in its selected 1-minute bar. V3 can therefore record
an economically impossible full fill. The first capacity experiment changes
only execution, not strategy selection: candidate
`trend_momentum_candidate@1.1.0` and its matched baseline use the same
manifest, 2024-12-01 to 2026-09-01 interval, symbols, 10/5 bps fee/slippage,
0.75 gross/net exposure, and `liquidate` final policy as source jobs 81–83.
The positive-new-target rule in candidate 1.2.0 has already failed its
one-source checkpoint and is excluded here.

The new `backtest-execution-v4` policy assumes the previously approved order
arrives at the selected 1-minute bar's open and evaluates its outcome at that
bar's close, when its volume is known. It permits a modeled full order fill at that close
with the same adverse fee/slippage model only if the risk-approved base
quantity is at most 10% of the selected bar's base volume,
rounded down to the point-in-time lot step. Otherwise the entire order is
cancelled at that bar's close. This all-or-none choice is intentionally
conservative and does **not** claim a partial-fill or order-book model. It
prevents a simulated fill above the declared capacity without deriving an
intrabar schedule from OHLCV. It may reject realistic executable orders; that
uncertainty remains a limitation of the data. The cap is fixed before the
source run and is not tuned against its return.
This is retrospective bar-level feasibility stress, not evidence of an actual
intraminute fill sequence or a tradable close-price order.
Because volume becomes known at bar close, v4 also moves accepted fills from
the selected open to that close. A v3-to-v4 return difference therefore
combines the capacity rule with this necessary timing/price change; it must
not be attributed to the cap alone.

Submit at most **one** exploratory Stage 05 source on the isolated clone at
this checkpoint, under a clean committed revision and the existing append-only
attempt ledger. Audit terminal status, exact request/manifest/policy identities,
candidate/baseline fill and reconciliation evidence, no-fill reasons and
capacity, return, costs, exposure, and final-position behavior. A failed run,
zero-trade result, or non-comparable output is not positive evidence. Do not
repeat automatically or consume the reserved B/C slots. The 2024–2026 data
have already been inspected, so this is not confirmatory validation. Stage 07
must reject v4 sources until its fold primitive/no-fill verifier supports this
policy; no existing v3 evidence may be relabeled. A genuine confirmatory
assessment requires later unseen data, a frozen single-use holdout, and the
existing human approval gates.
