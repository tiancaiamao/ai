.PHONY: build test clean vet coverage bench bench-build bench-run bench-resume bench-list bench-task bench-import bench-compare bench-baseline bench-clean bench-precheck reset reset-task reset-hard ab-list ab-test ab-compare ab-benchmark ab-report help

MANIFEST_FLAG := $(if $(strip $(MANIFEST)),--manifest $(MANIFEST),)
MAX_STEPS_MODE_FLAG := $(if $(strip $(MAX_STEPS_MODE)),--max-steps-mode $(MAX_STEPS_MODE),)
ASSERT_INIT_FAIL_FLAG := $(if $(filter 1 true yes,$(ASSERT_INIT_FAIL)),--assert-initial-fail,)
DEFAULT_PRECHECK_MANIFEST ?= tasks/agent_v1_manifest.json
PRECHECK_MANIFEST := $(or $(strip $(MANIFEST)),$(DEFAULT_PRECHECK_MANIFEST))
PRECHECK_MANIFEST_FLAG := $(if $(strip $(PRECHECK_MANIFEST)),--manifest $(PRECHECK_MANIFEST),)

# Default target
all: build

# Help - show all available commands
help:
	@echo "============================================================"
	@echo "ai-agent-benchmark"
	@echo "============================================================"
	@echo ""
	@echo "常用命令 (Quick Start):"
	@echo "  make bench-build                    构建 benchmark 工具"
	@echo "  make bench-precheck                 检查所有任务初始态是否失败 (确保任务可测)"
	@echo "  make bench-run                      运行所有任务 (从头开始)"
	@echo "  make bench-run TASK=agent_002       运行单个任务"
	@echo "  make bench-resume                   从上次中断处继续"
	@echo "  make reset                          重置所有任务到初始状态"
	@echo "  make reset TASK=agent_002           重置单个任务"
	@echo ""
	@echo "所有命令 (All Commands):"
	@echo "  make help                           显示此帮助信息"
	@echo "  make bench-build                    构建 benchmark 工具"
	@echo "  make bench                          运行所有任务 (bench-run 的别名)"
	@echo "  make bench-run                      运行所有任务 (从头开始)"
	@echo "  make bench-run TASK=xxx             运行单个任务"
	@echo "  make bench-resume                   从上次中断处继续"
	@echo "  make bench-precheck                 检查初始态 (默认 agent_v1_manifest)"
	@echo "  make bench-list                     列出所有任务"
	@echo "  make bench-compare                  对比上次结果"
	@echo "  make bench-baseline                 保存当前为基准"
	@echo "  make bench-clean                    清理结果文件"
	@echo "  make reset                          重置所有任务 (保留 results/current.json)"
	@echo "  make reset TASK=xxx                 重置单个任务"
	@echo "  make reset-hard                    强重置 (清理 nested repos, progress 等)"
	@echo "  make reset-all                     清理所有任务和所有结果"
	@echo ""
	@echo "A/B 测试:"
	@echo "  make ab-list                        列出可用 agent/model/task"
	@echo "  make ab-test AGENT=xxx              运行 A/B 单 agent 测试"
	@echo "  make ab-compare AGENTS=\"a b\"       对比多个 agent"
	@echo "  make ab-benchmark                   运行 agents.yaml 里所有 agent"
	@echo "  make ab-report                      输出历史 A/B 报告"
	@echo ""
	@echo "参数 (Options):"
	@echo "  AGENT=ai                           指定 agent 二进制 (默认: ../bin/ai)"
	@echo "  AGENTS=\"a b\"                      A/B 对比的 agent 列表"
	@echo "  MODEL=minimax                      A/B 运行模型名 (透传到 ab_test.py)"
	@echo "  TASK=task_id                       运行单任务 (bench-run 或 reset)"
	@echo "  TIMEOUT=0                          单任务超时 (默认: 0=不限制)"
	@echo "  MAX_STEPS_MODE=soft                max_steps 判分模式: soft|hard"
	@echo "  MANIFEST=tasks/xxx.json            按 manifest 精确筛选任务"
	@echo "  RESUME=results/xxx.json            指定进度文件 (默认: progress.json)"
	@echo "  VERBOSE=1                          详细输出"
	@echo "  ASSERT_INIT_FAIL=1                  bench-run 前强制初始态全 FAIL 校验"
	@echo ""
	@echo "使用示例 (Examples):"
	@echo "  # 基本流程"
	@echo "  make bench-build && make bench-precheck && make bench-run"
	@echo ""
	@echo "  # 单任务测试"
	@echo "  make bench-build"
	@echo "  make bench-run TASK=agent_002_rollback"
	@echo "  make reset TASK=agent_002_rollback"
	@echo "  make bench-run TASK=agent_002_rollback"
	@echo ""
	@echo "  # 续跑和对比"
	@echo "  make bench-resume                   # 从上次中断处继续"
	@echo "  make bench-resume RESUME=xxx.json   # 从指定进度文件继续"
	@echo "  make bench-compare                  # 对比 baseline.json 和 current.json"
	@echo "  make bench-baseline                 # 将 current.json 保存为 baseline.json"
	@echo ""
	@echo "  # 使用不同的 manifest"
	@echo "  make bench-precheck MANIFEST=tasks/agent_v1_manifest.json"
	@echo "  make bench-list MANIFEST=tasks/agent_v1_manifest.json"
	@echo "  make bench-run MANIFEST=tasks/agent_v1_manifest.json"
	@echo ""
	@echo "  # A/B 测试"
	@echo "  make ab-test AGENT=my-agent MODEL=minimax TASK=agent_001_forced_exploration"
	@echo "  make ab-compare AGENTS=\"agent-v1 agent-v2\" TASK=agent_002_rollback"
	@echo "  make ab-benchmark                    # 运行 agents.yaml 里所有 agent"
	@echo ""
	@echo "============================================================"

