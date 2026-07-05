# Design: Role as First-Class Configuration

## 1. 现状

### 当前 `-role` 实现

`-role` 只是 CLI 层的一个快捷方式：

```
ai run --role orchestrator
  → run.go 收到 role="orchestrator"
  → prompt.TemplateForRole("orchestrator") 返回嵌入的 orchestrator.md 内容
  → 作为 --system-prompt 字符串传给 ai rpc
  → ai rpc 层失去 role 信息，只有一段文本
```

三个 role 的 prompt 模板通过 `//go:embed` 编译进二进制：
- `pkg/prompt/prompt.md` — coder（默认）
- `pkg/prompt/orchestrator.md`
- `pkg/prompt/validator.md`

### 当前 skill-stats

全局单一文件 `~/.ai/skill-stats.json`，所有角色共享技能使用排序。

### 当前 `--agent-config`

存在但实际未被使用。支持加载 `agent.yaml` 自定义 system prompt、tools、middlewares。

### 当前 session 格式

`messages.jsonl` 第一行（session header）不记录 role 信息。
`meta.json` 不记录 role 信息。

## 2. 痛点 / 为什么要改

| 痛点 | 表现 |
|------|------|
| **Role 不可配置** | 加新 role 必须改 Go 代码 + 加 .md 文件 + 重新编译 |
| **Skill-stats 全局污染** | orchestrator 频繁使用的技能列表污染了 coder 的排序 |
| **工具集混用** | orchestrator(只读+delegation) 和 coder(全工具) 用同一套工具注册 |
| **Role 信息不持久** | session 不记录 role，resume 后丢失上下文 |
| **`--agent-config` 和 `--role` 两套机制** | 语义重叠，无人使用 |

## 3. 关键设计决策

### 决策 1：Role = 配置目录，不是代码概念

```
候选方案：
A. role 是嵌入代码的字符串 → 加 role 需改代码
B. role 是 ~/.ai/roles/<name>/ 目录 → 加 role = 新建目录

选择 B。
理由：orchestrator/validator 只是开始，用户可能需要按场景自定义更多角色。
不需要为每个角色重新编译。
```

### 决策 2：Skill 全局共享，Stats 按角色隔离

```
候选方案：
A. 角色有独立 skills/ 目录，完全隔离
B. 技能库全局共享，使用统计按角色隔离

选择 B。
理由：用户明确说技能不放在 role 目录。全局技能库 + 按角色统计使用频率，
既保持技能发现的一致性，又让每个角色的渐进式展示独立。
```

### 决策 3：Resume 时用 `--role` 覆盖 session 记录

```
行为规则：
- 命令行指定了 --role → 用指定的 role（记录日志警告不一致）
- 命令行未指定 --role → 自动恢复 session 记录的 role
- 从未记录过 role（旧 session）→ 不做特殊处理

理由：用户可能先无 role 启动干了一些事，然后想起要带 role 重启。
这时应该尊重 --role 的显式指定。忘记指定 role 时则用 session 原始值。
```

### 决策 4：系统 role 的 prompt 文件放在 git 中，role 目录内用 symlink 引用

```
~/.ai/roles/ 目录本身不是 symlink。
单个文件（如 system_prompt.md）可以是 symlink 指向 git 仓库内的源文件。
这样系统角色版本受 git 控制，用户可替换为自定义内容。
```

## 4. 实现方案

### 4.1 目录结构

```
~/.ai/roles/
├── coder/                          # 系统角色：--role coder
│   ├── agent.yaml                  # 配置引用
│   ├── system_prompt.md → .../ai/pkg/prompt/prompt.md  # symlink
│   └── skill-stats.json            # 自动生成
├── orchestrator/                   # 系统角色：--role orchestrator
│   ├── agent.yaml
│   ├── system_prompt.md → .../ai/pkg/prompt/orchestrator.md  # symlink
│   ├── context_management.md       # (可选)
│   └── skill-stats.json
└── validator/                      # 系统角色：--role validator
    ├── agent.yaml
    ├── system_prompt.md → .../ai/pkg/prompt/validator.md  # symlink
    └── skill-stats.json
```

### 4.2 数据流

```
# 有 --role
ai run --role orchestrator
  → run 不认识 --role（该 flag 被声明为透传）
  → 实际执行: ai rpc ... --role orchestrator
  → rpc.go 解析 --role = "orchestrator"
  → newRPCApp():
      ~/.ai/roles/orchestrator/agent.yaml 存在？
        → agentconfig.Load() 加载
        → system_prompt 从 agent.yaml 中 system_prompt 字段指向的文件读取
        → skillStats = LoadStats("~/.ai/roles/orchestrator/skill-stats.json")
      → 不存在 → return error
  → createBaseContext() 正常构建 system prompt
  → 创建 session 时：meta.json.Role = "orchestrator"

# 无 --role
ai run
  → ai rpc（不带 --role）
  → rpc.go 解析 --role = ""（默认空）
  → newRPCApp():
      → agentConfig = nil
      → skillStats = LoadStats("~/.ai/skill-stats.json")  # 全局
      → buildSystemPrompt(): 优先 customSystemPrompt, 否则 embedded prompt.md
      → session 不记录 role
  → /resume 时：SessionMeta 中有 role？
      → 有 role → buildSystemPrompt() 加载角色配置
      → 无 role → 默认行为
```

### 4.3 涉及修改的文件

