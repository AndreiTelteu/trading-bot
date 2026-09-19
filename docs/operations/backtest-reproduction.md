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

For performance investigation, set `BACKTEST_PPROF_ADDR` on a controlled, non-public listener and collect CPU/heap profiles from the standard Go `/debug/pprof/` endpoints. Profiling is operator telemetry only and must not change run inputs or be presented as strategy evidence.

After the engine starts, a manifest-backed replay must not generate a
sustained stream of `historical_bars` or `symbol_constraint_versions` queries.
Constraints and market-only signal contexts are loaded or computed once before
the chronological replay. Sustained database reads during `engine/*` indicate
a performance regression; stop and investigate the run rather than waiting
days for invalidly repeated dataset verification.

Copy the exact `reproduce` invocation from the immutable Stage 07 manifest when validating promotion evidence. Compare artifact/manifest digests and classifications. `coverage_failed`, `gating_zero_trades`, and `strategy_zero_trades` have different meanings and are exposed separately by operational status. A completed command is not evidence of profitability or promotion eligibility.

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

The default stale threshold is 30 minutes and can be changed with `-stale-after`. The ETA is observational. It starts as `n/a` and is calculated only after the running monitor observes the next progress update, using the measured wall-clock interval and progress delta between those two updates. It counts down every second and is recalculated on subsequent updates; phase/lane changes reset it to `n/a`. Monitoring only reads run artifacts and does not affect deterministic execution or evidence.
