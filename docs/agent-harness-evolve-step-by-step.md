# Agent Harness Evolve - 步骤化实现指南

## 工作目录说明

**当前工作目录**: `/Users/genius/project/ai-evolve-clean/`

这是一个通过 `git worktree` 创建的独立工作目录：
- 基于 main 分支（commit 45a9fb4）
- 包含完整的 `ai` 项目代码
- 已清理旧的 evolve 相关代码
- **不是一个新项目，而是 ai 项目的独立副本**

## 前置条件

在开始步骤 1 之前，需要：

### 1. 编译必要的二进制文件

```bash
cd /Users/genius/project/ai-evolve-clean

# 编译 benchmark
make benchmark
# 或者直接运行
go build -o bin/benchmark ./benchmark/cmd/benchmark

# 编译 ai
make ai
# 或者直接运行
go build -o bin/ai ./cmd/ai
```

验证编译结果：
```bash
ls -lh bin/benchmark  # 应该存在
ls -lh bin/ai          # 应该存在

./bin/benchmark evolve --help  # 验证能运行
./bin/ai --help             # 验证能运行
```

### 2. 确认 Python 脚本位置

```bash
ls -d benchmark/scripts/*.py
```

应该看到：
- `trace_normalizer.py` - 轨迹标准化
- `agent_debugger.py` - Debugger 分析
- 其他辅助脚本

### 3. 确认 agent 配置

```bash
ls -lh agent/agent.yaml
```

应该存在且可读。

---

## 原则

**先手动验证，再自动化**

- 每个步骤都要单独实现并验证
- 手动运行每个步骤，验证输出格式
- 确认符合预期后，才考虑脚本化
- 脚本化后仍要手动监督运行几轮
- 只有多次成功后才进入完全自动化

---

## 步骤 1: 测试集选择

### 目标
选出稳定、可重复的测试用例，作为 evolve 的基准集。

## 工具说明

### Benchmark 命令

**工具**: `./bin/benchmark evolve iterate`

**常用参数**:
- `--agent-config <path>` - Agent 配置文件路径
- `--manifest <path>` - 任务清单 JSON 文件
- `--iteration <N>` - 迭代编号（用于生成结果文件）
- `--output <path>` - 输出结果文件路径
- `--task-dir <path>` - 单个任务目录（可选，用于测试单个任务）

**输出格式**:
- stdout: JSON 格式的结果
- 文件: 通过 `--output` 指定文件路径

**示例**:
```bash
# 运行单个任务
./bin/benchmark evolve iterate \
  --agent-config agent/agent.yaml \
  --task-dir benchmark/tasks/agent_001_forced_exploration \
  --iteration 0

# 运行测试集
./bin/benchmark evolve iterate \
  --agent-config agent/agent.yaml \
  --manifest evolve-manifest.json \
  --iteration 0 \
  --output iteration-0.json
```

### Agent.yaml 配置

**位置**: `agent/agent.yaml`

**主要可调参数**:
```yaml
system_prompt: ./system_prompt.md
memory: ./memory.md
context_management:
  stale_annotation: true          # 是否标注 stale 状态
  stale_age_investigative: 20      # investigative 模式下 stale 阈值
  stale_age_modification: 30      # modification 模式下 stale 阈值
```

**参数说明**:
- `stale_annotation`: 启用后会在对话中标注 stale 的文件
- `stale_age_investigative`: 调试模式下，文件多少次对话后标记为 stale
- `stale_age_modification`: 修改模式下，文件多少次对话后标记为 stale
  - 值越大，上下文保留越多（适合复杂任务）
  - 值越小，刷新越快（适合快速迭代）

**如何修改**:
```yaml
# 增加 stale 阈值（保留更多上下文）
context_management:
  stale_age_investigative: 40
  stale_age_modification: 60

# 禁用 stale 标注
context_management:
  stale_annotation: false
```

### Debugger 和 Planner

