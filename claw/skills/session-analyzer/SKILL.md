---
name: session-analyzer
description: 像 code review 一样分析 AI agent 会话，发现设计问题并提供改进建议
license: MIT
---

# Session Analyzer - AI Code Review

像 senior engineer review 代码一样，分析 AI agent 的运行会话，发现设计问题并提供改进建议。

## 何时使用

- 周期性分析最近的 AI 对话（通过 /cron 触发）
- 发现 prompt 设计问题
- 识别工具选择错误
- 发现流程设计缺陷
- 找出反模式和可优化点

**目标**：持续优化 ai 项目的代码质量

## 核心思路：Code Review 范式

**不是**技术指标统计（重试率、token 消耗）
**而是**设计洞察（为什么这里选错了工具？为什么这个 prompt 效果不好？）

### 分析视角

1. **Prompt 设计**：系统提示词、工具描述是否清晰
2. **工具选择**：agent 是否选对了工具，为什么选错
3. **流程设计**：任务分解是否合理，执行顺序是否最优
4. **错误处理**：失败时的重试、fallback 策略
5. **反模式**：重复失败、无效循环、过度调用
6. **Subagent 协作**：delegation 是否合理，任务描述是否清晰
7. **上下文腐烂**：agent 在对话后期是否出现理解偏差、幻觉、指令不服从
8. **上下文管理**：agent 是否主动调用 llm_context_decision，系统提醒是否正常工作

## 数据源

**⚠️ 重要：路径必须正确**

- **Sessions**: `~/.ai/sessions/--<cwd>--/<session-id>/messages.jsonl`
- **Traces**: `~/.ai/traces/pid<pid>-sess<session-id>.<N>.perfetto.json`
  - N 是每次 prompt 递增的序号（1, 2, 3...）
  - 大文件可能还有 `-part-X` 后缀

**不是** `~/.aiclaw/` 目录！

## 工作流程

### 步骤 0：快速会话概览（配合 session-reader）

在深度分析前，先用 session-reader 快速了解会话：

```bash
# 获取会话概览
uv run ${CLAUDE_SKILL_ROOT}/scripts/read_session.py <path> --mode overview

# 查看工具调用
uv run ${CLAUDE_SKILL_ROOT}/scripts/read_session.py <path> --mode tools
```

参考：`references/session-reader.md`

---

### 步骤 1：选择分析模式

根据分析目标选择模式：

| 模式 | 目标 | 适用场景 |
|------|------|----------|
| `--mode full` | 全面分析 | 周期性深度 review |
| `--mode tools` | 仅工具选择 | 快速发现工具使用问题 |
| `--mode flow` | 仅流程设计 | 优化执行效率 |
| `--mode prompt` | 仅 prompt 设计 | 调试理解问题 |
| `--mode subagents` | 仅 subagent | 优化 delegation 策略 |

---

### 步骤 2：选择多个 session（批处理）

```bash
# 找到最近 5-10 个未分析的 session（按修改时间）
find ~/.ai/traces -name "*.perfetto.json" -type f -mtime -1 | xargs ls -t | head -10

# 从 checkpoint 开始，取接下来的 5 个
CHECKPOINT=$(cat ~/.aiclaw/analysis/checkpoint.json 2>/dev/null || echo '{}')
# 提取 session-id 列表
```

**批处理策略**：
- **每次分析 5-10 个 session**
- 从 checkpoint 继续，避免重复分析
- 分析完所有 session 后，生成汇总报告

---

### 步骤 3：按模式逐个分析 session

#### 模式 A: 工具选择分析 (`--mode tools`)

```bash
# 读取工具调用
uv run ${CLAUDE_SKILL_ROOT}/scripts/read_session.py <path> --mode tools
```

**关注点**：
- Agent 选择了什么工具？
- 是否选错了？（如应该用 read 却用了 bash cat）
- 为什么选错？工具描述不够清楚？
- 是否有更好的工具组合？

