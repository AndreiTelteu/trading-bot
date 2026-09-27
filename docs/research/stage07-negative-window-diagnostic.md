# Stage 07 negative-window diagnostic and exploratory hypothesis result

## Exploratory source result (2026-09-27)

The predeclared positive-new-target-momentum rule was exercised once on the
isolated `trading_bot_research` clone as Stage 05 job **84**, using clean code
revision `cd1a97e7ee1955f04e85c0043cae7fd1ab740ef9`, the unchanged
manifest `32fe7e3664ab9d1a14c7cff30c0db6dc673f37ca0eea0479ed9cd812011ec9bc`,
and v3 execution. The job completed with canonical comparison digest
`08b410938625af0cb89c0a0a11ac38977426223964b2df24f624e1e66ae10dbf`;
its retained Stage 07 source artifact digest is
`ccfbe0220c1297dac5416727739dc6e52640e2c8dfd5890a07de9eef5132e1d0`.
Both candidate and matched baseline reconciled and had nonzero fills.

| Strategy | After-cost return | Ending equity | Fills | Total costs | Average gross exposure |
|---|---:|---:|---:|---:|---:|
| Candidate 1.2.0, positive new targets | -0.7153% | 992.8470 | 559 | 63.9508 | 0.09855 |
| Matched baseline 1.1.0, same entry rule | -1.5899% | 984.1007 | 558 | 63.7557 | 0.09792 |
| Previous candidate 1.1.0, jobs 81–83 | +2.9501% | 1029.5010 | 589 | 67.3758 | 0.10240 |

The new candidate beat its matched baseline by 0.8746 percentage points, but
lost 0.7153% against cash and lagged the previous candidate by 3.6654
percentage points on this already-inspected source interval. The persisted
governance decision sets both `optimization_allowed` and `promotion_allowed`
to false (`candidate_does_not_beat_cash_after_costs`, plus pending Stage 07
validation). This one exploratory comparison does not prove the entry rule
caused the difference or generalize to unseen periods. It is sufficient to
**stop this hypothesis at the one-source checkpoint**: do not spend the
reserved B/C source slots or create a Stage 07 experiment for version 1.2.0.
The [private compact export](/home/andrei/.bb-machines/192.168.0.115-38886/thread-storage/thr_ig94ijqd28/stage05-positive-entry-job84-compact.json)
and [audit summary](/home/andrei/.bb-machines/192.168.0.115-38886/thread-storage/thr_ig94ijqd28/stage05-positive-entry-job84-audit.json)
are retained separately from the repository; the audit SHA-256 is
`808a1d248e3ca7243f4d02a0174b709fde06fadbf59a510442fc3ed68896ad22`.
The append-only clone attempt ledger records submission, terminal evidence, and
this stop decision. No holdout, paper promotion, or live execution occurred.

Further work should first define and version a capacity-aware execution model
that cannot fill more than the selected bar supports, with matched candidate
and baseline assumptions. That is a new economic policy and needs its own
predeclared experiment; existing v3 evidence must remain immutable. Any
confirmatory claim also requires genuinely later, unseen data and the existing
human-controlled promotion gates.

This is a read-only attribution of exploratory experiment
`16799f25f30d5e48e158329080375c805c047db16dbdf6087f92b3c3cf5f496b`
on the isolated research clone. The immutable v3 root and three v2 fold rows
were previously verified by repository hydration and metric rederivation. Root
status `passed` records technical integrity; aggregate statistical `passed` is
`false`. The source jobs are 81–83, and the root evidence digest is
`61bb442a4deca049643af1706cbc429d65999ae2312018235f5171028ef8ee61`.

The table sums the persisted closed-trade `gross_pnl`, `cost`, and `net_pnl`,
then adds explicit final marked-position P&L. Each fold starts with 1,000 units
of capital. Values are in those same units; minor last-digit differences in
the persisted float representation are not additional P&L.