# Build the project
build:
	go build ./...

# Build benchmark tools
bench-build:
	go build -o ../../bin/benchmark ./cmd/benchmark
	go build -o ../../bin/import-tbench ./cmd/import-tbench

# Run benchmark (all tasks) - alias for bench
bench: bench-run

# Run all testable tasks (fresh start)
bench-run: bench-build
	@if [ -n "$(TASK)" ]; then \
		echo "============================================================"; \
		echo "Running task: $(TASK)"; \
		echo "============================================================"; \
		rm -f results/progress.json; \
		../../bin/benchmark --task $(TASK) --agent $(or $(AGENT),../bin/ai) --timeout $(or $(TIMEOUT),0) $(MAX_STEPS_MODE_FLAG) $(MANIFEST_FLAG) $(ASSERT_INIT_FAIL_FLAG); \
	else \
		echo "============================================================"; \
		echo "Running all tasks (fresh start)"; \
		echo "============================================================"; \
		../../bin/benchmark --agent $(or $(AGENT),../bin/ai) --timeout $(or $(TIMEOUT),0) $(MAX_STEPS_MODE_FLAG) $(MANIFEST_FLAG) $(ASSERT_INIT_FAIL_FLAG) --clean; \
	fi

# Verify all selected tasks fail before agent modifications
bench-precheck: bench-build
	@echo "============================================================"
	@echo "Precheck: Verifying initial state fails for manifest"
	@echo "Manifest: $(PRECHECK_MANIFEST)"
	@echo "============================================================"
	../../bin/benchmark --precheck-only $(PRECHECK_MANIFEST_FLAG)

# Resume from previous progress
bench-resume: bench-build
	../../bin/benchmark --agent $(or $(AGENT),../bin/ai) --timeout $(or $(TIMEOUT),0) $(MAX_STEPS_MODE_FLAG) $(MANIFEST_FLAG) --resume $(or $(RESUME),results/progress.json)

# List available benchmark tasks
bench-list: bench-build
	../../bin/benchmark --list $(MANIFEST_FLAG)

# Run specific benchmark task (legacy alias)
bench-task: bench-build
	@echo "Usage: make bench-task TASK=tbench/chess-best-move"
	@echo "Or use: make bench-run TASK=tbench/chess-best-move"
	../../bin/benchmark --task $(TASK) --agent $(or $(AGENT),../../bin/ai) --timeout $(or $(TIMEOUT),0) $(MAX_STEPS_MODE_FLAG) $(MANIFEST_FLAG)

# Import Terminal Bench 2.0 tasks
bench-import:
	../../bin/import-tbench /tmp/tbench2 ../tasks/tbench

# Compare with baseline (requires current.json from bench-run)
bench-compare: bench-build
	@if [ ! -f "results/current.json" ]; then \
		echo "Error: No current results. Run 'make bench-run' first."; \
		exit 1; \
	fi
	@python3 scripts/compare_results.py results/baseline.json results/current.json

