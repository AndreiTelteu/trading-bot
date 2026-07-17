# Final roadmap evidence audit

Audit scope: repository `main` at starting commit `101c912`, plus this uncommitted final-audit diff. Roadmap and detailed-plan checkboxes were not edited. Status vocabulary is `verified`, `conditional N/A`, `pending operational`, and `external/unprovable`.

## Severity-ranked findings and resolution

| Severity | Verified finding | Resolution and direct evidence | Status |
| --- | --- | --- | --- |
| Critical | PostgreSQL protected wallet/position economic updates but allowed direct projection inserts and deletes outside a ledger transaction. | Migration `202607181900_final_audit_projection_lifecycle_guards` extends both triggers to insert/update/delete; seed authorization moved to the enclosing atomic seed transaction. `TestProjectionLifecycleCannotBypassLedger` proves direct lifecycle mutation fails while administrative capital, fill, fee, and reconciliation succeed. | Verified by the focused, full PostgreSQL, and race gates |
| High | Bound parity persistence trusted caller-supplied comparison digests/classification; policy verification only compared two stored digest fields. | `cutover.VerifyComparisonWithPolicy`, `verifyParityPolicy`, and `verifyParityPopulation` recompute canonical outcomes, tolerance handling, expected/unexplained classification, JSON, bindings, and content identities before persistence/evaluation/cutover use. PostgreSQL timestamps are normalized to stored microsecond precision before identity hashing. Forged comparison/classification/policy negative tests were added. | Verified by pure, PostgreSQL persistence, full, and race tests |
| High | Restore tooling accepted any `*_test` database, did not prove the preexisting database set survived unchanged, and always recorded operational evidence. | `stage08_backup_restore.sh` now requires exact source `trading_bot_test`, maintenance DB `postgres`, proves a random target did not preexist, records before/after DB inventories, explicitly drops only its target, and offers `--test-mode` that writes no operational evidence. The equivalent isolated sequence was exercised with container-internal PostgreSQL 16 clients against a unique target and canonical source/target fingerprints matched. | Verified; temporary target was dropped and DB inventory was unchanged |
| Medium | The existing parity fixture reused one paper context rather than independently proving pre-broker semantics for backtest, paper, and live contexts. | `TestFixtureParityAcrossBacktestPaperAndFencedLive` now builds all three immutable contexts, runs the same strategy and risk engine, compares order-intent/risk semantics, compares paper/backtest costs, and proves live remains non-submitting. | Verified |
| Medium | Several read APIs were unbounded. | Orders, proposals, backtest jobs, universe symbols, activity logs, and wallet snapshots now have explicit server-side caps. | Verified by compilation/static audit; existing handler tests remain applicable |
| Medium | Backup canonicalization lacked a direct semantically-equivalent JSON/JSONB normalization regression. | `TestCanonicalRowsDigestSurvivesJSONBNormalization` and `TestStage08JSONBNormalizationPreservesContentAddressedEvidence` cover reordered/normalized nested context JSON. | Verified by pure and PostgreSQL round-trip tests |
| Low | Fresh-install runbook named an initializer/log line not used by the real server; README still described SQLite and active live trading. | Commands and expected startup text now match `cmd/server`; README now states PostgreSQL and the live fence. | Verified |

## Non-negotiable principles (roadmap lines 16–25)

