# Stage 05 v4 capacity checkpoint

## Matched-marking source result (2026-09-27)

The predeclared pair completed on the isolated clone under clean revision
`97a7f1d2dc7c604061ef52779bf326086c1b4bd9`: job **86** used v4 and
job **87** used v3. Both used dataset manifest
`32fe7e3664ab9d1a14c7cff30c0db6dc673f37ca0eea0479ed9cd812011ec9bc`,
candidate `1.1.0`, identical candidate and matched-baseline configuration and
implementation digests, the same costs/exposure/interval, and
`mark_to_market` final policy. All non-execution normalized assumptions match.
Both candidate and matched baseline have nonzero fills and reconciled metrics.
The comparison digests are
`55481cacb9cfdf70e55d2925f4c653c88ba57177396b674c2ca98921d8909dbe`
(v4) and `d0d2cb041e170a2f28f6d17051a49ad160e2a18e98e8161f1cb7afeafa1f5c0e`
(v3).

| Strategy / policy | After-cost return | Fills | Closed trades | Total costs | No-fills | Average gross exposure |
|---|---:|---:|---:|---:|---:|---:|
| Candidate 1.1.0 / v4 | +3.4099% | 587 | 282 | 90.2810 | 4 | 0.10228 |
| Candidate 1.1.0 / v3 | +2.9875% | 588 | 282 | 67.1449 | 2 | 0.10240 |
| Matched baseline 1.0.0 / v4 | +1.2642% | 587 | 280 | 89.2638 | 3 | 0.10128 |
| Matched baseline 1.0.0 / v3 | +0.8882% | 588 | 280 | 66.3980 | 2 | 0.10144 |

V4's candidate return is 0.4224 percentage points above v3, while its
matched baseline is 0.3760 points above v3. Candidate excess over the matched
baseline is 2.1457 points in v4 versus 2.0993 in v3: the difference is only
**0.0464 points**. Candidate costs rise by 23.1361 capital units in v4. This
is an observed exploratory sensitivity to a policy that changes **both** the
bar-volume cap and accepted fill time/price; it is not evidence that the cap
improves strategy quality. Neither job used a new holdout. The persisted Stage
05 screening gate reports `optimization_allowed=true`, but promotion remains
false, Stage 07 does not admit v4, and prior Stage 07 statistical gates remain
failed for their historical policy.

All 115 v4 no-fill records across the seven rows are versioned v2 capacity
cancels: each has approved quantity above its recorded cap, zero filled
quantity, and cap at most 10% of its recorded base volume. The retained compact
source artifacts omit raw fill and final-inventory primitives. Thus this audit
cannot independently verify **every accepted fill's** bar participation or
reconstruct ending inventory from the persisted source alone. The shared
broker path and synthetic regressions enforce the cap in code, but that is not
a substitute for independent economic evidence. The private
[pair audit](/home/andrei/.bb-machines/192.168.0.115-38886/thread-storage/thr_ig94ijqd28/stage05-v3-v4-mark-pair-audit.json)
has SHA-256 `f209f557058ece6fb866f364d14ebdb3fcb8eb3e464777d6f0b18b38476fab1b`;
the [v4 source](/home/andrei/.bb-machines/192.168.0.115-38886/thread-storage/thr_ig94ijqd28/stage05-v4-mark-job86-source.json)
and [v3 source](/home/andrei/.bb-machines/192.168.0.115-38886/thread-storage/thr_ig94ijqd28/stage05-v3-mark-job87-source.json)
remain private. The append-only attempt ledger records both reviewed jobs.

Stop further searches on the inspected 2024–2026 interval. The Stage 07 fresh
fold verifier now checks v4 capacity cancels and accepted fill participation
against the selected 1m bar and point-in-time quantity step, and reconciles
fill-derived cash and final inventory against ledger, exposure, and curve
evidence. The compact jobs 86/87 remain unchanged and do not contain those
fresh fold primitives. A separately pinned v4 driver/manifest, fresh audited
v4 fold sources, and then a predeclared genuinely later unseen dataset are
still required. No paper/live promotion or direct live submission follows
from these sources.

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
