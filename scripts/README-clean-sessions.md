# AI Session Cleaner

清理 `.ai/sessions` 目录下的空 session 和短 session 的工具。

## 功能特性

- 📊 扫描所有 session 目录
- 🔍 识别空的或短的 session（基于行数和文件大小）
- ⚠️ 安全的 dry-run 模式（默认）
- 🗓️ 可选的时间过滤（只清理旧 session）
- 📈 显示清理统计信息

## 使用方法

### 基本用法

```bash
# Dry run（预览，不会实际删除）
./scripts/clean-sessions.sh

# 显示帮助
./scripts/clean-sessions.sh --help
```

### 自定义阈值

```bash
# 清理 < 5 行 且 < 2000 字节的 session
./scripts/clean-sessions.sh /Users/genius/.ai/sessions/--Users-genius-project-ai-- 5 2000
```

### 时间过滤

```bash
# 只清理 7 天前的短 session
DAYS_OLD=7 ./scripts/clean-sessions.sh

# 只清理 30 天前的短 session
DAYS_OLD=30 ./scripts/clean-sessions.sh
```

### 执行实际删除

⚠️ **警告：此操作不可逆！**

```bash
# 确认后执行删除
DRY_RUN=false ./scripts/clean-sessions.sh

# 结合时间过滤
DRY_RUN=false DAYS_OLD=7 ./scripts/clean-sessions.sh /Users/genius/.ai/sessions/--Users-genius-project-ai-- 3 1000
```

## 参数说明

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `sessions_base` | Session 目录路径 | `/Users/genius/.ai/sessions` |
| `min_lines` | 最小行数阈值 | `3` |
| `min_size` | 最小文件大小阈值（字节） | `1000` |

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `DRY_RUN` | 是否实际删除（false=删除，true=预览） | `true` |
| `DAYS_OLD` | 只清理 N 天前的 session（可选） | 无 |

## 清理规则

Session 会被删除当且仅当：

1. **满足以下任一条件**：
   - `messages.jsonl` 为空（0 行）
   - `messages.jsonl` 行数 < `min_lines` **且** 文件大小 < `min_size`

2. **（可选）** Session 修改时间 > `DAYS_OLD` 天

## 输出示例

```
[INFO] Scanning sessions in: /Users/genius/.ai/sessions/--Users-genius-project-ai--
[INFO] Thresholds: < 3 lines OR < 1000 bytes
[INFO] Dry run: true

[DELETE] 14953847-c8d5-4e8a-b3bb-783b15c927fe (short (       2 lines, 299 bytes))
[DELETE] 78c4424b-c29d-4afb-adeb-e49d3efd8036 (short (       2 lines, 290 bytes))
[WARN] No messages.jsonl in 2ca927c3-ee1e-47fd-8c92-76354b2a0992
...

[INFO] Summary:
  Total sessions: 159
  Would delete: 95 sessions
  Total size: 18772KB
  Would free: 1156KB
```

## 推荐工作流程

1. **先预览**：
   ```bash
   ./scripts/clean-sessions.sh
   ```

2. **调整阈值**（根据预览结果）：
   ```bash
   ./scripts/clean-sessions.sh /Users/genius/.ai/sessions/--Users-genius-project-ai-- 5 2000
   ```

3. **添加时间过滤**（可选）：
   ```bash
   DAYS_OLD=7 ./scripts/clean-sessions.sh
   ```

4. **确认后执行**：
   ```bash
   DRY_RUN=false DAYS_OLD=7 ./scripts/clean-sessions.sh
   ```

## 安全提示

- 默认使用 dry-run 模式，不会实际删除任何文件
- 设置 `DRY_RUN=false` 时会要求输入 `yes` 确认
- 建议先在小范围测试，确认阈值后再全局执行
- 可以使用 `DAYS_OLD` 避免误删最近创建的 session