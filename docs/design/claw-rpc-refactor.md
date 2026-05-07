# Design: Claw → ai RPC 重构

## 目标

消除 claw 和 ai 之间 ~1400 行重复代码。让 claw 成为 `ai --mode rpc` 的薄客户端。

## 现状

```
claw/pkg/adapter/adapter.go (1967行)    cmd/ai/rpc_handlers.go (2220行)
┌──────────────────────────┐           ┌──────────────────────────┐
│ MessageBus 接入           │           │ RPC stdin/stdout         │
│ 多平台消息格式转换         │           │ JSON 协议                │
├──────────────────────────┤           ├──────────────────────────┤
│ agent.NewAgentFromConfig │ ══重复══  │ agent.NewAgentFromConfig │
│ session.LoadSessionLazy  │ ══重复══  │ session.LoadSessionLazy  │
│ help/model/session 命令  │ ══重复══  │ help/model/session 命令  │
│ prompt 构建              │ ══重复══  │ prompt 构建              │
│ compaction 逻辑          │ ══重复══  │ compaction 逻辑          │
│ follow-up/steer          │ ══重复══  │ follow-up/steer          │
│ 事件收集与响应拼接         │ ══重复══  │ 事件发射                 │
│ models.json 加载         │           │                          │
│ clawCompactor            │           │                          │
└──────────────────────────┘           └──────────────────────────┘
```

## 目标架构

```
claw/pkg/adapter/rpc_client.go (~600行)    ai --mode rpc (subprocess)
┌────────────────────────────┐             ┌──────────────────────────┐
│ MessageBus 接入             │             │ agent.Agent              │
│ 多平台消息格式转换           │             │ session.Session          │
│ subprocess 生命周期管理     │──JSON RPC── │ 所有命令处理             │
│ sessionKey → ai 进程映射    │             │ prompt / compaction     │
│ 事件流 → 文本响应拼接        │             │ 事件发射                 │
│ /cron, /skills reload 本地  │             │ session 持久化           │
│ voice 转写                  │             │ tools (内置)             │
└────────────────────────────┘             └──────────────────────────┘
```

## 设计决策记录

### Q1: 架构选择 → subprocess RPC
不共享库。ai 是 agent 逻辑的唯一来源。claw 作为 RPC 客户端调用 `ai --mode rpc` 子进程。

### Q2: 子进程生命周期 → lazy start, never close
单用户场景，不需要复杂生命周期管理。首次消息时启动，进程一直活着。不主动关闭。

### Q3: 通信方式 → 复用现有 stdin/stdout RPC
不修改 ai 侧协议。客户端通过 JSON `type` 字段区分 response vs event。

### Q4: slash command 路由 → claw 白名单 + 转发其余
claw 本地处理：`/cron`、`/skills reload`。
其他所有 `/xxx` 作为普通 prompt 转发给 ai（ai 侧处理 `/help`、`/model`、`/session` 等）。

### Q5: 命令注册 → binary-only
`pkg/`（库）注册零命令。只有 `cmd/ai/` 和 `cmd/aiclaw/` 各自注册自己的命令。每个 binary 有自己的 `/help` 实现。

### Q6: help 格式 → 各 binary 自定义
ai 的 `/help` 输出 JSON，claw 的 `/help` 输出文本。不共享 description。

### Q7: tools → claw 不传 tools
claw 使用 ai 内置的 tools（ReadTool、BashTool、WriteTool、GrepTool、EditTool）。ai 子进程默认就有。claw 不需要通过 RPC 传递 tools。

### Q8: 消息拦截 → voice + 本地命令
voice 转写在 claw 层完成后作为文本发送。`/cron`、`/skills reload` 在 claw 本地处理。其他全部原样转发。

### Q9: 事件收集 → buffer to agent_end
claw 缓冲所有 `turn_end` 文本，在收到 `agent_end` 时通过 `bus.PublishOutbound` 发送单次响应。增量更新留到未来。

