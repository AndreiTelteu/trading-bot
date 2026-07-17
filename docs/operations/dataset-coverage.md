# Stage 04 dataset coverage and backfill

External ingestion availability is not guaranteed. Always start with a bounded dry run and explicit half-open UTC interval:

```bash
export DATABASE_URL='postgres://USER:PASSWORD@HOST:5432/trading_bot?sslmode=require'
export STAGE08_POINT_IN_TIME_UNIVERSE=research
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/marketdata \
  -action ingest -dataset-version vendor-2026-07 -symbol-id binance-btcusdt-v1 \
  -symbol BTCUSDT -timeframe 15m -role decision \
  -start 2026-07-01T00:00:00Z -end 2026-07-02T00:00:00Z -dry-run=true
```

After reviewing provenance/row bounds, repeat with `-dry-run=false`, build a manifest with an explicit `-knowledge-cutoff`, then inspect it:

```bash
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/marketdata -action coverage \
  -manifest-id MANIFEST_ID -start 2026-07-01T00:00:00Z -end 2026-07-02T00:00:00Z \
  -symbol-id binance-btcusdt-v1 -timeframe 15m -role decision
```

Build universe ranges first with `-dry-run=true`; supply the actual benchmark symbol/asset IDs and policy version. Missing benchmark, constraints, bars, or incomplete membership is a coverage failure, not an empty valid strategy result. Corrections require a new dataset version/manifest; never overwrite immutable history.