#### 模式 B: 流程设计分析 (`--mode flow`)

```bash
# 读取对话，关注执行顺序
tail -100 "$SESSION_DIR/messages.jsonl"
```

**关注点**：
- 任务分解是否合理？
- 是否有冗余步骤？
- 执行顺序是否最优？
- 是否可以并行执行？

#### 模式 C: Prompt 设计分析 (`--mode prompt`)

**关注点**：
- Agent 是否正确理解了用户意图？
- 如果误解：系统提示词/工具描述是否不够清晰？
- 如果理解正确但执行错误：工具描述是否有歧义？

#### 模式 D: Subagent 分析 (`--mode subagents`)

**⚠️ 子代理会话位置**（ai 项目使用 tmux 机制）：

```bash
# 1. 从主 session 中找到 subagent 调用
# 查找 toolCall 结果中的 tmux session 名称
grep "subagent-" ~/.ai/sessions/--<cwd>--/<session-id>/messages.jsonl

# 2. 通过 tmux session 找到对应的 AI session
tmux capture-pane -t subagent-<timestamp>-<random> -p -S - | grep "Session ID:"

# 3. 读取 subagent 的 messages.jsonl
cat ~/.ai/sessions/--<cwd>--/<subagent-session-id>/messages.jsonl
```

**关注点**：
- **任务描述清晰度**：父代理给子代理的指令是否明确？
- **子代理理解**：子代理是否理解了任务？
- **执行偏差**：子代理是否过度拆解/过度执行？
- **结果完整性**：子代理返回的结果是否完整？
- **协作效率**：父子代理间的交互是否合理？

**典型问题**：
- 父代理任务描述不清晰 → 子代理执行偏差
- 子代理过度执行（做了额外工作）
- 结果返回格式不统一
- Subagent 超时未完成
- 并发 subagent 没有正确协调

---

#### 模式 E: 上下文腐烂分析 (`--mode context-rot`)

**关注点**：
- Agent 在对话后期是否出现理解偏差？
- 是否出现幻觉（声称完成了实际未完成的任务）？
- 是否出现指令不服从（持续使用错误方法）？
- 上下文增长速度是否合理？

**典型问题**：
```
User: "基于 master 分支创建 git worktree"
Agent: 使用 cd 命令（错误工具）→ 失败
User: "change workspace 才能过去"
Agent: 继续使用 cd（不服从指令）→ 再次失败
User: "我没看到你的修复呀"
Agent: 声称已修复（幻觉）→ 但实际未完成
```

**根因分析**：
- 上下文持续增长，agent 注意力被早期信息分散
- 没有及时清理上下文，导致理解偏差
- 上下文 > 50K tokens 时，agent 开始出现严重腐烂

**改进建议**：
在系统提示词中添加：
```markdown
## 上下文管理触发条件

当满足以下条件时，必须主动调用 llm_context_decision：

1. **输入 tokens > 50K** → 立即调用
2. **连续 2 次指令失败** → 可能是上下文腐烂导致
3. **任务性质变化** → 考虑 compact 清理早期上下文
```

---

#### 模式 F: 上下文管理分析 (`--mode context-management`)

**关注点**：
- Agent 是否主动调用 llm_context_decision？
- 系统是否正确发送 remind 消息？
- runtime_state 是否被正确注入到对话中？
- agent 是否检查 runtime_state？

**数据来源**：
```bash
# 检查 agent 是否调用 llm_context_decision
grep '"name":"llm_context_decision"' messages.jsonl

# 检查系统是否发送 remind
grep "<agent:remind>" messages.jsonl

# 检查 agent 是否检查 runtime_state
grep "<agent:runtime_state>" messages.jsonl
```

**典型问题**：

**问题 1：Agent 不主动调用 llm_context_decision**
```
Context 增长轨迹：
- 23:31: 60K tokens (29.4%) ← 应该调用
- 23:46: 68K tokens (33.6%) ← 应该调用
- 00:07: 107K tokens (52.5%) ← 必须调用
- 00:39: 108K tokens (52.7%) ← 终于调用（太晚了）
```