### Q10: 配置传递 → 不传 model，用 `/model` 切换
ai 子进程启动时用自己的默认 config 中的 model。claw 不需要通过 CLI 参数指定 model。运行时通过 `/model <id>` RPC 命令切换。

### Q11: models.json → 删除，统一用 ai 配置
claw 不再维护独立的 `~/.aiclaw/models.json`。所有模型配置在 `~/.ai/` 下。单一配置源。

### Q12: system prompt → 基础身份 + skills 不嵌入
system prompt 只包含基础身份（"You are claw 🦀" + 规则），不嵌入 skills 列表。
启动时通过 `--system-prompt @<tmpfile>` 传给 ai 子进程（claw 写临时文件）。

### Q12b: skills 注入 → 注册为 tools
skills 本质是可调用的能力。binary 层把 skill 注册为 tool description，agent 通过 tool 列表发现。`pkg/` 不知道 skills 的存在。

### Q13: voice 转写 → 在 claw 层做
语音转写是平台适配逻辑（飞书语音要调飞书 API 下载）。claw 转写为文本后作为普通 prompt 发给 ai。

### Q14: cron → 无特殊设计，走普通消息路径
cron 触发的消息跟普通消息走同一条路径：`conn.Prompt(content)`。cron 不需要返回给用户。

### Q15: compaction → claw 不关心
删除 `clawCompactor`。compaction 在 ai 子进程内部自动发生。claw 完全不需要知道。

### Q16: 多 session → 每个 sessionKey 一个 ai 子进程
一个 sessionKey 对应一个 ai 子进程。`ConnManager` 管理 `map[string]*RPCConn`。无法共享单个进程处理并发请求。

### Q17: 错误处理 → 发送失败时重启重试
不主动健康检查。发送 prompt 时发现子进程死掉 → 重启 → 重试一次。cron 失败就等下次触发。

### Q18: 迁移分 4 个 phase
详见下方"迁移步骤"。

## 核心设计

### 1. RPCConn — 单个子进程连接

```go
type RPCConn struct {
    sessionKey string
    cmd        *exec.Cmd
    stdin      io.WriteCloser
    scanner    *bufio.Scanner
    pending    map[string]chan *RPCResponse  // id → response channel
    events     chan AgentEvent
    mu         sync.Mutex
    done       chan struct{}
}

func StartRPC(sessionKey, sessionsDir, systemPromptFile string) (*RPCConn, error) {
    // ai --mode rpc --session <sessionsDir>/<safeKey> --system-prompt @<systemPromptFile>
}

func (c *RPCConn) Prompt(ctx context.Context, message string) (string, error) {
    // 1. 发送 {"id":"req-1","type":"prompt","data":{"message":"hello"}}
    // 2. 等待 response（成功/失败）
    // 3. 收集事件流直到 agent_end
    // 4. 拼接 turn_end 文本 → 返回
}

func (c *RPCConn) Close() error {
    // 发送 {"type":"quit"}
    // 等 done channel
    // 超时后 kill
}
```

### 2. ConnManager — 多连接池

```go
type ConnManager struct {
    conns       map[string]*RPCConn  // sessionKey → conn
    mu          sync.RWMutex
    sessionsDir string
    sysprompt   string               // system prompt 内容
}

func (m *ConnManager) Prompt(ctx context.Context, sessionKey, message string) (string, error) {
    conn := m.getOrCreateConn(sessionKey)
    resp, err := conn.Prompt(ctx, message)
    if err != nil {
        // 重启重试一次
        m.restartConn(sessionKey)
        return conn.Prompt(ctx, message)
    }
    return resp, nil
}
```

### 3. 消息处理流程

```
用户消息 → claw processMessage()
  ├─ 是语音? → claw 转写为文本 → 继续下面
  ├─ 是 /cron? → 本地处理
  ├─ 是 /skills reload? → 本地处理
  └─ 其他 → connManager.Prompt(sessionKey, message)
       ↓
  收集 turn_end 文本 → agent_end 时返回
       ↓
  bus.PublishOutbound(response)
```

