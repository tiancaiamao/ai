#!/bin/bash
# Prebuild check for orchestrate binary
# If binary doesn't exist, build it automatically

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BINARY="$SCRIPT_DIR/bin/orchestrate"

# Check if binary exists
if [ ! -f "$BINARY" ]; then
    echo "🔨 Orchestrate binary not found, building..."
    cd "$SCRIPT_DIR"
    go build -o bin/orchestrate ./cmd/main.go
    echo "✅ Build complete: $BINARY"
else
    echo "✅ Orchestrate binary found: $BINARY"
fi