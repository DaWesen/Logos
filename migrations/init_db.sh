#!/bin/bash
# Logos Database Initialization Script
# Usage: ./init_db.sh [--seed]

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_ROOT"

echo "=========================================="
echo "  Logos Database Initialization"
echo "=========================================="
echo ""

# Check if Docker is running
if ! docker info > /dev/null 2>&1; then
    echo "Error: Docker is not running!"
    exit 1
fi

# Check if containers are already running
if docker compose ps -q > /dev/null 2>&1; then
    echo "Warning: Some containers are already running!"
    echo ""
    read -p "Do you want to stop and reset? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Aborted."
        exit 0
    fi
    docker compose down -v
fi

# Start database
echo ""
echo "Starting database..."
docker compose up -d postgres

# Wait for database
