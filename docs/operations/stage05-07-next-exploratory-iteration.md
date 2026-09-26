# Stage 05/07 next exploratory iteration (predeclared, 2026-09-26)

## Stage 07 clone driver checkpoint (2026-09-27)

`cmd/researchlab` has an explicit `-stage07-mode prepare|run` path for one **new
exploratory** validation manifest. The current `ad775fd` source pins are a
deliberate fail-closed placeholder: jobs #66–68 are diagnostic attempts after
the #66/#67 exact-repeat digest mismatch, and no Stage 07 preparation or run
is authorized. A deterministic replay fix and new audited source checkpoint
must precede updating the driver pins and reviewing an exact plan. The driver
is restricted to the same marked,
runtime-only PostgreSQL 16 research clone as the Stage 05 source runner. It
does not start the server or schedulers, use migration/ledger/parity credentials,
touch the production database, or consume a confirmatory holdout. The mode is
code only at this checkpoint: no Stage 07 prepare or run command has been
executed. A human must review the exact private plan and its SHA-256 before
invoking `run`.

The driver reads the byte-pinned prior exploratory spec (SHA-256
`a3b0d140308a1c383882012c92a4937df2da96908e5e4b6215bb037ae02e95c7`),
then admits exactly three distinct new, completed Stage 05 source jobs. Each
source must retain the candidate, baseline, configuration, dataset, and source
code revision `ad775fdb6af118d6f8a55db09aaeb22a0b20f9a6`. The v3
candidate and baseline implementation digests are taken from audited job #66
and required on both exact repeats; the old v1 implementation digests are
historical provenance, not the v3 executable identities.
The plan carries their full comparison references, run-manifest digests,
primitive validation-artifact digests, and the SHA-256 of the ordered reference
array used by repository registration. `Stage07ExperimentSource.Load` verifies
the actual source artifacts and replay contract before either plan creation or
execution. It requires `execution_semantics.execution_policy_version` equal to
`backtest-execution-v3` and `execution_semantics.no_fill_rule` equal to
`selected_zero_base_volume_cancel_at_bar_close_v1`, consistent with the source
and replay manifests. The new spec also changes `policies.execution` and the
authority envelope's `execution_policy` to v3, with a new policy-bundle label
and authority digest. The source `liquidity=full_fill_ohlcv` semantic remains
unchanged; the explicit no-fill rule describes the zero-volume exception.
Its old fold boundaries, gates, sample requirements, bootstrap 500, cost 10/5
bps, capacity stress, and comparability bounds remain pinned. A new family ID
is derived from the new implementation and composite policy; the old family ID
is retained in the private plan and ledger as research lineage. The old
`confirmatory_holdout` remains absent.

Jobs #66–68 retain `ad775fd` as historical provenance. The new manifest's
`code_revision` and replay environment must instead equal the **actual clean
committed driver SHA**. The driver rejects every source-to-driver diff path
outside `cmd/researchlab/**` and `docs/**`. A separately committed Stage 07
loader metadata repair restores the fixed v3 `NoFillRule` omitted by the
point-in-time constructor before requiring full equality with the source
policy. It never copies a rule from source evidence or overwrites a conflicting
nonempty rule. That repair belongs in the **next source revision**, not as an
exception allowing use of `ad775fd` sources. Until the deterministic replay
fix, loader repair, new source SHA, and audited implementation digests are
pinned together, the current driver's lineage guard stops preparation.

After the new three source IDs and digests are final and driver pins updated,
the operator can prepare a
private mode-0600 plan. These variable names denote paths and identities only;
never print the runtime DSN file. Use a new bounded idempotency key and an
explicit research creator. The append-only mode-0600 ledger records the plan,
execution intent, registration, and outcome, including failures. Preparation
creates no experiment or validation job.

