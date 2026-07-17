# Stage 05/06 backtest reproduction

Pin the database, manifest-backed settings, UTC interval, costs, registered strategy identity/version, exposure, and final policy. Example Stage 06 candidate reproduction:

```bash
export DATABASE_URL='postgres://USER:PASSWORD@HOST:5432/trading_bot?sslmode=require'
export STAGE08_NEW_BACKTEST=research
/home/andrei/.local/opt/go-v1.26.1/bin/go run ./cmd/backtest \
  -symbols BTCUSDT,ETHUSDT -start 2025-01-01 -end 2025-06-30 \
  -fee-bps 10 -slippage-bps 5 -universe-mode dynamic_replay \
  -strategy trend_momentum_candidate -strategy-version 1.0.0 \
  -target-gross 1 -max-net 1 -final-policy liquidate
```

Copy the exact `reproduce` invocation from the immutable Stage 07 manifest when validating promotion evidence. Compare artifact/manifest digests and classifications. `coverage_failed`, `gating_zero_trades`, and `strategy_zero_trades` have different meanings and are exposed separately by operational status. A completed command is not evidence of profitability or promotion eligibility.
