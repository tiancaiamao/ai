# Evolve 输出文件规格（Specification）

本文档定义了 evolve 过程中每个阶段输出的文件格式和命名约定。

---

## 文件清单

| 阶段 | 文件名 | 位置 | 格式 |
|-----|--------|------|------|
| 步骤 1 | `evolve-manifest.json` | `agent/benchmarks/evolve/` | JSON |
| 步骤 2 | `iteration-N.json` | `agent/benchmarks/evolve/` | JSON |
| 步骤 2 | `benchmark.log` | `iter-N-artifacts/` | Text |
| 步骤 3 | `trace-{task_id}-rollout-{k}.json` | `iter-N-artifacts/traces/` | JSON |
| 步骤 3 | `trace-summary.json` | `iter-N-artifacts/` | JSON |
| 步骤 4 | `debugger-analysis.json` | `iter-N-artifacts/` | JSON |
| 步骤 5 | `planner-input.md` | `iter-N-artifacts/` | Markdown |
| 步骤 5 | `planner-response.txt` | `iter-N-artifacts/` | Text |
| 步骤 5 | `config.yaml` | `iter-N-artifacts/` | YAML |
| 步骤 6 | `iteration-(N+1).json` | `agent/benchmarks/evolve/` | JSON |
| 步骤 6 | `decision.json` | `iter-N-artifacts/` | JSON |

---

## 步骤 1: evolve-manifest.json

### 路径
```
agent/benchmarks/evolve/evolve-manifest.json
```

### 格式

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

### 字段说明

| 字段 | 类型 | 必填 | 说明 |
|-----|------|------|------|
| `version` | string | 是 | 版本号（如 "1.0"） |
| `description` | string | 否 | 描述（如 "精选 evolve 测试集"） |
| `tasks` | array | 是 | 任务列表 |
| `tasks[].id` | string | 是 | 任务 ID（如 "agent_001"） |
| `tasks[].path` | string | 是 | 相对路径（相对于 manifest 所在目录） |
| `tasks[].category` | string | 否 | 类别（如 "agent", "tbench"） |

### 命名约定

- 文件名: `evolve-manifest.json`（固定命名）
- 路径约定: 任务路径使用相对路径 `../../benchmark/tasks/{task_id}`

---

## 步骤 2: iteration-N.json

### 路径
```
agent/benchmarks/evolve/iteration-N.json
```

### 格式

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

### 字段说明

| 字段 | 类型 | 必填 | 说明 |
|-----|------|------|------|
| `iteration` | integer | 是 | 迭代编号（从 0 开始） |
| `pass_rate` | float | 是 | 通过率（0-100） |
| `passed` | integer | 是 | 通过的任务数 |
| `total_tasks` | integer | 是 | 总任务数 |
| `duration_seconds` | float | 是 | 总耗时（秒） |
| `results` | array | 是 | 每个任务的结果 |
| `results[].task_id` | string | 是 | 任务 ID |
| `results[].passed` | boolean | 是 | 是否通过 |
| `results[].functional_passed` | boolean | 是 | 功能测试是否通过 |
| `results[].agentic_passed` | boolean | 是 | Agentic 测试是否通过 |
| `results[].output` | string | 是 | Agent 输出 |
| `results[].error` | string | 否 | 错误信息（如果失败） |
| `results[].duration_seconds` | float | 是 | 单个任务耗时 |
| `results[].timestamp` | string | 是 | 完成时间（ISO 8601） |
| `results[].trajectory` | array | 否 | 轨迹数据（可能为 null） |
| `results[].trajectory[].turn` | integer | 是 | 轮次编号 |
| `results[].trajectory[].tool` | string | 是 | 工具名称 |
| `results[].trajectory[].args_summary` | string | 是 | 参数摘要 |
| `results[].trajectory[].result_summary` | string | 是 | 结果摘要 |
| `results[].trajectory[].duration_s` | float | 否 | 耗时（秒） |
| `results[].trajectory[].thinking_before` | string | 否 | 思考内容 |

### 命名约定

