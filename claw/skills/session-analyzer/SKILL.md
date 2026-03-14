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

## 数据源

**⚠️ 重要：路径必须正确**

- **Sessions**: `~/.ai/sessions/--<cwd>--/<session-id>/messages.jsonl`
- **Traces**: `~/.ai/traces/pid<pid>-sess<session-id>.perfetto.json`

**不是** `~/.aiclaw/` 目录！

## 工作流程

### 步骤 1：选择多个 session（批处理）

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

### 步骤 2：逐个分析 session

对每个 session：

```bash
# 读取对话内容
tail -100 "$SESSION_DIR/messages.jsonl"
```

**关注点**：
- User 的意图是什么？
- Agent 的理解是否正确？
- 选择了什么工具？
- 执行结果如何？
- 是否有失败/重试？

**⚠️ Rate Limit 避免策略**：
- 每个 session 分析完后，等待 10-30 秒
- 如果遇到 rate limit (429)，等待 60 秒后重试
- 不要并发分析多个 session

### 步骤 3：Code Review 分析（逐个）

**像 review 代码一样思考**：

#### 检查 Prompt 设计
```
问题：agent 是否正确理解了用户意图？
- 如果误解：prompt 是否不够清晰？
- 如果理解正确但执行错误：工具描述是否有歧义？
```

#### 检查工具选择
```
问题：agent 选择了什么工具？
- 是否选错了？（比如应该用 read 却用了 bash cat）
- 为什么选错？工具描述不够清楚？
- 是否有更好的工具组合？
```

#### 检查流程设计
```
问题：任务分解是否合理？
- 是否有冗余步骤？
- 执行顺序是否最优？
- 是否可以并行执行？
```

#### 检查错误处理
```
问题：失败时如何处理？
- 重试策略是否合理？
- 是否有 fallback？
- 错误信息是否清晰？
```

### 步骤 4：生成单个 session 报告

每个 session 生成独立报告：

```bash
REPORT=~/.aiclaw/analysis/session-<session-id>.md
```

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

## 统计数据

### 问题分布
| Session | Critical | Medium | Suggestion |
|---------|----------|--------|------------|
| session-1 | 2 | 3 | 1 |
| session-2 | 1 | 2 | 0 |
| ... | ... | ... | ... |

### 问题类型统计
- 工具选择错误: X 次
- 并发设计问题: Y 次
- 错误处理不当: Z 次
- ...

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

### 步骤 6：更新 checkpoint

```json
{
  "lastSessionId": "<最后一个分析的 session-id>",
  "lastAnalyzedAt": "<ISO 时间>",
  "totalSessions": <累计分析数量>,
  "lastSummaryAt": "<上次汇总时间>"
}
```

**报告格式**（参考 code review comment）：

```markdown
# Session Analysis - Code Review Style

**Session**: <session-id>
**时间**: <timestamp>
**工作目录**: <cwd>

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

## 🟡 Medium Issues

### Issue 2: <具体问题>

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
2. [Medium] 系统提示词添加失败处理策略

### 可选优化
1. [Suggestion] 利用工具并发能力

### 下次关注点
- 检查修复后的效果
- 关注是否有新的反模式
```

## 分析重点

### ✅ 应该关注的

1. **设计问题**：prompt 不清晰、工具描述有歧义
2. **选择错误**：选错工具、执行顺序不合理
3. **反模式**：重复失败、无效循环、过度调用
4. **改进机会**：可以优化的流程、可以复用的模式

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

## 示例：好的分析 vs 差的分析

### ✅ 好的分析

```
问题：agent 选择了 bash cat 而不是 read 工具

根因：tools.md 中 read 工具描述不够清晰

改进：在 tools.md 添加对比说明
```

### ❌ 差的分析

```
统计：工具调用 328 次，重试率 23%

建议：优化调用策略
```

**差异**：好的分析有根因、有证据、有具体改进建议。