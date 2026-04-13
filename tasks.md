# Earlier Conservative Compact Tasks

让 compact 更早触发但更保守，让 LLM 主动决策何时进行物理压缩。

## Task List

- [x] Task 1: 在 AgentState 添加统计字段
  - 添加 `TotalTruncations int` - 累计 truncate 次数
  - 添加 `TotalCompactions int` - 累计 full compact 次数
  - 添加 `LastCompactTurn int` - 最近一次 full compact 的 turn
  - 更新 `Clone()` 方法以包含新字段

- [x] Task 2: 添加 CompactActionCompact 类型
  - 在 `pkg/context/context.go` 的 `CompactAction` 枚举中添加 `CompactActionCompact = "compact"`

- [x] Task 3: 创建 CompactTool 工具
  - 创建 `pkg/compact/compact_tool.go`
  - 实现 `CompactTool` 结构体，包含参数：strategy, keep_recent_tokens, reason
  - 实现工具的执行逻辑，调用 `Compactor.Compact()` 并支持参数化

- [x] Task 4: 增强 mini compact 的决策信息
  - 在 `buildContextMgmtMessages` 的 `<current_state>` 中添加累计统计信息
  - 显示：Total truncations so far, Total compactions so far, Last compact turn
  - 在 truncate 工具中更新 AgentState 统计

- [x] Task 5: 更新 registry 和 system prompt
  - 在 `GetMiniCompactTools()` 中添加 CompactTool
  - 更新 `llm_mini_compact_system.md` 添加 compact 工具的说明和使用场景

- [x] Task 6: 可观测性增强
  - 记录 compact 工具调用的决策理由
  - 记录累计统计到 trace events

- [x] Task 7: 测试验证
  - 编写单元测试验证新字段和工具
  - 集成测试验证 mini compact 可以调用 compact 工具

## 实现总结

### 核心改动

1. **AgentState 统计字段**：记录累计 truncate 和 compact 次数，以及最近一次 compact 的 turn

2. **CompactTool 工具**：
   - 支持三种策略：conservative, balanced, aggressive
   - 可自定义 keep_recent_tokens 参数
   - 必须提供 reason 说明为何进行 compact
   - 更新 AgentState 的统计信息

3. **Mini Compact 增强**：
   - 在 `<current_state>` 中展示累计统计信息
   - 给 LLM 提供更多决策依据（truncate 次数、compact 次数）
   - 添加 compact 工具的决策建议

4. **可观测性**：
   - 记录每次操作的统计信息到 trace events
   - 记录 compact 的策略和决策理由

### 设计理念

- **让 LLM 主动决策**：LLM 可以根据累计 truncate 次数、话题转换、任务完成等因素决定何时 compact
- **更早但更保守**：在 40%+ token 使用率时就可以考虑 compact，而不是等到 75%
- **可配置的策略**：支持不同激进程度的压缩策略