# Decision council v2 / candidate 1.4.0

## Frozen hypothesis

`trend_momentum_candidate@1.4.0` keeps the 1.1.0/v4 strategy and execution cadence intact, but lets an anonymized generalist decision model inspect every completed UTC 4h boundary. The model may recommend an early long entry for a flat symbol close to the v4 selection boundary, or an early exit for an existing long. It may not defer a v4 mandatory exit or the hard stop.

This is exploratory evidence. `experiential/jev-latest` post-dates the replay period, so results contain model look-ahead and cannot authorize promotion.

## Frozen activation and authority

At each complete 4h boundary:

* evaluate every economically open position;
* evaluate each flat, eligible symbol with rank `<= top_n+2`, positive absolute trend, and regime other than `risk_off`;
* preserve the v4 rebalance interval (`48h` in the frozen request);
* `observe_v2` records decisions and must remain economically byte-identical to 1.1.0;
* `active_v2` may sell a complete open position, or buy a flat near-signal symbol subject to v4 position sizing, position count, gross/net, cash, constraints, volume participation and execution costs;
* execute normal v4 decisions first. A model action cannot cancel or reverse a mandatory v4 exit. A symbol already opened by the normal v4 decision is not bought twice.

The v1 thresholds are frozen unchanged:

* entry/admit iff final=`buy`, bull `>=0.50`, bear `<0.50`;
* early exit iff final=`sell`, bear `>=0.60`, hodl `<0.40`.

Unavailable/invalid provider responses fall back to v4. The tenth failed request aborts. Cache corruption and cancellation abort immediately. Returned text is never executed; only typed `answers.<question_id>` is consumed.

## Point-in-time state v2

The anonymized, stable state contains no ticker, asset ID, exchange ID, wall-clock date, price level, or model secret. It contains:

* up to 360 completed 4h OHLCV bars for the asset and benchmark, expressed as rounded returns/ranges/body/volume ratios;
* up to 180 completed daily bars for asset and benchmark in the same relative representation (short history is explicit and allowed);
* position duration, unrealized P&L, MFE and MAE when held;
* bounded per-symbol v4 factor history accumulated only from completed 4h computations;
* the current anonymized cross-sectional factor snapshot, sorted by rank;
* current v4 regime, rank, factor values and proposed action.

The state remains under the decision-model request bound and is hashed into replay evidence. History is loaded only from the validated immutable manifest, filtered by knowledge cutoff and `available_at <= replay end`; each decision still slices only bars completed by its decision time.

## Determinism and evidence

Model responses are replay-authoritative through the PostgreSQL request cache. Evaluation output is reassembled in deterministic symbol order. Cache telemetry is excluded from the artifact digest. v2 has a separately bounded trace allowance sufficient for the expected 8–12k evaluations; Stage 07 source artifacts continue to exclude council traces.

The 1.4.0 implementation digest chains the 1.1.0 digest with `decision-council-v2` and the complete v2 prompt/state contract digest. Historical 1.1.0–1.3.0 digests and v1 state bytes remain unchanged.
