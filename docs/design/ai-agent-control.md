# ai 基础设施：Agent 控制 Agent

## 1. 现状

`ai` 已经有完整的 RPC 协议（prompt、steer、abort、follow-up）和 CLI 子命令（serve、send、watch、kill、ls）。但 agent 想控制另一个 agent 时，存在两个断裂点：

### 问题 1：`ai send` 不能发 steer

`ai send` 把所有消息都当 `prompt` 类型发到 socket。RPC server 的 prompt handler 走 slash 命令分发。

- `/abort` → 已注册为 slash 命令 ✅ → `ai send "/abort"` 可用
- `/follow-up xxx` → 已注册为 slash 命令 ✅ → `ai send "/follow-up xxx"` 可用
- `/steer xxx` → **没有注册为 slash 命令** ❌ → `ai send "/steer xxx"` 报 unknown command
- `/model opus`、`/compact` 等 → 已注册 ✅

`/steer` 只能通过发送 `{"type": "steer", "message": "xxx"}` 这种原生 RPC 命令触发，但 `ai send` 不支持指定命令类型。

### 问题 2：`ai watch` 只有 TUI

`ai watch` 默认启动 Bubble Tea TUI 界面。`--since` 参数提供了一次性文件读取模式（machineWatch），输出原始事件行 + `__offset`。但：

- 一次性读取，不是持续流
- agent 需要轮询才能获取新事件
- 没有阻塞等待新事件的能力
- 没有结构化输出（事件是原始 JSONL，格式随内部实现变化）

### 问题 3：ag 是不必要的中间层

当前 agent 间协作通过 `ag` CLI，它封装了 `ai serve` 的启动和 socket 通信。但 ag 引入了：
- task/channel 额外抽象
- 6k+ 行 Go 代码维护负担
- spawn/wait/prompt/steer 是 ag 定义的 API，不是 `ai` 原生能力

如果 `ai` 本身就提供完整的 agent 控制能力，ag 的核心价值就只剩 task DAG 调度——而这正是 PGE 要替代的。

## 2. 为什么改

目标：**agent 用 `ai` 的 CLI 命令控制其他 agent，跟人类操作体验一致。**

```
人类操作 agent 的方式：         agent 操作 agent 的方式（改后）：
ai serve                       ai serve
ai send "消息"                 ai send "消息"
ai send "/steer 换方向"        ai send "/steer 换方向"
ai send "/abort"               ai send "/abort"
ai send "/model opus"          ai send "/model opus"
ai watch                       ai watch --follow --output json
ai kill                        ai kill
```

完全一样，没有额外抽象。

## 3. 关键设计决策

### 决策 1：所有能力通过 slash 命令暴露

**选择：** `ai send` 始终发 prompt，`/steer`、`/abort`、`/follow-up` 等全部注册为 slash 命令。

**不选：** 给 `ai send` 加 `--type steer` 之类的参数。

理由：
- 一个入口，心智模型简单
- 新增能力只需注册 slash handler，不改 `ai send` 代码
- 跟人类操作方式完全一致

### 决策 2：`ai watch` 增加 `--follow` 持续监听模式

**选择：** `ai watch --follow --output json` 持续输出事件流，直到 agent 结束或被 Ctrl+C。

**不选：** 让 agent 轮询 `ai watch --since`。

理由：
- 轮询浪费且有延迟
- `--follow` 是 Unix 惯例（`tail -f`）
- agent 可以作为子进程启动 `ai watch --follow`，读 stdout 获取实时事件

### 决策 3：ag 暂时保留但不作为 PGE 的依赖

ag 不删不废，但 PGE 架构直接基于 `ai` CLI 构建。ag 作为遗留方案保留给不想迁移的场景。

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
    if !streaming {
        app.compactBeforeRequest("pre_request_steer")
    }
    app.stateMu.Lock()
    app.pendingSteer = true
    app.stateMu.Unlock()
    app.ag.Steer(expandedMessage)
    return map[string]any{"status": "steered"}, nil
})
```

注意：这段逻辑跟 `handleSteer`（rpc_app.go:677）基本一样。理想情况应该复用，但 `handleSteer` 从 RPCCommand 取 message 的方式跟 slash handler 从 args 取不一样。可以做一个小重构：

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

然后 `handleSteer` 和 slash handler 都调 `steerAgent`。

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
4. 跟 `handleSteer` RPC 命令行为一致（skill expansion、pending 检查等）

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