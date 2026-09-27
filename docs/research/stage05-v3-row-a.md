# Fixed row A under execution v3 — exploratory result

Job **66** completed on 2026-09-27 in the isolated PostgreSQL 16 research
clone. The executable checkout was clean at
`ad775fdb6af118d6f8a55db09aaeb22a0b20f9a6`; the pinned interval is
`[2024-12-01, 2026-09-01)`. This result uses already-seen research data and
is not confirmatory or promotion evidence.

## Reproducibility gate update

### Repaired checkpoint: exact retained-artifact reproduction passed

Replacement jobs **69, 70, and 71** completed from clean revision
`5c756fa14ddd53cdc7d8b6acd027760a6a7842b0` with the unchanged frozen request.
All three comparison exports (1,991,405 bytes) and compact source exports
(2,515,830 bytes) are byte-for-byte identical. Canonical comparison digest:
`8967b6cf813206cdf2095a7aade61795a91449b3addc8bda4b89ccbf84f44cd0`;
canonical source digest:
`6b4850c9e7eee66fc0ad0b070a0a0294dbea35d85f2169e4398dcfcf05e5b095`.
All seven rows and four embedded sensitivities report reconciliation.
Candidate and matched baseline returns remain +2.9500954783% and
+0.8515087519%, respectively. Full raw fill, ledger, order, and curve
histories are omitted from compact sources and are not established by this
comparison. These repeats are technical reproducibility checks, not new
independent statistical observations.

The three replacement slots are consumed. Stage 07 read-only preparation
passed and one exploratory execution was attempted, as recorded below.
The failed original attempts below are retained without replacement.

### Stage 07 attempt: training replay boundary failure

Experiment `2c1ab23be00a734b6f53224b7a65f79725e88dc63ed91a71a6fb6064a92dcc63`
used driver `cc7ac6272d886a74c762d29ca1d3ab42280fecb9` and reviewed plan SHA-256
`e6341ba2afc1827345f77a24ea91b2f0284e98a0bd85d11fef3ce036518b0356`.
It failed before producing any fold evidence with `invalid_manifest`:
`execution_bar_liquidity_unavailable`, selected execution bar after
`2025-11-30T23:59:59.999Z` missing for the candidate.
The immutable failed evidence digest is
`807a749f860b74a9051ac953022d6e4a3d3b4aff1903271f9563a79705b69209`.

No paired test-window returns or statistical gate outcomes were produced.
Read-only inspection found positive-volume bars at the excluded training
end boundary. A boundary-handling regression investigation is pending;
this is not evidence of statistical failure or missing market data.
No retry, threshold change, holdout use, or promotion was performed.

### Original checkpoint: jobs 66–68 failed reproduction

The first provenance repeat, job **67**, completed but did **not** reproduce
job 66's canonical artifact digests. An exact recursive JSON comparison found
43 comparison leaves and 15 source leaves differing: artifact/sensitivity
digests, exposure diagnostics, and last-bit derived metrics including Sharpe,
Sortino, exposure, and drawdown. The retained source trade lists, no-fill
records, configuration identities, returns, and economic totals match. The
compact source omits full fill/ledger/curve histories, so matching retained
fields is not proof that every intermediate economic state matched.

**These original sources failed exact reproducibility and cannot supply Stage 07.**
Job 68 had already been submitted sequentially before this audit detected the
difference and completed as diagnostic evidence. It differs from job 66 in
42 comparison leaves and 16 source leaves, and from job 67 in 34 and 12.
Its comparison digest is `55531593ce08517c5fa1d764baa16c3db4099f5b1c820f101f1b869fc327a5a8`
and source digest is `efc8bd8e4c52ee1aae885c0a9f43d3978ca73714673f5a32f42ce5bdba26e9c4`.
The same retained-field equality and omitted-history limitation apply.
No rounding, digest
tolerance, or retrospective replacement of these artifacts is permitted.
Pure regressions reproduced order-dependent floating-point accumulation in
portfolio equity, exposure, and planner weights. The repaired implementation
uses stable economic-symbol order. Planner quantities can change at exact
thresholds, so fresh sources under a new committed revision are required.
The row-66 technical audit and historical performance below do not constitute
a passed reproducibility or promotion gate.

