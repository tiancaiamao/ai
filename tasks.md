# Tasks: Subagent Orchestration Implementation

## Implementation Checklist

### Phase 1: CLI 参数扩展

- [ ] **T1.1** 修改 `cmd/ai/main.go`
  - 添加 `--system-prompt` flag 定义
  - 解析: 如果值以 `@` 开头，读取文件内容

- [ ] **T1.2** 修改 `runHeadless` 函数签名
  - 添加 `systemPrompt string` 参数
  - 传递到 AgentContext

- [ ] **T1.3** 在 headless_mode.go 实现文件读取逻辑
  - 处理 `@/path/to/file` 格式
  - 验证文件存在、可读
  - 限制文件大小 (64KB)

- [ ] **T1.4** 单元测试
  - 测试 `--system-prompt "text"` 模式
  - 测试 `--system-prompt @file` 模式
  - 测试文件不存在错误处理

### Phase 2: 默认 System Prompt

- [ ] **T2.1** 创建 `pkg/agent/subagent.go`
  - 定义默认 subagent system prompt
  - 提供 GetDefaultSubagentPrompt() 函数

- [ ] **T2.2** 集成到 headless_mode.go
  - 当启用 `--subagent` 且未提供 `--system-prompt` 时使用默认

### Phase 3: Orchestrate Skill

- [ ] **T3.1** 创建 `skills/orchestrate/SKILL.md`
  - 技能描述和使用场景
  - 任务分析 + 分解逻辑
  - agent persona 选择规则
  - 结果汇总格式

- [ ] **T3.2** 实现任务复杂度判断
  - 关键词匹配规则
  - 多步骤检测

- [ ] **T3.3** 实现 subagent spawn 逻辑
  - 构建命令行
  - 传递 --system-prompt @path

- [ ] **T3.4** 实现结果汇总
  - 拼接 subagent 输出
  - 返回最终结果给用户

### Phase 4: Agent Persona 文件

- [ ] **T4.1** 创建目录 `~/.ai/skills/orchestrate/agents/`

- [ ] **T4.2** 创建默认 persona 文件
  - `implementer.md` - 实现者
  - `reviewer.md` - 审查者
  - `researcher.md` - 调研者
  - `explorer.md` - 探索者

- [ ] **T4.3** 测试完整流程
  - 手动测试 `/orchestrate` 技能

## Task Dependencies

```
T1.1 → T1.2 → T1.3 → T1.4
  ↓
T2.1 → T2.2
  ↓
T3.1 → T3.2 → T3.3 → T3.4
  ↓
T4.1 → T4.2 → T4.3
```

## Notes

- 所有代码改动在 `~/project/ai/.worktrees/subagent-orchestration/` worktree
- Persona 文件创建在用户 HOME 目录 (~)
- 遵循 Go 代码风格，使用 pflag 库