| 文件 | 修改内容 |
|------|---------|
| `cmd/ai/main.go` | 无变化 |
| `subcommand/run/run.go` | 去掉 `--role` flag 定义（变成透传）；去掉 role→systemPrompt 解析逻辑；去掉 `prompt.TemplateForRole` 调用 |
| `subcommand/rpc/rpc.go` | 加 `--role` flag；传给 `RunRPC` 参数 |
| `pkg/rpc/rpc_handlers.go` | `RunRPC` 签名加 `role` 参数 |
| `pkg/rpc/rpc_setup.go` | `newRPCApp()` 根据 role 参数查找 `~/.ai/roles/<role>/agent.yaml`；skill-stats 路径随 role 变化 |
| `pkg/rpc/rpc_app.go` | `createBaseContext()` 记录 role 到 session meta |
| `pkg/rpc/rpc_helpers.go` | `buildSystemPrompt()` 逻辑调整（role config 优先级） |
| `pkg/session/manager.go` | `SessionMeta` 加 `Role` 字段；保存/恢复逻辑 |
| `pkg/session/entries.go` | 无变化（role 不写 messages.jsonl） |
| `pkg/prompt/builder.go` | 删除 `TemplateForRole()`；保留 `prompt.md` embed |
| `pkg/prompt/orchestrator.md` | 保留在 git 中 |
| `pkg/prompt/validator.md` | 保留在 git 中 |
| `pkg/skill/stats.go` | 无变化（已支持任意路径 `LoadStats(path)`） |

### 4.4 Session meta.json 变更

```go
// pkg/session/manager.go
type SessionMeta struct {
    ID           string    `json:"id"`
    Name         string    `json:"name"`
    Title        string    `json:"title"`
    CreatedAt    time.Time `json:"createdAt"`
    UpdatedAt    time.Time `json:"updatedAt"`
    MessageCount int       `json:"messageCount"`
    Workspace    string    `json:"workspace,omitempty"`
    CurrentWorkdir string `json:"currentWorkdir,omitempty"`
    Role         string    `json:"role,omitempty"`  // 新增
}
```

### 4.5 Resume 逻辑

```go
// rpc_app.go createBaseContext() — resume 时
if app.role == "" {
    // 命令行未指定 role → 尝试从 session meta 恢复
    if meta.Role != "" {
        app.role = meta.Role
        // 重新加载角色配置
        app.loadRoleConfig()
        // 重新构建 system prompt
        app.systemPrompt = app.buildSystemPrompt(...)
    }
}
// 如果 app.role != "" 且 meta.Role != "" 且不一致
if app.role != "" && meta.Role != "" && app.role != meta.Role {
    slog.Warn("Role mismatch",
        "session_role", meta.Role,
        "current_role", app.role)
}
```

## 5. P0 Feature 验收场景

### P0 Feature: Role 参数透传
**Acceptance Scenarios:**
1. `ai run --role orchestrator` → run 不解析 `--role`，子进程 `ai rpc` 收到 `--role orchestrator`
2. `ai run`（无 `--role`） → run 不产生 `--role` 参数传给 rpc
3. `ai run --session /tmp/s.json --role validator` → session 和 role 同时正确传递

### P0 Feature: 角色目录加载
**Acceptance Scenarios:**
1. `ai rpc --role orchestrator` 且 `~/.ai/roles/orchestrator/agent.yaml` 存在 → 加载成功
2. `ai rpc --role nonexistent` 且目录不存在 → 报错退出
3. `ai rpc`（无 `--role`）→ 用嵌入式默认 prompt.md + 全局 skill-stats
4. `ai rpc --role orchestrator` → skill-stats 从 `~/.ai/roles/orchestrator/skill-stats.json` 读取

### P0 Feature: System Prompt 覆盖优先级
**Acceptance Scenarios:**
1. `ai rpc --role orchestrator` → 用角色 agent.yaml 中 system_prompt 指向的文件
2. `ai rpc --role orchestrator --system-prompt @/path/to/custom.md` → system_prompt 用自定义内容，其它配置（middlewares, tools）仍从角色配置读取

### P0 Feature: Session Role 持久化
**Acceptance Scenarios:**
1. `ai run --role validator` → 新建 session → `meta.json` 包含 `"role": "validator"`
2. `ai run`（无 role）→ 新建 session → `meta.json` 不含 `role` 或 `role` 为 `""`
3. 旧 session（无 role 字段）resume → 正常工作，不报错

### P0 Feature: Resume Role 恢复
**Acceptance Scenarios:**
1. `ai run --role orchestrator` → exit → `ai run --role validator` → `/resume` → 用 validator 配置加载，日志记录 role 不一致警告
2. `ai run --role orchestrator` → exit → `ai run`（无 role）→ `/resume` → 从 meta.json 读取 role=orchestrator，自动恢复
3. `ai run`（无 role）→ exit → `ai run`（无 role）→ `/resume` → 正常恢复，不做特殊处理

## 6. 边界条件和注意事项

- **旧 session 兼容**：已有 session 的 meta.json 没有 `role` 字段，Resume 时 `Role` 为空串，不做特殊处理
- **role 目录缺少必要文件**（如 agent.yaml 存在但引用的 system_prompt.md 不存在）：`agentconfig.Load()` / `ResolveSystemPrompt()` 返回错误，`newRPCApp` 报错退出
- **并发安全**：role 目录读取是只读操作（skill-stats 写入除外，已有锁），无并发问题
- **skill-stats 迁移**：已有全局 `~/.ai/skill-stats.json` 不受影响，继续用于无 role 模式
- **`agent.yaml` 格式无需改动**：现有 `agentconfig` 包的结构完全满足需求，仅需在调用 `Load()` 时传入角色目录下的路径