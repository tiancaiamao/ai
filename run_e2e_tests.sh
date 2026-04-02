#!/bin/bash
# Run all end-to-end tests for the AI agent

set -e

echo "=========================================="
echo "AI Agent End-to-End Test Suite"
echo "=========================================="
echo ""

# Build the project first
echo "Building project..."
go build -o ./bin/ai ./cmd/ai || { echo "Build failed"; exit 1; }
echo "✓ Build successful"
echo ""

# Run all E2E tests
echo "Running end-to-end tests..."
echo ""

# Run tests by category
echo "Category 1: Event Sourcing - Journal Replay"
echo "-------------------------------------------"
go test -v ./pkg/agent -run TestE2E_1 || true
echo ""

echo "Category 2: Trigger Conditions"
echo "--------------------------------"
go test -v ./pkg/agent -run TestE2E_2 || true
echo ""

echo "Category 3: Mode-Specific Rendering"
echo "------------------------------------"
go test -v ./pkg/agent -run TestE2E_3 || true
echo ""

echo "Category 4: Checkpoint Persistence"
echo "-----------------------------------"
go test -v ./pkg/agent -run TestE2E_4 || true
echo ""

echo "Category 5: Context Management Flow"
echo "-------------------------------------"
go test -v ./pkg/agent -run TestE2E_5 || true
echo ""

echo "Category 6: Session Operations"
echo "-------------------------------"
go test -v ./pkg/agent -run TestE2E_6 || true
echo ""

echo "Category 7: Message Visibility"
echo "------------------------------"
go test -v ./pkg/agent -run TestE2E_7 || true
echo ""

echo "Category 8: Multi-Round Conversations"
echo "--------------------------------------"
go test -v ./pkg/agent -run TestE2E_8 || true
echo ""

echo "Category 9: Token Management"
echo "-----------------------------"
go test -v ./pkg/agent -run TestE2E_9 || true
echo ""

echo "Category 10: Error Handling"
echo "---------------------------"
go test -v ./pkg/agent -run TestE2E_10 || true
echo ""

# Run all E2E tests together
echo "=========================================="
echo "Running All E2E Tests Together"
echo "=========================================="
go test -v ./pkg/agent -run TestE2E || true
echo ""

echo "=========================================="
echo "E2E Test Suite Complete"
echo "=========================================="