# Save current results as baseline
bench-baseline:
	@if [ -f "results/current.json" ]; then \
		cp results/current.json results/baseline.json; \
		echo "Baseline saved to results/baseline.json"; \
	else \
		echo "Error: No current results found. Run 'make bench-run' first."; \
		exit 1; \
	fi

# Clean benchmark results
bench-clean:
	@echo "Cleaning benchmark results..."
	rm -f results/*.json
	rm -rf results/.pytest_cache
	@echo "Done."

# Reset tasks to clean initial state (keep results/current.json)
# Usage: make reset                    # Reset all tasks
#         make reset TASK=xxx          # Reset single task (e.g., agent_002_rollback)
#         make reset-hard              # Force reset (clean nested repos, remove progress)
#         make reset-hard TASK=xxx     # Hard reset single task
#
# What gets cleaned:
# - Task artifacts: .pytest_cache, __pycache__, compiled binaries (*.test, main, asm, etc.)
# - Git untracked files under tasks/ (via git clean -fd)
# - Results: progress.json (temporary), historical result_*.json (keeps current.json)
# - Baseline results: baseline.json (preserved)
reset:
	@if [ -n "$(TASK)" ]; then \
		echo "Resetting task: $(TASK)"; \
		./reset.sh "$(TASK)"; \
	else \
		echo "Resetting all tasks..."; \
		./reset.sh; \
	fi

# Hard reset tasks and runtime junk (keep current.json, baseline.json)
# - Uses git clean -ffd (removes nested git repos in tasks)
# - Removes progress.json, .pytest_cache in results/
reset-hard:
	@echo "Hard resetting all tasks..."
	@./reset.sh --hard
	@echo "Done. Kept result files: results/current.json, results/baseline.json"

# Reset without preserving any results (cleans everything)
reset-all:
	@echo "Resetting all tasks and cleaning all results..."
	@./reset.sh --no-keep-results
	@echo "Done. All results removed."

# A/B list
ab-list:
	python3 ab_test.py --list

# A/B single agent
ab-test:
	@if [ -z "$(AGENT)" ]; then \
		echo "Error: AGENT is required"; \
		echo "Usage: make ab-test AGENT=my-agent [MODEL=minimax] [TASK=task_id]"; \
		exit 1; \
	fi
	@cmd="python3 ab_test.py --agent $(AGENT)"; \
	if [ -n "$(MODEL)" ]; then cmd="$$cmd --model $(MODEL)"; fi; \
	if [ -n "$(TASK)" ]; then cmd="$$cmd --task $(TASK)"; fi; \
	echo "$$cmd"; \
	eval "$$cmd"

# A/B compare multiple agents
ab-compare:
	@if [ -z "$(AGENTS)" ]; then \
		echo "Error: AGENTS is required"; \
		echo "Usage: make ab-compare AGENTS=\"my-agent codex\" [MODEL=minimax] [TASK=task_id]"; \
		exit 1; \
	fi
	@cmd="python3 ab_test.py --compare $(AGENTS)"; \
	if [ -n "$(MODEL)" ]; then cmd="$$cmd --model $(MODEL)"; fi; \
	if [ -n "$(TASK)" ]; then cmd="$$cmd --task $(TASK)"; fi; \
	echo "$$cmd"; \
	eval "$$cmd"

# A/B benchmark all configured agents
ab-benchmark:
	@cmd="python3 ab_test.py --benchmark"; \
	if [ -n "$(MODEL)" ]; then cmd="$$cmd --model $(MODEL)"; fi; \
	if [ -n "$(TASK)" ]; then cmd="$$cmd --task $(TASK)"; fi; \
	echo "$$cmd"; \
	eval "$$cmd"

# A/B report
ab-report:
	python3 ab_test.py --report

# Run tests with coverage
test:
	go test -short ./... -race -coverprofile=coverage.out -covermode=atomic -timeout 30s

# Run vet
vet:
	go vet ./...

# Download dependencies
deps:
	go mod download
	go mod verify

# Clean build artifacts
clean:
	rm -f coverage.out coverage.html
	rm -f bin/benchmark bin/import-tbench
	go clean

# Coverage report
coverage: test
	go tool cover -html=coverage.out -o coverage.html