- 文件名: `iteration-N.json`（N 为迭代编号，从 0 开始）
- 示例: `iteration-0.json`, `iteration-1.json`, `iteration-2.json`

---

## 步骤 2: benchmark.log

### 路径
```
agent/benchmarks/evolve/iter-N-artifacts/benchmark.log
```

### 格式

纯文本日志，包含 stdout 和 stderr。

### 示例

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

### 命名约定

- 文件名: `benchmark.log`（固定命名）
- 位置: `iter-N-artifacts/` 目录

---

## 步骤 3: trace-{task_id}-rollout-{k}.json

### 路径
```
agent/benchmarks/evolve/iter-N-artifacts/traces/trace-{task_id}-rollout-{k}.json
```

### 格式

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
      "content": "Debug a User API issue where `get_user_display_name` is returning \"John ()\" instead of \"John (Admin)\" for admin users."
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

### 字段说明

| 字段 | 类型 | 必填 | 说明 |
|-----|------|------|------|
| `trace_id` | string | 是 | 追踪 ID（格式: `{task_id}-rollout-{k}`） |
| `task_id` | string | 是 | 任务 ID |
| `rollout_index` | integer | 是 | Rollout 索引（从 0 开始） |
| `passed` | boolean | 是 | 是否通过 |
| `verifier_output` | string | 否 | 验证器输出 |
| `messages` | array | 是 | OpenAI messages 格式的对话 |
| `messages[].role` | string | 是 | 角色（system/user/assistant/tool） |
| `messages[].content` | string | 否 | 内容 |
| `messages[].tool_calls` | array | 否 | 工具调用（role=assistant 时） |
| `messages[].tool_call_id` | string | 否 | 工具调用 ID（role=tool 时） |
| `messages[].name` | string | 否 | 工具名称（role=tool 时） |

### 命名约定

- 文件名: `trace-{task_id}-rollout-{k}.json`
  - `{task_id}`: 任务 ID（如 `agent_001`）
  - `{k}`: Rollout 索引（如 `0`, `1`, `2`）
- 示例: `trace-agent_001-rollout-0.json`
- 位置: `iter-N-artifacts/traces/` 目录

---

## 步骤 3: trace-summary.json

### 路径
```
agent/benchmarks/evolve/iter-N-artifacts/trace-summary.json
```

### 格式

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

### 字段说明

| 字段 | 类型 | 必填 | 说明 |
|-----|------|------|------|
| `source` | string | 是 | 源文件（iteration-N.json） |
| `total_tasks` | integer | 是 | 总任务数 |
| `failed_tasks` | integer | 是 | 失败任务数 |
| `extracted_traces` | integer | 是 | 提取的轨迹数 |
| `traces` | array | 是 | 轨迹汇总 |
| `traces[].task_id` | string | 是 | 任务 ID |
| `traces[].passed` | boolean | 是 | 是否通过 |
| `traces[].trace_files` | array | 是 | 轨迹文件列表 |
| `traces[].rollout_count` | integer | 是 | Rollout 次数 |

### 命名约定

- 文件名: `trace-summary.json`（固定命名）
- 位置: `iter-N-artifacts/` 目录

---

## 步骤 4: debugger-analysis.json

### 路径
```
agent/benchmarks/evolve/iter-N-artifacts/debugger-analysis.json
```

### 格式

