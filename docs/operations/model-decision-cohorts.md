# Model decision cohorts and fixed-horizon labels

Learned-model monitoring uses `decision_cohorts`, not only closed paper
positions. One row is written for every model-eligible shortlist candidate,
including opportunities the model policy rejects. Its deterministic identity
binds the decision timestamp, symbol, fixed horizon, model-artifact digest,
policy version, and fixed after-cost model. Feature snapshot and prediction-log
references preserve the point-in-time inputs.

`accepted` means the model selection policy accepted the opportunity; it does
not imply a paper order was sent. In `research_only` and `shadow` rollout
states, predictions remain advisory and no cohort grants execution authority.
Direct live submission remains fenced.

## Label lifecycle and recovery

The runtime retries mature cohorts during each analysis cycle. It reads only a
valid persisted Stage 04 `decision` 15-minute bar for the same exchange symbol,
at the cohort maturity timestamp. The after-cost return is the fixed-horizon
price return less the stored round-trip paper fee/slippage basis points. It does
not use the realized P&L of a position, a current quote, or a substituted
listing.

States are:

- `pending`: the fixed horizon has not matured.
- `unavailable`: it matured but the required stored bar was absent or not yet
  available; the row stays retryable.
- `labeled`: one immutable fixed-horizon outcome was recorded.

Do not backfill legacy `prediction_logs` as cohorts: they lack the complete
stored identity and cost/horizon evidence. Re-ingest the required point-in-time
bar data, then allow the ordinary retry worker to recover `unavailable` rows.
Never manually set an outcome, use a current price, or classify missing labels
as losses.

## Monitoring interpretation

`matured_label_count`, pending/unavailable counts, and `label_coverage` must
be inspected before calibration or drift evidence is used. Calibration buckets
contain only `labeled` observations; their counts are not prediction counts.
Low coverage is insufficient evidence, not poor performance. Feature drift is
ordered by absolute z-score, retaining both positive and negative deviations.

`model_label_horizon` is an authority-affecting model setting (default `24h`)
and cannot be changed using the generic settings endpoint. Alter it only by the
governed research/promotion workflow; existing cohort rows retain their stored
horizon and cost identity.
