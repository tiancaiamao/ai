# Agent Debugger Design

## Goal

实现专业的 Agent Debugger，作为自动 evolve 的基础设施。参考 agentic-harness-engineering 的 `adb` CLI，适配当前项目的 trajectory 格式。

---

## Problem Statement

当前项目已有：
- ✅ rollout 轨迹捕获（在 benchmark results JSON 的 `trajectory` 字段）
- ✅ per_task_rollouts 聚合数据（n_pass/n_fail/pass_rate）
- ✅ planner 上下文构建（build_planner_context.py）
- ⚠️ 基础的失败分析（手动 Python 脚本）

但缺少：
- ❌ 轨迹标准化（统一格式）
- ❌ LLM 深度分析（不仅仅是统计）
- ❌ 自动问题检测（工具错误/循环/幻觉）
- ❌ 多 trace 对比（PASS vs FAIL）
- ❌ Verifier 输出集成
- ❌ 标准化输出（JSON）

---

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                    Benchmark Runner                       │
│  (RunTask → extractTrajectory → Result JSON)              │
└────────────────────┬────────────────────────────────────┘
                     │ trajectory (raw)
                     ↓
┌─────────────────────────────────────────────────────────┐
│              trace_normalizer.py                          │
│  (normalize_trajectory → OpenAI messages 格式)           │
└────────────────────┬────────────────────────────────────┘
                     │ normalized messages
                     ↓
┌─────────────────────────────────────────────────────────┐
│               agent_debugger.py                           │
│                                                         │
│  ┌─────────────┐      ┌─────────────┐                   │
│  │ debugger    │      │     LLM     │                   │
│  │  agent      │◄────►│  (AI)       │                   │
│  └─────────────┘      └─────────────┘                   │
│         │                                             │
│         ▼                                             │
│  ┌─────────────────────────────────────┐              │
│  │  Output: JSON                     │              │
│  │  - mode: "ask" | "check"           │              │
│  │  - answer/response                 │              │
│  │  - issues[]                        │              │
│  └─────────────────────────────────────┘              │
└────────────────────┬────────────────────────────────────┘
                     │ JSON output
                     ↓