| Line | Principle | Production evidence | Direct/adversarial evidence | Status |
| --- | --- | --- | --- | --- |
| 16 | One strategy decision path across modes | `internal/tradingcore/strategy.go`, `orchestrator.go`; runtime adapter `internal/services/shared_engine_adapter.go`; backtest adapters `internal/backtest/shared_engine.go`, `stage05.go`; live request construction remains fenced in `internal/services/trading.go`. Legacy routing is retained only behind persisted Stage 08 authority in `internal/services/trending.go`. | Strengthened `TestFixtureParityAcrossBacktestPaperAndFencedLive`; Stage 02/06 parity and rollout tests. | Verified for implemented backtest/paper and pre-submit live semantics; live broker effects pending operational |
| 17 | One risk engine across modes | Every new-mode caller uses `tradingcore.PortfolioRiskEngine`; no second new risk implementation was found. Legacy checks remain only in the flagged rollback path. | Cross-mode golden fixture plus `risk_remediation_test.go`, Stage 02 scenarios, Stage 06 cap tests. | Verified |
| 18 | Immutable fill/fee/capital ledger | `internal/ledger/service.go`, `adjustment.go`, `correction.go`, `tradingcore_adapter.go`; database triggers/FKs/checks in `migrations.go`; API fences in positions/orders/wallet/trading handlers. | Ledger invariant/concurrency/rollback/API tests plus new lifecycle-bypass test. AI approval and generic settings tests prove no economic/authority mutation. | Verified by full PostgreSQL and race gates |
| 19 | Point-in-time data/universe | `internal/pointintime`; historical jobs require manifest identity, availability/retrieval cutoffs, effective listing/delisting, benchmark role, and snapshot membership. Current exchange universe remains only in the explicit legacy runtime path. | Point-in-time, review, backtest Stage 04, survivorship, constraint-gap, and handler fixtures. | Verified; external history completeness unprovable |
| 20 | Determinism and explicit coverage failure | Fixed clocks/sequence IDs, versioned manifests, typed `CoverageError`/run classifications, bounded data APIs. | Stage 00 determinism, Stage 03 no-lookahead/coverage/determinism, Stage 05 repeatability, Stage 07 typed refusals. | Verified |
| 21 | Same comparison costs | Risk reserves the versioned cost policy; Stage 05 normalized assumptions reject unequal cost/exposure manifests; simulation brokers validate intent reservations. | Stage 03 cost tests, Stage 05 golden/review regression and PostgreSQL comparison fixtures. | Verified |
| 22 | No zero-trade/unreconciled profitability | Backtest classifications distinguish coverage failure, gating zero trades, and strategy zero trades; validation and baseline gates require reconciled, nonzero, sufficient evidence. | `stage03_test.go`, `stage05_review_regression_test.go`, `validation/stage07_test.go`. | Verified; profitability itself external/unprovable |
| 23 | Model quarantine | Artifact classes, authority policy, deployment verification, and strategy authority prevent bootstrap/fixture/shadow control. | Model quarantine, AI-governance, validation, and governance negative-path tests. | Verified |
| 24 | Human live approval | Stage 07 approval/transition contracts bind authenticated capability, evidence, policy, monitoring, and exact deployment; Stage 08 limited-live transition re-verifies it. | Governance PostgreSQL tests and handler actor/role-spoof tests. | Verified in code; no real promotion performed |
| 25 | Legacy behind flags | Safe defaults, strict binary startup, DB-derived locked snapshot/authority, atomic transitions, explicit fallback, and rejection of `legacy_removal_eligible`. | Cutover/config/routes/rollout/operations tests. | Verified; legacy intentionally retained pending real cutover |

## Stage 00–08 reconciliation

| Roadmap group | Checked work mapped to production | Direct/adversarial tests | Status |
| --- | --- | --- | --- |
| Stage 00, lines 31–38 | Contracts, immutable snapshots, clocks/IDs, architecture boundary in `internal/tradingcore` and Stage 00 docs; runtime behavior retained behind adapters. | `internal/services/stage00_characterization_test.go`, `handlers/stage00_characterization_test.go`, `testing/stage00_characterization_test.go`, determinism tests. | Verified |
| Stage 01, lines 43–50 | Exact immutable ledger events/batches/fills; atomic projections; typed adjustments/reversals/corrections/backfill/reconciliation; paper costs; API fences; database enforcement. | `internal/ledger/*_test.go`, `handlers/stage01_api_test.go`, migration and integration tests, new lifecycle test. | Verified |
| Stage 02, lines 56–63 | Shared context/intent/strategy/risk/broker/orchestrator contracts, legacy strategy adapter, rollout/fallback routing. | Stage 02 fixture/scenario/shadow/live-request/application integration tests; strengthened cross-mode golden. | Verified |
| Stage 03, lines 69–76 | Next-executable fills, optional execution series, deterministic costs/partial fills/exit precedence, benchmark coverage, compact manifests and typed outcomes. | `stage03_test.go`, `stage03_cost_test.go`, job PostgreSQL tests. | Verified |
| Stage 04, lines 82–89 | Effective-time assets/symbols/tradability/constraints/bars/manifests/universe snapshots; resumable ingestion/build; benchmark separated from tradability. | `pointintime/*_test.go`, Stage 04 backtest and handler tests. | Verified; vendor/exchange completeness external/unprovable |
| Stage 05, lines 95–102 | Cash, buy/hold, benchmark trend, equal-weight, cross-sectional momentum; normalized exposure/costs; relative metrics and optimization fence. | Stage 05 synthetic/golden/review/PostgreSQL job tests and governance test. | Verified; robust real-market superiority external/unprovable |
| Stage 06, lines 108–115 | Versioned trend/momentum candidate, PIT inputs, regime/liquidity/rank/cadence, caps/turnover/exits, ablations/sensitivity, shadow/research default. | Stage 06 unit/scenario/feedback/parity tests and shared-engine integration. | Verified; capacity/profit/elapsed shadow external/unprovable |
| Stage 07, lines 121–128 | Immutable manifests/evidence, purged walk-forward/bootstrap units, typed insufficient evidence, diagnostics, authenticated governance, rollback and model quarantine. | Validation/governance PostgreSQL and pure tests, API actor-spoof, AI and model quarantine tests. | Verified |
| Stage 08, lines 134–141 | Safe flags, locked DB-derived authority, pure dual run, policy/population-bound parity, status/incidents, evidence-bound transitions, canonical backup fingerprints and runbooks. Final audit strengthened parity integrity and restore test mode. | Cutover/operations/config/routes/rollout/shadow/backup tests plus actual isolated dump/restore equivalence. | Verified technically; legacy removal and real cutover intentionally not performed |