**Debugger**: Python 脚本 `benchmark/scripts/agent_debugger.py`
- 使用 LLM 分析失败轨迹
- 输入: OpenAI messages 格式的轨迹文件
- 输出: JSON 格式的分析结果

**Planner**: AI Agent（使用 `./bin/ai serve` 启动）
- 基于 debugger 分析和当前性能，生成配置改进建议
- 输入: Markdown 格式的上下文文档
- 输出: YAML 格式的配置


### 输入
- 可用的 benchmark 任务目录
- 每个任务的运行记录（如果有）

### 输出
- `evolve-manifest.json`

### 格式约定

详细格式见：[evolve-output-spec.md](./evolve-output-spec.md)


```json
{
  "version": "1.0",
  "description": "精选 evolve 测试集（15-20 个稳定任务）",
  "tasks": [
    {
      "id": "agent_001_forced_exploration",
      "path": "../../benchmark/tasks/agent_001_forced_exploration",
      "category": "agent"
    },
    {
      "id": "agent_003_hidden_dep",
      "path": "../../benchmark/tasks/agent_003_hidden_dep",
      "category": "agent"
    },
    {
      "id": "tbench/prove-plus-comm",
      "path": "../../benchmark/tasks/tbench/prove-plus-comm",
      "category": "tbench"
    }
  ]
}
```

### 选择标准

1. **稳定性**: 同一任务连续运行 3 次，pass/fail 状态一致
2. **运行时间**: 单个任务 < 5 分钟，总集 < 60 分钟
3. **代表性**: 覆盖不同失败模式（工具错误、上下文、逻辑等）
4. **非 flaky**: 避免依赖外部服务、网络、时间等不稳定因素

### 手动验证步骤

```bash
# 1. 列出所有可用任务
ls -d /Users/genius/project/ai-evolve-clean/benchmark/tasks/agent_* /Users/genius/project/ai-evolve-clean/benchmark/tasks/tbench/*

# 2. 对每个任务运行 3 次
for task in agent_001 agent_003 agent_004; do
  echo "Testing $task..."
  for i in {1..3}; do
    ./bin/benchmark evolve iterate \
      --agent-config agent/agent.yaml \
      --task-dir "benchmark/tasks/$task" \
      --iteration 0 \
      > /tmp/test_${task}_${i}.log 2>&1
  done
done

# 3. 检查稳定性
# 所有任务的 3 次运行结果应该一致

# 4. 选择稳定的任务，创建 manifest
# 手动编写 evolve-manifest.json
```

### 验证标准

- [ ] 每个任务连续 3 次运行结果一致
- [ ] 运行时间 < 60 分钟（完整集）
- [ ] manifest 格式正确（JSON 可解析）
- [ ] 路径正确（相对路径存在）

---

## 步骤 2: 跑一次测试集

### 目标
使用当前配置运行完整测试集，生成 baseline 结果。

### 输入
- `evolve-manifest.json`（步骤 1 输出）
- `agent/agent.yaml`（当前 harness 配置）

### 输出
- `iteration-0.json`（benchmark 结果）
- `benchmark.log`（运行日志）
- `system_prompt.md`（使用的 system prompt，备份）
- `config.yaml`（使用的配置，备份）

### 格式约定

详细格式见：[evolve-output-spec.md](./evolve-output-spec.md)


**iteration-0.json**:
```json
{
  "iteration": 0,
  "pass_rate": 62.5,
  "passed": 5,
  "total_tasks": 8,
  "duration_seconds": 1850.5,
  "results": [
    {
      "task_id": "agent_001_forced_exploration",
      "passed": false,
      "functional_passed": false,
      "agentic_passed": false,
      "output": "...",
      "error": "constraint violations: files_read_before_fix",
      "duration_seconds": 245.3,
      "timestamp": "2026-06-01T15:30:00Z",
      "trajectory": [
        {
          "turn": 1,
          "tool": "read",
          "args_summary": "{\"path\":\".../user.py\"}",
          "result_summary": "class User:\n...",
          "duration_s": 0.1,
          "thinking_before": "Let me read the user.py file..."
        },
        {
          "turn": 2,
          "tool": "bash",
          "args_summary": "{\"command\":\"python test_user.py\"}",
          "result_summary": "AssertionError: expected 'John (Admin)' got 'John ()'",
          "duration_s": 1.2
        }
      ]
    }
  ]
}
```