```bash
export DATABASE_URL_FILE="$CLONE_RUNTIME_DSN_FILE"
export STAGE08_NEW_BACKTEST=research BACKTEST_MAX_CONCURRENT_JOBS=1
export BACKTEST_CODE_REVISION="$(git rev-parse HEAD)"
go run ./cmd/researchlab \
  -clone-marker-file "$CLONE_MARKER_FILE" \
  -reviewed-code-sha "$BACKTEST_CODE_REVISION" \
  -ledger-file "$PRIVATE_ATTEMPT_LEDGER" \
  -stage07-mode prepare \
  -stage07-old-manifest-file "$PINNED_OLD_SPEC_FILE" \
  -stage07-source-job-ids "$NEW_AUDITED_SOURCE_JOB_IDS" \
  -stage07-creator "$RESEARCH_CREATOR" \
  -stage07-idempotency-key "$FRESH_STAGE07_KEY" \
  -stage07-plan-file "$PRIVATE_REVIEWED_PLAN_FILE"
```

Only after reviewing the exact plan, source references, and ledger, invoke
the one-shot execution with its reported plan SHA-256. The plan's `reproduce`
field records this CLI mode and the actual driver SHA; the reviewed plan hash
is supplied separately to avoid a self-referential digest.

```bash
go run ./cmd/researchlab \
  -clone-marker-file "$CLONE_MARKER_FILE" \
  -reviewed-code-sha "$BACKTEST_CODE_REVISION" \
  -ledger-file "$PRIVATE_ATTEMPT_LEDGER" \
  -stage07-mode run \
  -stage07-plan-file "$PRIVATE_REVIEWED_PLAN_FILE" \
  -stage07-plan-sha256 "$REVIEWED_PLAN_SHA256"
```

The run mode rechecks plan bytes, pinned spec derivation, live source
references, clone identity, clean Git identity, and source replay before
registering through `validation.Repository.CreateManifestAuthenticated` with
the explicit creator and idempotency key. It then calls one
`validation.JobService.Run`. A key already registered is refused so a retry
cannot silently create another validation outcome. An incomplete or failing
result remains a recorded failed exploratory attempt, not promotion evidence.

This is a **research-only** plan at code HEAD
`5e51cec7b4715ee06a1d3fa8bfeedd3533d617aa`. It does not authorize a
production job, a changed execution policy, a dataset edit, a confirmatory
holdout use, or promotion. The first executable action is the read-only
preflight below. Stop before replay if its facts remain unresolved.

## Implementation and isolation update (2026-09-27)

The operator selected normal-cadence reevaluation. Opt-in v3, strict Stage 07
no-fill evidence, and the runtime-only research runner are integrated at
`fa63480e4589de83ec68e8b587c2337bf9ece8cb`. The earlier v2 stop below remains
historical context; no bar, interval, or candidate parameter was changed to
remove that failure. Integrated verification used PATH Go 1.26.6 because the
documented Go 1.26.1 binary was absent. The full `go test -p 1 -count=1 ./...`
run used an explicit PostgreSQL 16 test DSN on port 5433: all packages passed
except three AI endpoint fixtures that omitted the empty digest field during
hashing. The fixture-only correction preserves production verification; the
entire `internal/testing` package then passed in a separate serial rerun.
`go vet ./...` and `git diff --check` passed. The first isolated v3 source
submission is cleared after committing this checkpoint.

The separate PostgreSQL 16 clone is `trading_bot_research` on loopback port
5544. Restore exited successfully; the source custom archive and restored
archive copy both hash to
`bf3937413759609047f51f9096212d0c1ccee31dfd95a7935e1b9b2316624fa1`.
Read-only checks matched the pinned manifest, the AVAX minute, pre-cutoff v2
bar counts (10,241,721), and schema catalog counts (65 tables, 387 indexes,
155 constraints). These are restore checks, not a full canonical row-by-row
fingerprint. Readiness passed for the declared interval, three folds, 639
universe snapshots, eight tradables, and BTC. No source job has been submitted
at this checkpoint, and no post-cutoff outcomes or holdout were inspected.

## Frozen lineage and what the prior runs mean

