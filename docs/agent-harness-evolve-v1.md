# Design: Agent Kernel/Shell Separation + Harness Evolution Infrastructure

## Background

灵感来自论文 *"Agentic Harness Engineering"* (Fudan, 2025)。核心发现：

- Coding agent 的 **harness**（prompt / memory / middleware 等外围组件）对性能的影响不亚于底座模型
- 在 Terminal-Bench 2.0 上，10 轮自动演化将 pass@1 从 69.7% → 77.0%（+7.3 百分点）
- 消融实验：memory +5.6pp，tool 增强 +3.3pp，middleware +2.2pp，system prompt 单独反而 -2.3pp
- middleware 的价值来自**结构性防御**（管线拦截），而非 prompt 劝告

**本文档目标**：借鉴 AHE 的思路，将 agent 的稳定内核与动态 harness 分离，通过实验迭代提升 agent 能力。先手动迭代积累直觉，后续再自动化。

---

## 1. 现状

### 包结构概览

```
pkg/agent/       ← 执行循环 + LoopConfig + Agent 公开 API
pkg/context/     ← AgentContext（运行时世界状态）、AgentMessage、ContentBlock、Tool 接口、Compactor
pkg/compact/     ← 上下文压缩（major + mini compaction）
pkg/tools/       ← 工具实现（bash, read, edit, write, grep）+ Registry
pkg/prompt/      ← Builder 模式构建 system prompt（go:embed prompt.md + 占位符替换）
pkg/session/     ← 消息持久化（messages.jsonl）
pkg/config/      ← YAML 配置 + ToLoopConfig()
pkg/llm/         ← 多 provider LLM 客户端
pkg/skill/       ← SKILL.md 加载器
cmd/ai/          ← RPC 层（组装 agent、处理用户消息）
```

### 执行循环（runInnerLoop in loop.go）

```
for {
    ① performCompaction()       — 按阈值压缩上下文
    ② streamAssistantResponse() — 调 LLM
    ③ processToolCalls()        — 并发执行工具（在 loop_state.go 中）
    ④ 追加结果到 messages       — 无拦截点
    ⑤ 回到 ①
}
```

**没有 hook / middleware 扩展点。** 唯一的干预机制是 `tool_guard.go`（检测连续相同工具调用，防无限循环），hard-coded 在 `processToolCalls()` 中。

### 关键类型

- `AgentMessage`：消息，`Role` 字段取 "user" | "assistant" | "tool_result" | "framework"（"framework" = 系统注入，LLM 可见）
- `ContentBlock`：工具输出的基本单元（discriminated union），文本内容通过 `NewTextContent(text)` 创建
- `Tool` 接口：`Name()` / `Description()` / `Parameters()` / `Execute()`
- `Compactor` 接口：`ShouldCompact()` / `Compact()`
- `LoopConfig`：循环的全部配置（Model, Tools, Compactors, MaxTurns, ToolOutput limits 等）

### Prompt 构建

`prompt.Builder` 支持 `SetTemplate(t string)` 覆盖默认模板。`Build()` 返回完整 prompt 字符串。当前通过 `go:embed` 编译进二进制。

---

## 2. 为什么改

| 痛点 | 具体表现 | AHE 论文佐证 |
|------|---------|-------------|
| **Prompt/Memory 无法外部修改** | 改 `prompt.md` → 重编译 → 重跑 benchmark。evolve agent 无法自行修改。 | AHE 证明大部分改进来自 prompt/memory/middleware 的动态组合 |
| **无结构性行为干预** | 只能改 prompt 劝告 agent "别做 X"，但效果不稳定 | system prompt 单独 -2.3pp；middleware +2.2pp 来自管线拦截 |
| **无 harness 变更闭环** | 改完配置后无 "改前 vs 改后" 自动对比 | benchmark runner 存在但 case 多（37-40 个）、成本高、部分 flaky |
| **内核与外壳耦合** | LoopConfig 混杂了 RPC/LLM/工具/压缩等全部层的参数 | 无法独立测试内核或独立演化 harness |

---

## 3. 设计决策

### 决策 1：三层分离

