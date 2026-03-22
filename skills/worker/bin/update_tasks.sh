#!/bin/bash
# update_tasks.sh - Update task status in tasks.md
# Usage: update_tasks.sh <tasks.md> <task_pattern> <status>
#   <tasks.md>      Path to tasks.md file
#   <task_pattern>  Pattern to match task (fuzzy)
#   <status>        new status: done, in_progress, failed

set -e

TASKS_FILE="$1"
PATTERN="$2"
STATUS="$3"

if [ -z "$TASKS_FILE" ] || [ -z "$PATTERN" ] || [ -z "$STATUS" ]; then
    echo "Usage: update_tasks.sh <tasks.md> <task_pattern> <status>"
    echo "  <task_pattern>  Pattern to match (fuzzy)"
    echo "  <status>        done | in_progress | failed"
    exit 1
fi

if [ ! -f "$TASKS_FILE" ]; then
    echo "Error: tasks.md not found: $TASKS_FILE"
    exit 1
fi

# Convert status to checkbox format
case "$STATUS" in
    done)
        CHECKBOX="[X]"
        ;;
    in_progress)
        CHECKBOX="[-]"
        ;;
    failed)
        CHECKBOX="[!]"
        ;;
    pending|todo)
        CHECKBOX="[ ]"
        ;;
    *)
        echo "Error: Unknown status: $STATUS"
        exit 1
        ;;
esac

# Find the line number of the task pattern
# Look for lines starting with - [ ] or - [X] etc. containing the pattern
LINE_NUM=$(grep -n -i "$PATTERN" "$TASKS_FILE" | head -1 | cut -d: -f1)

if [ -z "$LINE_NUM" ]; then
    echo "Warning: Pattern not found: $PATTERN"
    exit 0
fi

echo "Found task at line $LINE_NUM"

# Update the checkbox in the found line
# Use different syntax for macOS sed
if sed -i '' "${LINE_NUM}s/\[.\]/${CHECKBOX}/" "$TASKS_FILE" 2>/dev/null; then
    echo "Updated: $PATTERN → $CHECKBOX"
else
    # Fallback for GNU sed (Linux)
    sed -i "${LINE_NUM}s/\[.\]/${CHECKBOX}/" "$TASKS_FILE" 2>/dev/null && \
        echo "Updated: $PATTERN → $CHECKBOX"
fi

exit 0