| Test window (UTC, end exclusive) | Closed gross | Closed costs | Closed net | Final marked P&L | Equity change | Candidate return | Matched baseline return |
|---|---:|---:|---:|---:|---:|---:|---:|
| 2026-01-01 to 2026-03-01 | -24.7272 | 4.7351 | -29.4623 | -0.1118 | -29.5741 | -2.9574% | -3.0457% |
| 2026-04-01 to 2026-06-01 | -5.3015 | 7.3414 | -12.6429 | -0.0209 | -12.6638 | -1.2664% | -1.2677% |
| 2026-07-01 to 2026-08-31 20:00 | +29.1576 | 7.7591 | +21.3985 | +0.0146 | +21.4131 | +2.1413% | +2.1190% |

The first negative window loses before costs. The second loses before costs as
well, and costs exceed its gross trading loss. These costs are the verified
closed-trade aggregate, not a separately reconstructed fee/slippage split. The
small matched-baseline excess (+0.0883, +0.0013, and +0.0223 percentage points)
does not establish positive absolute returns. The predeclared after-cost and
stressed-return lower-bound gates fail; coverage, drawdown, and matched-baseline
relative-return gates pass.

| Fold | Neutral trades / net P&L | Risk-on trades / net P&L | Largest losing symbols by net P&L |
|---|---:|---:|---|
| 0 | 13 / -22.7629 | 5 / -6.6994 | BNB -10.9963; XRP -5.8020; SOL -5.7134 |
| 1 | 34 / -14.4533 | 1 / +1.8104 | AVAX -7.0505; BNB -4.6372; XRP -2.1158 |
| 2 | 27 / +20.0546 | 8 / +1.3439 | AVAX -12.6138; DOGE -8.0832; BNB -3.7472 |

Trades attributed to the neutral regime account for most of the two losing
windows' closed-trade net loss, but neutral-attributed trades are also
profitable in fold 2. This does not support
turning off the neutral regime after inspecting these tests. Fold 2 includes a
single ETH close with +43.7737 net P&L, larger than that fold's entire +21.4131
equity gain. The aggregate domination diagnostic did not trip; this observation
is still a practical robustness concern. Excluding AVAX or BNB based on these
results would be post-hoc symbol selection. Fold 1 also records maximum fill
liquidity participation of 1.6224; capacity should be inspected independently
before claiming executability, even though the recorded stress gate's failure
is already decisive.

### Read-only fold fill audit (2026-09-27)

The fold export has 21, 37, and 39 candidate buy fills. For every buy, a
read-only query reconstructed the raw 20-bar momentum at the preceding
03:59:59.999 UTC decision from the manifest-pinned `decision:15m` series:
only bars available by that decision and retrieved by the manifest knowledge
cutoff were used; each 4-hour bucket required all 16 bars. All 97 buys had 21
complete 4-hour closes. Their nonpositive-momentum counts were **4, 7, and 3**
by fold. The first ADA buy in fold 1 (2026-04-05 04:00 UTC) had momentum
`-0.011222444889779559` and is necessarily a new position in that fold.
This establishes that the proposed rule would exclude at least one actual
new-position buy. It does not establish that excluding it would improve P&L.
The Stage 05 source comparison's bounded factor traces do not align at these
fold decision clocks, and the fold primitive export lacks order factor traces
and fill quantities. Later buys cannot be reliably classified as new positions
versus additions from this export alone. The private
[read-only fill audit](/home/andrei/.bb-machines/192.168.0.115-38886/thread-storage/thr_ig94ijqd28/fold-entry-attribution.json)
retains every buy fill ID, order ID, signal time, raw momentum and linked
closed-trade IDs, without treating linked trade P&L as causal attribution.
Its SHA-256 is
`7c25f6806507dff38b20484443e4c9d819e03a9c81b9b4b02c67eeb4773399ec`.

