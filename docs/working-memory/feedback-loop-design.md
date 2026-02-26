# Working Memory 反馈循环设计

## 问题

LLM在多轮交互中经常忘记更新working memory，导致：
- 重要上下文丢失
- 重复计算或重复工具调用
- 无法有效压缩上下文

需要一种机制让agent"提醒"自己更新working memory。

## 设计目标

1. **非侵入性**：不改变现有agent loop流程
2. **自适应**：根据实际使用情况动态触发
3. **可配置**：允许调整触发条件
4. **低开销**：不影响性能

## 方案对比

| 方案 | 优点 | 缺点 |
|------|------|------|
| 1. 外部监控进程 | 完全解耦 | 无法实时触发，复杂度高 |
| 2. 修改Agent Loop | 精确控制 | 侵入性强，修改核心逻辑 |
| 3. System Prompt模板 | 无代码修改 | 不可靠，依赖LLM自我认知 |
| 4. WorkingMemory内部跟踪 | ✅ 平衡 | ⭐ 推荐 |

## 实现方案：Working Memory 更新跟踪器

### 核心机制

在`WorkingMemory`结构中添加更新跟踪：

```go
type WorkingMemory struct {
    // ... 现有字段 ...

    // Update tracking
    lastUpdateTime    time.Time
    lastCheckTime     time.Time
    roundsSinceUpdate int
}

const (
    maxRoundsWithoutUpdate = 5  // 最多5轮不更新就提醒
    minRoundsBeforeCheck   = 3  // 至少3轮后才开始检查
)
```

### 工作流程

```
1. 每次调用 `write(overview.md)` 时:
   - 更新 lastUpdateTime
   - 重置 roundsSinceUpdate

2. 每次调用 Load() 之前:
   - 检查是否超过 minRoundsBeforeCheck
   - 如果 roundsSinceUpdate > maxRoundsWithoutUpdate:
     * 生成提醒消息
     * 在返回内容中插入提醒

3. 返回内容格式:
   [OVERVIEW内容]
   
   <!-- ⚠️ WORKING MEMORY REMINDER -->
   <!-- 你已经 6 轮没有更新 working memory 了 -->
   <!-- 当前上下文使用了 65% tokens -->
   <!-- 建议：将已完成任务归档到 detail/，压缩关键信息 -->
   <!-- 使用 write tool 更新 %s -->
```

### 触发条件

| 条件 | 说明 |
|------|------|
| roundsSinceUpdate > maxRoundsWithoutUpdate | 超过最大轮次 |
| tokensPercent > threshold | 上下文使用率过高（可选） |
| messagesInHistory > threshold | 历史消息过多（可选） |

### 提醒消息内容

提醒消息会动态生成，包含：

```
⚠️ WORKING MEMORY UPDATE NEEDED

- 不更新轮数: {rounds} 轮
- Token 使用: {tokens_percent}%
- 历史消息: {messages_count} 条

建议操作:
1. 总结已完成任务，移到 detail/completed-{date}.md
2. 更新"当前任务"状态
3. 删除过时信息
4. 保留最近决策和关键问题

使用 write tool 更新: {overview_path}
```

## 实现细节

### 1. WorkingMemory 结构扩展

```go
// pkg/agent/working_memory.go

type WorkingMemory struct {
    mu sync.RWMutex

    // ... 现有字段 ...

    // Update tracking
    lastUpdateTime    time.Time
    lastCheckTime     time.Time
    roundsSinceUpdate int
}

// MarkUpdated 记录 working memory 已更新
func (wm *WorkingMemory) MarkUpdated() {
    wm.mu.Lock()
    defer wm.mu.Unlock()

    wm.lastUpdateTime = time.Now()
    wm.roundsSinceUpdate = 0
}

// CheckUpdateNeeded 检查是否需要提醒更新
func (wm *WorkingMemory) CheckUpdateNeeded() (bool, string) {
    wm.mu.Lock()
    defer wm.mu.Unlock()

    // 首次检查
    if wm.lastCheckTime.IsZero() {
        wm.lastCheckTime = time.Now()
        wm.roundsSinceUpdate = 1
        return false, ""
    }

    wm.roundsSinceUpdate++
    wm.lastCheckTime = time.Now()

    // 检查是否需要提醒
    if wm.roundsSinceUpdate < minRoundsBeforeCheck {
        return false, ""
    }

    if wm.roundsSinceUpdate > maxRoundsWithoutUpdate {
        return true, wm.buildReminder()
    }

    // 可选：基于 token 使用率的提醒
    meta := wm.GetMeta()
    if meta.TokensPercent > 70 && wm.roundsSinceUpdate > 3 {
        return true, wm.buildReminder()
    }

    return false, ""
}

// buildReminder 构建提醒消息
func (wm *WorkingMemory) buildReminder() string {
    meta := wm.GetMeta()

    reminder := fmt.Sprintf(`

<!--
⚠️ WORKING MEMORY UPDATE NEEDED

你已经连续 %d 轮没有更新 working memory 了。
当前上下文状态:
- Token 使用: %.0f%% (%d / %d)
- 历史消息: %d 条
- Working Memory 大小: %.2f KB

建议操作:
1. 总结已完成的任务，归档到 %s
2. 更新"当前任务"状态和进度
3. 删除过时信息，保留最近决策
4. 将详细讨论移到 detail/ 目录

