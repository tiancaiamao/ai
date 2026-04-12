# AI Session Cleaner

清理 `.ai/sessions` 目录下的空 session 和短 session 的工具。

## 功能特性

- 📊 扫描所有 session 目录（支持顶层或指定 workspace）
- 🔍 识别空的或短的 session（基于行数和文件大小）
- ⚠️ 安全的 dry-run 模式（默认）
- 🗓️ 可选的时间过滤（只清理旧 session）
- 🧹 可清理空 session-id 目录和空 cwd-hash 目录
- 📈 显示清理统计信息
- ✅ 仅匹配 UUID 格式目录名，自动跳过 internal 目录（checkpoints/、llm-context/ 等）

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
./scripts/clean-sessions.sh /Users/genius/.ai/sessions 5 2000
```

### 指定 Workspace

```bash
# 只扫描特定 workspace
./scripts/clean-sessions.sh /Users/genius/.ai/sessions/--Users-genius-project-ai--
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
DRY_RUN=false DAYS_OLD=7 ./scripts/clean-sessions.sh
```

### 激进清理

```bash
# 同时清理空的 cwd-hash 目录
CLEAN_EMPTY=true CLEAN_EMPTY_HASH=true DRY_RUN=false DAYS_OLD=7 ./scripts/clean-sessions.sh
```

## 参数说明

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `sessions_base` | Session 目录路径（顶层或 workspace） | `/Users/genius/.ai/sessions` |
| `min_lines` | 最小行数阈值 | `3` |
| `min_size` | 最小文件大小阈值（字节） | `1000` |

## 环境变量

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `DRY_RUN` | 是否实际删除（false=删除，true=预览） | `true` |
| `DAYS_OLD` | 只清理 N 天前的 session（可选） | 无 |
| `CLEAN_EMPTY` | 清理没有 messages.jsonl 的空 session 目录 | `true` |
| `CLEAN_EMPTY_HASH` | 清理空的 cwd-hash 目录 | `false` |

## 清理规则

Session 会被删除当且仅当：

1. **目录名匹配 UUID 格式**（`xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx`），内部目录（checkpoints/、llm-context/、current/ 等）自动跳过

2. **满足以下任一条件**：
   - `messages.jsonl` 不存在（空 session 目录，`CLEAN_EMPTY=true` 时）
   - `messages.jsonl` 为空（0 行）
   - `messages.jsonl` 行数 < `min_lines` **且** 文件大小 < `min_size`

3. **（可选）** Session 修改时间 > `DAYS_OLD` 天

## 输出示例

```
[INFO] Scanning sessions in: /Users/genius/.ai/sessions
[INFO] Thresholds: < 3 lines AND < 1000 bytes
[INFO] Clean empty session dirs: true
[INFO] Clean empty hash dirs: false
[INFO] Dry run: true

[DELETE] b1a7e093-e04c-4779-a30c-8025cc31d696 (no messages.jsonl)
[DELETE] d5ad2613-84dc-415b-8f17-263304f2247d (short (2 lines, 311 bytes))

[INFO] Summary:
  Total sessions:       1670
  Would delete:         2 sessions
```

## 推荐工作流程

1. **先预览**：
   ```bash
   ./scripts/clean-sessions.sh
   ```

2. **调整阈值**（根据预览结果）：
   ```bash
   ./scripts/clean-sessions.sh /Users/genius/.ai/sessions 5 2000
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
- 仅匹配 UUID 格式的目录名，不会误删 internal 目录
- 设置 `DRY_RUN=false` 时会要求输入 `yes` 确认
- 建议先在小范围测试，确认阈值后再全局执行
- 可以使用 `DAYS_OLD` 避免误删最近创建的 session