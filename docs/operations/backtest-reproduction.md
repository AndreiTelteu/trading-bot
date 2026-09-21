# Stage 05/06 backtest reproduction

Pin the database, manifest-backed settings, UTC interval, costs, registered strategy identity/version, exposure, and final policy. Example Stage 06 candidate reproduction:

```bash
export DATABASE_URL='postgres://USER:PASSWORD@HOST:5432/trading_bot?sslmode=require'
export STAGE08_NEW_BACKTEST=research
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/backtest \
  -symbols BTCUSDT,ETHUSDT -start 2025-01-01 -end 2025-06-30 \
  -fee-bps 10 -slippage-bps 5 -universe-mode dynamic_replay \
  -validation-train-months 12 -validation-test-months 3 \
  -validation-bootstrap-iterations 500 \
  -strategy trend_momentum_candidate -strategy-version 1.0.0 \
  -target-gross 1 -max-net 1 -final-policy liquidate
```

The command runs a fail-closed validation-window preflight after the manifest-backed interval is resolved and before either baseline or volatility-sizing lane starts. The interval must contain at least one complete walk-forward window. With the governed `12` training months plus `3` test months policy, `2025-01-01T00:00:00Z` requires an end at or after `2026-04-01T00:00:00Z`; warmup history does not count toward this validation interval. The CLI flags above override only the run snapshot, and the effective train/test/bootstrap policy is included in the versioned run manifest.

For a Stage 05/06 candidate comparison, the stricter research-readiness
preflight runs before either comparison lane. Its default requires three
independent folds, so the same start needs an end at or after
`2026-10-01T00:00:00Z` (21 calendar months), 1-minute execution evidence, and
a complete point-in-time universe. Run the `marketdata -action readiness`
command in [dataset coverage](dataset-coverage.md) first; a failed readiness
report is not a zero-trade result and cannot be bypassed with generic settings.

`scripts/backtest_init.sh` keeps the governed defaults and exposes explicit launcher inputs through `BACKTEST_INIT_VALIDATION_TRAIN_MONTHS`, `BACKTEST_INIT_VALIDATION_TEST_MONTHS`, and `BACKTEST_INIT_VALIDATION_BOOTSTRAP_ITERATIONS`. It performs the same calendar-month interval check before market-data ingestion, universe construction, or replay, while the Go preflight remains authoritative after manifest resolution. Do not reduce the first two for promotion evidence; extend the immutable dataset instead.

When `BACKTEST_INIT_REQUIRE_RESEARCH_READINESS=1`, the launcher also rejects
the request before ingestion unless it contains at least eight tradable
symbols in `BACKTEST_INIT_SYMBOLS`, keeps the independent benchmark in
`BACKTEST_INIT_BENCHMARK_SYMBOL` (default `BTCUSDT`), and has at least 45
warmup days for the governed listing-age filter. Use a new positive
`BACKTEST_INIT_SYMBOL_IDENTITY_VERSION` when corrected lifecycle evidence
changes a symbol identity. The bootstrap records the earliest Binance daily
kline as public lifecycle evidence instead of inventing the listing date from
the requested research window. The readiness gate measures
eligible ranked membership before intentional regime-based shortlist
contraction; a risk-off shortlist of two is valid only when the underlying
point-in-time universe still has the governed minimum capacity.

After coverage succeeds, the launcher binds the exact evaluation start, end,
and tradable symbol set to the research settings together with the manifest.
These bounds deliberately exclude the manifest's warmup prefix. The web
readiness gate must use the evaluation interval; treating the manifest's
earlier warmup boundary as the experiment start creates a false initial-gap
failure because universe snapshots begin at the evaluation boundary.

For performance investigation, set `BACKTEST_PPROF_ADDR` on a controlled, non-public listener and collect CPU/heap profiles from the standard Go `/debug/pprof/` endpoints. Profiling is operator telemetry only and must not change run inputs or be presented as strategy evidence.

