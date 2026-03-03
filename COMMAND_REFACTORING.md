# 命令重构总结

## 变更概述

将分散的设置命令统一到 `/set` 命令下，简化命令空间。

## 新命令格式

### `/set <key> [value]`

支持的设置项：

| 设置项 | 旧命令 | 新命令 | 示例 |
|--------|--------|--------|------|
| tools | `/tools off\|on\|verbose\|toggle` | `/set tools off\|on\|verbose\|toggle` | `/set tools verbose` |
| prefix | `/prefix on\|off\|toggle` | `/set prefix on\|off\|toggle` | `/set prefix on` |
| thinking | `/thinking on\|off\|toggle\|level` | `/set thinking on\|off\|toggle\|level` | `/set thinking high` |
| auto-compaction | `/auto-compaction on\|off` | `/set auto-compaction on\|off` | `/set auto-compaction on` |

## 向后兼容性

旧命令仍然保留，确保平滑迁移：

- `/tools` - 仍然可用
- `/prefix` - 仍然可用
- `/thinking` - 仍然可用
- `/auto-compaction` - 仍然可用

## 使用示例

### 工具显示设置

```bash
# 新命令
/set tools on          # 显示工具调用
/set tools verbose      # 显示详细工具调用
/set tools toggle       # 切换工具显示

# 旧命令（仍然可用）
/tools on
/tools verbose
```

### 前缀显示设置

```bash
# 新命令
/set prefix on         # 显示角色前缀
/set prefix off
/set prefix toggle

# 旧命令（仍然可用）
/prefix on
```

### 思考显示设置

```bash
# 新命令
/set thinking on       # 显示思考内容
/set thinking off
/set thinking medium    # 设置思考级别为 medium

# 旧命令（仍然可用）
/thinking on
/thinking medium
```

### 自动压缩设置

```bash
# 新命令
/set auto-compaction on    # 启用自动压缩
/set auto-compaction off   # 禁用自动压缩

# 旧命令（仍然可用）
/auto-compaction on
```

## 查看所有设置

```bash
/show settings
```

## 帮助信息

```bash
/help        # 显示所有命令帮助
/set         # 显示 /set 命令用法
```

## 实现细节

### 文件修改
- `internal/winai/interpreter.go`
  - 添加了 `handleSet()` 函数
  - 更新了 `handleCommand()` 的命令路由
  - 更新了 `showHelp()` 的帮助文本

### 命令解析逻辑

`handleSet()` 函数：
1. 解析命令参数（key 和可选的 value）
2. 根据key分发到对应的处理器
3. 如果没有提供value，使用默认行为（通常是 toggle）
4. 复用现有的处理函数（`handleTools`, `handleToggle`, `handleThinking`, `handleAutoCompaction`）

## 优势

1. **统一命名空间** - 所有设置命令使用 `/set` 前缀
2. **更好的可发现性** - 用户可以尝试 `/set <tab>` 来查看所有设置项
3. **易于扩展** - 添加新设置只需在 `handleSet()` 中添加新的 case
4. **向后兼容** - 旧命令仍然可用，不会破坏现有用户习惯
5. **代码复用** - 复用现有的处理函数，减少代码重复

## 未来改进

- 添加 `/set <tab>` 补全支持
- 在旧命令中添加弃用提示
- 添加更多可配置项到 `/set` 命令