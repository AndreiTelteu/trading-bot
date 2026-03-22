#!/bin/bash
# Deploy script for trading platform

git push

echo "Deploying trading platform to trading.local..."

ssh andrei@dockernas "cd trading-bot && git pull && docker compose restart app"

echo "Deployment complete!"
