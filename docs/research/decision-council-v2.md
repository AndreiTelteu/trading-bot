# Decision council v2 / candidate 1.4.0

## Frozen hypothesis

`trend_momentum_candidate@1.4.0` keeps the 1.1.0/v4 strategy and execution cadence intact, but lets an anonymized generalist decision model inspect every completed UTC 4h boundary. The model may recommend an early long entry for a flat symbol close to the v4 selection boundary, or an early exit for an existing long. It may not defer a v4 mandatory exit or the hard stop.

This is exploratory evidence. `experiential/jev-latest` post-dates the replay period, so results contain model look-ahead and cannot authorize promotion. The same council contract can use `tokenrouter/typesafe/jev-1.13` through `POST https://api.tokenrouter.com/api/alpha/decisions`, configured with `TOKENROUTER_API_TOKEN[_FILE]` and optional `TOKENROUTER_BASE_URL`. Provider identity and wire driver remain part of the immutable cache digest, so TokenRouter responses cannot be silently substituted for Experiential responses even when the upstream JEV family is equivalent. The self-hosted `decider/decider-4b` model is also supported through the SystemOne endpoint at `DECIDER_BASE_URL` (default `https://wsl.p.ohost.cloud`), without application-level authentication; its responses have a separate provider/model cache identity.

## Frozen activation and authority

At each complete 4h boundary:

* evaluate every economically open position;
* evaluate each flat, eligible symbol with rank `<= top_n+2`, positive absolute trend, and regime other than `risk_off`;
* preserve the v4 rebalance interval (`48h` in the frozen request);
* `observe_v2` records decisions and must remain economically byte-identical to 1.1.0;
* `active_v2` may sell a complete open position, or buy a flat near-signal symbol subject to v4 position sizing, position count, gross/net, cash, constraints, volume participation and execution costs;
* execute normal v4 decisions first. A model action cannot cancel or reverse a mandatory v4 exit. A symbol already opened by the normal v4 decision is not bought twice.

The first full Decider replay established the v2 exploratory baseline. The next pre-registered iteration keeps the bear and exit score thresholds unchanged, raises entry selectivity, and uses action-specific final choices:

* entry/admit iff final=`admit`, bull `>=0.70`, bear `<0.50`;
* otherwise an entry answer is `wait_for_normal_signal` or `reject` and no early position is opened;
* early exit iff final=`exit_early`, bear `>=0.60`, hodl `<0.40`; otherwise final=`keep`.

The entry threshold change was selected from post-hoc diagnostics of the prior run and therefore requires a new full replay; it is not validation evidence by itself. The bear threshold remains fixed for this experiment.

Unavailable/invalid provider responses fall back to v4. The tenth failed request aborts. Cache corruption and cancellation abort immediately. Returned text is never executed; only typed `answers.<question_id>` is consumed.

## Point-in-time state v2

The anonymized, stable state contains no ticker, asset ID, exchange ID, wall-clock date, price level, or model secret. It contains:

* compact multi-horizon 4h indicators over up to 360 completed bars for the asset and benchmark: returns, realized volatility, ATR, RSI, moving-average distance, drawdown, range position, volume ratios and the latest six relative bars;
* compact 1/7/30/90-day return summaries plus 30-day volatility and moving-average distance over up to 180 completed daily bars for asset and benchmark (short history is explicit and allowed);
* position duration, unrealized P&L, MFE and MAE when held;
* the latest three per-symbol v4 factor observations accumulated only from completed 4h computations;
* the top four rows of the current anonymized cross-sectional factor snapshot, sorted by rank;
* current v4 regime, rank, factor values and proposed action;
* round-trip fee/slippage estimate and required six-bar edge;
* distance to the normal entry rank, available position slots, current gross exposure and per-decision turnover budget remaining before orders at that boundary;
* for held positions, giveback from maximum favorable excursion in addition to unrealized P&L, MFE and MAE.

The final entry question explicitly compares early admission with waiting for the normal signal or rejecting the setup, including transaction costs and the opportunity cost of crowding out a stronger position. The held-position prompt explicitly evaluates deterioration, MFE giveback, adverse excursion, weakening rank, recovery potential and whipsaw risk instead of defaulting to the status quo.

The serialized state is hard-capped at 1,800 bytes (approximately 3 KiB for the complete request including questions) to provide comfortable latency for the self-hosted model; the full bounded histories remain inputs to deterministic local feature computation rather than being copied verbatim into the model request. The state is hashed into replay evidence. History is loaded only from the validated immutable manifest, filtered by knowledge cutoff and `available_at <= replay end`; each decision still slices only bars completed by its decision time.

## Determinism and evidence

Model responses are replay-authoritative through the PostgreSQL request cache. Evaluation output is reassembled in deterministic symbol order. Cache telemetry is excluded from the artifact digest. v2 has a separately bounded trace allowance sufficient for the expected 8–12k evaluations; Stage 07 source artifacts continue to exclude council traces.

The 1.4.0 implementation digest chains the 1.1.0 digest with `decision-council-v2` and the complete v2 prompt/state contract digest, including the compact state schema identity. Historical 1.1.0–1.3.0 digests and v1 state bytes remain unchanged.