```
Application (cmd/ai/)          — 变化频繁：用户交互、部署
Harness     (agent/ + middlewares/) — 变化中等：实验迭代、evolve
Kernel      (pkg/agent/)       — 变化缓慢：稳定后很少改
```

依赖方向：App → Harness → Kernel，单向不循环。

### 决策 2：LoopConfig 不拆分，只加 Hooks

当前 LoopConfig 被多处使用，拆分是高风险重构。v0 只加一个 `Hooks *HookRegistry` 字段。v1 再拆分 KernelConfig / HarnessConfig。

### 决策 3：Hook 用 Go func，不用 plugin

手动迭代阶段，编译 30 秒不是瓶颈。Go plugin 仅支持 Linux。后续需要自动 evolve 时再引入 plugin。

### 决策 4：Memory 追加到 system prompt，不走 BeforeModelHook

System prompt 是 LLM 最稳定的上下文（不会被 compaction 丢弃）。BeforeModelHook 每个 LLM 调用前都触发（含 compaction），用 hook 注入 memory 需要额外防重复逻辑。直接拼接更简单可靠。

### 决策 5：agentconfig / middlewares 独立包

`pkg/agent`（kernel）**不导入** `pkg/agentconfig` 和 `pkg/middlewares`。依赖方向全部从外向内。

### 决策 6：ToolOutputConfig 推到 v1

当前 `tool_output.go` 的 10000 字符硬编码不在 agent.yaml 中配置。v0 聚焦 hook + prompt 文件化。

### 决策 7：context 包不改

AgentContext 被几乎所有包依赖，重构风险过高。

---

## 4. 方案

### 4.1 Harness 配置（agent/ 目录）

```
agent/
├── agent.yaml              ← 配置入口
├── system_prompt.md         ← 从二进制中解放的 prompt
├── memory.md                ← 持久经验（追加到 system prompt 末尾）
└── benchmarks/
    └── evolve-manifest.json ← 精选 benchmark case 列表
```

**agent.yaml 格式**：
```yaml
version: 1
system_prompt: ./system_prompt.md
memory: ./memory.md
middlewares:
  - name: destructive_command_guard
    enabled: true
    params: { ... }
```

**加载规则**：
- `version`：当前只接受 `1`，其他值报错
- 路径：相对路径相对于 `agent.yaml` 所在目录解析；绝对路径直接使用
- `memory`：可选字段，文件不存在时静默跳过
- `system_prompt`：必选字段，文件不存在时报错退出

**启动参数**：`ai --agent-config agent/agent.yaml`。不加时行为与现在完全一致（零迁移成本）。

**System prompt 构建**：
- 有 agentconfig → `ResolveSystemPrompt()` 读文件 + 拼接 memory，跳过 Builder
- 无 agentconfig → 走现有 Builder.Build() 流程

### 4.2 Hook 机制

**3 个回调点**：

| Hook | 触发时机 | 用途 | 语义 |
|------|---------|------|------|
| `BeforeModel` | 每次 LLM 调用前（含 compaction 触发的） | 注入动态提醒 | 返回的 messages（Role = "framework"）追加到对话末尾 |
| `AfterTool` | 每个工具执行后 | 修改工具输出 / 注入警告 | 返回的 ContentBlock **替换**原始输出；保留原始需先 copy 再 append |
| `AfterAgent` | 循环正常结束后 | 后处理 / 日志 / 状态清理 | 无返回值 |

**执行模型**：
- `BeforeModel`：**fan-out** — 每个 hook 独立接收相同的 messages，所有输出合并追加
- `AfterTool`：**链式** — 前一个 hook 的输出是后一个 hook 的输入
- `AfterAgent`：顺序执行，无数据传递

**约束**：
- Hook 必须纯内存操作（不能调 LLM / IO / 网络），这是文档约定
- Hook 返回 error → `slog.Warn`（含 hook index + tool name 等上下文），不中断循环
- `HookRegistry == nil` 时所有方法零开销返回