**benchmark.log**:
```
[2026-06-01 15:30:00] INFO: Starting benchmark iteration 0
[2026-06-01 15:30:00] INFO: Loading agent config: agent/agent.yaml
[2026-06-01 15:30:00] INFO: Loading manifest: evolve-manifest.json
[2026-06-01 15:30:00] INFO: Running 8 tasks
[2026-06-01 15:30:05] INFO: Running task agent_001_forced_exploration
[2026-06-01 15:34:10] INFO: Task completed: agent_001_forced_exploration (245s)
...
[2026-06-01 15:55:00] INFO: Benchmark completed: pass_rate=62.5% (5/8)
```

### 手动验证步骤

```bash
# 1. 运行 benchmark
cd /Users/genius/project/ai-evolve-clean/agent/benchmarks/evolve

./bin/benchmark evolve iterate \
  --agent-config ../agent.yaml \
  --manifest evolve-manifest.json \
  --iteration 0 \
  --output iteration-0.json \
  > benchmark.log 2>&1

# 2. 检查结果
jq '.pass_rate' iteration-0.json          # 应该输出数字
jq '.results | length' iteration-0.json   # 应该等于 manifest 中的任务数
jq '.results[0].task_id' iteration-0.json

# 3. 检查 trajectory
jq '.results[0].trajectory' iteration-0.json | head -20

# 4. 备份配置
cp ../agent.yaml iter-0-artifacts/config.yaml
cat ../system_prompt.md > iter-0-artifacts/system_prompt.md
```

### 验证标准

- [ ] benchmark 成功完成（退出码 0）
- [ ] iteration-0.json 格式正确（JSON 可解析）
- [ ] 结果包含所有 manifest 中的任务
- [ ] 每个任务有 trajectory 字段（可能为 null）
- [ ] benchmark.log 包含完整的运行信息

---

## 步骤 3: 提取 trajectory

### 目标
从 benchmark 结果中提取失败任务的 trajectory，标准化为 OpenAI messages 格式。

### 输入
- `iteration-N.json`（步骤 2 输出）
- 筛选条件：只提取 `passed == false` 的任务

### 输出
- `trace-{task_id}-rollout-{k}.json`（多个文件）
- `trace-summary.json`（汇总信息）

### 格式约定

详细格式见：[evolve-output-spec.md](./evolve-output-spec.md)


**trace-{task_id}-rollout-{k}.json**:
```json
{
  "trace_id": "agent_001-rollout-0",
  "task_id": "agent_001_forced_exploration",
  "rollout_index": 0,
  "passed": false,
  "verifier_output": "constraint violations: files_read_before_fix",
  "messages": [
    {
      "role": "system",
      "content": "You are a coding agent..."
    },
    {
      "role": "user",
      "content": "Debug a User API issue where `get_user_display_name` is returning \"John ()\" instead of \"John (Admin)\" for admin users.\n\nAvailable files:\n- setup/user.py\n- setup/api.py\n- setup/utils.py\n\nFiles must be read before modification."
    },
    {
      "role": "assistant",
      "content": null,
      "tool_calls": [
        {
          "id": "call_1",
          "type": "function",
          "function": {
            "name": "read",
            "arguments": "{\"path\":\".../user.py\"}"
          }
        }
      ]
    },
    {
      "role": "tool",
      "tool_call_id": "call_1",
      "name": "read",
      "content": "class User:\n    def __init__(self, name, role):\n        self.name = name\n        self.role = role"
    }
  ]
}
```

