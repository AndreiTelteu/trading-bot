# Stage 05 v4 capacity checkpoint

## First source outcome: failed full-liquidation boundary

The single predeclared `liquidate` source ran as clone job **85** under clean
revision `ead0cf6b71bc7978400a7f55a73a403dfd3411d9`. It failed at the
equal-weight baseline's final ADA sell; no canonical comparison or Stage 07
source artifact was persisted. At 2026-08-31 23:59 UTC the manifest-pinned 1m
bar reported 1,201.3 ADA base volume. The fixed 10% cap, rounded down to the
0.1 ADA lot step, was 120.1 ADA, while the remaining position was about
217.2 ADA (18.08% of reported volume). The broker cancelled the all-or-none
sell and `final_policy=liquidate` correctly failed closed. The
[private failure audit](/home/andrei/.bb-machines/192.168.0.115-38886/thread-storage/thr_ig94ijqd28/stage05-v4-capacity-job85-failure-audit.json)
has SHA-256 `c56c624b9dac39318a0582cb8eab7d3580250b0487824685e8912362d29ed217`.
Do not infer a candidate return or retry this exact boundary.

## Follow-on exploratory boundary: matched final marking

The inability to fully liquidate a constrained position is an economic
outcome, not a reason to manufacture a fill. A distinct checkpoint therefore
uses the already-supported `mark_to_market` final policy for **both** v3 and
v4, retaining the same candidate 1.1.0, matched baseline, dataset, interval,
symbols, exposure, costs, and all strategy parameters. There is no final
forced sell; residual inventory is explicitly marked. This changes the final
valuation contract for both policies, so the old v3 `liquidate` source jobs
81–83 are not direct return comparators. Predeclare at most one v4 source and,
only if it completes with reconciled nonzero candidate and matched-baseline
evidence, one v3 source under the same `mark_to_market` boundary. Stop on any
failure or non-comparable output; no retries, Stage 07, holdout, or promotion.
Both jobs use a fresh clean code revision and append-only attempt ledger.
The comparison remains exploratory because these dates were already inspected
and v4 is retrospective OHLCV capacity stress, not exchange fill evidence.

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
