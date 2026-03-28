#!/bin/bash
# Demo: Using orchestrate skill with delegate pattern
# This script demonstrates serial delegation with two-stage review

set -e

OUTPUT_DIR="/tmp/orchestrate-demo"
mkdir -p "$OUTPUT_DIR"

echo "=== Orchestrate Skill Demo ==="
echo ""

# Check if subagent skill exists
if [ ! -f "$HOME/.ai/skills/subagent/bin/start_subagent_tmux.sh" ]; then
    echo "ERROR: subagent skill not found at ~/.ai/skills/subagent/"
    exit 1
fi

# Check if tmux_wait exists
if [ ! -f "$HOME/.ai/skills/tmux/bin/tmux_wait.sh" ]; then
    echo "ERROR: tmux skill not found at ~/.ai/skills/tmux/"
    exit 1
fi

echo "✓ All dependencies found"
echo ""
echo "Usage:"
echo "  ~/.ai/skills/orchestrate/bin/demo-delegate.sh"
echo ""
echo "This is a demo script. To use orchestrate:"
echo ""
echo "1. Create task list in a file"
echo "2. Run subagent with delegate-task.md template:"
echo ""
echo "   SESSION=\$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \\"
echo "     /tmp/output.txt \\"
echo "     15m \\"
echo "     @~/.ai/skills/orchestrate/references/delegate-task.md \\"
echo "     \"Your task description\")"
echo ""
echo "3. Wait for completion:"
echo "   ~/.ai/skills/tmux/bin/tmux_wait.sh \"\$(echo \$SESSION | cut -d: -f1)\" 900"
echo ""
echo "See ~/.ai/skills/orchestrate/SKILL.md for full documentation"
