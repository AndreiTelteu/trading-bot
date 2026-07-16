# Stage 04 — Point-in-Time Market Data and Universe

## Objective

Ensure every historical decision sees only assets, metadata, liquidity, and bars that were actually available at that timestamp. Remove current-universe leakage, inferred listing-age shortcuts, and silent data gaps.

## Data model

### Asset lifecycle

- [ ] Stable internal asset/symbol identity separate from exchange ticker renames.
- [ ] Exchange listing effective time and optional delisting time.
- [ ] Quote/base assets and tradability intervals.
- [ ] Symbol constraints effective over time when available.
- [ ] Provenance and retrieval timestamp for every metadata record.

### Historical market data

- [ ] Canonical OHLCV keys by symbol, timeframe, and open timestamp.
- [ ] Duplicate/gap detection and explicit quality flags.
- [ ] Separation of decision-resolution, execution-resolution, and benchmark series.
- [ ] Immutable or versioned ingestion; corrections produce a new dataset version/manifest.

### Universe snapshots

- [ ] Effective timestamp, policy version, candidate pool, accepted members, shortlist/ranks, regime, and rejection reasons.
- [ ] Membership generated from point-in-time data only.
- [ ] Benchmark membership represented separately from tradability.
- [ ] Empty membership represented as a valid observed state only when input coverage is complete; otherwise classify as coverage failure.

## Implementation work

### Dataset manifests

- [ ] Add a manifest containing interval, symbols, timeframes, row counts, gaps, source/provenance, build version, and content hash.
- [ ] Validate manifests before a backtest.
- [ ] Persist manifest identity into every run.
- [ ] Provide machine-readable coverage inspection through CLI/API.

### Historical universe builder

- [ ] Recompute liquidity, listing-age, volatility, breadth, regime, and ranking using only data through each snapshot timestamp.
- [ ] Use actual listing metadata rather than `(bar index / bars per day)` as listing age.
- [ ] Include assets later delisted when they were tradable historically.
- [ ] Avoid selecting candidates from the exchange's current symbol list for old periods.
- [ ] Make snapshot generation idempotent and resumable.

### Backfill tooling

- [ ] Add dry-run and bounded date/symbol options.
- [ ] Add retry/rate-limit handling and checkpointing.
- [ ] Never overwrite a dataset silently.
- [ ] Separate download/ingestion from universe computation.
- [ ] Emit explicit unresolved coverage rather than synthetic bars.

## Testing instructions

### Point-in-time fixtures

- [ ] An asset listed midway through a fixture is invisible before listing.
- [ ] A delisted asset remains visible during its historical tradability interval.
- [ ] A ticker rename does not duplicate economic exposure.
- [ ] Future volume/returns cannot affect an earlier universe rank.
- [ ] Benchmark data is available to regime logic without entering the tradable shortlist.

### Data quality and idempotency

- [ ] Duplicate bars are rejected or deterministically resolved with provenance.
- [ ] Missing bars appear in coverage diagnostics.
- [ ] Rebuilding identical inputs produces the same manifest hash and snapshots.
- [ ] Interrupted backfill resumes without duplicates.
- [ ] Backtest refuses incompatible or insufficient manifests.
- [ ] Full `go test ./...` passes.

### Cannot yet be proven

- [ ] Complete historical coverage depends on exchange/vendor availability and credentials/network access.
- [ ] Delisted-symbol history may remain incomplete; the manifest must disclose it.
- [ ] OHLCV cannot prove fill liquidity or order-book impact.
- [ ] Strategy profitability and promotion remain blocked until Stages 05–07.

## Acceptance criteria

- [ ] Historical runs no longer derive universe membership from current exchange state.
- [ ] Listing/delisting and benchmark availability are explicit.
- [ ] Every run references a validated dataset manifest.
- [ ] Missing external data is visible as a limitation, never fabricated.
- [ ] Reviewer verifies no future timestamp is reachable through universe/data APIs.