**trace-summary.json**:
```json
{
  "source": "iteration-0.json",
  "total_tasks": 8,
  "failed_tasks": 3,
  "extracted_traces": 3,
  "traces": [
    {
      "task_id": "agent_001_forced_exploration",
      "passed": false,
      "trace_files": ["trace-agent_001-rollout-0.json"],
      "rollout_count": 1
    }
  ]
}
```

### 手动验证步骤

```bash
# 1. 标准化轨迹
cd /Users/genius/project/ai

python3 benchmark/scripts/trace_normalizer.py \
  --input agent/benchmarks/evolve/iteration-0.json \
  --output-dir agent/benchmarks/evolve/iter-0-artifacts/traces/ \
  --only-failed

# 2. 检查输出
ls agent/benchmarks/evolve/iter-0-artifacts/traces/*.json

# 3. 验证格式
cat agent/benchmarks/evolve/iter-0-artifacts/traces/trace-agent_001-rollout-0.json | jq .

# 4. 验证内容
jq '.messages[0].role' agent/benchmarks/evolve/iter-0-artifacts/traces/trace-agent_001-rollout-0.json  # 应该是 system
jq '.messages | length' agent/benchmarks/evolve/iter-0-artifacts/traces/trace-agent_001-rollout-0.json
```

### 验证标准

- [ ] 只提取失败任务的 trajectory
- [ ] 每个文件格式正确（JSON 可解析）
- [ ] messages 字段包含完整的对话
- [ ] tool_calls 和 tool 消息配对正确
- [ ] trace-summary.json 汇总信息准确

---

## 步骤 4: 执行结果分析（Debugger）

### 目标
使用 LLM 分析失败的轨迹，识别根本原因和潜在风险。

### 输入
- `trace-*.json`（步骤 3 输出）
- 系统提示词

### 输出
- `debugger-analysis.json`

### System Prompt

