#!/bin/bash
# Run hashline A/B tests - compares hashline mode vs traditional mode

set -e

BENCHMARK_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$BENCHMARK_DIR"

echo "========================================"
echo "Hashline A/B Testing"
echo "========================================"
echo ""

# Check if benchmark tools are built
if [ ! -f "../bin/benchmark" ]; then
    echo "Building benchmark tools..."
    make bench-build
fi

# Check if agent binary exists
AI_BIN="${AI_BIN:-/Users/genius/go/bin/ai}"
if [ ! -f "$AI_BIN" ]; then
    echo "Error: AI binary not found at $AI_BIN"
    echo "Please set AI_BIN environment variable or build the agent"
    exit 1
fi

# Create temporary config for hashline mode
HASHLINE_CONFIG="/tmp/ai_hashline_config.json"
cat > "$HASHLINE_CONFIG" << 'EOF'
{
  "toolOutput": {
    "hashLines": true
  },
  "edit": {
    "mode": "hashline"
  }
}
EOF

# Create temporary config for traditional mode
TRADITIONAL_CONFIG="/tmp/ai_traditional_config.json"
cat > "$TRADITIONAL_CONFIG" << 'EOF'
{
  "toolOutput": {
    "hashLines": false
  },
  "edit": {
    "mode": "replace"
  }
}
EOF

# Create agents.yaml for A/B testing
AGENTS_CONFIG="/tmp/hashline_ab_agents.yaml"
cat > "$AGENTS_CONFIG" << 'EOF'
agents:
  hashline:
    name: "Hashline Mode"
    command: 'ai --mode headless --max-turns 50 --config /tmp/ai_hashline_config.json "{prompt}"'
    type: "custom"

  traditional:
    name: "Traditional Mode"
    command: 'ai --mode headless --max-turns 50 --config /tmp/ai_traditional_config.json "{prompt}"'
    type: "custom"

models:
  glm5:
    name: "GLM-5"
    provider: "zai"

config:
  timeout: 300
  max_turns: 50
  retries: 1
EOF

# Copy the config files to /tmp for the agents to access
cp "$HASHLINE_CONFIG" /tmp/ai_hashline_config.json
cp "$TRADITIONAL_CONFIG" /tmp/ai_traditional_config.json

echo "Test configuration:"
echo "  - Hashline mode:   hashLines=true, edit.mode=hashline"
echo "  - Traditional mode: hashLines=false, edit.mode=replace"
echo ""

# Run A/B tests
echo "Running A/B comparison..."
python3 ab_test.py \
    --compare hashline traditional \
    --task 020_hashline_ambiguous_edit \
    --task 021_hashline_formatting_preserves \
    --task 022_hashline_token_efficiency

echo ""
echo "========================================"
echo "A/B Test Complete"
echo "========================================"
echo ""
echo "To view detailed results:"
echo "  make ab-report"
echo ""
echo "To run individual tasks:"
echo "  python3 ab_test.py --agent hashline --task 020_hashline_ambiguous_edit"
echo "  python3 ab_test.py --agent traditional --task 020_hashline_ambiguous_edit"