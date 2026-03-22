# Explore-Driven Development: Implementation Plan

**Date:** 2025-03-22
**Status:** Draft
**Reference:** spec.md

---

## 1. Parallel/Chain 的 Bash 实现

**来源参考**：pi-config TypeScript extension 的 parallel/chain 实现

| 模式 | pi-config | 用户项目（Bash） |
|------|-----------|-----------------|
| **Parallel** | `Promise.all` + 最多 4 并发 | 后台启动 + `wait` + `tmux_wait.sh` |
| **Chain** | `for` + `{previous}` 占位符 | 顺序执行 + 文件传递结果 |
| **实时 UI** | `onUpdate` 回调 | 通过文件 + `tail -f` |

### 1.1 Parallel 实现（Bash）

```bash
# 并行启动多个 subagent（使用 & 后台启动）
SESSION1=$(start_subagent_tmux.sh /tmp/out1.txt 10m @explorer.md "task1") &
SESSION2=$(start_subagent_tmux.sh /tmp/out2.txt 10m @explorer.md "task2") &

# 提取 session names（后台运行时命令立即返回）
NAME1=$(echo "$SESSION1" | cut -d: -f1)
NAME2=$(echo "$SESSION2" | cut -d: -f1)

# 同时等待所有后台任务
wait

# 收集结果
cat /tmp/out1.txt
cat /tmp/out2.txt
```

**关键参数**：
- **MAX_PARALLEL = 2-3**（API rate limit 保护，用户偏好不超过 3）
- **优先 delegate（顺序）**，只有需要时才 parallel

### 1.2 Chain 实现（Bash）

```bash
# 第一步
SESSION1=$(start_subagent_tmux.sh /tmp/step1.txt 10m @worker.md "task1")
NAME1=$(echo "$SESSION1" | cut -d: -f1)
tmux_wait.sh "$NAME1" 600
RESULT1=$(cat /tmp/step1.txt)

# 第二步（使用 {previous} 替换）
SESSION2=$(start_subagent_tmux.sh /tmp/step2.txt 10m @worker.md \
  "task2: Result from previous step was: $RESULT1")
NAME2=$(echo "$SESSION2" | cut -d: -f1)
tmux_wait.sh "$NAME2" 600
RESULT2=$(cat /tmp/step2.txt)
```

**关键机制**：
- `{previous}` 占位符：在 prompt 中用前一步的结果替换
- 顺序执行：必须等上一步完成
- 结果传递：通过文件 + 变量注入

### 1.3 实时监控

```bash
# 监控多个 session 的输出
tail -f /tmp/out1.txt /tmp/out2.txt &

# 或者用 tmux 的 capture-pane
tmux capture-pane -t "$NAME1" -p
tmux capture-pane -t "$NAME2" -p
```

### 1.4 Rate Limit 考虑

| 场景 | 并行数 | 说明 |
|------|--------|------|
| **Explore（信息收集）** | 2-3 | 可以并行探索多个 repo |
| **Worker（执行任务）** | 1-2 | 重点在质量，避免 rate limit |
| **Review（代码审查）** | 1 | 专注审查，不需要并行 |
| **混合场景** | 不超过 3 | 总并发数限制 |

---

## 2. 技能实现计划

### 2.1 Phase 1: 完善 Explore（已完成）

**已创建**：
- `skills/explore/SKILL.md`
- `skills/explore/explorer.md`

**待验证**：
- [ ] 单目标探索
- [ ] 并行探索多个目标

---

### 2.2 Phase 2: 创建 Worker 技能

**文件**：
- `skills/worker/SKILL.md`
- `skills/worker/worker.md`

**核心功能**：
1. 从 `tasks.md` 读取当前任务
2. 标记 `status: in_progress`
3. 执行实现
4. 运行测试验证
5. 标记 `status: done` + 填写 `result`

**tasks.md 格式**：
```markdown
- [ ] TASK-1: 描述
  agent: worker
  status: pending
  result: |

- [ ] TASK-2: 描述
  agent: worker
  status: in_progress
  result: |
    - 修改了 xxx
    - 添加了 yyy

- [x] TASK-3: 描述
  agent: worker
  status: done
  result: |
    - 完成
```

**Worker persona**：
```markdown
# Persona: Worker

你是专门执行任务的 agent。

## 职责
- 只执行，不规划
- 不重新设计，不扩展范围
- 信任上游规划，按计划执行

## 流程
1. Read task from tasks.md
2. Mark status: in_progress
3. Implement
4. Verify (run tests)
5. Mark status: done
6. Fill result
```

---

### 2.3 Phase 3: 创建 Parallel/Chain 工具

**文件**：
- `skills/orchestrate/bin/parallel.sh`
- `skills/orchestrate/bin/chain.sh`

**parallel.sh**：
```bash
#!/bin/bash
# Usage: parallel.sh <output_dir> <timeout> <persona> <task1> <task2> ...
#
# 并行执行多个 subagent（最多 2-3 个）
# 使用 & 后台启动 + wait 等待

OUTPUT_DIR="$1"
TIMEOUT="$2"
PERSONA="$3"
shift 3
TASKS=("$@")

MAX_PARALLEL=3
SESSIONS=()

mkdir -p "$OUTPUT_DIR"

# 并行启动所有任务
for i in "${!TASKS[@]}"; do
  OUTPUT_FILE="$OUTPUT_DIR/task-$i.txt"
  
  # 使用 & 后台启动
  start_subagent_tmux.sh "$OUTPUT_FILE" "$TIMEOUT" "$PERSONA" "${TASKS[$i]}" &
  SESSIONS+=("$!")
done

# 同时等待所有后台任务
wait

# 收集结果
for i in "${!TASKS[@]}"; do
  OUTPUT_FILE="$OUTPUT_DIR/task-$i.txt"
  echo "=== Task $i ==="
  cat "$OUTPUT_FILE"
  echo ""
done
```