```json
{
  "mode": "ask",
  "summary": "分析失败的根本原因：强制探索约束、上下文溢出",
  "answer": "## 失败分析\n\n三个主要失败模式：\n\n1. **强制探索问题**...",
  "risks": {
    "description": "调整 stale_age 可能导致回退",
    "affected_tasks": ["agent_003_hidden_dep", "tbench/prove-plus-comm"],
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

### 字段说明

| 字段 | 类型 | 必填 | 说明 |
|-----|------|------|------|
| `mode` | string | 是 | 模式（"ask" 或 "check"） |
| `summary` | string | 是 | 简短摘要（2-3 句话） |
| `answer` | string | 是 | 详细分析 |
| `risks` | object | 否 | 风险预测 |
| `risks.description` | string | 否 | 风险描述 |
| `risks.affected_tasks` | array | 否 | 受影响的任务 ID 列表 |
| `risks.confidence` | string | 否 | 置信度（"high"|"medium"|"low"） |
| `issues` | array | 是 | 问题列表（check 模式） |
| `metadata` | object | 是 | 元数据 |
| `metadata.total_traces` | integer | 是 | 总轨迹数 |
| `metadata.failed_tasks` | integer | 是 | 失败任务数 |
| `metadata.llm_model` | string | 否 | 使用的 LLM 模型 |
| `metadata.tokens_used` | integer | 否 | 使用的 tokens |
| `metadata.duration_seconds` | float | 否 | 耗时（秒） |

### 命名约定

- 文件名: `debugger-analysis.json`（固定命名）
- 位置: `iter-N-artifacts/` 目录

---

### 步骤 5: planner-input.md

### 路径
```
agent/benchmarks/evolve/iter-N-artifacts/planner-input.md
```

### 格式

Markdown 格式的 planner 输入文档，**必须包含历史信息以避免重复失败策略**。

### 示例

```markdown
# Agent Harness Optimization - Iteration 0

## 📊 Current Iteration Overview

Pass rate: 62.5% (5/8)
Passed: agent_002, agent_005, agent_006, agent_010, tbench/cancel-async-tasks
Failed: agent_001, agent_003, agent_004

## 🔄 Strategy History

**(关键：避免重复失败策略)**

| Iteration | Pass Rate | Changes | Verdict | Regressions |
|-----------|-----------|---------|---------|------------|
| 0 | 62.5% | Initial baseline | - | - |

**(第一次迭代，无历史)**

## 🛡️ Task Stability

**(关键：了解哪些任务稳定/不稳定)**

### Stable Pass (0)
无

### Stable Fail (3)
- agent_001_forced_exploration
- agent_003_hidden_dep
- agent_004_context_overflow

### Unstable (0)
无

## ⚠️ Debugger Analysis

### Summary
分析失败的根本原因：强制探索约束、上下文溢出

### ⚠️ Predicted Risks

**Risk Description**: 如果调整 stale_age，可能导致 agent_003_hidden_dep 和 tbench/prove-plus-comm 回退

**Affected Tasks**: agent_003_hidden_dep, tbench/prove-plus-comm
**Confidence**: high

## 📋 Current Harness Configuration

```yaml
system_prompt: ../system_prompt.md
memory: ../memory.md
context_management:
  stale_annotation: true
  stale_age_investigative: 20
  stale_age_modification: 30
```

## 🎯 Your Task

Based on the debugger analysis and failure patterns, propose specific configuration changes.

**关键要求：**
1. 查看 Strategy History，避免重复失败的策略
2. 查看 Task Stability，重点关注 unstable 任务（优化潜力最大）
3. 考虑 Predicted Risks，避免回退稳定通过的任务
4. 如果某个策略之前尝试过但失败，说明方法不对，需要新的思路
```

### 重要：历史信息的作用

**避免重复失败策略：**
- Strategy History 显示所有过去的迭代、更改、结果
- 如果某个策略之前尝试过但结果为 "REJECTED" 或 "HARMFUL"，不要重复
- 检测重复策略并给出警告

**优化优先级：**
- Stable Fail: 始终失败，需要新策略
- Unstable: 有时成功，优先优化（容易改进）
- Stable Pass: 始终成功，保护这些任务（不要让它们回退）

**风险意识：**
- Debugger 的 Predicted Risks 明确告诉哪些任务可能回退
- Planner 必须在预期改进中说明如何应对这些风险

## Your Task

Based on the debugger analysis and failure patterns, propose specific configuration changes.
```

### 命名约定

- 文件名: `planner-input.md`（固定命名）
- 位置: `iter-N-artifacts/` 目录

---

## 步骤 5: planner-response.txt

### 路径
```
agent/benchmarks/evolve/iter-N-artifacts/planner-response.txt
```

### 格式

纯文本，包含 Planner LLM 的完整回复。

### 命名约定