Unchecked detailed-plan statements labelled “cannot yet be proven” remain correctly unresolved: provider fee samples, reliable legacy historical returns, full historical/delisted coverage, order-book impact/capacity, positive performance/generalization, elapsed shadow/paper stability, real exchange behavior, external alert delivery, and future profitability.

## Global definition of done and final-audit items

| Roadmap line(s) | Evidence/status |
| --- | --- |
| 145–147, reconcile/find/close gaps | This document and the final-audit diff provide the repository-wide reconciliation and implemented fixes. Full executable gates passed. |
| 148, independent post-diff verification | `pending operational`: this is the single requested implementation session, not an independent post-diff reviewer. |
| 149 and 155–157, full backend/frontend/Compose | Full PostgreSQL, frontend build, race, vet, and Compose gates passed. |
| 150, clean and pushed | `pending operational`: intentionally impossible in this session because the user prohibited commit/push and requested a working diff. |
| 151–152 and 162, limitations | Recorded below. |
| 158, fresh/upgraded/restore migrations | Fresh and representative upgrade fixtures pass; the stronger trigger migration safely skips absent optional legacy tables; actual isolated restore equivalence passed. |
| 159, no direct economic bypass | Database lifecycle guards plus service/API fences and executed adversarial PostgreSQL tests. |
| 160, cross-mode parity | Strengthened single golden fixture passes. Live broker is explicitly non-submitting. |
| 161, missing data typed failure | Stage 03/04/05/07 typed failure tests; no neutral passing fallback found. |

## Verification log

- Starting state: `git rev-parse HEAD` → `101c91201327c1576ce2c832abc7cdfb007b43b3`; branch `main`; initially clean except an unrelated untracked `tokscale-export-20260717-122733.json`, which was not touched.
- Focused regression tests passed for the Stage 03 shaped-schema upgrade, parity persistence, and guarded legacy-position fixture. The final migration installs wallet/position triggers only when those optional historical tables exist.
- Full serial PostgreSQL: `TEST_DATABASE_URL=postgres://postgres:***@127.0.0.1:55433/trading_bot_test?sslmode=disable go test -p 1 -count=1 ./...` passed every package.
- Serial race: `go test -race -p 1 -count=1` passed `backtest`, `tradingcore`, `database`, `services`, `handlers`, `ledger`, `testing`, `pointintime`, `validation`, `governance`, `cutover`, and `operations`.
- Static/format gates: touched Go was processed by `gofmt`; `go vet ./...`, `git diff --check`, and `bash -n scripts/stage08_backup_restore.sh` exited 0.
- Frontend: `frontend/package.json` defines build but no typecheck script. `npm run build -- --outDir /tmp/trading-bot-final-audit-frontend-dist --emptyOutDir` compiled 932 modules and passed in 3.43 seconds. The repository build directory was not modified.
- Compose: dummy-credential rendering exited 0 and produced 76 lines. No service was started.
- Docker identity was verified as `/trading-ledger-test-pg`, image `postgres:16-alpine`, with only `127.0.0.1:55433 -> 5432/tcp`. No other container was used.
- Restore source was exactly `trading_bot_test`; initial non-template DB inventory was exactly `postgres`, `trading_bot_test`.
- A unique target `trading_bot_restore_bd44b5aafe40ab1f790e8460852206f7` was proven absent, created in that test container, identity-token checked, restored from a custom-format dump, migrated/reconciled with production `cmd/operations -action restore-verify`, and fingerprinted.
- Source-before, target, and source-after canonical digest all matched `2cd69996906d1be318e19e15f4b0c837fa959bcab8d04f8f82ada9cd9ac7ba31`; dump SHA-256 was `88de158350196ce60d0e0dde71808f645af2083a3e252d626305b53a91ecfd4a`.
- The temporary target and dump were deleted. Final non-template DB inventory again contained exactly `postgres`, `trading_bot_test`. No `BackupVerification` was persisted because this was a test-mode exercise.

## Precise limitations and items that must remain unchecked

- Complete real market/delisted history and provider fee/fill samples: external/unprovable here.
- Required elapsed shadow/paper observation time and long-term stability: pending operational time.
- External alert-channel delivery: external/unprovable; durable failure state is tested.
- Live/testnet exchange behavior, capacity, and market impact: external/unprovable and deliberately not exercised.
- Profitability/generalization: not established and cannot be inferred from implementation gates.
- Real deployment, migration promotion, cutover, limited/full-live approval, rollback window, and legacy removal: pending operational and explicitly not performed.
- Independent review of this final diff and repository clean/pushed state remain pending at this implementation-session boundary.

The audit did not commit, push, deploy, start application/Compose services, submit orders, call an exchange/LLM network, access an operational database, remove a legacy path, or edit roadmap/detailed-plan checkboxes.