The maximum participation in fold 1 is the AVAX sell at
`2026-04-03T04:00:00Z`, fill
`fill-stage05-AVAXUSDT-1775188800000000000-3`: notional 74.1381 against
45.69795 of bar-volume-derived quote liquidity, a ratio of 1.622350674.
The pinned 1-minute bar reports 5.19 AVAX base volume. The simulated sell
quantity is 8.42 AVAX (notional divided by the fill price 8.805). The current
v3 simulator fully fills any positive-volume selected bar and Stage 07 records
the participation plus an impact-stress penalty; neither applies a hard
volume cap. Four fold-1 fills exceed 10% participation and one exceeds 100%;
fold 2 has one above 10%, while fold 0 has none. The stored `trade_count=0`
is not evidence of zero source trades because this ingestion path does not
preserve vendor trade count. The 1-minute OHLCV volume is also not order-book
depth, so this audit cannot establish a real exchange outcome. It does show
that the v3 full-fill assumption is too optimistic to claim executable
capacity for this run. A volume-capped/partial-fill execution policy would be
a new economic model and requires a separately versioned manifest and matched
candidate/baseline rerun; it cannot rewrite the immutable v3 result.
The private [capacity audit](/home/andrei/.bb-machines/192.168.0.115-38886/thread-storage/thr_ig94ijqd28/fold-capacity-audit.json)
binds the fold fill ratios and selected bar row; its SHA-256 is
`2bd0a4cf4c5cefa9d5e058df17842f9f98121783ce43190662a33c70b18fef5e`.

## Predeclared bounded hypothesis for the exploratory version

The current combined planner checks positive absolute trend and ranks relative
momentum, but it can select an asset whose own completed 20-bar momentum is
zero or negative. Test exactly one new rule: **an asset is eligible for a new
long target only when its raw 20 completed 4-hour-bar momentum is strictly
positive at the decision time**. Keep the existing absolute-trend check,
ranking, volatility normalization, regime/exposure policy, costs, stops,
rebalancing, risk limits, and final-position policy fixed. Existing holdings
must retain the current explicit exit semantics; a failed new-entry eligibility
check must not silently force liquidation. Use the same positive-momentum
eligibility rule in the matched baseline, so baseline exposure/turnover
comparability is still checked rather than assumed. Zero is a sign boundary,
not a threshold selected from the observed test returns.

This is a mechanism hypothesis, **not** a finding that weak-momentum entries
caused the losses: the persisted fold primitives include fills and trades but
not their entry factor scores. The exported Stage 05 source comparisons for
jobs 81–83 contain an identical bounded sample of 1,024 factor traces (the
source adapter caps this list). In each export, 36 of 290 `selected` trace rows
have nonpositive raw momentum. Within the three test intervals, the counts are
4/38, 8/69, and 10/69 selected trace rows, respectively. These are full-interval
Stage 05 selection observations, **not** a count of executed fold entries or a
causal P&L attribution. In particular, the positive fold also contains ten.
The terminal Stage 07 export has no entry factor traces. At the original
read-only review checkpoint, source jobs 81–83 were not in the local
`trading_bot` database; they are retained in the separate
`trading_bot_research` clone.

The fill audit links causal raw momentum to executed buys and identifies one
unambiguous first-position buy. It does not assign counterfactual P&L: a new
rule changes later portfolio state, and the fold export cannot classify every
subsequent buy as a fresh position or an addition. A fresh replay must measure
those effects. Do not claim the selected-trace count predicts its return.

The read-only fold fill audit above found one unambiguous first-position buy
with negative momentum. Candidate `1.2.0` and matched baseline `1.1.0` now
implement the fixed new-target rule in the shared planner; targeted synthetic
tests exercise exclusion, historical-version preservation, holding/exit
semantics, version/digest separation, and matched comparison wiring. This is
the implementation checkpoint. The one real-data Stage 05 result is recorded
above; no Stage 07 experiment has been completed for these versions.

This is one additional distinct hypothesis in the append-only research ledger,
alongside all prior configurations and failures. The replay on the already
inspected 2024-12 to 2026-09 dataset is exploratory and cannot become a fresh
confirmatory holdout. The Stage 07 windows, sample/cost and statistical gates
were not changed; the two reserved B/C source slots are not independent trials,
and historical source digests cannot be reused for a changed rule.
Before a promotion claim, predeclare and acquire genuinely later unseen data,
lock a new single-use holdout, and obtain the required human approval. This
diagnostic and the single source result do not authorize promotion.
