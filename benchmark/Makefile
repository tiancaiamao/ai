.PHONY: help list run clean

# ============================================
# Configuration
# ============================================

RUNNER_DIR := runner
AGENT ?= my-agent
MODEL ?=
TASK ?=

# ============================================
# Help & Documentation
# ============================================

help:
	@echo ""
	@echo "========================================"
	@echo "   AI Agent Benchmark - A/B Testing"
	@echo "========================================"
	@echo ""
	@echo "Quick Start:"
	@echo "  make reset                           # Reset all tasks to initial state"
	@echo "  make test AGENT=my-agent             # Test your agent on all tasks"
	@echo "  make compare-agent AGENTS=\"a b\"     # Compare multiple agents"
	@echo ""
	@echo "----------------------------------------"
	@echo " Basic Commands"
	@echo "----------------------------------------"
	@echo "  make list                            # List all tasks"
	@echo "  make run                             # Run verification only (no agent)"
	@echo "  make reset                           # Reset all tasks to initial state"
	@echo "  make reset-task TASK=<id>            # Reset specific task"
	@echo "  make clean                           # Clean generated files"
	@echo ""
	@echo "----------------------------------------"
	@echo " A/B Testing"
	@echo "----------------------------------------"
	@echo "  make test AGENT=<name> [MODEL=<m>]   # Test agent (optionally with model)"
	@echo "  make test-task TASK=<id>             # Test single task"
	@echo "  make compare-agent AGENTS=\"a b c\"    # Compare multiple agents"
	@echo "  make compare-model MODELS=\"m1 m2\"    # Compare models on same agent"
	@echo "  make report                          # Show historical results"
	@echo ""
	@echo "----------------------------------------"
	@echo " Regression Testing"
	@echo "----------------------------------------"
	@echo "  make baseline                        # Save current results as baseline"
	@echo "  make diff                            # Compare current vs baseline"
	@echo ""
	@echo "----------------------------------------"
	@echo " Configuration"
	@echo "----------------------------------------"
	@echo "  make list-agents                     # List available agents"
	@echo "  make list-models                     # List available models"
	@echo "  make list-all                        # List everything"
	@echo ""
	@echo "----------------------------------------"
	@echo " Examples"
	@echo "----------------------------------------"
	@echo "  make reset && make test AGENT=my-agent"
	@echo "  make test AGENT=my-agent MODEL=glm5"
	@echo "  make compare-agent AGENTS=\"my-agent claude-code\""
	@echo "  make compare-model MODELS=\"glm5 deepseek\""
	@echo ""

# ============================================
# Basic Commands
# ============================================

## list: List all available benchmark tasks
list:
	@go run $(RUNNER_DIR)/runner.go list

## run: Run all benchmark tasks (verification only, no agent)
run:
	@go run $(RUNNER_DIR)/runner.go run

## clean: Clean up generated files
clean:
	@rm -f results/current.json 2>/dev/null || true
	@rm -f tasks/*/setup/*.test 2>/dev/null || true
	@rm -f tasks/*/setup/basic 2>/dev/null || true
	@rm -f tasks/*/setup/assembler 2>/dev/null || true
	@rm -f tasks/*/setup/counter 2>/dev/null || true
	@echo "✓ Cleaned up generated files"

## reset: Reset all tasks to initial state (before agent fixes)
reset:
	@./reset.sh

## reset-task: Reset specific task to initial state
reset-task:
ifndef TASK
	@echo "Error: TASK is required"
	@echo "Usage: make reset-task TASK=<task_id>"
	@exit 1
endif
	@./reset.sh $(TASK)

# ============================================
# A/B Testing
# ============================================

## test: Run A/B test with specified agent (optionally with model)
test:
ifndef AGENT
	@echo "Error: AGENT is required"
	@echo "Usage: make test AGENT=<agent_name> [MODEL=<model_name>]"
	@exit 1
endif
ifdef MODEL
	@python3 ab_test.py --agent $(AGENT) --model $(MODEL)
else
	@python3 ab_test.py --agent $(AGENT)
endif

## test-task: Run A/B test on a single task
test-task:
ifndef TASK
	@echo "Error: TASK is required"
	@echo "Usage: make test-task TASK=<task_id>"
	@exit 1
endif
	@python3 ab_test.py --agent $(AGENT) --task $(TASK)

## compare-agent: Compare multiple agents
compare-agent:
ifndef AGENTS
	@echo "Error: AGENTS is required"
	@echo "Usage: make compare-agent AGENTS=\"agent1 agent2 agent3\""
	@exit 1
endif
	@python3 ab_test.py --compare $(AGENTS)

## compare-model: Compare multiple models on same agent
compare-model:
ifndef MODELS
	@echo "Error: MODELS is required"
	@echo "Usage: make compare-model MODELS=\"model1 model2\""
	@echo "Note: Uses AGENT=$(AGENT) by default"
	@exit 1
endif
	@for model in $(MODELS); do \
		echo ""; \
		echo "Testing with model: $$model"; \
		python3 ab_test.py --agent $(AGENT) --model $$model; \
	done

## report: Generate report from all A/B test results
report:
	@python3 ab_test.py --report

# ============================================
# Regression Testing
# ============================================

## baseline: Save current results as baseline
baseline:
	@if [ ! -f results/current.json ]; then \
		echo "Error: No current results. Run 'make run' or 'make test' first."; \
		exit 1; \
	fi
	@cp results/current.json results/baseline.json
	@echo "✓ Baseline saved to results/baseline.json"

## diff: Compare current results with baseline
diff:
	@if [ ! -f results/baseline.json ]; then \
		echo "Error: No baseline found. Run 'make baseline' first."; \
		exit 1; \
	fi
	@go run $(RUNNER_DIR)/runner.go compare

# ============================================
# Configuration
# ============================================

## list-agents: List available agents
list-agents:
	@echo "Available Agents:"
	@echo "================="
	@python3 ab_test.py --list 2>/dev/null | grep -A 10 "Available Agents:" | tail -n +2 || \
		grep -A 10 "agents:" agents.yaml | grep "  [a-z]" | sed 's/  /  - /' | sed 's/:/ :/'

## list-models: List available models
list-models:
	@echo "Available Models:"
	@echo "================="
	@python3 ab_test.py --list 2>/dev/null | grep -A 10 "Available Models:" | tail -n +2 || \
		grep -A 10 "models:" agents.yaml | grep "  [a-z]" | sed 's/  /  - /' | sed 's/:/ :/'

## list-all: List all available agents, models, and tasks
list-all:
	@python3 ab_test.py --list

# ============================================
# Convenience (backward compatibility)
# ============================================

agent:
	@./run_agent.sh

agent-task:
ifndef TASK
	@echo "Error: TASK is required"
	@echo "Usage: make agent-task TASK=<task_id>"
	@exit 1
endif
	@./run_agent.sh $(TASK)
