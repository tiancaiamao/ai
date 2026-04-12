#!/bin/bash
# Mock: stage processor — adds a stage header
# Usage: stage-mock.sh <input-file>
echo "=== Processed by $(basename "$0") ==="
cat "$1"
echo "=== End ==="