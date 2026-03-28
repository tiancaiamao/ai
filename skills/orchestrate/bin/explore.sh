#!/bin/bash
# Quick parallel explore - simpler interface
# Usage: explore <topic1> <topic2> ... -- <your question>

set -e

if [[ $# -lt 2 ]]; then
    echo "Usage: explore <topic1> [topic2] ... -- <your question>"
    echo ""
    echo "Example:"
    echo "  explore gsd-2 picoclaw codex -- 'How does each project implement subagent orchestration?'"
    exit 1
fi

# Find the separator
SEPARATOR_INDEX=0
TOPICS=()
QUESTION=""

for i in "$@"; do
    if [[ "$i" == "--" ]]; then
        SEPARATOR_INDEX=$((SEPARATOR_INDEX + 1))
        shift
        QUESTION="$*"
        break
    fi
    TOPICS+=("$i")
    shift
done

if [[ -z "$QUESTION" ]]; then
    QUESTION="${TOPICS[-1]}"
    unset 'TOPICS[-1]'
fi

echo "Exploring: ${TOPICS[*]}"
echo "Question: $QUESTION"
echo ""

# Create output file
OUTPUT="/tmp/orchestrate-explore-$(date +%s).txt"
> "$OUTPUT"

for topic in "${TOPICS[@]}"; do
    TOPIC_OUTPUT="/tmp/explore-single-$RANDOM.txt"
    
    echo "=== Exploring: $topic ===" >> "$OUTPUT"
    
    ~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
        "$TOPIC_OUTPUT" \
        5m \
        @~/.ai/skills/orchestrate/references/explorer.md \
        "Topic: $topic\nQuestion: $QUESTION"
    
    SESSION_NAME=$(head -1 "$TOPIC_OUTPUT" | cut -d: -f1 2>/dev/null || echo "")
    if [[ -n "$SESSION_NAME" ]]; then
        ~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" 300
    fi
    
    tail -n +2 "$TOPIC_OUTPUT" >> "$OUTPUT" 2>/dev/null || true
    echo "" >> "$OUTPUT"
    rm -f "$TOPIC_OUTPUT"
done

echo ""
echo "=== Results ==="
cat "$OUTPUT"
echo ""
echo "Saved to: $OUTPUT"
