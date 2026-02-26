#!/bin/bash
# Start script for Trading Bot

cd "$(dirname "$0")"

echo "Installing frontend dependencies..."
cd frontend
npm install

echo "Building frontend..."
npm run build

cd ..

echo "Starting Flask server..."
cd backend
export FLASK_APP=app.py
cd ..
python -m backend.app