The pinned Stage 04 manifest ID and content hash are both
`32fe7e3664ab9d1a14c7cff30c0db6dc673f37ca0eea0479ed9cd812011ec9bc`;
its dataset version is
`binance-spot-research-21m-8tradable-btc-1m-20260920-v2`. The evaluation
interval is `[2024-12-01T00:00:00Z, 2026-09-01T00:00:00Z)`, with eight
tradable symbols and independent BTC benchmark, 15-minute decisions and
1-minute execution. The last test window ends at `2026-08-31T20:00:00Z` so
four-hour forward labels mature inside the dataset. Keep the pinned universe,
constraint, metadata, and knowledge-cutoff evidence; current exchange listings
cannot replace them.

The historical fixed candidate is `trend_momentum_candidate@1.1.0`, from
job #42's parameter vector: `variant=combined`, `vol_normalization=true`,
`lookback_bars=20`, `trend_bars=20`, `regime_bars=30`, `rebalance=48h`,
`top_n=3`, `max_positions=3`, `risk_on_gross=0.75`, `neutral_gross=0.25`,
`risk_off_gross=0`, `regime_band=0.02`, `position_cap=0.25`, `max_gross=0.75`,
`max_net=0.75`, `cash_reserve=0.25`, `vol_floor=0.02`,
`turnover_budget=0.10`, `skip_delta=0.015`, `execution_gap_reserve=0.1`,
`allocation_tolerance=0.02`, `hard_stop=0.08`, `include_shortlist=true`,
`execution_intent=backtest`, `model_observation=0`, `target_gross=0.75`,
`final_policy=liquidate`. Its historical config digest is
`e9ae563659746db1c18af711ed23c44ea8e1e036d23a937f57bcb310e6b18967`.
The matched baseline is `matched_momentum_baseline@1.0.0`, config digest
`8e2cd4726a8da7c5ae544b66dbbfd8a7ba259305d93567abe7dfb12720c2c72d`.
Jobs #63–65 reported +2.9501% versus +0.8515%, but were repeated source
comparisons, not three independent return observations. Their recorded
implementation/config/dataset/source-artifact digests must be checked against
any new artifact; the old performance does not survive an execution-policy
change by assumption.

The old exploratory Stage 07 experiment is
`7dec2b69ab26d919c1d842ce9fb505c983595bdd9eafd9845f370fcbde8f8e2c`,
code revision `e3c7f8452a1903c596f2dc04aa17347352127ff0`, source jobs
`[63,64,65]`. Its chronological test windows are January–February,
April–May, and July–`2026-08-31T20:00:00Z`; the corresponding validation
windows are December 2025, March 2026, and June 2026. Preserve the exact
train/validation/test, purge, embargo, label/feature horizon, seed, policy,
capacity-stress, sample, and baseline-comparability fields by exporting that
manifest from the isolated clone. This old manifest is exploratory and failed
integrity under older code; it cannot be edited in place. Current HEAD adds
fill attribution, turnover/stress, marked residual P&L, and v2 liquidity
checks. New Stage 05 runs pin `backtest-execution-v2`; v1 is retained only for
reproduction and is not comparable v2 evidence. The known zero-volume failure
is from the **first Stage 07 fold replay under v2**. The full 21-month Stage 05
source comparison has **not** been rerun under v2, so its outcome is unknown;
do not claim that a full-window v2 job failed or reproduced the old return.

## Read-only preflight: run before any clone or research job

Run this against the existing PostgreSQL container as the restricted runtime
login; `-i` passes the heredoc to `psql`. The transaction is read-only and
prints no secrets:

