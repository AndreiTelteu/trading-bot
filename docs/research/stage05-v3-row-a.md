# Fixed row A under execution v3 — exploratory result

Job **66** completed on 2026-09-27 in the isolated PostgreSQL 16 research
clone. The executable checkout was clean at
`ad775fdb6af118d6f8a55db09aaeb22a0b20f9a6`; the pinned interval is
`[2024-12-01, 2026-09-01)`. This result uses already-seen research data and
is not confirmatory or promotion evidence.

## Reproducibility gate update

The first provenance repeat, job **67**, completed but did **not** reproduce
job 66's canonical artifact digests. An exact recursive JSON comparison found
43 comparison leaves and 15 source leaves differing: artifact/sensitivity
digests, exposure diagnostics, and last-bit derived metrics including Sharpe,
Sortino, exposure, and drawdown. The retained source trade lists, no-fill
records, configuration identities, returns, and economic totals match. The
compact source omits full fill/ledger/curve histories, so matching retained
fields is not proof that every intermediate economic state matched.

**Exact reproducibility has failed; Stage 07 and B/C submissions are blocked.**
Job 68 had already been submitted sequentially before this audit detected the
difference and is retained as diagnostic evidence. No rounding, digest
tolerance, or retrospective replacement of these artifacts is permitted.
The suspected cause is order-dependent floating-point accumulation; a
regression and code fix are required before a new source checkpoint is frozen.
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

Next: two exact row-A source repetitions, then one new exploratory Stage 07
manifest with the predeclared windows and strict gates. See the
[bounded program](../operations/stage05-07-next-exploratory-iteration.md).
