#!/bin/bash
# Parallel Exploration - Execute multiple explore tasks concurrently
# Usage: explore-parallel.sh <topic1> <topic2> ... <topicN>
# Or: explore-parallel.sh --topics <file_with_topics>

set -e

MAX_PARALLEL=${MAX_PARALLEL:-5}
TIMEOUT=${TIMEOUT:-600}  # 10 minutes per task

usage() {
    echo "Usage: $0 <topic1> [topic2] [topic3] ..."
    echo "   or: $0 --topics <file>"
    echo ""
    echo "Environment variables:"
    echo "  MAX_PARALLEL=$MAX_PARALLEL  # Max concurrent explorations"
    echo "  TIMEOUT=$TIMEOUT           # Seconds per task"
    exit 1
}

# Parse arguments
TOPICS=()
MODE="args"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --topics)
            MODE="file"
            TOPICS_FILE="$2"
            shift 2
            ;;
        --help|-h)
            usage
            ;;
        *)
            TOPICS+=("$1")
            shift
            ;;
    esac
done

# Load topics from file if specified
if [[ "$MODE" == "file" ]]; then
    if [[ ! -f "$TOPICS_FILE" ]]; then
        echo "ERROR: File not found: $TOPICS_FILE"
        exit 1
    fi
    TOPICS=($(cat "$TOPICS_FILE"))
fi

# Validate
if [[ ${#TOPICS[@]} -eq 0 ]]; then
    echo "ERROR: No topics provided"
    usage
fi

echo "=== Parallel Explorer ==="
echo "Topics: ${TOPICS[*]}"
echo "Max parallel: $MAX_PARALLEL"
echo "Timeout per task: ${TIMEOUT}s"
echo ""

OUTPUT_DIR="/tmp/orchestrate-explore-$(date +%s)"
mkdir -p "$OUTPUT_DIR"

# Export for subshells
export OUTPUT_DIR
export TIMEOUT

# Run explorations in parallel (with limit)
PIDS=()
INDEX=0

for topic in "${TOPICS[@]}"; do
    # Check if we're at the limit
    while [[ ${#PIDS[@]} -ge $MAX_PARALLEL ]]; do
        # Wait for any to finish
        for i in "${!PIDS[@]}"; do
            if ! kill -0 "${PIDS[$i]}" 2>/dev/null; then
                unset 'PIDS[$i]'
            fi
        done
        PIDS=("${PIDS[@]}")  # Re-index
        sleep 1
    done
    
    TASK_NUM=$((INDEX + 1))
    OUTPUT_FILE="$OUTPUT_DIR/explore-${TASK_NUM}.txt"
    
    echo "Starting exploration $TASK_NUM: $topic"
    
    # Start subagent for this exploration
    ~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
        "$OUTPUT_FILE" \
        "${TIMEOUT}s" \
        @~/.ai/skills/workflow/orchestrate/references/explorer.md \
        "$topic" &
    
    PIDS+=($!)
    INDEX=$((INDEX + 1))
done

# Wait for all to complete
echo ""
echo "Waiting for ${#PIDS[@]} explorations to complete..."
for pid in "${PIDS[@]}"; do
    wait $pid 2>/dev/null || true
done

# Merge results
echo ""
echo "=== Merged Exploration Results ==="
echo "Output directory: $OUTPUT_DIR"
echo ""

for f in "$OUTPUT_DIR"/explore-*.txt; do
    if [[ -f "$f" ]]; then
        echo "--- $(basename $f) ---"
        cat "$f"
        echo ""
    fi
done

echo "Results saved to: $OUTPUT_DIR/"
