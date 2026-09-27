# V4 paper authority readiness — 2026-09-28

The operator requested paper trading for the Stage 05 candidate 1.1.0 under
`backtest-execution-v4` even if its statistical result is poor. This request
authorizes a paper-only rollout decision; it does not turn exploratory or
failed evidence into passed validation and does not authorize live exchange
submission.

## Read-only state observed before any transition

The application PostgreSQL 16 database is at cutover stage
`research_ingestion`, authority `legacy`, version 2. It has zero governance
deployments, zero parity observations, zero backup verifications, and zero
cutover prerequisite-evidence rows. The latest application backtest job is
65; the v4 exploratory source jobs 85–87 were run on an isolated research
clone. The application setting `trading_engine_mode` is `legacy` while
`auto_trade_enabled` is `true`; the latter is only an operational paper-entry
switch and does not promote the candidate.

The runtime registry currently knows `trend_momentum_candidate@1.0.0` with its
base shared-strategy digest. The v4 comparison uses candidate `1.1.0`, whose
implementation digest additionally binds the mandatory-exit residual rule.
The current runtime cannot instantiate that exact approved identity. V4 is a
backtest execution-capacity policy; the runtime paper broker has separate
observed-price fill semantics. A v4 source comparison alone cannot be deployed
as a paper broker or as a Stage 07 approval.

## Conditions for an honest paper-only pilot

1. Complete a manifest-backed PostgreSQL Stage 07 v4 fold replay with the
   candidate and matched baseline, exact point-in-time bars/constraints,
   fill/cancel/cost/inventory verification, and immutable failed gates shown
   without relabeling. Source jobs from the inspected interval may establish
   technical reproducibility, not a new confirmatory holdout.
2. Bind the candidate 1.1.0 runtime implementation and configuration to the
   reviewed Stage 05/07 identity, with backtest-to-paper decision parity and
   mandatory-exit residual behavior. Keep the paper broker's actual semantics
   explicit and reconcile every economic fill to the ledger.
3. If statistical performance is intentionally waived for paper, add a
   separately named, authenticated, immutable **paper-only trial** decision.
   It must preserve the failed statistical gates, require technical integrity,
   point-in-time coverage, accounting reconciliation, bounded exposure,
   rollback criteria, and an operator capability. It must never satisfy
   limited-live/full-live gates or be represented as confirmatory success.
4. Progress through the persisted Stage 08 cutover sequence with real parity,
   backup, and prerequisite evidence. Do not directly edit flags/settings or
   database rows to skip the sequence. Once the exact paper authority is
   active, observe it over elapsed time and retain the ability to roll back.

At this checkpoint no paper-only v4 promotion or production flag mutation was
performed. Poor returns alone need not end a **paper trial**, but absent
technical and cutover evidence still fails closed. Later unseen performance
evidence remains necessary before any claim of strategy quality.