```bash
podman exec -i trading-postgres psql -U trading_bot_app_runtime -d trading_bot -X -v ON_ERROR_STOP=1 -P pager=off <<'SQL'
BEGIN READ ONLY;
SELECT id, content_hash, dataset_version FROM dataset_manifests
WHERE id = '32fe7e3664ab9d1a14c7cff30c0db6dc673f37ca0eea0479ed9cd812011ec9bc';
SELECT dataset_version, exchange_symbol_id, role, timeframe, open_time, volume, content_hash
FROM historical_bars
WHERE exchange_symbol_id LIKE 'binance-avaxusdt-%'
  AND role = 'execution' AND timeframe = '1m'
  AND open_time = '2026-02-14T04:00:00Z'
ORDER BY dataset_version;
SELECT open_time, (extract(epoch FROM open_time)*1000)::bigint AS open_time_ms,
       open, high, low, close, volume, quote_volume, trade_count,
       quality_status, source, provenance_json, available_at, retrieved_at
FROM historical_bars
WHERE dataset_version = 'binance-spot-research-21m-8tradable-btc-1m-20260920-v2'
  AND exchange_symbol_id = 'binance-avaxusdt-v2'
  AND role = 'execution' AND timeframe = '1m'
  AND open_time = '2026-02-14T04:00:00Z';
SELECT m.id, m.content_hash, m.dataset_version, m.knowledge_cutoff,
       x.item->>'ticker' AS ticker, x.item->>'exchange_symbol_id' AS symbol_id,
       x.item->>'role' AS role, x.item->>'timeframe' AS timeframe
FROM dataset_manifests m
CROSS JOIN LATERAL jsonb_array_elements(m.roles_timeframes_json::jsonb) x(item)
WHERE m.id = '32fe7e3664ab9d1a14c7cff30c0db6dc673f37ca0eea0479ed9cd812011ec9bc'
  AND x.item->>'ticker' = 'AVAXUSDT'
  AND x.item->>'role' = 'execution' AND x.item->>'timeframe' = '1m';
SELECT id, status, job_type, dataset_manifest_id, validation_artifact_digest
FROM backtest_jobs WHERE id IN (42, 63, 64, 65) ORDER BY id;
SELECT id, content_json::jsonb->>'code_revision' AS code_revision,
       content_json::jsonb->'fold_source_job_ids' AS source_jobs
FROM validation_experiments WHERE id LIKE '7dec2b%';
COMMIT;
SQL
```

Observed on 2026-09-26: the manifest exists with matching ID/content hash;
AVAXUSDT at `2026-02-14T04:00:00Z` has **zero volume** in both the v1 and
v2 dataset versions (same bar content hash). Independent verification against
official REST and a checksum-verified archive confirms a genuine no-trade
minute (peer evidence commit `42f4259`; that peer did not query the DB).
The targeted DB row has `open_time_ms=1771041600000`, OHLC all `9.20`,
`volume=0`, `quote_volume=0`, `trade_count=0`, `quality_status=valid`,
`source=binance-public`, and `provenance_json={"endpoint":"public_klines"}`;
its content hash is
`a901927bc9fe32f5f4b04f0add1f60fdb32655dd72ce4ae79899dcf397d46384`.
The manifest's role/timeframe entry binds `AVAXUSDT` to
`binance-avaxusdt-v2/execution/1m` under that exact dataset version, with
knowledge cutoff `2026-09-20T07:30:40Z`. This closes the pinned dataset
attribution without inspecting other research outcomes. No dataset correction
is justified. In the first Stage 07 fold, current v2 semantics reject the
intended fill at this clock with `execution_bar_liquidity_unavailable`;
therefore **stop the Stage 07 rerun**. A full-window Stage 05 source run under
v2 has not been attempted. Do not skip/defer that fill or change the interval,
symbol list, or parameters to
evade the failure. The separate unfilled-order policy design must be decided,
implemented, versioned, tested, and committed before a new exploratory
replay can be considered. A v1 diagnostic replay retains historical meaning
only; it cannot be relabeled as current v2 evidence.

## Isolated execution after an explicit execution-policy decision

