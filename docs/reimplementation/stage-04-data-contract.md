# Stage 04 point-in-time data contract

All requested market-data intervals are half-open: `[start, end)`. A bar is a
member of the interval when `open_time >= start && open_time < end`. Its normal
event availability is the inclusive bar close (`open_time + timeframe - 1ms`);
a source may
provide a later `available_at`, but never an earlier one. Historical decisions
may use a bar only when `available_at <= decision_time`. Binance's inclusive
`endTime` is converted at the public-client boundary by subtracting one
millisecond; it does not change the internal half-open contract.

Stage 04 deliberately has two clocks:

- `available_at` is source/event knowledge: the first instant a market fact,
  lifecycle version, tradability interval, constraint version, or completed bar
  could have been observed by a decision.
- `retrieved_at` is ingestion wall time. It records when an immutable fact was
  acquired and may be much later during a legitimate historical backfill.

Every manifest persists a deterministic `knowledge_cutoff`. Only immutable rows
whose `retrieved_at` is at or before that cutoff can form the manifest. The
cutoff is part of the canonical manifest hash; build timestamps and map iteration
order are not. Runtime still enforces `available_at` at each requested as-of, so
a post-hoc dataset can reproduce history without making a later-known event
visible to an earlier decision.

Manifest v2 binds each series by exact `exchange_symbol_id`, stable `asset_id`,
symbol version/lifecycle, role, timeframe, row count, and content digest. It also
pins constraint and tradability row counts/digests at the same retrieval cutoff.
Adding or changing rows represented by an existing manifest makes verification
fail; corrections therefore require a new dataset version and manifest.

Historical constraints fail closed. A manifest-backed production run must have
continuous quantity-step, price-tick, minimum-quantity, and minimum-notional
metadata over each executable lifecycle interval. The permissive tiny-lot
fallback remains only for legacy/unit configurations that do not require a
dataset manifest.