┌─────────────────────────────────────────────────────────┐
│              build_planner_context.py                    │
│  (集成到 planner 输入：Section 2 Failure Analysis)      │
└─────────────────────────────────────────────────────────┘
```

---

## Input Format Specification

### 1. Raw Trajectory Format（当前 benchmark 输出）

```json
{
  "task_id": "agent_001_forced_exploration",
  "passed": false,
  "trajectory": [
    {
      "tool": "bash",
      "args": {"command": "ls -la"},
      "output": "total 64...",
      "duration": 0.0,
      "thinking": "Let me understand the task..."
    },
    {
      "tool": "read",
      "args": {"path": "/path/to/file.py"},
      "output": "def sort_asc(arr):\n    ...",
      "duration": 0.1
    },
    {
      "tool": "edit",
      "args": {"newText": "...", "oldText": "..."},
      "output": "Edited /path/to/file.py",
      "duration": 0.2
    }
  ]
}
```

**字段说明**：
- `tool`: 工具名称（bash/read/edit/write/grep）
- `args`: 工具参数（dict）
- `output`: 工具输出（string）
- `duration`: 执行时间（秒）
- `thinking`: Agent 的思考过程（可选）

---

### 2. Normalized Trace Format（标准化后）

```json
{
  "trace_id": "agent_001_forced_exploration-rollout-0",
  "task_id": "agent_001_forced_exploration",
  "rollout_index": 0,
  "passed": false,
  "verifier_output": "ERROR: constraint violations: files_read_before_fix",
  "messages": [
    {
      "role": "system",
      "content": "You are a coding agent..."
    },
    {
      "role": "user",
      "content": "Fix the bug in the sorting function...",
      "metadata": {
        "kind": "user",
        "timestamp": 1716904800
      }
    },
    {
      "role": "assistant",
      "content": [
        {"type": "thinking", "text": "Let me start by listing the files..."},
        {"type": "text", "text": "I'll begin with listing the directory structure."}
      ],
      "tool_calls": [
        {
          "id": "call_1",
          "type": "function",
          "function": {
            "name": "bash",
            "arguments": "{\"command\":\"ls -la\"}"
          }
        }
      ],
      "metadata": {
        "kind": "assistant",
        "timestamp": 1716904801,
        "tokens": {"input": 120, "output": 50}
      }
    },
    {
      "role": "tool",
      "tool_call_id": "call_1",
      "content": "total 64\ndrwxr-xr-x...",
      "name": "bash",
      "metadata": {
        "kind": "tool_result",
        "timestamp": 1716904801,
        "duration": 0.05
      }
    }
  ]
}
```

**转换规则**：
| Raw 字段 | Normalized 字段 | 处理规则 |
|----------|----------------|---------|
| `trajectory[i].tool` | `tool_calls[].function.name` | 直接映射 |
| `trajectory[i].args` | `tool_calls[].function.arguments` | JSON stringify |
| `trajectory[i].output` | `content`（tool role） | 作为 tool_result |
| `trajectory[i].thinking` | `content[0].text`（type=thinking） | 放在 assistant 消息开头 |
| `trajectory[i].duration` | `metadata.duration` | 保留 |

---

## Output Format Specification

### 1. Ask Mode（问答分析）

```json
{
  "status": "success",
  "command": "ask",
  "trace_ids": ["trace-1", "trace-2"],
  "question": "为什么失败的尝试失败了？",
  "answer": "失败的根本原因是：Agent 读了 8 个文件才找到 bug（module_c.py），违反了'最多读 2 个文件'的约束。成功的尝试（trace-2）先用 grep 搜索排序函数，然后只读可疑的文件。",
  "response": "失败的根本原因是文件读取策略问题...",
  "request_id": "req-123",
  "metadata": {
    "llm_model": "gpt-4.1",
    "tokens_used": 350,
    "duration": 2.3
  }
}
```

**字段说明**：
- `status`: "success" | "failed"
- `command`: "ask" | "check"
- `trace_ids`: 分析的 trace 列表
- `question`: 用户问题（如果没有则用默认问题）
- `answer`: 主要答案（详细分析）
- `response`: 简短总结（用于 planner 上下文）
- `metadata`: LLM 元信息

---

### 2. Check Mode（自动问题检测）

```json
{
  "status": "success",
  "command": "check",
  "trace_ids": ["trace-1"],
  "issues_count": 2,
  "issues": [
    {
      "issue_type": "工具错误",
      "summary": "Agent 调用了错误的工具",
      "evidence": "Message 7 调用了 'read' 但应该用 'grep' 搜索模式",
      "trace_id": "trace-1",
      "message_index": 7,
      "severity": "high",
      "suggestion": "使用 grep/pattern 搜索工具先定位问题，再用 read 查看细节"
    },
    {
      "issue_type": "循环",
      "summary": "Agent 在同一个操作上重复多次",
      "evidence": "tool='read' repeated 8x (args: {})",
      "trace_id": "trace-1",
      "message_index": 12,
      "severity": "medium",
      "suggestion": "检查工具调用逻辑，避免重复读取相同文件"
    }
  ],
  "response": "发现 2 个问题：1 个工具错误，1 个循环模式",
  "request_id": "req-123",
  "metadata": {
    "llm_model": "gpt-4.1",
    "tokens_used": 280,
    "duration": 1.8
  }
}
```

**issue_type 枚举**（5 种，与 AHE 一致）：
1. **工具错误**: 调用错误的工具，或参数错误
2. **幻觉**: 输出内容与工具结果不符，或声称做了实际没做的事
3. **循环**: 重复相同的操作而没有进展
4. **不合规**: 违反约束（如 files_read_before_fix, max_steps）
5. **截断**: 因 token 或超时而中断

**severity 等级**：
- `high`: 严重影响性能/正确性
- `medium**: 有影响但不致命
- `low`: 次要问题

---

## Debugger Agent Prompt

### System Prompt