### 4. Session 持久化

claw 不管理 session 持久化。每个 ai 子进程通过 `--session <path>` 自带：
- 存储：`~/.aiclaw/sessions/<safeKey>/`
- ai 负责 load/save
- 子进程重启后自动从磁盘恢复

### 5. 命令路由

| 命令 | 处理方 | 原因 |
|------|--------|------|
| `/cron` | claw 本地 | claw 独有功能 |
| `/skills reload` | claw 本地 | claw 的 skill 加载 |
| `/help` | ai (RPC) | ai 有自己的命令列表 |
| `/model` | ai (RPC) | ai 管理模型配置 |
| `/set` | ai (RPC) | ai 管理配置状态 |
| `/session` | ai (RPC) | ai 管理 session |
| 其他 `/xxx` | ai (RPC) | ai 是 agent 逻辑唯一来源 |

## 迁移步骤

### Phase 1: RPC Client 基础设施

1. **新建 `claw/pkg/adapter/rpc_client.go`**
   - `RPCConn` — 单个子进程连接管理
   - `ConnManager` — 多连接池
   - stdin/stdout 读写循环
   - 请求-响应匹配 + 事件分发
   - 启动参数：`--mode rpc --session <path> --system-prompt @<tmpfile>`

2. **新建 `claw/pkg/adapter/rpc_client_test.go`**
   - 用 mock stdin/stdout 测试协议解析

### Phase 2: 替换 AgentLoop 核心

3. **重写 `AgentLoop.processMessage`**
   - 不再直接调 `agent.Agent`
   - 通过 `ConnManager.Prompt()` 发 RPC

4. **删除 claw 侧不再需要的代码**
   - 删除 `clawCompactor`
   - 删除 `models.json` 加载逻辑
   - 删除 `resolveModel` 等配置方法
   - system prompt 只保留基础身份（不含 skills 列表）
   - voice 转写在 claw 层完成后传文本给 RPC

5. **替换 session 创建**
   - `createSession` → `connManager.getOrCreateConn(sessionKey)`
   - 不再直接创建 `agent.Agent` / `session.Session`

### Phase 3: 命令路由

6. **实现 claw 本地命令**
   - 只注册 `/cron`、`/skills reload`、`/help`
   - 其他 `/xxx` 转发为 RPC prompt

7. **各自注册 `/help`**
   - claw 的 `/help` 输出文本格式
   - ai 的 `/help` 输出 JSON 格式

### Phase 4: 清理

8. **删除 claw 对 agent/session/compact 包的直接依赖**
   - `adapter.go` 不再 import `agent`, `session`, `compact`
   - `go.mod` 精简

9. **验证**
   - claw 所有 channel 正常工作
   - cron 触发正常
   - session 持久化正常
   - 错误恢复（杀掉 ai 子进程后自动重连）

## 预期效果

| 文件 | 当前行数 | 目标行数 | 变化 |
|------|---------|---------|------|
| `adapter.go` | 1967 | ~600 | -1367 |
| `rpc_client.go`（新） | 0 | ~400 | +400 |
| **净效果** | 1967 | ~1000 | **-967** |

claw 从直接依赖 `agent`+`session`+`compact` 变为只依赖 `pkg/rpc/types.go` 的协议定义。

## 风险

| 风险 | 缓解 |
|------|------|
| 子进程启动延迟 | lazy start，首次消息约 100-200ms 启动开销，之后常驻 |
| 子进程崩溃 | 发送时检测 → 重启 → 重试一次，session 从磁盘恢复 |
| system prompt 更新 | 写新临时文件，重启子进程 |
| 调试复杂度 | 每个 ai 子进程有独立 log |
| backward compat | 分 4 phase 迁移，先让两种模式共存 |