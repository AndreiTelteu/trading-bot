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
end boundary. The failure was not evidence of statistical failure or missing
market data.
No retry, threshold change, holdout use, or promotion was performed. A later
committed half-open boundary repair at `7e8eadb5eec18fb69837f682d2de8d3033ec269e`
passed a private read-only fold-0 train diagnostic (164 trades, zero no-fills)
without registering evidence. Fresh source jobs #72 and #73 at that revision
have exactly equal canonical comparison and compact-source digests; a third
repeat remains pending. The failed experiment above remains immutable, and the
new Stage 07 attempt requires a fresh idempotency key and new family lineage.

The boundary repair was committed as
`7e8eadb5eec18fb69837f682d2de8d3033ec269e` and passed serial PostgreSQL
regressions. A private harness with SHA-256
`ac46fdcac4ce7701142f53cde971a473230f20af5536bedeaa3d5cda90899cfe`
then replayed only fold-0 training in one read-only clone transaction.
It passed in 406.39 seconds with 164 trades and zero no-fills; the original
boundary error was absent. Source 69 supplied historical configuration/data
provenance explicitly under a different executable revision. This diagnostic
does not establish validation/test outcomes or replace authoritative evidence.
Fresh sources are required under the bounded boundary-repair program.

### Boundary-repair sources and second Stage 07 attempt

Sources 72–74 at `7e8eadb` produced byte-identical retained comparison and
compact source exports. Their canonical source digest is
`d73087603cc8158756670939208fe9e51bf2418e5420d854c774236ae1edab3a`;
the comparison digest remains `8967b6cf813206cdf2095a7aade61795a91449b3addc8bda4b89ccbf84f44cd0`.
During source 74, its shared checkout changed after job submission; only
driver/documentation files changed and the already-compiled process continued.
Exact build/precheck timestamps were not retained. The audit preserves this
provenance limitation and a hash-linked correction; subsequent execution
uses a dedicated lab-owned checkout.

The next reviewed exploratory experiment,
`4cceee430468b9515c60033e1ca5edac0ab46289a47761e9d9ece441918264b4`,
used clean driver `46fe2e1e0e8fa485fdd4a478ba5a4868677b9dc4` and plan digest
`69303ad9a2f23267a91ade23ea585a7d72f06d3a511636cac5e75121386a27f0`.
It failed with `achieved_allocation_out_of_bounds` for the matched baseline:
`achieved=0.000088288074822941 target=0 tolerance=0.02`.
Failed evidence digest:
`17e83693dc6fb775081eee7e05d5adb1be46e7e31d6ecdd92fb86a80305201d3`.

The call path identifies baseline test replay, after fitting/selection and
the failing fold's candidate replay. The fold index, symbol, and timestamp
are not present in the diagnostic. Fold results persist only after the
whole walk-forward succeeds; zero persisted folds therefore does not locate
the failure or establish that earlier tests failed. No paired statistics or
promotion-gate outcomes are available. Residual-allocation investigation is
pending; no threshold relaxation or retry is authorized.

The private read-only allocation trace subsequently identified fold 1,
2026-04-03 04:00 UTC: an AVAX sell of 8.42 left approximately 0.01 AVAX
worth 0.0881 USDT, below the 5 USDT minimum notional. No no-fill or rejection
occurred. The matched baseline skipped post-fill residual evidence because
of its strategy-version guard. Fix `149747021a7e40bf76c0a9c295466470e2770618`
shares the corresponding candidate rule; executable leftovers still fail.

A single private read-only diagnostic under that committed repair passed
all three frozen test-window replay pairs and primitive reconciliation:

| Fold | Observations | Candidate trades | Fills | Candidate / baseline no-fills | Residual positions |
|---|---:|---:|---:|---:|---:|
| 0 | 45,184 | 18 | 39 | 1 / 1 | 4 |
| 1 | 46,720 | 35 | 72 | 0 / 0 | 3 |
| 2 | 47,360 | 35 | 74 | 0 / 0 | 5 |

Diagnostic log SHA-256:
`861cd258eec1679df3229c78828d1db208479179b597f8819a2ef56f96dafdfc`.
This omitted fitting/selection, bootstrap, promotion gates, and registration;
source 72 was historical configuration/data provenance only. Temporary test
and binary were removed and the dedicated checkout remained clean. Selected
clone metadata counts were unchanged, which is not a full database fingerprint.
Fresh authoritative sources and a new reviewed exploratory attempt remain
necessary under the declared residual-repair budget.

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