There is no safe read-only Stage 05 CLI replay: `cmd/backtest` opens migration
and ledger pools and seeds data before parsing flags. The authenticated
`POST /api/backtest/compare` and `/api/backtest/compare/batch` paths also persist jobs. Never
point either at the paper application's DB. The `cmd/marketdata -action
readiness` path is read-only and needs only the runtime pool; run it first on
an isolated PostgreSQL 16 clone. The normal paper app stays running and its
flags, DB, queues, and cache are untouched.

1. Measure clone capacity and reserve a private directory before taking a
   consistent `pg_dump -Fc` snapshot of the 22 GB source database. Restore
   into a **separate PostgreSQL 16 instance/port and database**, with separate
   volume and non-production credentials; do not restore into
   `trading-postgres` or `trading-postgres-test`. Snapshot lineage must include
   source DB fingerprint, dump checksum, restore checksum, manifest row, and
   timestamp. Restrict files to mode `0600`. The observed host had about
   25 GiB available RAM and 395 GiB free disk; remeasure before cloning.
2. Use the isolated `cmd/researchlab` CLI with the clone's runtime-only DSN
   file, a private database marker file, `STAGE08_NEW_BACKTEST=research`, and
   `BACKTEST_MAX_CONCURRENT_JOBS=1`. The CLI verifies the loopback port,
   database, restricted login, marker, locked Stage 08 flags, clean committed
   code identity, exact frozen request, and append-only attempt ledger before
   submitting a job. It opens no migration, ledger, or parity pool and starts
   no app scheduler or HTTP server. Bootstrap the clone only if required by
   its schema state, using the isolated migration DSN in an explicit operator
   workflow. Do not start a second Compose stack using the paper app's
   volumes, ports, secrets, or project name.
3. Before submitting, run the read-only CLI against the clone's runtime DSN:

   ```bash
   go run ./cmd/marketdata \
     -action readiness \
     -manifest-id 32fe7e3664ab9d1a14c7cff30c0db6dc673f37ca0eea0479ed9cd812011ec9bc \
     -symbols ETHUSDT,SOLUSDT,BNBUSDT,XRPUSDT,ADAUSDT,DOGEUSDT,AVAXUSDT,LINKUSDT \
     -benchmark-symbol BTCUSDT -timeframe 15m \
     -start 2024-12-01T00:00:00Z -end 2026-09-01T00:00:00Z
   ```

   Supply `DATABASE_URL_FILE` for the **clone** through the local environment;
   check the actual manifest tradable symbol list before using the example
   list above. Exit 2 or `passed=false` is a stop, not a reason to weaken
   readiness. Verify cloned policy/settings, manifest content hash, universe
   and constraints, and the first AVAX bar before any write. The clone must
   also have enough memory for one replay; keep concurrency at one.
4. **First v3 control cleared after the verified checkpoint above is committed.**
   Submit one candidate comparison through the isolated CLI only
   after preflight passes. The request's `strategy_id`,
   `strategy_version`, `execution_policy_version=backtest-execution-v3`, full
   `parameters`, `target_gross_exposure=0.75`,
   `max_net_exposure=0.75`, `final_policy=liquidate`, and `overrides` must pin
   the manifest ID, interval, symbol list, fee 10 bps, slippage 5 bps, and
   `backtest_execution_1m=true`. Capture the exact JSON request, response
   job ID, result digest, code revision, implementation/config digests,
   replay-settings digest, dataset digest, execution-policy version, and
   failure diagnostic in a local attempt ledger. A CLI submission is not a
   dry-run; never point it at production. Any invalid/missing volume or
   unexplained
   unfilled-order state stops the entire matrix and Stage 07; do not
   reinterpret it as zero trades. Do not reuse v2's identifier for new
   economic semantics.
5. Only if row A succeeds, create two exact row-A source repetitions for the
   three fold IDs. They must have the **same new versioned** policy, dataset,
   configuration, and costs, with matched exposure (difference at most 0.02)
   and turnover (relative difference at most 0.10). These repetitions supply
   provenance, not three independent observations. Create a **new exploratory**
   Stage 07 manifest using those source IDs. Copy the old three fold
   intervals and strict gates, bind the new code and source digests, set
   `study_type=exploratory`, `exploratory=true`, and
   `execution_semantics.execution_policy_version=backtest-execution-v3` and
   `execution_semantics.no_fill_rule=selected_zero_base_volume_cancel_at_bar_close_v1`;
   leave `confirmatory_holdout` absent. Use a fresh idempotency key. The
   Stage 07 path validates server-derived source provenance. Execute only in the clone,
   then inspect fold cash + marked inventory, closed + residual P&L, fees,
   slippage, all-fill turnover/participation, stress, regime and symbol
   cohorts, and the explicit gate result. No outcome is promotion evidence
   because the 2026-08-31 dataset was already used for tuning.

## Bounded exploratory matrix and attempt accounting

Freeze these three configurations before any result after the execution-policy
decision. All other fields equal job #42; `regime_band` alone varies. Run
sequentially, maximum one comparison per row plus one exact retry for
infrastructure failure. These are sensitivity checks on an already-seen
interval, not three independent samples:

| Row | `regime_band` | Prior job | Purpose |
|---|---:|---|---|
| A | 0.02 | #42, #63–65 | fixed candidate under the newly decided policy |
| B | 0.03 | #45 | neighboring, already explored parameter |
| C | 0.04 | #48 | wider, already explored parameter |

At most five source jobs are planned: three exact row-A sources for Stage 07
and one comparison for each B and C. Do not run any row under current v2 policy, and do not run B or C if A fails
data/execution integrity under the future policy. Do not tune on the three
test windows or open any confirmatory holdout. No new parameter may be added
after inspecting a fold without registering a new exploratory attempt and
disclosing the full tuning history.

Each Stage 05 comparison also evaluates four fixed, embedded Stage 06
sensitivity configurations under strategy `1.0.0`: `absolute-20-24h`,
`relative-30-24h`, `combined-30-24h`, and `combined-vol-20-48h`. Record their
outcomes and failures as evaluated components of each source job. They are not
independent replications or post-result winners, so the three-row outer
matrix is not the total count of configurations evaluated internally.

The append-only attempt ledger includes historical jobs #30–49 and #50–65
(including failures, zero-trade outputs, and repeats), all Stage 07 manifest
attempts, and any new submissions. Jobs #42/#50/#51 and #54–65 include
repeated same-configuration runs; #63–65 are source artifacts for three folds,
not three independent tests. The earlier Stage 07 attempts
`8e5529…`, `bea10f…`, `279eca…`, `97c635…`, `e554c4…`,
`d7b462…`, and `7dec2b…` remain in the ledger with their failures; verify
full IDs in the DB before using them. Keep **submission count** separate from
**distinct hypothesis/trial count**: exact deterministic repetitions remain
in the audit ledger but are not extra multiple-testing hypotheses. A changed
parameter vector, policy, or post-result selection is a new hypothesis.
Current `internal/validation` deflated-Sharpe calculation uses the manifest's
`allowed_tuning` choice count and three fold units; it does not ingest this
external historical ledger. Its output is a heuristic diagnostic, not a
history-adjusted significance claim. Disclose the distinct historical tuning
count and this limitation alongside any exploratory result.
The statistical unit for any valid Stage 07 assessment is the chronological
test window, with minimum three windows, not the repeated source job.

Maintain an append-only local CSV/JSONL ledger with: UTC submitted time,
study/family ID, hypothesis row, parent job, code SHA, strategy/implementation
and config digests, dataset ID/hash/version, source job ID, v1/v2 execution
policy, request/replay-settings digest, status, diagnostic, observed metrics,
and whether it is a repeat. Preserve failed and pending attempts. The strict
gates from the old manifest remain: after-cost return > 0,
benchmark-relative return > 0, coverage >= 1, maximum drawdown <= 0.20,
stressed after-cost return > 0; also retain the existing minimum fold,
observation, trade, regime, bootstrap, exposure, turnover, and attribution
checks. A passing exploratory result is still not a confirmatory result.