使用 write tool 更新: %s
下次请求时，你会看到更新后的内容。
-->`,
        wm.roundsSinceUpdate,
        meta.TokensPercent,
        meta.TokensUsed,
        meta.TokensMax,
        meta.MessagesInHistory,
        float64(meta.WorkingMemorySize)/1024,
        wm.detailPath,
        wm.overviewPath)

    return reminder
}
```

### 2. 修改 Load 方法

```go
// Load 加载内容，并在需要时插入提醒
func (wm *WorkingMemory) Load() (string, error) {
    content, err := wm.loadContent()
    if err != nil {
        return "", err
    }

    // 检查是否需要提醒
    needsUpdate, reminder := wm.CheckUpdateNeeded()
    if needsUpdate {
        content = content + reminder
    }

    return content, nil
}

// loadContent 内部方法，只加载文件内容
func (wm *WorkingMemory) loadContent() (string, error) {
    wm.mu.Lock()
    defer wm.mu.Unlock()

    // ... 原有的 Load 逻辑 ...
}
```

### 3. 在 Agent Loop 中集成

检测到 `write(overview.md)` 调用时，标记为已更新：

```go
// pkg/agent/loop.go

// 在工具调用完成后检查
if toolCall.Name == "write" {
    args := toolCall.Arguments.(map[string]interface{})
    if path, ok := args["path"].(string); ok {
        if path == agent.workingMemory.GetPath() {
            // User updated working memory
            agent.workingMemory.MarkUpdated()
        }
    }
}
```

或者更简洁：每次 write 后检查文件路径。

## 配置选项

通过环境变量或配置文件调整：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `WM_MAX_ROUNDS` | 5 | 最大不更新轮数 |
| `WM_MIN_ROUNDS` | 3 | 最小检查轮数 |
| `WM_TOKEN_THRESHOLD` | 70 | Token 提醒阈值（%） |
| `WM_ENABLE_REMINDER` | true | 是否启用提醒 |

## 示例效果

### 场景1：正常更新

```
轮次1: Load() → 无提醒
轮次2: User 更新 overview.md → MarkUpdated()
轮次3: Load() → 无提醒（roundsSinceUpdate=0）
```

### 场景2：需要提醒

```
轮次1: Load() → 无提醒（roundsSinceUpdate=1）
轮次2: Load() → 无提醒（roundsSinceUpdate=2）
轮次3: Load() → 无提醒（roundsSinceUpdate=3）
轮次4: Load() → 无提醒（roundsSinceUpdate=4）
轮次5: Load() → 无提醒（roundsSinceUpdate=5）
轮次6: Load() → ⚠️ 提醒！（roundsSinceUpdate=6）
```

### 场景3：高Token使用提前触发

```
轮次1: Token=45% → 无提醒
轮次2: Token=52% → 无提醒
轮次3: Token=68% → 无提醒
轮次4: Token=73% → ⚠️ 提醒！（超过70%阈值）
```

## 测试计划

1. **单元测试**
   - TestMarkUpdated: 验证标记重置
   - TestCheckUpdateNeeded: 验证触发条件
   - TestBuildReminder: 验证提醒内容格式

2. **集成测试**
   - 模拟多轮对话，验证提醒触发时机
   - 验证write overview.md后的重置

3. **手动测试**
   - 观察提醒在真实对话中的效果
   - 调整参数找到最佳平衡点

## 优势

1. ✅ **非侵入**：只在Load()和工具回调中添加逻辑
2. ✅ **自适应**：根据实际使用动态提醒
3. ✅ **可配置**：支持环境变量调整
4. ✅ **信息丰富**：提醒包含详细状态和建议
5. ✅ **无需额外tool**：不引入新tool，降低复杂度

## 潜在改进

1. **智能提醒强度**
   - 初次提醒：温和建议
   - 持续不更新：加强提醒
   - 严重情况：暂停请求

2. **内容分析**
   - 检测overview.md是否过时
   - 分析内容相关性

3. **自动压缩建议**
   - 分析历史消息，提供压缩建议
   - 生成detail归档内容草稿

4. **学习和适应**
   - 记录LLM更新频率
   - 动态调整阈值

## 实施步骤

Phase 1: 基础跟踪
- [ ] 添加字段到 WorkingMemory
- [ ] 实现 MarkUpdated()
- [ ] 实现 CheckUpdateNeeded()
- [ ] 实现 buildReminder()
- [ ] 修改 Load() 集成提醒

Phase 2: 集成检测
- [ ] 在 Agent Loop 中检测 write(overview.md)
- [ ] 调用 MarkUpdated()
- [ ] 添加单元测试

Phase 3: 优化和配置
- [ ] 添加环境变量支持
- [ ] 调整默认参数
- [ ] 集成测试
- [ ] 文档更新

## 总结

这个方案通过在WorkingMemory内部添加轻量级跟踪机制，实现了非侵入的反馈循环。它在不改变核心流程的情况下，让agent能够"意识到"自己需要更新working memory，并通过动态提醒促进行为改变。

方案的核心优势是：
- 简单：~150行代码
- 可靠：基于实际行为而非猜测
- 有效：提供具体、可操作的提醒