```
你是一个专业的 agent debugger，专门分析 agent 执行轨迹并回答问题。

## 任务
用户会给你一个或多个 agent 执行轨迹的本地文件路径（OpenAI messages 格式）。
每个文件包含 {"trace_id": "...", "messages": [...]}。
你不会在 system prompt 中看到完整的 trace 内容，必须通过工具读取。

## 工具
你有：read_file（读取文件）、search_file_content（搜索内容）、list_directory（列目录）、complete_task（完成任务）。

## 工作流程
严格按以下顺序执行，不要跳过：

1. **快速浏览**（≈ tool call 1-3）：对每个路径，用 small limit 读取文件头部，了解大致形状（system/user/assistant/tool 模式、错误标记、是否有 trace_id）。

2. **定位问题**（≈ tool call 4-10）：用 search_file_content 搜索工具名称、错误关键词、用户文本，找到与问题相关的范围。

3. **读取上下文**（≈ tool call 11-15）：用 offset/limit 完整读取每个命中的工具 I/O 上下文，然后得出结论。

4. **多 trace 对比**（≈ tool call 16-18，仅当多个 traces 时）：对比发现 —— 哪些点一致，哪些点分歧，哪个 trace 在每个争议点上更正确。

5. **完成**（≤ 20 calls）：调用 complete_task 恰好一次。

## 输出格式
调用 complete_task 恰好一次，result 字段匹配以下 schema：

### 对于 ask 模式
{
  "mode": "ask",
  "answer": "自由格式文本；引用确切的 message_index"
}

### 对于 check 模式
{
  "mode": "check",
  "issues": [
    {
      "issue_type": "工具错误 | 幻觉 | 循环 | 不合规 | 截断",
      "summary": "一行摘要",
      "evidence": "引用的文本 / 确切原因",
      "trace_id": "trace ID",
      "message_index": 123
    }
  ],
  "response": "简短总段落"
}

issue_type 必须是上述 5 个枚举值之一。
message_index 是该 trace 的 messages 数组的 0-based 索引。
trace_id 必须匹配输入文件中的 trace_id；如果文件缺少，使用文件名。

## 风格要求
- 优先用具体证据 —— 精确的 message_index、引用 trace 中的字符串 —— 而不是模糊的声明。
- 每个证据都引用 trace_id + message_index（例如 trace_id=abc123 #42）。仅在 trace_id 缺失或重复时回退到文件名；永远不要用完整文件路径引用。
- 当给多个 traces 时，不要只是依次总结每个 —— 明确指出哪些点一致，哪些点分歧。
- 如果证据不足以回答，在 answer/response 中说清楚，并列出你检查了哪些 traces 和哪些 message_index 范围。不要编造。
- 保持答案简洁；读者是自动化的。
```

---

### User Prompt（Ask 模式）

```python
# 单个失败任务（k>1）
DEFAULT_ASK_QUERY_MULTIPLE = (
    "这个任务有 {n_total} 次 rollouts：{n_pass} 次通过，{n_fail} 次失败。\n"
    "Traces: {trace_labels}\n\n"
    "重要：如果提供了验证器测试输出（verifier_output），那显示了决定 pass/fail 的真实外部测试结果。\n"
    "Agent 永远看不到这个输出。将验证器的实际失败信息与 agent 的 trace 交叉引用，\n"
    "以找到真正的根本原因 —— agent 可能认为自己成功了，但验证器显示不同。\n\n"
    "识别：\n"
    "1. **失败点**：失败的尝试在哪个具体步骤开始出错？交叉引用验证器输出（如果可用）。\n"
    "2. **根本原因**：失败的基本原因是什么？区分 'agent 认为成功但验证器不同意' vs 'agent 遇到错误'。\n"
    "3. **正确做法**：在失败点应该怎么做？\n"
    "4. **通用机制**：什么结构性机制（非任务特定知识）可以防止这类失败？\n\n"
    "关注通用模式。保持简洁（300 字以内）。"
)

# 单个失败任务（k=1）
DEFAULT_ASK_QUERY_SINGLE_FAIL = (
    "这个任务有一次 rollout，结果为 **失败**。\n"
    "Verifier 输出：{verifier_output}\n\n"
    "分析 trace 并定位问题。\n\n"
    "识别：\n"
    "1. **失败点**：在哪个具体步骤开始出错？\n"
    "2. **根本原因**：失败的基本原因？\n"
    "3. **正确做法**：应该怎么做？\n"
    "4. **通用机制**：什么结构性机制可以防止这类失败？\n\n"
    "保持简洁（300 字以内）。"
)

# 单个成功任务（k>1，总结经验）
DEFAULT_ASK_QUERY_SUMMARY = (
    "这个任务有 {n_total} 次 rollouts，全部通过。\n"
    "Traces: {trace_labels}\n\n"
    "分析一个代表性的 trace。\n\n"
    "识别：\n"
    "1. **关键策略**：Agent 的方法是什么，为什么成功？\n"
    "2. **可重用模式**：哪些通用行为模式可以应用到其他任务？\n"
    "3. **脆弱性风险**：有什么看起来脆弱或幸运的吗？\n\n"
    "保持简洁（150 字以内）。"
)
```

