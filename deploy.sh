#!/bin/bash
# Deploy script for trading platform

echo "Deploying trading platform to trading.ryzen.cloud..."

ssh andrei@trading.ryzen.cloud "cd trading && git pull && docker compose restart app"

echo "Deployment complete!"
