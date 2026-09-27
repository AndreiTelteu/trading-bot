# Stage 07 negative-window diagnostic and next research hypothesis

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

## One bounded hypothesis for the next *exploratory* version

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
The terminal Stage 07 export has no entry factor traces; the isolated
`trading_bot_research` clone was no longer present at this review checkpoint,
and source jobs 81–83 were not in the local `trading_bot` database.

Before implementation, link point-in-time factor observations to actual new
entry intents in a read-only, causally reconstructed fold trace and report
their count and outcomes. If none exist, reject the hypothesis. Otherwise
implement it as a new strategy version with tests of the shared decision path,
signal timing, stable entry identities, no-trade outcomes, and matched-baseline
comparability. Do not claim the selected-trace count predicts the return of
that version.

Record this as one additional distinct hypothesis in the append-only research
ledger, including all prior configurations and failures. Any replay on the
already inspected 2024-12 to 2026-09 dataset is exploratory and cannot become
a fresh confirmatory holdout. Keep the same Stage 07 windows, sample/cost and
statistical gates for comparison; do not use the two reserved B/C source slots
as independent trials or reuse historical source digests for a changed rule.
Before a promotion claim, predeclare and acquire genuinely later unseen data,
lock a new single-use holdout, and obtain the required human approval. No new
job, experiment, holdout, or promotion is initiated by this diagnostic.
