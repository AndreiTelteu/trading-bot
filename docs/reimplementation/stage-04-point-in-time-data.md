# Stage 04 — Point-in-Time Market Data and Universe

## Objective

Ensure every historical decision sees only assets, metadata, liquidity, and bars that were actually available at that timestamp. Remove current-universe leakage, inferred listing-age shortcuts, and silent data gaps.

## Data model

### Asset lifecycle

- [x] Stable internal asset/symbol identity separate from exchange ticker renames.
- [x] Exchange listing effective time and optional delisting time.
- [x] Quote/base assets and tradability intervals.
- [x] Symbol constraints effective over time when available.
- [x] Provenance and retrieval timestamp for every metadata record.

### Historical market data

- [x] Canonical OHLCV keys by symbol, timeframe, and open timestamp.
- [x] Duplicate/gap detection and explicit quality flags.
- [x] Separation of decision-resolution, execution-resolution, and benchmark series.
- [x] Immutable or versioned ingestion; corrections produce a new dataset version/manifest.

### Universe snapshots

- [x] Effective timestamp, policy version, candidate pool, accepted members, shortlist/ranks, regime, and rejection reasons.
- [x] Membership generated from point-in-time data only.
- [x] Benchmark membership represented separately from tradability.
- [x] Empty membership represented as a valid observed state only when input coverage is complete; otherwise classify as coverage failure.

## Implementation work

### Dataset manifests

- [x] Add a manifest containing interval, symbols, timeframes, row counts, gaps, source/provenance, build version, and content hash.
- [x] Validate manifests before a backtest.
- [x] Persist manifest identity into every run.
- [x] Provide machine-readable coverage inspection through CLI/API.

### Historical universe builder

- [x] Recompute liquidity, listing-age, volatility, breadth, regime, and ranking using only data through each snapshot timestamp.
- [x] Use actual listing metadata rather than `(bar index / bars per day)` as listing age.
- [x] Include assets later delisted when they were tradable historically.
- [x] Avoid selecting candidates from the exchange's current symbol list for old periods.
- [x] Make snapshot generation idempotent and resumable.

### Backfill tooling

- [x] Add dry-run and bounded date/symbol options.
- [x] Add retry/rate-limit handling and checkpointing.
- [x] Never overwrite a dataset silently.
- [x] Separate download/ingestion from universe computation.
- [x] Emit explicit unresolved coverage rather than synthetic bars.

## Testing instructions

### Point-in-time fixtures

- [x] An asset listed midway through a fixture is invisible before listing.
- [x] A delisted asset remains visible during its historical tradability interval.
- [x] A ticker rename does not duplicate economic exposure.
- [x] Future volume/returns cannot affect an earlier universe rank.
- [x] Benchmark data is available to regime logic without entering the tradable shortlist.

### Data quality and idempotency

- [x] Duplicate bars are rejected or deterministically resolved with provenance.
- [x] Missing bars appear in coverage diagnostics.
- [x] Rebuilding identical inputs produces the same manifest hash and snapshots.
- [x] Interrupted backfill resumes without duplicates.
- [x] Backtest refuses incompatible or insufficient manifests.
- [x] Full `go test ./...` passes.

### Cannot yet be proven

- [ ] Complete historical coverage depends on exchange/vendor availability and credentials/network access.
- [ ] Delisted-symbol history may remain incomplete; the manifest must disclose it.
- [ ] OHLCV cannot prove fill liquidity or order-book impact.
- [ ] Strategy profitability and promotion remain blocked until Stages 05–07.

## Acceptance criteria

- [x] Historical runs no longer derive universe membership from current exchange state.
- [x] Listing/delisting and benchmark availability are explicit.
- [x] Every run references a validated dataset manifest.
- [x] Missing external data is visible as a limitation, never fabricated.
- [x] Reviewer verifies no future timestamp is reachable through universe/data APIs.

## Completion evidence

- Initial implementation: `6df8c0d`.
- Independent read-only review: session `019f6db0-f78c-7881-986a-5aeb6f472375`, verdict `Reject` before remediation.
- Single feedback pass resumed in original implementation session `019f6d74-0196-77b2-8409-d5f829b1b03f` (1/1 consumed).
- Review remediation: `decd442`.
- Verified after remediation against isolated PostgreSQL 16: full serial suite, serial race suite, `go vet ./...`, and `git diff --check` pass.
- Timestamp and interval semantics: `docs/reimplementation/stage-04-data-contract.md`.