**chain.sh**：
```bash
#!/bin/bash
# Usage: chain.sh <timeout> <persona> <step1> <step2> ...
#
# 顺序执行多个 subagent，用 {previous} 传递结果

TIMEOUT="$1"
PERSONA="$2"
shift 2
STEPS=("$@")

PREVIOUS=""
for i in "${!STEPS[@]}"; do
  OUTPUT_FILE="/tmp/chain-step-$i.txt"
  
  # 替换 {previous}
  TASK="${STEPS[$i]}"
  if [ -n "$PREVIOUS" ]; then
    TASK=$(echo "$TASK" | sed "s/{previous}/$PREVIOUS/g")
  fi
  
  SESSION=$(start_subagent_tmux.sh "$OUTPUT_FILE" "$TIMEOUT" "$PERSONA" "$TASK")
  NAME=$(echo "$SESSION" | cut -d: -f1)
  tmux_wait.sh "$NAME" "$TIMEOUT"
  
  PREVIOUS=$(cat "$OUTPUT_FILE")
  echo "=== Step $i ==="
  cat "$OUTPUT_FILE"
  echo ""
done
```

**关键设计**：
- **parallel.sh**：使用 `&` 后台启动 + `wait` 同时等待
- **chain.sh**：顺序执行 + `{previous}` 占位符
- **MAX_PARALLEL = 3**：不超过 3 个并发

---

### 2.4 Phase 4: 改进 Orchestrate

**改进点**：

1. **使用 parallel/chain 工具**
2. **与 tasks.md 联动**
3. **进度跟踪**

**Orchestrate 流程**：
```
1. 读取 tasks.md
2. 分类任务：
   - 可并行的任务 → parallel.sh
   - 有依赖的任务 → chain.sh
   - 独立任务 → worker.sh
3. 执行并跟踪进度
4. 更新 tasks.md
```

---

## 3. 文件结构

```
skills/
├── explore/
│   ├── SKILL.md
│   └── explorer.md
├── worker/
│   ├── SKILL.md
│   └── worker.md
├── orchestrate/
│   ├── SKILL.md
│   ├── references/
│   │   ├── explorer.md
│   │   ├── researcher.md
│   │   ├── implementer.md
│   │   └── reviewer.md
│   └── bin/
│       ├── parallel.sh
│       └── chain.sh
└── review/
    ├── SKILL.md
    └── reviewer.md
```

---

## 4. 实施顺序

### Step 1: 测试 Explore（验证）
```bash
# 单目标探索
/skill:explore 探索 ~/project/pi-mono 的 subagent 实现

# 并行探索
/skill:explore 并行探索 repo1 和 repo2
```

### Step 2: 创建 Worker 技能
```bash
mkdir skills/worker/
# 创建 SKILL.md 和 worker.md
```

### Step 3: 创建 parallel/chain 工具
```bash
mkdir skills/orchestrate/bin/
# 创建 parallel.sh 和 chain.sh
```

### Step 4: 改进 Orchestrate
```bash
# 更新 SKILL.md，使用 parallel/chain 工具
# 与 tasks.md 联动
```

### Step 5: 端到端测试
```bash
# 测试完整工作流
```

---

## 5. 关键设计决策

### 5.1 Session ID 捕获

**问题**：后台启动 subagent 时，Session ID 可能捕获不到

**解决方案**：
- 使用 `start_subagent_tmux.sh` 脚本
- 脚本会自动捕获 Session ID
- 格式：`session-name:session-id`

### 5.2 并发控制

**问题**：API rate limit

**解决方案**：
- `MAX_PARALLEL = 4`
- 超过后分批执行

### 5.3 结果传递

**问题**：Chain 模式下如何传递结果

**解决方案**：
- `{previous}` 占位符
- 通过文件 + 变量注入

---

## 6. 验收标准

### Explore
- [ ] 可以探索本地代码库
- [ ] 可以并行探索多个目标
- [ ] 输出格式符合规范

### Worker
- [ ] 可以从 tasks.md 读取任务
- [ ] 可以执行并更新状态
- [ ] 可以填写 result

### Parallel/Chain
- [ ] parallel.sh 可以并行执行多个任务
- [ ] chain.sh 可以顺序执行并传递结果
- [ ] 支持最多 4 并发

### Orchestrate
- [ ] 可以使用 parallel/chain
- [ ] 可以与 tasks.md 联动
- [ ] 可以跟踪进度

---

## 7. 风险与开放问题

| 问题 | 解决方案 |
|------|----------|
| Session ID 捕获失败 | 使用 `start_subagent_tmux.sh` 脚本 |
| API rate limit | `MAX_PARALLEL = 4` |
| 结果传递格式 | `{previous}` 占位符 + 文件 |
| Worker 测试验证 | 明确要求 Worker 运行测试 |
| Review 循环 | 手动触发或配置自动化 |

---

## 8. 参考资料

- pi-config subagent: `/Users/genius/project/pi-mono/packages/coding-agent/examples/extensions/subagent/index.ts`
- subagent 技能: `/Users/genius/project/ai/skills/subagent/SKILL.md`
- tmux 技能: `/Users/genius/project/ai/skills/tmux/SKILL.md`