**问题 2：系统 remind 机制失效**
```
期望行为：
- Context >= 30% 时，系统发送 <agent:remind> 消息
- Agent 收到 remind 后立即调用 llm_context_decision

实际行为：
- Context 从 29% 增长到 52.5%，没有收到任何 remind
- Agent 8 小时内完全依赖自主判断
```

**问题 3：runtime_state 未被注入**
```
期望：
<agent:runtime_state>
context_usage_percent: 35.2
stale_tool_outputs: 12+
...
</agent:runtime_state>

实际：
- Session 中没有 <agent:runtime_state> 标签
- Agent 无法知道上下文压力
```

**改进建议**：

**1. 修复系统提醒机制**
```python
# 在 ai 项目中添加提醒逻辑
def check_and_send_remind(messages, context_size, max_size):
    percent = context_size / max_size * 100
    
    if percent >= 30 and not recent_remind:
        # 注入 remind 消息
        remind_msg = {
            "type": "system",
            "content": f"""
            <agent:remind>
            context_usage_percent: {percent}%
            stale_tool_outputs: {count_stale()}
            
            You MUST call llm_context_decision before continuing.
            </agent:remind>
            """
        }
        messages.append(remind_msg)
        record_remind()
```

**2. 确保 runtime_state 被注入**
```python
# 在每次 LLM 调用前注入 runtime_state
def inject_runtime_state(messages):
    state = {
        "context_usage_percent": calculate_percent(),
        "stale_tool_outputs": count_stale(),
        "tokens_used_approx": calculate_tokens(),
        # ... 其他指标
    }
    
    runtime_state_msg = {
        "type": "system",
        "content": f"<agent:runtime_state>\n{yaml.dump(state)}\n</agent:runtime_state>"
    }
    
    # 注入到 messages 开头（最近一条）
    messages.insert(0, runtime_state_msg)
```

**3. 强制 Agent 检查 runtime_state**
```markdown
## Turn Protocol (MANDATORY)

在每轮对话开始时，你必须：

1. **检查** `<agent:runtime_state>` — 读取遥测数据
2. **评估** context_usage_percent 和 stale_tool_outputs
3. **决定** 是否需要调用 llm_context_decision

示例：
```
[User 消息]
↓
[系统注入 runtime_state]
<agent:runtime_state>
context_usage_percent: 42.3%
stale_tool_outputs: 15
...
</agent:runtime_state>
↓
[Agent 检查]
"我看到 context_usage_percent 是 42.3%，stale_tool_outputs 是 15。
根据指南，我应该调用 llm_context_decision。"
```

**不要跳过 runtime_state 检查！**
```

---

#### 模式 G: Traces 性能分析（可选）

当需要深入理解性能/并发问题时：

```bash
# 检查工具调用时序
grep "toolCall" ~/.ai/traces/pid<pid>-sess<session-id>.*.perfetto.json

# 检查并发执行
# 寻找同一时间段内并发的 toolCall
```

**关注点**：
- 工具是否真正并行执行？（还是串行）
- 是否有工具调用延迟异常？
- 是否有空转/等待时间？
- Subagent 启动/等待时间是否合理？

---

### 步骤 4：生成单个 session 报告

每个 session 生成独立报告：

```bash
REPORT=~/.aiclaw/analysis/session-<session-id>.md
```

---

### 步骤 5：生成汇总报告

分析完所有 session 后，生成汇总报告：

```bash
SUMMARY_REPORT=~/.aiclaw/analysis/summary-$(date +%Y-%m-%d).md
```

**汇总报告格式**：