## Results after costs

| Strategy | Return | Max drawdown | Average gross exposure | Trades | Simulated no-fills |
|---|---:|---:|---:|---:|---:|
| Fixed candidate #42 | +2.9501% | 13.9097% | 10.2398% | 283 | 2 |
| Matched momentum baseline | +0.8515% | 14.2995% | 10.1433% | 281 | 2 |
| Cash | 0% | 0% | 0% | 0 | 0 |
| BTC buy and hold | −14.1554% | 42.8475% | 73.0649% | 1 | 0 |
| Benchmark trend | −88.1692% | 89.8137% | 38.5567% | 908 | 0 |
| Cross-sectional momentum | −65.9154% | 72.8987% | 56.2991% | 935 | 4 |
| Equal-weight universe | −54.8726% | 68.7030% | 74.6402% | 948 | 1 |

Candidate minus matched-baseline return is **+2.0986 percentage points**.
Their average gross exposures differ by **0.0965 percentage points** and
turnover ratios by **1.2763% relative** (43.1244 versus 42.5810). Both have
589 economic fills. All seven strategy rows reconcile. The other simple
baselines have substantially different achieved exposure and turnover; their
raw returns do not establish an exposure-matched component effect.

The four fixed embedded sensitivity rows also reconcile:

| Embedded configuration (strategy 1.0.0) | Return after costs | Trades |
|---|---:|---:|
| absolute-20-24h | −19.1184% | 307 |
| relative-30-24h | −46.3212% | 424 |
| combined-30-24h | −19.3702% | 514 |
| combined-vol-20-48h | +2.9501% | 283 |

These are evaluated configurations in the research lineage. They were not
selected after seeing this run. The losses limit any claim of robustness
across configurations. Repeating this comparison creates provenance records,
not new independent return observations.

## Execution and integrity

The candidate and matched baseline each record two approved but unfilled
AVAX sell intents, on 2026-01-24 at 08:00 UTC and 2026-02-14 at 04:00 UTC.
Across all strategy rows, nine no-fill records bind seven independently
checked zero-volume, zero-trade bars. Each no-fill has positive approved
quantity, zero filled quantity, and evaluation at the selected minute close.
These are explicit simulation outcomes, not actual exchange rejections.

The policy is `backtest-execution-v3` with
`selected_zero_base_volume_cancel_at_bar_close_v1`. Dataset and fixed
configuration match historical jobs 63–65; implementation and run identities
are different. The historical return has now been reproduced under the new
policy, but Stage 07 fresh fold replay and its gates are still pending.

| Identity | Digest |
|---|---|
| Dataset manifest | `32fe7e3664ab9d1a14c7cff30c0db6dc673f37ca0eea0479ed9cd812011ec9bc` |
| Frozen request | `1ea94986c14c5822fe4af8e1dcc954c97bc40438b5d73e2511208da750c4149a` |
| Comparison artifact | `5cc5a773da15e1087137cf2e4ddd47054bd5915d37d4885ec7627e545309fdfe` |
| Source artifact | `af899417a126620de912e9d0fcd88cf746a924f1ec44e55b1da87c82285cca86` |
| Replay settings | `3727c3851c924ee56182fabfeadb4c300e04edd3646d406c7b40837c5c0dce31` |

The protected source/comparison JSON, audit, raw bar check, and attempt ledger
are retained under the research-lab thread storage. No production jobs,
settings changes, deployment, or holdout use were performed. Governance marks
research optimization allowed and promotion blocked pending Stage 07.

Next: freeze the repaired revision and run the bounded technical reproduction
program before any exploratory Stage 07 manifest. See the
[bounded program](../operations/stage05-07-next-exploratory-iteration.md).
