# Stage 04 dataset coverage and backfill

External ingestion availability is not guaranteed. Always start with a bounded dry run and explicit half-open UTC interval:

```bash
export DATABASE_URL='postgres://USER:PASSWORD@HOST:5432/trading_bot?sslmode=require'
export STAGE08_POINT_IN_TIME_UNIVERSE=research
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/marketdata \
  -action ingest -dataset-version vendor-2026-07 -symbol-id binance-btcusdt-v1 \
  -symbol BTCUSDT -timeframe 15m -role decision \
  -start 2026-07-01T00:00:00Z -end 2026-07-02T00:00:00Z -dry-run=true
```

After reviewing provenance/row bounds, repeat with `-dry-run=false`, build a manifest with an explicit `-knowledge-cutoff`, then inspect it:

```bash
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/marketdata -action coverage \
  -manifest-id MANIFEST_ID -start 2026-07-01T00:00:00Z -end 2026-07-02T00:00:00Z \
  -symbol-id binance-btcusdt-v1 -timeframe 15m -role decision
```

Build universe ranges first with `-dry-run=true`; supply the actual benchmark symbol/asset IDs and policy version. Missing benchmark, constraints, bars, or incomplete membership is a coverage failure, not an empty valid strategy result. Corrections require a new dataset version/manifest; never overwrite immutable history.

A newer versioned exchange-symbol identity may provide earlier public
lifecycle evidence for the same stable asset. Manifest construction uses the
earliest immutable asset-or-symbol availability evidence and binds its matching
retrieval timestamp into the new digest. It never updates the stable asset row
or rewrites an older manifest.

The range command validates the immutable manifest once, bulk-loads only the required decision/benchmark series for the bounded range plus the 90-day lookback, and reuses binary-searched in-memory windows for each snapshot. Keep operational ranges bounded to control peak memory; checkpoint resume starts loading from the first unresolved timestamp rather than repeating the completed prefix.

## Research-readiness preflight (Stage 04/05/07)

Promotion-quality research needs more than a syntactically valid manifest. The
default immutable readiness policy requires 12 training months and three
separate 3-month test folds (21 complete calendar months), at least eight
point-in-time tradable symbols **in addition to** the independent benchmark,
`decision:15m` plus `execution:1m` evidence
for every candidate and the independent benchmark, executable constraints,
fresh metadata, and complete universe observations across at least two
regimes. It also checks per-symbol row counts, universe gaps, candidate and
shortlist capacity, and regime sample counts. These checks are evidence gates,
not forecasts of profitability.

First import a saved, provenance-bearing metadata envelope for the broader
historical universe. It must contain actual symbol lifecycles, tradability
intervals, and constraint versions; do not construct it from today's exchange
listing.

```bash
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/marketdata \
  -action import-metadata -metadata-file /secure/operator/historical-universe-v1.json \
  -start 2024-01-01T00:00:00Z -end 2025-10-01T00:00:00Z -dry-run=true
```

After reviewing the dry-run output, repeat with `-dry-run=false`. For every
reviewed point-in-time symbol ID, ingest a `decision:15m` series followed by
an `execution:1m` series. Ingest the benchmark as `benchmark:15m` and
`execution:1m`; it is not thereby made tradable. For example:

```bash
# Repeat both candidate commands for every stable historical symbol identity.
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/marketdata \
  -action ingest -dataset-version vendor-2025-10-v1 -symbol-id binance-ethusdt-v1 \
  -symbol ETHUSDT -role decision -timeframe 15m \
  -start 2024-01-01T00:00:00Z -end 2025-10-01T00:00:00Z -dry-run=false
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/marketdata \
  -action ingest -dataset-version vendor-2025-10-v1 -symbol-id binance-ethusdt-v1 \
  -symbol ETHUSDT -role execution -timeframe 1m \
  -start 2024-01-01T00:00:00Z -end 2025-10-01T00:00:00Z -dry-run=false
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/marketdata \
  -action ingest -dataset-version vendor-2025-10-v1 -symbol-id binance-btcusdt-v1 \
  -symbol BTCUSDT -role benchmark -timeframe 15m \
  -start 2024-01-01T00:00:00Z -end 2025-10-01T00:00:00Z -dry-run=false
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/marketdata \
  -action ingest -dataset-version vendor-2025-10-v1 -symbol-id binance-btcusdt-v1 \
  -symbol BTCUSDT -role execution -timeframe 1m \
  -start 2024-01-01T00:00:00Z -end 2025-10-01T00:00:00Z -dry-run=false
```

Build a new immutable manifest, then build the universe range under the
governed policy. Do not fill an unresolved period in place: obtain source
evidence under a new dataset version and rebuild.

```bash
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/marketdata \
  -action build-manifest -dataset-version vendor-2025-10-v1 \
  -start 2024-01-01T00:00:00Z -end 2025-10-01T00:00:00Z \
  -knowledge-cutoff 2025-10-02T00:00:00Z
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/marketdata \
  -action build-universe-range -manifest-id MANIFEST_ID -policy-version GOVERNED_POLICY_ID \
  -benchmark-symbol-id binance-btcusdt-v1 -benchmark-asset-id bitcoin-asset-id \
  -start 2024-01-01T00:00:00Z -end 2025-10-01T00:00:00Z -step 24h -dry-run=false
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/marketdata \
  -action readiness -manifest-id MANIFEST_ID \
  -symbols ETHUSDT,SOLUSDT,BNBUSDT,XRPUSDT,ADAUSDT,DOGEUSDT,AVAXUSDT,LINKUSDT \
  -benchmark-symbol BTCUSDT -timeframe 15m \
  -start 2024-01-01T00:00:00Z -end 2025-10-01T00:00:00Z
```

For `scripts/backtest_init.sh`, pass those eight candidate symbols through
`BACKTEST_INIT_SYMBOLS` and pass `BTCUSDT` separately through
`BACKTEST_INIT_BENCHMARK_SYMBOL`. The launcher ingests benchmark decision and
execution evidence but never places the benchmark in the tradable universe.

The last command prints a machine-readable `research-readiness-report-v1` and
exits `2` when `passed` is false. Typical failures are
`calendar_span_insufficient`, `fold_count_insufficient`,
`execution_sample_insufficient`, `constraints_incomplete`, `metadata_stale`,
`universe_snapshot_gap`, and `regime_sample_insufficient`. They are evidence
deficits, not permission to reduce a requirement. The
`research_readiness_*` namespace is authority-affecting and rejected by the
generic settings API; record any approved policy revision in a new research
experiment/configuration identity.
