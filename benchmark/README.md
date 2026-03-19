# AI Agent Benchmark

评估 AI agent 能力的 benchmark 框架，支持 regression 检测。

## 快速开始

```bash
# 列出所有测试任务
make bench-list

# 运行所有测试
make bench

# 运行单个任务
make bench-task TASK=tbench/chess-best-move

# 导入 Terminal Bench 2.0 任务
make bench-import
```

## 特性

- ✅ **无 Docker** - 直接在本地运行
- ✅ **白盒测试** - 可 debug 失败的 case
- ✅ **Regression 检测** - 对比历史结果
- ✅ **多测试集** - 8 个自定义 + 55 个 Terminal Bench 2.0 任务
- ✅ **`/app` 兼容层** - 自动将 legacy `/app` 路径映射到每个 task 的 `setup`，无需 sudo 创建 `/app`

## 目录结构

```
benchmark/
├── tasks/               # 测试任务
│   ├── 001_fix_off_by_one/  # 自定义任务
│   ├── ...
│   └── tbench/              # Terminal Bench 2.0 任务
│       ├── chess-best-move/
│       └── ...
└── results/             # 测试结果

cmd/benchmark/           # Go benchmark runner
cmd/import-tbench/       # Terminal Bench 导入工具
```

## 添加新测试

1. 在 `benchmark/tasks/` 创建任务目录
2. 添加 `task.md` 和 `verify.sh`
3. 运行 `make bench-list` 查看新任务

更完整的 case 设计与冻结流程见：

- `docs/agent_case_authoring.md` (English)

## Manifest 运行（冻结测试集）

```bash
# 仅列出冻结清单中的任务
make bench-list MANIFEST=tasks/agent_v1_manifest.json

# 仅运行冻结清单中的任务
make bench-run MANIFEST=tasks/agent_v1_manifest.json
```

## 参考

- [Terminal Bench 2.0](https://github.com/harbor-framework/terminal-bench-2)
- [Letta Evals](https://github.com/letta-ai/letta-evals)
- [8 Benchmarks Shaping AI Agents](https://tessl.io/blog/8-benchmarks-shaping-the-next-generation-of-ai-agents)
