# Offline learned-model proposals

This directory is not a trading, backtest, validation, or promotion subsystem.
Go/PostgreSQL is authoritative for dataset construction, point-in-time
constraints, fold evaluation, portfolio simulation, costs, manifests,
promotion evidence, and runtime inference.

Python is retained only for optional offline sklearn logistic experimentation.
It cannot connect to `trading.db`, SQLite, or PostgreSQL; it accepts only a
Go-exported, hash-bound dataset. Its `research_proposal` output is explicitly
rejected by Go runtime loading and cannot grant paper/live authority.

## Export a proposal dataset

Build the complete Stage 04 manifest and matching universe snapshots, then
export fixed-horizon, after-cost labeled decision cohorts:

```bash
go run ./cmd/marketdata -action export-model-dataset \
  -manifest-id <64-hex-stage04-manifest> \
  -policy-version <governed-composite-policy-version> \
  -start 2026-01-01T00:00:00Z -end 2026-06-01T00:00:00Z \
  -label-horizon 24h -output-dir /controlled/research/export-2026-06
```

The new output directory contains `dataset.jsonl` and
`dataset.manifest.json`. The exporter fails closed unless each row is a labeled
cohort with a feature snapshot bound through its universe snapshot to the
supplied immutable dataset manifest. It rejects missing labels, feature quality
flags, schema drift, non-finite values, incomplete coverage, and reused output
directories.

## Generate a proposal

```bash
python -m pip install -r research/learned_model/requirements.txt
python research/learned_model/train_logistic.py \
  --dataset-dir /controlled/research/export-2026-06 \
  --train-end 2026-04-01T00:00:00Z \
  --validation-end 2026-05-01T00:00:00Z \
  --version logistic-proposal-2026-06 \
  --output-path /controlled/research/proposals/logistic-proposal-2026-06.json
```

The output uses `calibration_method: unvalidated_identity`; its exploratory
classification diagnostics are not calibration, portfolio, cost, or promotion
evidence. Recreate a candidate under the shared Go contracts and use Stage 07
for any research result that needs evaluation or promotion.

Run the optional boundary check:

```bash
cd research/learned_model && python test_proposal_contract.py
```