```markdown
# Session Analysis Summary - YYYY-MM-DD

## 分析范围
- 时间段：<起止时间>
- Session 数量：<N 个>
- 分析模式：<tools/flow/prompt/subagents>
- 发现问题总数：<M 个>

---

## 🔴 高优先级问题（跨 session 共性）

### Issue 1: <问题类型>（出现 N 次）

**影响范围**：
- Session 1: [链接到具体报告]
- Session 2: [链接到具体报告]
- ...

**共性问题**：<总结根本原因>

**统一修复方案**：<给出代码/配置级别的改进建议>

**优先级**：P1（立即修复）

---

## 🟡 中优先级问题

### Issue 2: <问题类型>（出现 M 次）
...

---

## 🟢 优化建议

### Suggestion 1: <优化方向>（出现 K 次）
...

---

## 按目标分类统计

| 用户目标 | Session 数 | 成功率 | 典型问题 |
|---------|-----------|--------|----------|
| 读取文件 | 5 | 80% | 1 个用 bash cat |
| 修改代码 | 3 | 100% | 无 |
| 调试问题 | 2 | 50% | 1 个重复失败 |
| Subagent 任务 | 4 | 75% | 1 个任务描述不清 |

---

## 统计数据

### 问题分布
| Session | Critical | Medium | Suggestion |
|---------|----------|--------|------------|
| session-1 | 2 | 3 | 1 |
| session-2 | 1 | 2 | 0 |
| ... | ... | ... | ... |

### 问题类型统计
- 工具选择错误: X 次
- Subagent 协作问题: Y 次
- 并发设计问题: Z 次
- 错误处理不当: W 次
- Prompt 不清晰: V 次

---

## 改进建议优先级

### P1 - 立即修复（影响稳定性/正确性）
1. [Issue 1] - 修改位置：xxx
2. [Issue 3] - 修改位置：yyy

### P2 - 短期优化（影响效率/体验）
1. [Issue 2] - 修改位置：xxx

### P3 - 长期改进（技术债务）
1. [Suggestion 1] - 修改位置：xxx

---

## 下次分析重点

- [ ] 验证 P1 修复效果
- [ ] 关注是否有新的反模式
- [ ] 跟踪长期改进项进度
```

---

### 步骤 6：更新 checkpoint

```json
{
  "lastSessionId": "<最后一个分析的 session-id>",
  "lastAnalyzedAt": "<ISO 时间>",
  "totalSessions": <累计分析数量>,
  "lastSummaryAt": "<上次汇总时间>",
  "analysisMode": "<使用的分析模式>",
  "pendingSessions": ["sess-124", "sess-125"]
}
```

## 单个 Session 报告格式（Code Review Style）

```markdown
# Session Analysis - Code Review Style

**Session**: <session-id>
**时间**: <timestamp>
**工作目录**: <cwd>
**分析模式**: <tools/flow/prompt/subagents>

## 摘要
- 用户意图：<一句话描述>
- agent 执行：<一句话描述结果>
- 发现问题：<数量> 个

---

## 🔴 Critical Issues

### Issue 1: <具体问题>

**位置**：messages.jsonl:23（第 23 轮对话）

**问题描述**：
agent 选择了 bash cat 而不是 read 工具读取文件

**具体证据**：
```
User: "读取 pkg/agent/loop.go 文件"
Agent: 调用 bash 工具，执行 `cat pkg/agent/loop.go`
```

**为什么这是个问题**：
- read 工具有路径补全、错误处理更好
- bash cat 可能受 shell 转义影响
- 违反了"优先使用专用工具"原则

**根因分析**：
- tools.md 中 read 工具描述不够清晰
- 没有明确说明"读取文件用 read，执行命令用 bash"

**改进建议**：
```go
// 在 tools.md 中添加：
- read: 读取文件内容（支持路径补全、自动错误处理）
- bash: 执行 shell 命令（仅当需要 shell 特性时使用）
```

---

### Issue 2: Subagent 任务描述不清（Subagent 模式特有）

**位置**：messages.jsonl:45（subagent 调用）

**问题描述**：
父代理启动 subagent 时，任务描述过于宽泛，导致子代理执行了不必要的工作

**具体证据**：
```
父代理: "Review the code in pkg/agent/ directory"
Subagent 执行: 分析了 pkg/agent/ 下的所有 15 个文件，但用户只关心 loop.go
```

**根因分析**：
- 父代理没有明确子代理的范围
- 缺少输出格式要求
- 没有设置合理的 timeout

**改进建议**：
```bash
# 在启动 subagent 前明确任务
cat > /tmp/task.txt << 'EOF'
Review pkg/agent/loop.go only.

