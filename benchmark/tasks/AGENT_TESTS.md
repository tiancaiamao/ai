# Agent 能力测试集

基于 /tmp/txt.md 方法论设计的 11 个测试 case

## 已完成的测试（11个）

### 1. agent_001_forced_exploration - 强制探索
- **测什么**: planning, tool selection
- **任务**: 8 个文件中只有 1 个有 bug
- **约束**: 必须先用 grep/search
- **bad patterns**: read_all_files, patch_without_search

### 2. agent_002_rollback - 强制回滚
- **测什么**: error recovery, state management
- **任务**: 第一次修复会让情况更糟
- **约束**: 必须继续排查根因，不能停在首个表象错误
- **bad patterns**: fix_symptom_not_root_cause

### 3. agent_003_hidden_dep - 跨文件依赖
- **测什么**: multi-hop reasoning
- **任务**: bug 表现在 api.py，原因在 utils.py
- **约束**: 必须跟踪调用链
- **bad patterns**: shallow_local_fix

### 4. agent_004_context_overflow - Context 压缩
- **测什么**: context 压缩策略
- **任务**: 大文件，bug 在中间
- **约束**: 必须用 grep 搜索
- **bad patterns**: read_entire_file

### 5. agent_005_delayed_signal - 延迟反馈
- **测什么**: context tracking, patience
- **任务**: 日志分散在多个文件
- **约束**: 必须读取所有日志文件
- **bad patterns**: partial_information

### 6. agent_006_tool_trap - 工具选择陷阱
- **测什么**: tool strategy
- **任务**: 提供 trap 工具 vs 正确工具
- **约束**: 必须用 search，不能用 open_all_files

### 7. agent_007_misleading - 中间误导
- **测什么**: reasoning robustness
- **任务**: 第一次错误是误导，真正问题在后面
- **约束**: 必须完整运行测试

### 8. agent_008_budget - 策略质量
- **测什么**: planning efficiency
- **任务**: 限制 tool 调用次数
- **约束**: max_steps=6（该题使用 hard 模式，超预算直接失败）

### 9. agent_009_partial_info - 主动性
- **测什么**: 是否主动获取信息
- **任务**: 不提供 test output，必须自己运行
- **约束**: 必须先 run_tests

### 10. agent_010_memory - 长程记忆
- **测什么**: context persistence
- **任务**: step 1 发现信息，step 5 才能用
- **约束**: 中间有大量无关操作

### 11. agent_011_compact_tool_call_mismatch - Trace 驱动协议修复
- **测什么**: trace-driven debugging, root-cause isolation
- **任务**: 根据真实 perfetto trace 修复 compact 后 tool_call/tool_result 配对错误
- **约束**: 必须先跑测试并做最小修复（不改 fixture）

## 运行测试

```bash
cd benchmark/benchmark
make bench-run TASK=agent_001_forced_exploration
```

## 下一步

### Step 1: 让现有测试跑起来 ✅
- [x] 添加 verify.sh
- [x] 统一 tests 从 setup/ 导入代码
- [x] verify.sh 支持自动从 init/ 恢复 setup/

### Step 2: 添加约束和过程评估 ✅
- [x] 为每个测试添加 constraints.json
- [x] benchmark 框架读取并评估 constraints.json
- [x] 实现 tool/steps/bad patterns 过程判分

### Step 3: 统一 agent 判分信号 ✅
- [x] 输出拆分为 `functional_passed` 与 `agentic_passed`（`passed` = 两者同时满足）
- [x] 约束支持 `must_use_capabilities`（`search/read/edit/test/rollback`）
- [x] codex JSON 事件支持 `file_change` 解析，避免漏记编辑行为
- [x] `max_steps` 默认作为软约束（扣 agentic score，不直接判 fail；可切 hard）

### Step 4: 冻结 v1 测试集 ✅
- [x] 冻结任务清单（11题）
- [x] 固化默认评分策略（global soft + per-task override）
- [x] 提供对比报告模板