- 文件名: `planner-response.txt`（固定命名）
- 位置: `iter-N-artifacts/` 目录

---

## 步骤 5: config.yaml

### 路径
```
agent/benchmarks/evolve/iter-N-artifacts/config.yaml
```

### 格式

```yaml
system_prompt: ../system_prompt.md
memory: ../memory.md
context_management:
  stale_annotation: true
  stale_age_investigative: 40
  stale_age_modification: 60
```

### 字段说明

| 字段 | 类型 | 必填 | 说明 |
|-----|------|------|------|
| `system_prompt` | string | 是 | System prompt 文件路径 |
| `memory` | string | 是 | Memory 文件路径 |
| `context_management` | object | 是 | 上下文管理配置 |
| `context_management.stale_annotation` | boolean | 是 | 是否标注 stale |
| `context_management.stale_age_investigative` | integer | 是 | Investigative 模式的 stale 阈值 |
| `context_management.stale_age_modification` | integer | 是 | Modification 模式的 stale 阈值 |

### 命名约定

- 文件名: `config.yaml`（固定命名）
- 位置: `iter-N-artifacts/` 目录

---

## 步骤 6: decision.json

### 路径
```
agent/benchmarks/evolve/iter-N-artifacts/decision.json
```

### 格式

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

### 字段说明

| 字段 | 类型 | 必填 | 说明 |
|-----|------|------|------|
| `status` | string | 是 | 状态（"accept"|"reject"|"failed"） |
| `current_pass_rate` | float | 是 | 当前通过率 |
| `best_pass_rate` | float | 是 | 最佳通过率 |
| `delta` | float | 是 | 差异（current - best） |
| `regressions` | integer | 是 | 回退的任务数 |
| `improvements` | integer | 是 | 改进的任务数 |
| `timestamp` | string | 是 | 决策时间（ISO 8601） |
| `reason` | string | 是 | 决策原因 |

### 命名约定

- 文件名: `decision.json`（固定命名）
- 位置: `iter-N-artifacts/` 目录

---

## 目录结构总览

```
agent/benchmarks/evolve/
├── evolve-manifest.json           ← 步骤 1 输出
├── agent.yaml                     ← 当前配置
├── iteration-0.json               ← 步骤 2 输出
├── iteration-1.json               ← 步骤 6 输出
│
├── iter-0-artifacts/              ← 迭代 0 的完整上下文
│   ├── benchmark.log              ← 步骤 2 输出
│   ├── traces/                    ← 步骤 3 输出
│   │   ├── trace-agent_001-rollout-0.json
│   │   └── trace-agent_003-rollout-0.json
│   ├── trace-summary.json         ← 步骤 3 输出
│   ├── debugger-analysis.json     ← 步骤 4 输出
│   ├── planner-input.md           ← 步骤 5 输出
│   ├── planner-response.txt       ← 步骤 5 输出
│   ├── config.yaml                ← 步骤 5 输出
│   └── decision.json              ← 步骤 6 输出
│
└── iter-1-artifacts/              ← 迭代 1 的完整上下文
    └── ...
```

---

## 验证规则

### JSON 格式验证

```bash
# 验证 JSON 格式
jq empty <file.json

# 验证必需字段
jq '.iteration' iteration-0.json  # 应该返回数字
jq '.pass_rate' iteration-0.json   # 应该返回数字
jq '.results' iteration-0.json     # 应该返回数组
```

### YAML 格式验证

```bash
# 验证 YAML 格式
yq eval '.' <file.yaml

# 验证必需字段
yq '.system_prompt' < config.yaml
yq '.context_management' < config.yaml
```

### 文件存在性验证

```bash
# 验证文件存在
test -f agent/benchmarks/evolve/evolve-manifest.json
test -f agent/benchmarks/evolve/iteration-0.json
test -f agent/benchmarks/evolve/iter-0-artifacts/benchmark.log
test -f agent/benchmarks/evolve/iter-0-artifacts/debugger-analysis.json
test -f agent/benchmarks/evolve/iter-0-artifacts/config.yaml
test -f agent/benchmarks/evolve/iter-0-artifacts/decision.json
```

