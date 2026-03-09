# Implementation Plan: Subagent Orchestration

## Technical Context
- **Stack**: Go (ai CLI), Markdown (agent persona 文件)
- **Architecture**: Command-line tool with subprocess spawning
- **Dependencies**: 无新外部依赖，使用标准库

## Data Model

### Agent Persona 文件格式
```
~/.ai/skills/orchestrate/agents/
├── implementer.md   # 实现者 - 完整工具权限
├── reviewer.md      # 审查者 - 只读工具
├── researcher.md    # 调研者 - 只读工具
└── explorer.md      # 探索者 - 只读工具
```

每个 persona 文件为 Markdown，内容直接作为 system prompt。

## Components

### 1. CLI 参数扩展
**文件**: `cmd/ai/main.go`
- 新增 `--system-prompt <text>` 参数
- 解析逻辑: 如果值以 `@` 开头，则读取对应文件内容作为 system prompt
- 优先级: `--system-prompt @file` > `--system-prompt <text>` > 默认

**文件**: `cmd/ai/headless_mode.go`
- `runHeadless` 函数签名添加 `systemPrompt string` 参数
- 如果提供了自定义 system prompt，覆盖默认行为

### 2. runHeadless 函数签名变更
```go
func runHeadless(sessionPath string, noSession bool, maxTurns int, 
    allowedTools []string, isSubagent bool, 
    systemPrompt string,  // 新增
    prompts []string, output io.Writer) error
```

### 3. 默认 Subagent System Prompt
**文件**: `pkg/agent/subagent.go` (新文件)
- 当 `--subagent` 启用时使用的默认 system prompt
- 可通过 `--system-prompt-file` 覆盖

### 4. Orchestrate Skill
**文件**: `skills/orchestrate/SKILL.md`
- 自动分析任务复杂度
- 任务分解逻辑
- 选择合适的 agent persona
- 汇总结果返回用户

### 5. Agent Persona 文件
**目录**: `~/.ai/skills/orchestrate/agents/` (创建在用户 HOME 目录)
- 实现时创建默认文件模板

## Security Considerations
- 验证 `--system-prompt-file` 路径存在且可读
- 限制 system prompt 文件大小 (最大 64KB)
- subagent 不可嵌套调用 orchestrate (防止无限循环)

## Implementation Order

### Phase 1: CLI 参数 (基础)
1. 修改 `cmd/ai/main.go`，添加 `--system-prompt-file` 和 `--system-prompt` flag
2. 修改 `runHeadless` 函数签名
3. 在 headless_mode.go 中实现加载逻辑
4. 单元测试覆盖参数解析

### Phase 2: 默认 System Prompt
5. 创建 `pkg/agent/subagent.go`，定义默认 subagent prompt
6. 当启用 `--subagent` 时使用此 prompt

### Phase 3: Orchestrate Skill
7. 创建 `skills/orchestrate/SKILL.md`
8. 实现任务分析 + 分解 + subagent 选择逻辑
9. 实现结果汇总

### Phase 4: Agent Persona 文件
10. 在 `~/.ai/skills/orchestrate/agents/` 创建默认 persona 文件
11. 测试完整流程

## 关键设计决策

### Q: 为什么 agent persona 放在 `~/.ai/skills/orchestrate/agents/`？
A: 与现有 skills 架构一致，便于用户自定义和版本控制

### Q: --system-prompt 参数格式？
A: 
- `--system-prompt "You are a helpful assistant"` - 直接文本
- `--system-prompt @/path/to/persona.md` - 读取文件内容

### Q: orchestrate 如何选择 agent persona？
A: 基于任务关键词匹配：
- "实现"、"开发" → implementer
- "审查"、"review" → reviewer
- "调研"、"分析" → researcher
- "探索"、"查找" → explorer

### Q: 任务分解的触发条件？
A: 复杂任务自动触发，衡量标准：
- 多步骤操作 (涉及多个文件)
- 需要不同技能 (实现 + 测试)
- 长时间执行 (预计 > 5 分钟)