---
name: backtest-improvement-loop
description: Run the trading platform's governed four-candidate backtest improvement loop. Use when the user asks to inspect backtest history, tune strategy parameters, launch four parallel experiments, wait for completion, and compare results.
compatibility: Requires the local trading platform, authenticated API, and PostgreSQL services.
metadata:
  author: trading-bot
  version: "1.0"
---

# Backtest Improvement Loop

Use this workflow to iteratively improve the registered Stage 06 candidate through the same Stage 05 batch mechanism used by the frontend. This is research automation, not promotion authority.

## Invariants

- Read `roadmap.md`, `docs/operations/backtest-reproduction.md`, and `AGENTS.md` before changing behavior.
- Use the immutable configured dataset and point-in-time readiness evidence.
- Keep fee, slippage, evaluation interval, symbol universe, target exposure, and final-position policy unchanged within one comparison batch.
- Submit at most four experiments unless the user explicitly requests another bounded count.
- Change one principal hypothesis per candidate when possible. A combination candidate is allowed when prior completed jobs independently support each component.
- Never weaken data readiness, baseline, reconciliation, zero-trade, Stage 07, approval, or rollout gates.
- Never present a completed command, a zero-trade result, or a lower loss as profitable or promotable.
- Do not create Stage 07 evidence until Stage 05 establishes baseline-relative value after costs.
- Do not print credentials, DSNs, passwords, or secret-file contents.

## 1. Establish Runtime Readiness

Confirm the application and PostgreSQL containers are running. Check that no existing backtest jobs are active:

```bash
podman exec trading-postgres psql -U postgres -d trading_bot -P pager=off \
  -c "SELECT id,status,job_type,created_at FROM backtest_jobs WHERE status IN ('pending','queued','running') ORDER BY id;"
```

Read `BACKTEST_MAX_CONCURRENT_JOBS` from runtime configuration without printing unrelated environment variables. Four submitted jobs run in parallel only when the effective limit is at least four; otherwise they remain safely queued.

Call the authenticated `GET /api/market-data/readiness` endpoint with the configured manifest, symbols, evaluation bounds, benchmark, and decision timeframe. Stop if `passed` is false.

## 2. Inspect Historical Evidence

Load recent completed `stage05_comparison` jobs from `backtest_jobs`. For each candidate extract:

- exact parameter map;
- total return after costs;
- maximum drawdown;
- trade count;
- turnover and total costs;
- average gross exposure;
- reconciliation state;
- governance reasons;
- failure diagnostic when the job failed.

Always include cash and the best relevant baseline in the comparison. Rank candidates by gate eligibility first, then total return after costs. Use drawdown, turnover, costs, trade count, and exposure to choose between close results.

The seed candidate should normally be the highest-return reconciled non-zero-trade run. A slightly lower-return candidate can be more promising when it has materially lower drawdown or turnover and suggests a clear next hypothesis.

## 3. Design Four Candidates

Derive four predeclared candidates from the seed:

1. Combine independently successful parameter changes.
2. Vary the strongest performance-sensitive parameter by one registered step.
3. Test one risk-control change, such as hard stop, turnover budget, or neutral exposure.
4. Test one robustness alternative around the current best configuration.

Use only values accepted by the registered strategy descriptor. Preserve `execution_intent=backtest`, the governed target/max-net exposure, and `final_policy=liquidate` unless the research question explicitly concerns final-position policy.

Do not silently alter generic settings. Put all candidate parameters in the immutable request body.

## 4. Submit Through the Frontend Mechanism

Authenticate through `/api/auth/login`, reading local credentials without displaying them. Submit all candidates in one request:

```text
POST /api/backtest/compare/batch
Content-Type: application/json

{"experiments":[...]}
```

This is the same endpoint used by `frontend/src/components/SelfImprovement.jsx`. Verify the response count is four, record all job IDs, and report the returned `concurrency_limit`.

Do not retry blindly after a partial HTTP failure. Inspect accepted jobs and database state first to avoid duplicate experiments.

## 5. Wait Until Every Job Is Terminal

Prefer polling over an unobserved long `sleep`. Poll PostgreSQL every 30 seconds for only the submitted IDs and stop when every status is `completed` or `failed`:

```text
deadline = now + 2 hours
while before deadline:
  SELECT id,status,progress,message,error,updated_at
  FROM backtest_jobs
  WHERE id IN (...)
  if all statuses are completed or failed: stop successfully
  sleep 30 seconds
fail with timeout after the deadline
```

A single Bash or Python polling command may remain active with a two-hour tool timeout. Print only status changes to avoid noisy output. Do not mistake unchanged coarse progress for a hung engine; inspect `updated_at`, process health, and logs before intervening.

Never mutate a running job. If a job fails, retain it in the final analysis and record its exact diagnostic.

## 6. Analyze Final Results

After all jobs are terminal, produce one table containing:

- job ID and principal hypothesis;
- terminal status;
- total return after costs;
- maximum drawdown;
- trades;
- turnover and costs;
- governance result.

Compare every result against:

- the seed job;
- cash;
- buy-and-hold;
- the best relevant registered baseline.

State separately:

- best return candidate;
- best risk-adjusted or operationally efficient candidate;
- failed or zero-trade candidates;
- whether any candidate passed the Stage 05 optimization gate;
- the next four bounded hypotheses, if another loop is warranted.

An improvement from a large loss to a smaller loss is useful research evidence but remains a failed economic gate. Promotion remains unavailable until the candidate beats required baselines after costs and later passes independent Stage 07 validation and human governance.

## 7. Repository Changes

The loop normally writes only immutable application records through the API. Do not edit application code merely to make an experiment pass. If a genuine platform bug is discovered, handle it as a separate tested code change and do not rewrite existing evidence.

If this skill file is newly created or changed, remind the user that a new OpenCode session/restart is required before automatic skill discovery reflects the update.
