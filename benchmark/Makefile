.PHONY: build test clean vet coverage bench bench-build bench-run bench-resume bench-list bench-task bench-import bench-compare bench-baseline bench-clean help

MANIFEST_FLAG := $(if $(strip $(MANIFEST)),--manifest $(MANIFEST),)
MAX_STEPS_MODE_FLAG := $(if $(strip $(MAX_STEPS_MODE)),--max-steps-mode $(MAX_STEPS_MODE),)

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
	@echo "  make bench-run           运行所有任务 (从头开始)"
	@echo "  make bench-resume        从上次中断处继续"
	@echo "  make bench-run TASK=xxx  运行单个任务"
	@echo "  make bench-list          列出所有任务"
	@echo "  make bench-compare       对比上次结果"
	@echo "  make bench-baseline      保存当前为基准"
	@echo "  make bench-clean         清理结果文件"
	@echo ""
	@echo "Options:"
	@echo "  AGENT=ai                 指定 agent 二进制 (默认: /Users/genius/go/bin/ai)"
	@echo "  TIMEOUT=0                单任务超时 (默认: 0=不限制)"
	@echo "  MAX_STEPS_MODE=soft      max_steps 判分模式: soft|hard (可选，不传则使用 runner/manifest 默认)"
	@echo "  MANIFEST=tasks/xxx.json  按 manifest 精确筛选任务"
	@echo "  RESUME=results/xxx.json  指定进度文件 (默认: results/progress.json)"
	@echo "  VERBOSE=1                详细输出"
	@echo ""
	@echo "Examples:"
	@echo "  make bench-run                      # 从头运行所有任务"
	@echo "  make bench-resume                   # 从上次中断处继续"
	@echo "  make bench-resume RESUME=xxx.json   # 从指定进度文件继续"
	@echo "  make bench-run TASK=001_fix_off_by_one"
	@echo "  make bench-list MANIFEST=tasks/agent_v1_manifest.json"
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
		../../bin/benchmark --task $(TASK) --agent $(or $(AGENT),/Users/genius/go/bin/ai) --timeout $(or $(TIMEOUT),0) $(MAX_STEPS_MODE_FLAG) $(MANIFEST_FLAG); \
	else \
		../../bin/benchmark --agent $(or $(AGENT),/Users/genius/go/bin/ai) --timeout $(or $(TIMEOUT),0) $(MAX_STEPS_MODE_FLAG) $(MANIFEST_FLAG) --clean; \
	fi

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
