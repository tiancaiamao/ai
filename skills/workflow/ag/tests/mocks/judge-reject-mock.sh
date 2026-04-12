#!/bin/bash
# Mock: judge that rejects first 2 times, approves on 3rd
# Uses a fixed counter file (caller must clean up)
COUNT_FILE="/tmp/ag-test-judge-multi-round"
if [ ! -f "$COUNT_FILE" ]; then
    echo "1" > "$COUNT_FILE"
    echo "REJECTED: round 1, not good enough"
elif [ "$(cat "$COUNT_FILE")" = "1" ]; then
    echo "2" > "$COUNT_FILE"
    echo "REJECTED: round 2, still needs work"
else
    rm -f "$COUNT_FILE"
    echo "APPROVED: finally acceptable"
fi