---

## Implementation Plan

### Phase 1: Core Infrastructure（P0）

1. **`benchmark/scripts/trace_normalizer.py`**
   - `normalize_trajectory(raw_trajectory, task_id, rollout_index)`
   - `normalize_all_task_trajectories(result_json_path)`
   - 输出：`{task_id}-rollout-{i}.normalized.json`

2. **`benchmark/scripts/agent_debugger.py`**
   - `DebuggerAgent` 类（LLM wrapper）
   - `ask_mode(traces, question)`
   - `check_mode(traces)`
   - 输出：JSON 格式

3. **`benchmark/scripts/debugger_agent_config.yaml`**
   - Debugger 的 LLM 配置
   - 工具定义（read_file, search_file_content, complete_task）

---

### Phase 2: Integration to Evolve（P1）

4. **更新 `benchmark/scripts/build_planner_context.py`**
   - 在 `build_failure_details()` 中调用 `agent_debugger.ask_mode()`
   - 集成到 Section 2: Failure Analysis

5. **更新 `benchmark/scripts/evolve_loop.sh`**
   - 在 Step 5（构建 planner 输入）后，运行 debugger
   - 保存 debugger 输出到 `iter-N-artifacts/debugger-analysis.json`

---

### Phase 3: Verification（P2）

6. **`benchmark/scripts/verify_debugger.py`**
   - 单元测试：trace_normalizer
   - 集成测试：agent_debugger
   - 端到端测试：evolve 流程

---

## File Structure

```
benchmark/scripts/
├── trace_normalizer.py          # 新增：轨迹标准化
├── agent_debugger.py            # 新增：Debugger CLI
├── debugger_agent_config.yaml   # 新增：Debugger agent 配置
├── build_planner_context.py     # 修改：集成 debugger
├── evolve_loop.sh               # 修改：调用 debugger
└── verify_debugger.py           # 新增：验证脚本

benchmark/results/
└── *.normalized.json            # 标准化后的 traces

agent/benchmarks/experiments/
└── iter-N-artifacts/
    └── debugger-analysis.json   # Debugger 输出
```

---

## Example Usage

### 1. 手动分析单个 trace

```bash
# 标准化轨迹
python3 benchmark/scripts/trace_normalizer.py \
  --input benchmark/results/result_20260528_172109.json \
  --output-dir /tmp/normalized-traces/

# 分析失败原因
python3 benchmark/scripts/agent_debugger.py \
  ask \
  --traces /tmp/normalized-traces/agent_001_forced_exploration-rollout-*.json \
  --question "为什么失败的尝试失败了？" \
  --config benchmark/scripts/debugger_agent_config.yaml
```

### 2. 自动集成到 evolve

```bash
# evolve_loop.sh 自动运行：
# 1. 运行 benchmark
# 2. 提取 trajectory
# 3. 标准化轨迹
# 4. 调用 debugger ask
# 5. 将结果集成到 planner 输入
# 6. 运行 planner
```

---

## Success Criteria

- [ ] trace_normalizer 能正确转换所有工具类型（bash/read/edit/write/grep）
- [ ] Debugger agent 能输出符合 JSON schema 的结果
- [ ] ask 模式能正确识别失败的根本原因
- [ ] check 模式能自动检测常见问题（工具错误/循环）
- [ ] 集成到 evolve_loop.sh 后，planner 能看到结构化的失败分析
- [ ] 验证脚本通过所有测试用例

---

## References

- agentic-harness-engineering: `trace_converter.py`（标准化逻辑）
- agentic-harness-engineering: `evolve.py` Phase 2.5a（集成逻辑）
- agentic-harness-engineering: `SKILL.md`（prompt 设计）