Focus on:
- Concurrency safety
- Error handling
- Performance bottlenecks

Output format: /tmp/review-result.json (JSON)
EOF
```

---

## 🟡 Medium Issues

### Issue 3: <具体问题>

**位置**：messages.jsonl:45-50

**问题描述**：
连续 5 次调用同一工具失败，没有提前验证

**具体证据**：
```
Turn 45: bash: grep "pattern" file.txt → 失败（文件不存在）
Turn 46: bash: grep "pattern" file.txt → 失败（文件不存在）
Turn 47: bash: grep "pattern" file.txt → 失败（文件不存在）
...
```

**为什么这是个问题**：
- 应该先检查文件是否存在
- 失败后没有调整策略
- 浪费了 5 次 LLM 调用

**改进建议**：
在 agent 的系统提示词中添加：
```
当工具调用失败时：
1. 分析失败原因
2. 调整策略（不要重复相同操作）
3. 考虑使用验证步骤（如先检查文件存在）
```

---

## 🟢 Suggestions

### Suggestion 1: <优化建议>

**位置**：messages.jsonl:60-70

**观察**：
agent 依次执行了 3 个独立的文件读取

**优化方案**：
这 3 个读取可以并行执行（使用工具的并发能力）

**收益**：
- 减少总耗时 ~60%
- 提升 user experience

---

## 总结

### 优先修复
1. [Critical] tools.md 中明确 read vs bash 使用场景
2. [Critical] Subagent 任务描述模板化
3. [Medium] 系统提示词添加失败处理策略

### 可选优化
1. [Suggestion] 利用工具并发能力

### 下次关注点
- 检查修复后的效果
- 关注是否有新的反模式
- 监控 subagent 协作效率
```

## 分析重点

### ✅ 应该关注的

1. **设计问题**：prompt 不清晰、工具描述有歧义
2. **选择错误**：选错工具、执行顺序不合理
3. **反模式**：重复失败、无效循环、过度调用
4. **Subagent 协作**：任务描述不清、过度执行、结果不完整
5. **改进机会**：可以优化的流程、可以复用的模式

### ❌ 不应该关注的

1. **技术指标**：重试率、token 消耗（用脚本统计即可）
2. **性能数据**：P50/P95/P99 响应时间（监控工具负责）
3. **数量统计**：文件数、调用次数（不提供洞察）

## 注意事项

- **慢工出细活**：这是离线分析，要像 code review 一样深入思考
- **⚠️ 批处理策略**：每次分析 **5-10 个 session**，然后生成汇总报告
  - 不要只分析 1 个（样本太少，看不出共性问题）
  - 不要一次分析太多（避免超时和 rate limit）
  - 逐个分析，中间等待 10-30 秒避免 rate limit
- **必须包含证据**：每个问题都要引用具体对话内容
- **可操作建议**：改进建议要具体到代码/配置修改
- **Code Review 范式**：像 senior engineer review PR 一样思考
- **目标明确**：持续优化 ai 项目的代码质量
- **汇总报告**：分析完一批 session 后，必须生成汇总报告（跨 session 共性问题）
- **与 session-reader 配合**：先快速概览，再深度分析

## 参考文档

- **Session Reader**: `references/session-reader.md` - 会话读取和概览
- **Subagent**: `/skills/subagent` - 子代理机制和最佳实践