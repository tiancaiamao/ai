.PHONY: build test clean vet coverage bench bench-build bench-run bench-resume bench-list bench-task bench-import bench-compare bench-baseline bench-clean bench-precheck reset reset-task reset-hard ab-list ab-test ab-compare ab-benchmark ab-report help

# Default to fast-manifest.json (excludes tasks >10min)
DEFAULT_MANIFEST ?= tasks/fast-manifest.json
MANIFEST := $(or $(strip $(MANIFEST)),$(DEFAULT_MANIFEST))
MANIFEST_FLAG := --manifest $(MANIFEST)
MAX_STEPS_MODE_FLAG := $(if $(strip $(MAX_STEPS_MODE)),--max-steps-mode $(MAX_STEPS_MODE),)
ASSERT_INIT_FAIL_FLAG := $(if $(filter 1 true yes,$(ASSERT_INIT_FAIL)),--assert-initial-fail,)
DEFAULT_PRECHECK_MANIFEST ?= tasks/agent_v1_manifest.json
PRECHECK_MANIFEST := $(or $(strip $(MANIFEST)),$(DEFAULT_PRECHECK_MANIFEST))
PRECHECK_MANIFEST_FLAG := --manifest $(PRECHECK_MANIFEST)

# Default target
all: build

# Help - show all available commands
help:
	@echo "============================================================"
	@echo "ai-agent-benchmark"
	@echo "============================================================"
	@echo ""
	@echo "Usage:"
	@echo "  make help                显示此帮助信息"
	@echo "  make bench-build         构建 benchmark 工具"
	@echo "  make bench-run           运行所有任务 (默认只跑快任务，~2h)"
	@echo "  make bench-resume        从上次中断处继续"
	@echo "  make bench-run TASK=xxx  运行单个任务 (不受 manifest 限制)"
	@echo "  make bench-precheck      仅做初始态预检（默认用 agent_v1_manifest）"
	@echo "  make bench-list          列出所有任务 (默认只显示快任务)"
	@echo "  make bench-compare       对比上次结果"
	@echo "  make bench-baseline      保存当前为基准"
	@echo "  make bench-clean         清理结果文件"
	@echo "  make reset               重置 tasks 工作区（保留 benchmark/results/*.json）"
	@echo "  make reset-task TASK=xx  重置单个任务的 setup"
	@echo "  make reset-hard          强重置（额外清理 progress/run/临时缓存）"
	@echo "  make ab-list             列出可用 agent/model/task"
	@echo "  make ab-test AGENT=xxx   运行 A/B 单 agent 测试"
	@echo "  make ab-compare AGENTS=\"a b\"  对比多个 agent"
	@echo "  make ab-benchmark        运行 agents.yaml 里所有 agent"
	@echo "  make ab-report           输出历史 A/B 报告"
	@echo ""
	@echo "Options:"
	@echo "  AGENT=ai                 指定 agent 二进制 (默认: /Users/genius/go/bin/ai)"
	@echo "  AGENTS=\"a b\"            A/B 对比的 agent 列表"
	@echo "  MODEL=minimax            A/B 运行模型名（会透传到 ab_test.py）"
	@echo "  TASK=task_id             A/B 或 bench 运行单任务"
	@echo "  TIMEOUT=0                单任务超时 (默认: 0=不限制)"
	@echo "  MAX_STEPS_MODE=soft      max_steps 判分模式: soft|hard (可选，不传则使用 runner/manifest 默认)"
	@echo "  MANIFEST=tasks/xxx.json  指定 manifest (默认: tasks/fast-manifest.json, 63个快任务)"
	@echo "                          使用 MANIFEST=tasks/all.json 可运行全部任务"
	@echo "  RESUME=results/xxx.json  指定进度文件 (默认: results/progress.json)"
	@echo "  VERBOSE=1                详细输出"
	@echo "  ASSERT_INIT_FAIL=1       bench-run 前强制初始态全 FAIL 校验"
	@echo ""
	@echo "Examples:"
	@echo "  make bench-run                      # 运行快任务 (~2h)"
	@echo "  make bench-run MANIFEST=tasks/all.json  # 运行所有任务 (~4h)"
	@echo "  make bench-resume                   # 从上次中断处继续"
	@echo "  make bench-resume RESUME=xxx.json   # 从指定进度文件继续"
	@echo "  make bench-precheck                    # 默认 manifest 预检"
	@echo "  make bench-precheck MANIFEST=tasks/xxx.json"
	@echo "  make bench-run TASK=001_fix_off_by_one  # 运行单个任务"
	@echo "  make bench-list                      # 列出快任务 (63个)"
	@echo "  make bench-list MANIFEST=tasks/all.json  # 列出所有任务"
	@echo "  make ab-test AGENT=my-agent MODEL=minimax TASK=agent_001_forced_exploration"
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
		echo "Running task: $(TASK)"; \
		../../bin/benchmark --task $(TASK) --agent $(or $(AGENT),/Users/genius/go/bin/ai) --timeout $(or $(TIMEOUT),0) $(MAX_STEPS_MODE_FLAG) $(ASSERT_INIT_FAIL_FLAG); \
	else \
		../../bin/benchmark --agent $(or $(AGENT),/Users/genius/go/bin/ai) --timeout $(or $(TIMEOUT),0) $(MAX_STEPS_MODE_FLAG) $(MANIFEST_FLAG) $(ASSERT_INIT_FAIL_FLAG) --clean; \
	fi

# Verify all selected tasks fail before agent modifications
bench-precheck: bench-build
	@echo "Precheck manifest: $(PRECHECK_MANIFEST)"
	../../bin/benchmark --precheck-only $(PRECHECK_MANIFEST_FLAG)

# Resume from previous progress
bench-resume: bench-build
	../../bin/benchmark --agent $(or $(AGENT),/Users/genius/go/bin/ai) --timeout $(or $(TIMEOUT),0) $(MAX_STEPS_MODE_FLAG) $(MANIFEST_FLAG) --resume $(or $(RESUME),results/progress.json)

# List available benchmark tasks
bench-list: bench-build
	../../bin/benchmark --list $(MANIFEST_FLAG)

# Run specific benchmark task (legacy alias)
bench-task: bench-build
	@echo "Usage: make bench-task TASK=tbench/chess-best-move"
	@echo "Or use: make bench-run TASK=tbench/chess-best-move"
	../../bin/benchmark --task $(TASK) --agent $(or $(AGENT),../../bin/ai) --timeout $(or $(TIMEOUT),0) $(MAX_STEPS_MODE_FLAG) $(ASSERT_INIT_FAIL_FLAG)

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

# Reset tasks to clean initial state (keep benchmark/results/*.json)
reset:
	@if [ -n "$(TASK)" ]; then \
		./reset.sh "$(TASK)"; \
	else \
		./reset.sh; \
	fi

# Reset specific task
reset-task:
ifndef TASK
	@echo "Error: TASK is required"
	@echo "Usage: make reset-task TASK=<task_id>"
	@exit 1
endif
	@./reset.sh "$(TASK)"

# Hard reset tasks and runtime junk (keep historical results/*.json)
reset-hard:
	@./reset.sh --hard
	@rm -f results/progress.json
	@rm -rf results/.pytest_cache
	@rm -f run reset
	@echo "Done. Kept historical result files (results/*.json except progress.json)."

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
