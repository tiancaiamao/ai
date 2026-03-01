# LLM Context: LLM 自主上下文管理

## Feature Overview

将 Agent 的上下文管理权从规则驱动转变为 LLM 自主管理。LLM 通过 tool 操作 LLM Context 文件，自己决定记住什么、丢弃什么，实现从 Level 0.5 到 Level 3 的跨越。

### 背景演进模型

```
Level 0: 线性对话 + 压缩（大多数简单 agent）
         - Token 超限触发压缩
         - 工具输出 summary
         - 保留最近 k 轮
         
Level 1: Plan 结构（焦点控制）
         - 解决 attention drift
         - 但不处理历史压缩
         
Level 2: Working State Object（显式任务状态）
         - 状态从对话中抽离
         - 对话可以被丢弃
         
Level 3: Memory Policy Learning（LLM 自主决策）
         - LLM 决定什么时候压缩
         - LLM 决定记住什么/丢弃什么
         - LLM 自己管理上下文
```

**当前系统：Level 0.5**（有 compaction 但没有显式状态）
**目标：Level 3**（LLM 完全自主管理 memory policy）

### 核心理念

```
传统 Agent：规则决定压缩 → LLM 被动接受
本设计：LLM 自主决策 → LLM 主动管理 → 闭环控制

成熟系统：用结构化状态替代历史对话
传统系统：用历史对话承载结构
```

### 关键变化

| 维度 | 以前 | 现在 |
|------|------|------|
| 上下文来源 | chat history summary | llm context 文件 |
| 压缩策略 | Agent 规则触发（75% token） | LLM 自己决定 |
| 历史管理 | 增量 compaction | LLM 自己维护 memory.md |
| Tool 输出 | 自动截断/summary | LLM 自己决定保留什么 |
| 状态存储 | 隐式在 Messages 中 | 显式在 llm-context/ |

---

## User Stories

### P1 - 核心功能（MVP）

**US-1: Session 目录结构改造**
> As System, I want session 从单文件变为目录结构，以便容纳 llm context。

**Acceptance Criteria:**
- 新 session 创建时，创建目录结构：
  ```
  ~/.ai/sessions/--<cwd>--/
  └── <session-id>/
      ├── messages.jsonl
      ├── meta.json
      └── llm-context/
          ├── overview.md
          └── detail/
  ```
- Session 切换时正确识别目录结构

**US-2: LLM Context 自动注入**
> As LLM, I want 我的 llm context 自动加载到 prompt 中，以便我看到自己上次写的内容。

**Acceptance Criteria:**
- 每次请求时读取 `llm-context/overview.md` 注入 prompt
- 注入位置：system prompt 之后，recent messages 之前
- 文件不存在时创建并注入默认模板
- 不破坏现有 prompt cache 机制
- history strip 采用两阶段策略：
  - `overview.md` 尚未被有效维护时，保留 history 注入（避免上下文丢失）
  - 确认 `overview.md` 已有有效内容后，再切换到只保留 active turn

**US-3: 上下文元信息自动注入**
> As LLM, I want 每次请求时自动看到上下文状态（token 使用量等），以便我决定何时压缩/更新 memory。

**Acceptance Criteria:**
- 每次请求自动注入 `context_meta` 到消息末尾
- 格式：
  ```json
  <context_meta>
  {
    "tokens_used": 45000,
    "tokens_max": 128000,
    "tokens_percent": 35,
    "messages_in_history": 42,
    "llm_context_size": 2400
  }
  
  💡 Remember to update your llm-context/overview.md to track progress and compress context if needed.
  </context_meta>
  ```
- 注入位置：消息末尾（不破坏 prompt cache）
- 不消耗额外对话轮次

**US-4: 自主压缩策略（Level 3）**
> As LLM, I want 完全自主管理所有上下文压缩，包括对话历史和工具输出。

**设计理念：**
- **移除所有自动压缩触发**（compact 75%、tool summary cutoff）
- LLM 通过 `compact_history` tool 完全控制压缩
- 压缩策略写在 System Prompt 中作为指南
- 75% compaction 保留作为**最后兜底**（理想情况下不应触发）

**压缩目标：**
1. **对话历史**：总结已完成任务、归档详细讨论
2. **工具输出**：工具结果往往很大，几轮后价值降低，需要压缩

**压缩策略指南（LLM 参考）：**
```
Token 使用量    建议操作
─────────────────────────────────────────
< 20%          正常工作，无需压缩
20% - 40%      轻度压缩：总结已完成任务，移除冗余工具输出
40% - 60%      中度压缩：归档详细讨论到 detail/，压缩旧工具结果
60% - 75%      重度压缩：只保留关键决策和当前任务
> 75%          系统会自动触发兜底压缩（你应该在此之前主动压缩）

保留规则：
- 最近 3-5 条对话记录始终保留
- 当前任务状态必须保留
- 关键决策必须保留
- 最近的工具输出可以保留（视情况）
```

**Acceptance Criteria:**
- [ ] 提供 `compact_history` tool（支持对话历史和工具输出压缩）
- [ ] 移除自动 tool summary 触发（ToolCallCutoff 机制）
- [ ] System Prompt 包含完整压缩策略指南
- [ ] LLM 能够自主决定压缩时机和目标
- [ ] 75% compaction 兜底机制保留有效