---

## 相关文档

- **agent-harness-evolve-step-by-step.md** - 步骤化实现指南（引用本文档）
- **agent-debugger-design.md** - Debugger 架构设计
- **evolve-directions.md** - 方向论
---

## Planner System Prompt

### 路径
```
docs/design/planner-system-prompt.md
```

### 说明

Planner 是一个 **AI Agent**（使用 `./bin/ai serve` 启动），它的 system prompt 定义在 `docs/design/planner-system-prompt.md`。

### 使用方式

```bash
# 启动 planner
./bin/ai serve \
  --input-file iter-0-artifacts/planner-input.md \
  --system-prompt docs/design/planner-system-prompt.md \
  > iter-0-artifacts/planner-response.txt
```

或者，如果没有指定 `--system-prompt`，会使用 agent 默认的 system prompt。

---

## Planner 输入模板

### 路径
```
agent/benchmarks/evolve-planner-input-template.md
```

### 说明

这是 planner 输入的 Markdown 模板，包含所有占位符。`build_planner_context.py` 会用实际数据填充这些占位符。

### 使用方式

```bash
python3 benchmark/scripts/build_planner_context.py \
  --baseline baseline.json \
  --current iteration-0.json \
  --config agent/agent.yaml \
  --template agent/benchmarks/evolve-planner-input-template.md \
  --output iter-0-artifacts/planner-input.md
```

### 占位符

| 占位符 | 说明 |
|-------|------|
| `{{CURRENT_OVERVIEW}}` | 当前迭代概览 |
| `{{TASK_CLASSIFICATION}}` | 任务分类 |
| `{{FAILURE_DETAILS}}` | 失败详情 |
| `{{DEBUGGER_ANALYSIS}}` | Debugger 分析 |
| `{{CROSS_ITERATION_CHANGES}}` | 跨迭代变化 |
| `{{HISTORICAL_TRENDS}}` | 历史趋势 |
| `{{TASK_STABILITY}}` | 任务稳定性 |
| `{{PREVIOUS_ATTRIBUTION}}` | 之前 attribution |
| `{{ATTRIBUTION_VERDICT}}` | Attribution 评估 |
| `{{STRATEGY_HISTORY}}` | 策略历史 |
| `{{CURRENT_CONFIG_YAML}}` | 当前配置 |
| `{{SYSTEM_PROMPT}}` | System prompt 内容 |
| `{{MEMORY}}` | Memory 内容 |
| `{{CONTEXT_MANAGEMENT}}` | Context management 内容 |

---

## System Prompt 文档

### Debugger System Prompt

**文档**: `docs/design/agent-debugger-design.md`

**章节**: Debugger Agent Prompt → System Prompt

**内容**: 定义 debugger 的身份、任务、工具、工作流程

**引用**: 在步骤 4 中引用

### Planner System Prompt

**文档**: `docs/design/planner-system-prompt.md`

**内容**: 定义 planner 的身份、输入、任务、关键要求、输出格式

**引用**: 在步骤 5 中引用

**关键要求**:
1. 避免重复失败策略（查看 Strategy History）
2. 优化优先级（查看 Task Stability）
3. 考虑风险预测（查看 Predicted Risks）
4. 解释原因

---

## 相关文档

- **agent-harness-evolve-step-by-step.md** - 步骤化实现指南
- **agent-debugger-design.md** - Debugger 架构设计
- **planner-system-prompt.md** - Planner System Prompt（新增）
- **evolve-directions.md** - 方向论

---

## 完整文档清单

| 文档 | 大小 | 说明 |
|-----|------|------|
| `evolve-output-spec.md` | 16K | 输出文件规格 |
| `agent-harness-evolve-step-by-step.md` | 26K | 步骤化实现指南 |
| `planner-system-prompt.md` | 2.7K | Planner System Prompt |
| `agent-debugger-design.md` | 17K | Debugger 架构设计 |
| `evolve-directions.md` | 6K | 方向论 |
| `evolve-planner-input-template.md` | 756B | Planner 输入模板 |
