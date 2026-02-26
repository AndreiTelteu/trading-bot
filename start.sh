#!/bin/bash
set -e

cd frontend && npm i && npm run build && cd ..

uv pip install --system --no-cache-dir -e .

uv run python -m backend.app