**US-4: LLM 自主更新 LLM Context**
> As LLM, I want 用 write tool 更新我的 llm context，以便我控制自己看到的内容。

**Acceptance Criteria:**
- LLM 可以用现有 `write` tool 更新 `llm-context/overview.md`
- LLM 可以用现有 `write` tool 在 `llm-context/detail/` 创建文件
- 更新立即生效（下次请求可见）
- System prompt 中说明 LLM Context 的能力和责任

**US-5: 新 Session 空模板**
> As LLM, I want 新 session 时看到空模板，以便我知道如何填写 llm context。

**Acceptance Criteria:**
- 新 session 创建时，`overview.md` 包含引导模板
- 模板包含结构化槽位和注释说明

### P2 - 增强功能

**US-6: 详细内容分层存储**
> As LLM, I want 将详细内容存入 `detail/` 目录，以便保持 overview.md 精简。

**Acceptance Criteria:**
- LLM 可以 `write llm-context/detail/xxx.md` 保存详情
- LLM 可以 `read llm-context/detail/xxx.md` 读取详情
- detail/ 内容不自动注入 prompt

**US-7: 历史归档**
> As LLM, I want 将完成的任务归档，以便清理当前工作记忆。

**Acceptance Criteria:**
- LLM 可以创建 `llm-context/archive/` 目录
- archive 内容不注入 prompt

### P3 - 未来扩展

**US-8: 详细归档策略**
> As LLM, I want 更智能的归档建议，以便系统提示我何时应该归档。

**Acceptance Criteria:**
- 当 context_meta 显示 token > 50% 时，提醒更强烈
- 可选：自动建议归档哪些内容

---

## Technical Context

### 现有架构

```
~/.ai/sessions/--<cwd>--/
├── <session-id>.jsonl        # 消息历史（单文件）
├── <session-id>.meta.json    # 元数据
└── ...

pkg/session/
├── session.go                # Session 结构
├── manager.go                # Session 管理
└── lazy.go                   # 延迟加载

pkg/prompt/builder.go         # Prompt 构建
pkg/compact/compact.go        # 压缩逻辑（保留，默认启用）
```

### 目标架构

```
~/.ai/sessions/--<cwd>--/
└── <session-id>/             # ✨ Session 变成目录
    ├── messages.jsonl        # 消息历史（保留，不再注入 prompt）
    ├── meta.json             # 元数据
    └── llm-context/       # ✨ 新增
        ├── overview.md       # L1: 注入 prompt
        └── detail/           # L2: 按需读取
```

### 消息结构变化

```
以前：
system prompt / tools / skills / AGENTS.md / history summary / recent messages

现在：
system prompt / tools / skills / AGENTS.md / llm context / (history or active-turn messages)
```

---

## LLM Context 模板

### overview.md 模板

```markdown
# LLM Context

<!--
这是你的外部记忆。每次请求时，这个文件的内容会被加载到你的 prompt 中。
你自己决定记住什么、丢弃什么。

你的 context 是有限的。你需要自己决定：
- 什么时候压缩
- 记住什么信息
- 丢弃什么历史

使用 write tool 更新此文件：llm-context/overview.md
下次请求时，你会看到自己写的内容。

这是 YOUR memory。你控制你看到的内容。
-->

## 当前任务
<!-- 用户让你做什么？当前进度？ -->


## 关键决策
<!-- 你做过什么重要决定？为什么？ -->


## 已知信息
<!-- 项目结构、技术栈、关键文件等 -->


## 待解决
<!-- 待处理的问题或阻塞项 -->


## 最近操作
<!-- 最近几步做了什么（可选，用于快速回顾） -->


<!--
提示：
- 查看 context_meta（每次自动注入）了解 token 使用情况
- 需要保存详细内容时，写入 llm-context/detail/ 目录
- 文件路径相对于 session 目录
-->
```

---

## Success Criteria

1. **功能完整性**
   - [ ] Session 目录结构正确创建
   - [ ] llm-context/overview.md 自动注入到 prompt
   - [ ] 仅在 llm context 已确认维护后才 strip history
   - [ ] context_meta 自动注入到消息末尾
   - [ ] LLM 可以用 write/read 操作 memory 文件
   - [ ] 新 session 创建空模板

2. **性能**
   - [ ] 注入 overview.md 不增加明显延迟 (< 10ms)
   - [ ] 文件读写效率合理（overview.md < 5KB）

3. **LLM 可理解性**
   - [ ] System prompt 清晰说明 LLM Context 能力
   - [ ] 模板引导 LLM 如何使用
   - [ ] LLM 能够自主更新 memory（需实测验证）

4. **系统稳定性**
   - [ ] 现有 compaction 逻辑保留（默认启用）
   - [ ] 如果 LLM 管理得当，compaction 不应频繁触发

---

## Out of Scope

- L0 (abstract.md) 分层（未来可扩展）
- 自动从 messages.jsonl 生成 llm context（LLM 自己决定）
- 多 session 间的 memory 共享
- Memory 版本控制
- 旧 session 自动迁移
