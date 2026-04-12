#!/bin/bash
# test-real-pair.sh — Real agent pair test (burns tokens!)
#
# A minimal worker-judge test:
#   Worker: summarize a piece of text
#   Judge:  check if summary mentions the key point
#
# Usage: bash test-real-pair.sh
set -euo pipefail

AG_BIN="$(cd "$(dirname "$0")/.." && pwd)/ag"
WORKSPACE="/tmp/ag-real-test-$$"
mkdir -p "$WORKSPACE"
cd "$WORKSPACE"

# Create prompt files
cat > worker-prompt.md << 'EOF'
You are a summarizer. Read the input text and write a 2-3 sentence summary.
Focus on the main point. Be concise.
Write ONLY the summary, nothing else.
EOF

cat > judge-prompt.md << 'EOF'
You are a reviewer. The input contains a summary of a text.
Check if the summary mentions "quantum computing".
If it does, respond with exactly: APPROVED
If it does not, respond with exactly: REJECTED and explain what is missing.
Keep your response to one line.
EOF

cat > input.md << 'EOF'
Quantum computing is a rapidly advancing field that promises to solve problems 
classical computers cannot. Recent breakthroughs in error correction have brought 
us closer to practical quantum advantage. Companies like IBM and Google are racing 
to build the first fault-tolerant quantum computer, which could revolutionize 
drug discovery, cryptography, and materials science.
EOF

echo "=== Running real agent pair test ==="
echo "Worker: summarize text"
echo "Judge:  check if summary mentions 'quantum computing'"
echo ""

$AG_BIN patterns/pair.sh worker-prompt.md judge-prompt.md input.md 3

EXIT=$?
echo ""
if [ $EXIT -eq 0 ]; then
  echo "✅ Real pair test passed"
else
  echo "❌ Real pair test failed (exit $EXIT)"
fi

# Cleanup
rm -rf "$WORKSPACE"
exit $EXIT