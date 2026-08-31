# ai 基础设施：Agent 控制 Agent

> Status: ✅ Implemented. All slash commands registered, `ai watch --follow` available.

## 背景

`ai` 有完整的 RPC 协议（prompt、steer、abort、follow-up）和 CLI 子命令（serve、send、watch、kill、ls）。
本文档描述 agent 如何通过 CLI 命令控制其他 agent。

## 设计决策

### 所有能力通过 slash 命令暴露

`ai send` 始终发 prompt，`/steer`、`/abort`、`/follow-up` 等全部注册为 slash 命令。
这样 agent 控制其他 agent 跟人类操作方式完全一致，无需额外抽象层。

## 4. 怎么做

### 4.1 注册 `/steer` 为 slash 命令

**文件：** `cmd/ai/rpc_help_handlers.go`

在 `registerHelpHandlers()` 中添加：

```go
// /steer
app.server.RegisterSlash("steer", "Inject mid-conversation guidance", func(args string) (any, error) {
    message := strings.TrimSpace(args)
    if message == "" {
        return nil, fmt.Errorf("usage: /steer <message>")
    }
    expandedMessage := app.expandSkillCommands(message)
    app.stateMu.Lock()
    mode := app.steeringMode
    pending := app.pendingSteer
    streaming := app.isStreaming
        app.stateMu.Unlock()
    if mode == "one-at-a-time" && pending {
        return nil, fmt.Errorf("steer already pending")
    }
    app.stateMu.Lock()
    app.pendingSteer = true
    app.stateMu.Unlock()
    app.ag.Steer(expandedMessage)
    return map[string]any{"status": "steered"}, nil
})
```

注意：这段逻辑跟 `handleSteerSlash`（rpc_help_handlers.go:14）基本一样。理想情况应该复用，但 `handleSteerSlash` 从 args 取 message 的方式跟其他调用方不一样。可以做一个小重构：

```go
func (app *rpcApp) steerAgent(message string) (any, error) {
    message = strings.TrimSpace(message)
    if message == "" {
        return nil, fmt.Errorf("empty steer message")
    }
    expandedMessage := app.expandSkillCommands(message)
    // ... 共用的状态检查和 Steer 调用 ...
    app.ag.Steer(expandedMessage)
    return nil, nil
}
```

然后 `handleSteerSlash` 和其他调用方都调 `steerAgent`。

### 4.2 `ai watch --follow` 持续监听模式

**文件：** `cmd/ai/watch.go`

新增 `--follow` flag：

```
ai watch --follow              # 持续输出事件（默认格式：每行一个 JSON 事件）
ai watch --follow --output json # 同上，显式 json
ai watch --since 0             # 一次性读取（现有行为不变）
ai watch                       # TUI（现有行为不变）
```

实现思路：

```
--follow 模式:
1. 读 events 文件从 offset 0 开始（或 --since 指定的 offset）
2. 输出到 stdout，每行一个 JSON 事件
3. 文件读到末尾后，poll 间隔 200ms 继续读
4. 检测到 agent 进程退出（读 run.json 的 status 字段）→ 输出完剩余事件后退出
5. 每个事件后跟一个 __offset:NNN 行（跟现有 --since 模式一致）
```

为什么用文件 poll 而不是 socket stream：
- 文件 poll 实现简单，不需要维护 socket 连接
- events 文件本身就是 append-only JSONL，天然适合 tail
- agent 进程退出后文件还在，不会丢失最后几个事件
- 200ms 延迟对 agent 间通信可接受

### 4.3 agent 控制命令速查

改完后，agent 控制 agent 的完整命令集：

```bash
# 启动一个 agent
ai serve --input "初始 prompt" --system-prompt "@prompt.md" --session /path/to/session

# 发送消息（等 agent 空闲时处理）
ai send "继续实现下一个功能"

# 中途给方向（agent 正在工作时）
ai send "/steer 优先处理错误处理逻辑"

# 排队消息（agent 忙完后自动处理）
ai send "/follow-up 做完后跑一下测试"

# 取消当前操作
ai send "/abort"

# 切换模型
ai send "/model opus"

# 压缩上下文
ai send "/compact"

# 查看实时输出
ai watch --follow

# 查看一次性历史输出
ai watch --since 0

# 终止 agent
ai kill

# 列出运行中的 agent
ai ls
```

### 4.4 事件输出格式

`ai watch --follow` 输出的事件需要让 agent 能解析。当前 events 文件里的格式：

```jsonl
{"type":"text_delta","text":"Hello","turn":1,"seq":1}
{"type":"tool_execution_start","toolName":"bash","args":{"command":"ls"},"seq":2}
{"type":"tool_execution_end","toolName":"bash","result":"...","seq":3}
{"type":"agent_end","seq":4}
```

`--follow` 直接输出原始 JSONL 行，agent 用 `jq` 或代码解析即可。关键是 `agent_end` 事件——agent 用它判断 subagent 做完了。

## 5. 验收场景

### P0: `/steer` slash 命令

**Acceptance Scenarios:**
1. `ai serve` 启动后，`ai send "/steer 换个思路"` 成功执行，agent 收到 steer 消息并调整方向
2. agent 不在 streaming 状态时发 `/steer`，自动触发 compact 后再执行
3. 空 steer 消息 `ai send "/steer"` 返回错误提示
4. 跟 `handleSteerSlash` slash 命令行为一致（skill expansion、pending 检查等）

### P0: `ai watch --follow` 持续监听

**Acceptance Scenarios:**
1. `ai watch --follow` 启动后持续输出事件，每行一个 JSON
2. agent 完成后输出 `agent_end` 事件然后 `watch` 进程退出
3. `--since` 和 `--follow` 可以组合使用：先读历史，再持续跟新事件
4. Ctrl+C 能干净退出，不丢已读事件的 __offset
5. agent 被 kill 后，`watch --follow` 检测到进程退出，输出完剩余事件后退出

### P1: agent 间协作端到端验证

**Acceptance Scenarios:**
1. 用 `ai serve` 启动 subagent → `ai send` 发任务 → `ai watch --follow` 看到完成 → `ai kill` 清理
2. subagent 工作中发 `ai send "/steer xxx"` → subagent 实际改变方向
3. subagent 工作中发 `ai send "/abort"` → subagent 停止当前操作

## 6. 边界条件

- **send 超时**：`ai send` 当前有 30s socket 超时。如果 agent 正在忙（LLM 调用中），prompt 和 slash 命令会排队。steer 和 follow-up 不受此限制（它们是异步的）。需要确认 30s 超时是否足够。
- **watch --follow 的退出检测**：怎么判断 agent 进程退出了？检查 run.json 的 status 字段，或者检查 agent 进程 PID 是否存活。文件 poll 的好处是不依赖 socket 连接，坏处是需要额外的退出检测机制。
- **并发 send**：多个 agent 同时给一个 subagent 发 send，socket 是串行的，不会有并发问题。但逻辑上可能有 steer 和 prompt 冲突。
- **session 文件冲突**：多个 `ai serve` 不能共用同一个 session 文件。这已经是现有行为（`ai ls` 按 cwd 区分），不需要额外处理。