详细内容见：[agent-debugger-design.md](./agent-debugger-design.md#system-prompt)

```
You are an expert agent debugger. Your job is to analyze failed agent traces and identify:
1. Root causes of failures
2. Patterns across multiple failures
3. Potential risks of proposed fixes

Input format: OpenAI messages format trace files.

For each analysis, you should:
- Identify the failure point (where things started going wrong)
- Explain the root cause (why it failed)
- Suggest what should have been done
- Identify general mechanisms that can prevent this type of failure
- Predict which tasks might regress if certain fixes are applied

Output format: JSON with the following structure:
{
  "mode": "ask",
  "summary": "Brief summary of the analysis (2-3 sentences)",
  "answer": "Detailed explanation of root causes and patterns",
  "risks": {
    "description": "Description of potential risks",
    "affected_tasks": ["task_id1", "task_id2"],
    "confidence": "high|medium|low"
  },
  "issues": [],
  "metadata": {
    "total_traces": 3,
    "failed_tasks": 3
  }
}
```

### 技能（如果使用 ai binary）

需要加载以下技能：
- `/Users/genius/project/ai-evolve-clean/agent/skills/file-operations.md` - 读取 trace 文件
- `/Users/genius/project/ai-evolve-clean/agent/skills/search.md` - 搜索轨迹中的模式

### 格式约定

详细格式见：[evolve-output-spec.md](./evolve-output-spec.md)


**debugger-analysis.json**:
```json
{
  "mode": "ask",
  "summary": "分析失败的根本原因：强制探索约束、上下文溢出、误导性提示",
  "answer": "## 失败分析\n\n三个主要失败模式：\n\n1. **强制探索问题** (agent_001_forced_exploration, agent_004_context_overflow): 约束冲突。Agent 被要求在修改前读取所有文件，但某些测试任务需要快速迭代。\n\n2. **上下文溢出** (agent_007_misleading, agent_008_budget, agent_009_partial_info): 信息处理不足。Stale annotation 的设置导致 agent 丢失关键上下文。\n\n3. **取消异步任务** (tbench/cancel-async-tasks): 框架限制。Agent 不理解异步任务的管理机制。",
  "risks": {
    "description": "如果调整 stale_age 参数（从 20/30 增加到 40/60），可能导致 agent_003_hidden_dep 和 tbench/prove-plus-comm 回退，因为这两个任务依赖更积极的 stale 检测来发现隐藏依赖。",
    "affected_tasks": [
      "agent_003_hidden_dep",
      "tbench/prove-plus-comm"
    ],
    "confidence": "high"
  },
  "issues": [],
  "metadata": {
    "total_traces": 3,
    "failed_tasks": 3,
    "llm_model": "gpt-4.1",
    "tokens_used": 1500,
    "duration_seconds": 5.2
  }
}
```

### 手动验证步骤

```bash
# 1. 运行 debugger
cd /Users/genius/project/ai

python3 benchmark/scripts/agent_debugger.py ask \
  --traces agent/benchmarks/evolve/iter-0-artifacts/traces/*.json \
  --question "分析这些失败任务的根本原因，并预测哪些任务可能回退" \
  --output agent/benchmarks/evolve/iter-0-artifacts/debugger-analysis.json

# 2. 检查输出
cat agent/benchmarks/evolve/iter-0-artifacts/debugger-analysis.json | jq .

# 3. 验证字段
jq '.mode' agent/benchmarks/evolve/iter-0-artifacts/debugger-analysis.json  # 应该是 "ask"
jq '.risks.confidence' agent/benchmarks/evolve/iter-0-artifacts/debugger-analysis.json  # 应该是 high|medium|low
```

### 验证标准

- [ ] debugger-analysis.json 格式正确（JSON 可解析）
- [ ] 包含所有必需字段（mode, summary, answer, risks, metadata）
- [ ] answer 字段包含有意义的分析
- [ ] risks 字段包含 affected_tasks 和 confidence
- [ ] 分析能识别失败的根本原因

---

## 步骤 5: Planner 优化

### 目标
基于 debugger 分析和基准对比，生成改进的 harness 配置。

### 输入
- `debugger-analysis.json`（步骤 4 输出）
- `iteration-N.json`（步骤 2 输出，当前结果）
- `iteration-(N-1).json`（上一次迭代结果，如果有）
- `agent/agent.yaml`（当前配置）

### 输出
- `planner-input.md`（planner 输入）
- `planner-response.txt`（planner 输出）
- `config.yaml`（提取的配置）

### System Prompt

详细内容见：[planner-system-prompt.md](./planner-system-prompt.md)

**关键要求**：
1. 避免重复失败策略（查看 Strategy History）
2. 优化优先级（查看 Task Stability）
3. 考虑风险预测（查看 Predicted Risks）
4. 解释原因

简版内容如下：

```
You are an agent harness engineer. Your job is to improve the agent harness configuration based on failure analysis and performance data.

You will receive:
1. Current iteration overview (pass rate, task breakdown)
2. Debugger analysis (root causes, risks)
3. Task classification (which tasks passed/failed/regressed)
4. Current harness configuration (agent.yaml)

Your task:
1. Analyze the failure patterns from debugger analysis
2. Identify which configuration changes might help
3. Consider the risks predicted by the debugger
4. Propose specific configuration changes

Output format:
Wrap your proposed changes in a YAML code block with triple backticks and `yaml` marker:

```yaml
system_prompt: ./system_prompt.md
memory: ./memory.md
context_management:
  stale_annotation: false
  stale_age_investigative: 20
  stale_age_modification: 30
```

If you propose changes to system_prompt.md or memory.md, include the full content after the YAML block.

Important:

**关键：历史信息的作用**

> 为了避免重复失败的策略，planner 输入**必须包含完整的历史信息**：

1. **Strategy History**: 显示所有过去的迭代、更改、结果
   - 检测重复策略并给出警告
   - 避免尝试已知失败的方法

2. **Task Stability**: 分类任务为稳定通过/稳定失败/不稳定
   - Stable Fail: 始终失败，需要新策略
   - Unstable: 有时成功，优先优化（容易改进）
   - Stable Pass: 始终成功，保护这些任务

3. **Predicted Risks**: 来自 debugger 的风险预测
   - 明确哪些任务可能回退
   - Planner 必须说明如何应对这些风险

**完整格式见**: [evolve-output-spec.md](./evolve-output-spec.md#步骤-5-planner-inputmd)
- Explain why you're making each change
- Explain how the change addresses specific failure patterns
- Reference the risks from debugger analysis
- Avoid repeating changes that didn't work in previous iterations
- Focus on structural mechanisms, not task-specific knowledge
```

### 技能（如果使用 ai binary）

需要加载以下技能：
- `/Users/genius/project/ai-evolve-clean/agent/skills/file-operations.md` - 读取配置文件
- `/Users/genius/project/ai-evolve-clean/agent/skills/yaml-operations.md` - 理解和修改 YAML 配置

### 避免重复探索

**如何避免在相同的错误重复探索**：

1. **迭代历史**：
   - 记录每次迭代做的修改
   - 记录每次迭代的结果（pass rate, 变化）
   - 在 planner input 中包含历史

2. **失败原因追踪**：
   - 记录哪些修改导致了回退
   - 在后续迭代中明确警告

3. **风险提醒**：
   - debugger 分析的 risks 字段明确告诉 planner 哪些任务可能回退
   - planner 必须在预期改进中说明如何应对这些风险

### 格式约定

详细格式见：[evolve-output-spec.md](./evolve-output-spec.md)


**planner-input.md**:
```markdown
# Agent Harness Optimization - Iteration 0

## Current Iteration Overview

Pass rate: 62.5% (5/8)
Passed: agent_002, agent_005, agent_006, agent_010, tbench/cancel-async-tasks
Failed: agent_001, agent_003, agent_004

## Debugger Analysis

### Summary
分析失败的根本原因：强制探索约束、上下文溢出、误导性提示

### Root Causes
三个主要失败模式：
1. **强制探索问题** (agent_001_forced_exploration, agent_004_context_overflow): 约束冲突...
2. **上下文溢出** (agent_007_misleading, agent_008_budget, agent_009_partial_info): 信息处理不足...
3. **取消异步任务** (tbench/cancel-async-tasks): 框架限制...

### ⚠️ Predicted Risks

**Risk Description**: 如果调整 stale_age 参数，可能导致 agent_003_hidden_dep 和 tbench/prove-plus-comm 回退

**Affected Tasks**: agent_003_hidden_dep, tbench/prove-plus-comm
**Confidence**: high

## Task Classification

### Flipped fail→pass (0)
(无)

### Regressed pass→fail (0)
(无，这是第一次迭代)

### Stable pass (5)
agent_002, agent_005, agent_006, agent_010, tbench/cancel-async-tasks

### Stable fail (3)
agent_001, agent_003, agent_004

## Current Harness Configuration

```yaml
system_prompt: ../system_prompt.md
memory: ../memory.md
context_management:
  stale_annotation: true
  stale_age_investigative: 20
  stale_age_modification: 30
```

## Your Task

Based on the debugger analysis and failure patterns, propose specific configuration changes to improve the pass rate.

Requirements:
1. Address the root causes identified by debugger
2. Consider the predicted risks
3. Explain why each change should help
4. If making changes to stale_age, explain how to mitigate the risk to agent_003 and tbench/prove-plus-comm
```

**config.yaml**:
```yaml
system_prompt: ../system_prompt.md
memory: ../memory.md
context_management:
  stale_annotation: true
  stale_age_investigative: 40
  stale_age_modification: 60
```

### 手动验证步骤

```bash
# 1. 构建 planner input
cd /Users/genius/project/ai

python3 benchmark/scripts/build_planner_context.py \
  --baseline agent/benchmarks/evolve/iteration-0.json \
  --current agent/benchmarks/evolve/iteration-0.json \
  --config agent/agent.yaml \
  --debugger-analysis agent/benchmarks/evolve/iter-0-artifacts/debugger-analysis.json \
  --output agent/benchmarks/evolve/iter-0-artifacts/planner-input.md

# 2. 运行 planner
./bin/ai serve \
  --input-file agent/benchmarks/evolve/iter-0-artifacts/planner-input.md \
  > agent/benchmarks/evolve/iter-0-artifacts/planner-response.txt 2>&1

# 3. 提取配置
# 手动从 planner-response.txt 中提取 YAML 代码块，保存到 config.yaml

# 4. 验证配置格式
cat agent/benchmarks/evolve/iter-0-artifacts/config.yaml | yq .
```

### 验证标准

- [ ] planner-input.md 包含所有必需的章节
- [ ] planner-response.txt 包含明确的配置建议
- [ ] config.yaml 格式正确（YAML 可解析）
- [ ] 配置改变有明确的解释
- [ ] 考虑了 debugger 的风险预测

---

## 步骤 6: 用新 config 重测试并验证

### 目标
应用新配置，重新运行测试集，对比结果，决定接受或拒绝。

### 输入
- `config.yaml`（步骤 5 输出）
- `iteration-N.json`（当前结果）

### 输出
- `iteration-(N+1).json`（新结果）
- `decision.json`（决策结果）

### 决策逻辑

**Accept 条件**：
1. `pass_rate(new) > pass_rate(best)`（严格改进）
2. 无强制回归（关键任务不回退）

**Reject 条件**：
1. `pass_rate(new) <= pass_rate(best)`（无改进或回退）
2. 有严重的回退（改进 < 回退）

### 格式约定

详细格式见：[evolve-output-spec.md](./evolve-output-spec.md)


**iteration-(N+1).json**: 同步骤 2

**decision.json**:
```json
{
  "status": "accept",
  "current_pass_rate": 75.0,
  "best_pass_rate": 62.5,
  "delta": 12.5,
  "regressions": 0,
  "improvements": 1,
  "timestamp": "2026-06-01T16:00:00Z",
  "reason": "Strict improvement (75.0 > 62.5)"
}
```

### 手动验证步骤

```bash
# 1. 应用配置
cd /Users/genius/project/ai-evolve-clean/agent/benchmarks/evolve

# 备份当前配置
cp ../agent.yaml ../agent.yaml.backup

# 应用新配置
cp iter-0-artifacts/config.yaml ../agent.yaml

# 2. 运行 benchmark
../bin/benchmark evolve iterate \
  --agent-config ../agent.yaml \
  --manifest evolve-manifest.json \
  --iteration 1 \
  --output iteration-1.json \
  > benchmark.log 2>&1

# 3. 对比结果
python3 - <<'PYTHON'
import json

current = json.load(open('iteration-1.json'))
best = json.load(open('iteration-0.json'))

current_rate = current['pass_rate']
best_rate = best['pass_rate']
delta = current_rate - best_rate

print(f"Current: {current_rate}%")
print(f"Best: {best_rate}%")
print(f"Delta: {delta:+.1f}pp")

if current_rate > best_rate:
    status = "accept"
elif current_rate < best_rate:
    status = "reject"
else:
    # 相同，检查任务级变化
    # ...
    status = "reject"

print(f"Decision: {status}")

decision = {
    "status": status,
    "current_pass_rate": current_rate,
    "best_pass_rate": best_rate,
    "delta": delta,
    "timestamp": "2026-06-01T16:00:00Z",
    "reason": f"{'Improvement' if status == 'accept' else 'No improvement'}"
}

with open('iter-1-artifacts/decision.json', 'w') as f:
    json.dump(decision, f, indent=2)
PYTHON

# 4. 如果 reject，回滚配置
if [[ $(jq -r '.status' iter-1-artifacts/decision.json) == "reject" ]]; then
  cp ../agent.yaml.backup ../agent.yaml
  echo "Config rolled back"
fi
```

### 验证标准

- [ ] benchmark 成功完成
- [ ] iteration-(N+1).json 格式正确
- [ ] decision.json 包含正确的决策
- [ ] accept 时新配置生效
- [ ] reject 时配置回滚

---

## 自动化流程（仅在所有步骤手动验证通过后）

### 条件
- [ ] 步骤 1-6 全部手动验证通过
- [ ] 每个步骤的输入/输出格式确认
- [ ] 手动运行多次无问题
- [ ] 文档更新完整

### 自动化脚本
在所有手动验证通过后，才考虑编写 `evolve-loop.sh` 脚本。

脚本结构：
1. 检查前置条件（文件存在性）
2. 顺序执行步骤 2-6
3. 错误处理和回滚
4. 状态记录和恢复

### 监督运行
- 前几轮迭代需要人工监督
- 检查每个步骤的输出
- 确认决策合理
- 发现问题立即停止并修复

### 完全自动化
- 多轮迭代成功运行
- 决策逻辑可靠
- 错误处理完善
- 日志和监控完备

---

## 目录结构（最终）

```
agent/benchmarks/evolve/
├── evolve-manifest.json           ← 步骤 1 输出
├── agent.yaml                     ← 当前配置
├── agent.yaml.backup              ← 备份
│
├── iteration-0.json               ← 步骤 2 输出
├── benchmark.log                  ← 步骤 2 输出
│
├── iter-0-artifacts/              ← 所有中间产物
│   ├── traces/                    ← 步骤 3 输出
│   │   ├── trace-agent_001-rollout-0.json
│   │   └── ...
│   ├── trace-summary.json         ← 步骤 3 输出
│   ├── debugger-analysis.json     ← 步骤 4 输出
│   ├── planner-input.md           ← 步骤 5 输出
│   ├── planner-response.txt       ← 步骤 5 输出
│   ├── config.yaml                ← 步骤 5 输出
│   └── decision.json              ← 步骤 6 输出
│
├── iteration-1.json               ← 步骤 6 输出
├── iter-1-artifacts/
│   └── ...
│
└── evolve-loop.sh                 ← 自动化脚本（最后）
```

---

## 下一步行动

### 立即开始：步骤 1
1. 列出所有可用任务
2. 对每个任务运行 3 次，检查稳定性
3. 创建 `evolve-manifest.json`

### 验证标准
- manifest 包含 15-20 个稳定任务
- 每个任务连续 3 次运行结果一致
- 运行时间 < 60 分钟

---

## 记录和文档化

### 每个步骤的验证记录

创建一个 `verification-log.md` 文件，记录每个步骤的验证结果：

```markdown
# Evolve 验证日志

## 步骤 1: 测试集选择
- 日期: 2026-06-01
- 验证人: xxx
- 结果: ✅ 通过
- 备注: 选择了 18 个稳定任务

## 步骤 2: 跑一次测试集
- 日期: 2026-06-01
- 验证人: xxx
- 结果: ✅ 通过
- 验证人: xxx
- 结果: ✅ 通过
- 备注: pass_rate 从 62.5% 提升到 75.0%，accept
```

### 方向论文档

创建一个 `directions.md` 文件，记录整体方向和原则：

```markdown
# Agent Harness Evolve - 方向论

## 核心原则
- 先手动验证，再自动化
- 每一步都要单独验证
- 文件格式明确约定
- 输入输出清晰定义

## 实施策略
1. 步骤 1-6 逐个实现
2. 每个步骤手动验证通过
3. 编写自动化脚本
4. 监督运行多轮
5. 完全自动化

## 当前状态
- 步骤 1: 测试集选择（进行中）
- 步骤 2-6: 待开始

## 已知问题
- agent.yaml 路径问题（需要修复）
- trajectory 为空的问题（需要排查）

## 下一步
- 完成步骤 1 的验证
- 开始步骤 2
```