#!/bin/bash
# Test runner script for ai

set -e

echo "🧪 ai Test Runner"
echo "==================="
echo ""

# Colors
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Parse arguments
VERBOSE=""
COVERAGE=""
SHORT="-short"
PACKAGES=""

while [[ $# -gt 0 ]]; do
  case $1 in
    -v|--verbose)
      VERBOSE="-v"
      shift
      ;;
    -c|--coverage)
      COVERAGE="-cover"
      shift
      ;;
    -s|--short)
      SHORT="-short"
      shift
      ;;
    -p|--package)
      PACKAGES="$2"
      shift 2
      ;;
    *)
      echo "Unknown option: $1"
      exit 1
      ;;
  esac
done

# Default packages if not specified
if [ -z "$PACKAGES" ]; then
  PACKAGES="./..."
fi

echo "Running tests with:"
echo "  Packages: $PACKAGES"
echo "  Verbose: ${VERBOSE:-no}"
echo "  Coverage: ${COVERAGE:-no}"
echo "  Short: ${SHORT:-no}"
echo ""

# Run tests
if go test $SHORT $COVERAGE $VERBOSE $PACKAGES; then
  echo ""
  echo -e "${GREEN}✅ All tests passed!${NC}"
  echo ""

  # Show coverage if requested
  if [ -n "$COVERAGE" ]; then
    echo "Coverage Report:"
    echo "================"
    go test $SHORT $COVERAGE $PACKAGES | grep -E "coverage:|ok"
  fi

  exit 0
else
  echo ""
  echo -e "${RED}❌ Tests failed!${NC}"
  echo ""
  echo "Run with -v for verbose output:"
  echo "  ./scripts/test.sh -v"
  exit 1
fi