**集成到 loop**：
- `BeforeModel` + `AfterAgent` 的调用点在 `loop.go` 的 `runInnerLoop()` 中
- `AfterTool` 的调用点在 `loop_state.go` 的 `processToolCalls()` 中
- `processToolCalls` 已持有 `*LoopConfig`（通过 loop state struct），可直接访问 `config.Hooks`
- `hookCtx`（`&HookContext{Turn: state.turnCount}`）由 loop state 构建，在 processToolCalls 内可访问

**Kernel 不感知 harness**：`pkg/agent/hooks.go` 定义纯接口，不导入 agentconfig / middlewares。

### 4.3 Middleware

**注册表**：`pkg/middlewares/registry.go`，`Lookup(name) → Factory`，`Register(name, Factory)` 供外部包扩展。

**v0 内置**：`destructive_command_guard`（AfterTool hook）— 检测 `bash` 工具中包含破坏性命令（`rm -rf`, `kill -9` 等），在输出末尾追加警告文本（保留原始输出）。

**v0 限制**：注册表只支持 `AfterToolHook` 类型的 factory。`BeforeModelHook` / `AfterAgentHook` 类型的 middleware 推到 v1。

### 4.4 现有 tool_guard.go 处理

保持不变。tool_guard 在工具执行**之前**运行（决定是否阻止），AfterTool hook 在工具执行**之后**运行（决定如何修改输出）。两者互补。

### 4.5 Benchmark 精选

**manifest 格式**（`evolve-manifest.json`）：
```json
{
  "version": 1,
  "tasks": [
    { "name": "python-sieve", "test_cmd": "python3 test.py", "timeout_seconds": 300, "working_dir": "..." }
  ]
}
```

**迭代结果格式**（`bench-compare` 的输入）：
```json
{
  "iteration": "iter1", "timestamp": "...", "manifest": "...",
  "results": [
    { "name": "python-sieve", "status": "pass|fail|timeout", "duration_seconds": 42.3 }
  ]
}
```

**Runner 改动**：新增 `manifest.go`（按 manifest 过滤 task）+ `compare.go`（对比两个结果 JSON，输出 flipped / regressed / unchanged + pass rate 变化）。

---

## 5. 验收场景

### P0: Hook 机制

1. `LoopConfig.Hooks == nil` → 行为与现在完全一致
2. 注册 AfterToolHook → 每次 `bash` 执行后被调用，`toolName="bash"`，`args` 含 `command`
3. AfterToolHook 返回修改后的 ContentBlock → 修改后内容（非原始）追加到 messages
4. 注册两个 AfterToolHook（A, B）→ A 的返回值作为 B 的输入（链式）
5. BeforeModelHook 返回的 messages（Role="framework"）→ 追加到 RecentMessages，在下次 LLM 调用中可见
6. BeforeModelHook 在**每个 LLM 调用前**触发（含 compaction 触发的额外调用），不是每 turn 一次
7. AfterAgentHook 在 AgentEndEvent 之前被调用
8. Hook 返回 error → slog.Warn（含 hook index + tool name），不中断循环，后续 hook 继续执行

### P0: agent.yaml 配置加载

1. `ai --agent-config agent/agent.yaml` → system prompt 来自文件
2. 不带 `--agent-config` → 行为完全不变
3. `system_prompt` 指向不存在文件 → 启动报错退出
4. `memory` 为空 → 正常启动，不追加内容
5. `memory` 指向有内容文件 → memory 追加到 system prompt 末尾
6. `memory` 指向不存在文件 → 正常启动（可选字段，静默跳过）
7. 路径为相对路径 → 相对于 agent.yaml 所在目录解析
8. `version: 2` → 报错 "unsupported agent config version"
9. middleware name 不在注册表中 → `BuildHooks()` 静默跳过，不 panic

### P0: DestructiveCommandGuard

1. `bash` command 含 `rm -rf` → 输出末尾追加警告，**原始输出保留**
2. `bash` command 是 `ls -la` → 输出不变
3. 非 `bash` 工具 → 直接返回原始 result
4. `enabled: false` → 不注册，零效果
5. 自定义 `protected_patterns` → 使用自定义列表

### P0: Benchmark 精选

1. `make bench-run MANIFEST=agent/benchmarks/evolve-manifest.json` → 只跑精选 case
2. 精选集 15-20 个 task
3. 每个 task 连续 3 次 pass/fail 状态一致
4. 单 task < 5 分钟，总运行 < 60 分钟

