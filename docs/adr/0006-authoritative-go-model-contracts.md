# ADR 0006: Go/PostgreSQL owns authoritative learned-model decisions

## Status

Accepted, 2026-09-18.

## Decision

Go/PostgreSQL is the only authoritative implementation for learned-model data
construction, feature provenance, fold evaluation, portfolio simulation,
costs, manifests, promotion evidence, and runtime inference. `marketdata`
exports immutable, hash-bound proposal data only from labeled decision cohorts
whose feature snapshots are tied to the supplied Stage 04 manifest through
universe snapshots.

Python remains only for offline logistic proposal generation because sklearn is
useful for exploratory work. It has no database access path, verifies the Go
export, rejects mutable CSV input, emits `research_proposal`, and uses
unvalidated identity calibration. Go rejects that class before runtime
inference; it cannot enter the registry, evidence, or promotion path.

The SQLite extractor, independent evaluator, false GBDT-to-linear conversion,
normalization exporter, pseudo feature-parity fixture, and unused dependencies
are removed.

## Consequences

Python output can form a hypothesis only. It cannot establish after-cost
expectancy or authorize paper/live execution. Operators must rebuild a
candidate using shared Go contracts and complete Stage 07 evidence and human
approval.