After the engine starts, a manifest-backed replay must not generate a
sustained stream of `historical_bars` or `symbol_constraint_versions` queries.
Constraints and market-only signal contexts are loaded or computed once before
the chronological replay. Sustained database reads during `engine/*` indicate
a performance regression; stop and investigate the run rather than waiting
days for invalidly repeated dataset verification.

Copy the exact `reproduce` invocation from the immutable Stage 07 manifest when validating promotion evidence. Compare artifact/manifest digests and classifications. `coverage_failed`, `gating_zero_trades`, and `strategy_zero_trades` have different meanings and are exposed separately by operational status. A completed command is not evidence of profitability or promotion eligibility.

## Self Improvement operator interface

The authenticated `/self-improvement` route is the guided research surface for
dataset readiness, registered candidate configuration, Stage 05/06 jobs,
baseline-relative results, proposal generation, and rollout visibility. Its
readiness panel calls the read-only `GET /api/market-data/readiness` gate. A
failed evidence gate is returned as a machine-readable report and disables new
candidate submission; it is not converted into an empty or successful run.

The interface never receives migration credentials, ingests historical data,
changes authority-affecting settings, or promotes a candidate. Data expansion
remains a bootstrap/operator workflow, and shadow/paper/live transitions remain
separate authenticated governance actions backed by immutable evidence.

The Experiment Builder can ask the configured LLM for up to eight distinct
advisory parameter sets. The server forces the non-capital `backtest` intent,
validates every draft through the registered strategy descriptor, rejects
duplicates, and shows the exact parameter differences before an operator may
submit the selected set. Batch submission queues every selected experiment;
it does not grant the LLM execution or promotion authority.

Stage 05 workers are bounded by `BACKTEST_MAX_CONCURRENT_JOBS` (default `1`,
clamped to `1..8`). Submitting a batch means "queue all", not "allocate
unbounded memory to all". Keep the default while one job consumes multiple
gigabytes. Increase it only after measuring p95 peak RSS and leaving sufficient
headroom for the server, database, and Go garbage collector.

Point-in-time runtime bars are cached read-only inside the backend process and
shared by jobs only when manifest content identity, dataset version, knowledge
cutoff, exact series, interval, and as-of time are identical. Concurrent cold
requests use one loader; failed loads are never cached. Retention is bounded by
`BACKTEST_DATASET_CACHE_MAX_BYTES` (default 4 GiB, `0` disables it), with LRU
eviction. A backend restart intentionally starts cold. This cache is not a new
data authority: PostgreSQL and the immutable manifest remain authoritative.

## Monitor a file-backed CLI run

The monitor discovers runs under `instance/backtest-init`, prefers the newest active run, follows its initialization log and later engine telemetry, and refreshes an ASCII progress bar and ETA once per second:

```bash
podman exec -it trading-backend go run ./cmd/backtest-monitor latest
```

Discovery also includes a run that is still ingesting data and therefore has
only `backtest_init.log`. During that phase the monitor shows the latest
timestamped initialization step with an unknown ETA, ignores verbose database
output, and automatically consumes engine progress from the same log once the
deterministic replay starts. The command therefore works immediately after the
background launcher creates the run directory; `backtest.json.raw` does not
need to exist yet.

List discovered active, stale, failed, and completed runs without following one:

```bash
podman exec trading-backend go run ./cmd/backtest-monitor list
```

The default stale threshold is 30 minutes and can be changed with
`-stale-after`. Each telemetry record contains an RFC3339-nanosecond
`emitted_at` timestamp. The monitor reconstructs an ETA immediately from
existing timestamped records, exponentially smooths subsequent throughput
samples, and counts down between updates. Pre-timestamp logs remain supported
through their monotonic `elapsed_ms` values. Phase/lane changes reset the rate;
`n/a` is expected only until two increasing samples exist for the current
track. Engine telemetry is rate-limited to roughly ten seconds, so a new run
normally acquires its first ETA quickly without producing per-bar log noise.
Monitoring only reads run artifacts and does not affect deterministic
execution or evidence.