### P1: Benchmark 对比

1. `make bench-compare` → 输出 flipped / regressed / unchanged + pass rate 变化（如 +5.1pp）

---

## 6. 边界条件

- **Hook 性能**：纯内存操作约定。违反只影响性能不影响正确性。
- **BeforeModel 频率**：每个 LLM 调用前都触发。只需注入一次的 hook 需内部管理状态（闭包 bool 标志）。`HookContext` 每轮新建，不能存跨调用状态。
- **Token 超限**：BeforeModelHook 注入的消息导致超限 → 走正常 compaction 处理。
- **配置加载**：不支持 `${env.X}` / `~` / symlinks。memory 可选（不存在静默跳过），system_prompt 必选（不存在报错）。
- **Benchmark 稳定性**：agent temperature > 0 导致非确定性。v0 用 k=1，排除 flaky case。后续可 pass@k。

---

## 7. 涉及的文件

| 操作 | 文件 | 说明 |
|------|------|------|
| 新建 | `pkg/agent/hooks.go` | Hook 接口 + HookRegistry（含 Run* 方法） |
| 新建 | `pkg/agent/loop_hooks.go` | extractToolName/Args 辅助函数 |
| 新建 | `pkg/agentconfig/config.go` | YAML 加载 + system prompt + memory 拼接 |
| 新建 | `pkg/agentconfig/hooks.go` | 配置 → HookRegistry 桥接 |
| 新建 | `pkg/middlewares/destructive_guard.go` | AfterTool hook |
| 新建 | `pkg/middlewares/registry.go` | Factory 注册表 + Lookup + Register |
| 新建 | `agent/agent.yaml` | 默认 harness 配置 |
| 新建 | `agent/system_prompt.md` | 从 pkg/prompt/prompt.md 拷贝 |
| 新建 | `agent/memory.md` | 空 memory 模板 |
| 新建 | `agent/benchmarks/evolve-manifest.json` | 精选 case manifest |
| 新建 | `benchmarks/manifest.go` | manifest 解析 + task 过滤 |
| 新建 | `benchmarks/compare.go` | 结果对比 |
| 修改 | `pkg/agent/loop.go` | LoopConfig 加 Hooks 字段；runInnerLoop 加 BeforeModel + AfterAgent 调用点 |
| 修改 | `pkg/agent/loop_state.go` | processToolCalls 加 AfterTool 调用点 |
| 修改 | `cmd/ai/main.go` | --agent-config flag |
| 修改 | `cmd/ai/rpc_setup.go` | agentconfig.Load 集成 |
| 修改 | `cmd/ai/rpc_app.go` | buildSystemPrompt 支持 agentconfig 覆盖 |
| 修改 | `cmd/ai/rpc_handlers.go` | loopCfg.Hooks 桥接 |

**依赖约束**：`pkg/agent` 不导入 `pkg/agentconfig` / `pkg/middlewares`。

---

## 8. 依赖方向

```
cmd/ai → pkg/agentconfig → pkg/middlewares → pkg/agent → pkg/context
                                        ↗              ↘
cmd/ai ─────────────────────────────→ pkg/agent → pkg/llm, pkg/prompt
```

---

## Non-Goals（v0 不做）

- LoopConfig 拆分 KernelConfig / HarnessConfig（v1）
- 重构 AgentContext
- 拆分 metrics.go / run/conv.go
- 自动 evolve 循环 / Best-of-N / Change manifest
- Go plugin 动态加载
- Tool description 文件化
- ToolOutputConfig（工具输出截断配置文件化）
- BeforeModelHook / AfterAgentHook 类型的 middleware（v1）

---

## 路线图

```
v0 (本版) ─── Hook + agentconfig + benchmark 精选
v1 ────────── LoopConfig 拆分 + BeforeModel/AfterAgent middleware 支持
v2 ────────── 自动 evolve 循环（失败分析 → LLM 改 harness → 重跑 benchmark）
v3 ────────── Best-of-N + Self-attribution
v4 ────────── 跨 benchmark 泛化
```