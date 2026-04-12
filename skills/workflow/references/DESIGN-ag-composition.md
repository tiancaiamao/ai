# 编排编程模型 — 如何用 `ag` 原语组合工作流

## 核心问题

原语有了：`spawn`, `wait`, `send`, `recv`, `task`...
但谁来编排？编排者本身是什么？

```
答案：编排者本身也是一个 agent，或者一个 bash 脚本。
区别只在于：脚本是确定性的，agent 是自适应的。
但它们用的是同一套 CLI 原语。
```

## 三种编排层次

```
Level 1: Bash 脚本编排 — 固定流程，确定性
Level 2: Agent + 脚本编排 — agent 决策流程走向，脚本执行原语
Level 3: 纯 Agent 编排 — agent 自己调 CLI，完全自治
```

---

## Level 1: Bash 脚本 — 固定 pipeline

最简单，完全确定性。适合已经稳定的套路。

```bash
#!/bin/bash
# feature-pipeline.sh — 固定的 feature 开发流程
set -e

TASK="$1"
WORKSPACE="$2"

cd "$WORKSPACE"

# ===== Brainstorm =====
echo ">>> Phase: brainstorm"

# 并行探索 3 个方向
for angle in "user-needs" "technical" "alternatives"; do
  ag spawn --id "bs-$angle" \
    --system brainstormer.md \
    --input <(echo "Feature: $TASK | Angle: $angle")
done

# 收集所有结果到 channel
ag channel create bs-results
for angle in "user-needs" "technical" "alternatives"; do
  ag wait "bs-$angle"
  ag output "bs-$angle" | ag send bs-results
done

# 汇总 brainstorm 结果
ag spawn --id "bs-synth" \
  --system synthesizer.md \
  --input <(ag recv bs-results --all)
ag wait bs-synth
ag output bs-synth > brainstorm.md


# ===== Spec =====
echo ">>> Phase: spec"

# Worker-Judge: spec 写出来，reviewer 检查
ag spawn --id spec-writer \
  --system spec-writer.md \
  --input brainstorm.md

MAX=3
for i in $(seq 1 $MAX); do
  ag wait spec-writer || true
  draft=$(ag output spec-writer 2>/dev/null || ag recv spec-writer)

  ag spawn --id spec-reviewer \
    --system spec-reviewer.md \
    --input <(echo "$draft")
  ag wait spec-reviewer
  verdict=$(ag output spec-reviewer)

  if echo "$verdict" | grep -q "APPROVED"; then
    echo "$draft" > spec.md
    break
  fi

  # 需要修改：带着反馈重新来
  ag spawn --id spec-writer \
    --system spec-writer.md \
    --input <(printf "ORIGINAL DRAFT:\n%s\n\nFEEDBACK:\n%s\n" "$draft" "$verdict")
done


# ===== Plan =====
echo ">>> Phase: plan"

ag spawn --id planner \
  --system planner.md \
  --input spec.md
ag wait planner
ag output planner > plan.md


# ===== Tasks — 拆分 =====
echo ">>> Phase: decompose tasks"

ag spawn --id decomposer \
  --system decomposer.md \
  --input plan.md
ag wait decomposer

# decomposer 的输出格式约定为每行一个 task 描述
# 由脚本解析成 ag task create
while IFS= read -r task_desc; do
  ag task create "$task_desc"
done < <(ag output decomposer)


# ===== Execute — 并行 =====
echo ">>> Phase: parallel execution"

ag channel create impl-results

# 启动 worker pool
POOL_SIZE=3
for i in $(seq 1 $POOL_SIZE); do
  (
    # 每个 worker 循环认领 task
    while true; do
      task_id=$(ag task list --status pending | head -1 | awk '{print $1}')
      [ -z "$task_id" ] && break

      if ag task claim "$task_id" --as "worker-$i" 2>/dev/null; then
        desc=$(ag task show "$task_id" | grep "^description:" | cut -d' ' -f2-)

        ag spawn --id "impl-$task_id" \
          --system implementer.md \
          --input <(printf "Plan:\n%s\n\nTask: %s\n" "$(cat plan.md)" "$desc")
        ag wait "impl-$task_id"

        if ag output "impl-$task_id" > /dev/null 2>&1; then
          ag task done "$task_id"
          ag output "impl-$task_id" | ag send impl-results
        else
          ag task fail "$task_id"
        fi
      fi
    done
  ) &
done
wait


# ===== Verify =====
echo ">>> Phase: verify"

ag spawn --id verifier \
  --system verifier.md \
  --input <(cat plan.md; echo "---"; ag recv impl-results --all)
ag wait verifier

if [ $? -eq 0 ]; then
  echo "✅ Feature complete"
else
  echo "❌ Verification failed"
  exit 1
fi
```

**Level 1 的问题**：流程写死了。如果 brainstorm 不需要 3 个角度呢？如果 spec 一次就过了不需要 review 呢？如果想跳过某个阶段呢？

---

## Level 2: Agent 决策 + 脚本执行原语

把"做什么决策"交给 agent，"怎么执行"交给脚本。

核心是一个**编排 agent**，它的 system prompt 里教会它用 `ag` CLI：

```markdown
# Orchestrator Agent

你是一个编排 agent。你可以通过 bash 执行 `ag` CLI 命令来管理其他 agent。

## 你能用的命令

- `ag spawn --id <name> --system <prompt-file> --input <input>` — 启动 agent
- `ag wait <name>` — 等待完成
- `ag output <name>` — 获取结果
- `ag send <target> --file <file>` — 发消息
- `ag recv <source> --wait` — 收消息
- `ag kill <name>` — 终止
- `ag task create / claim / done / fail / list / show` — 任务管理

## 你的工作方式

1. 收到用户需求后，决定需要哪些阶段
2. 每个阶段 spawn 合适的 agent，选择合适的 system prompt
3. 用 worker-judge pattern 保证质量（需要 review 的阶段才用）
4. 并行能并行的，串行该串行的
5. 遇到问题自己决定是重试、换方案、还是问用户
```

然后用户只需要：

```bash
ag spawn --id orchestrator \
  --system orchestrator.md \
  --mode rpc

ag send orchestrator "帮我实现 feature: 用户登录支持 OAuth2"
```

编排 agent 自己决定流程。它可能：

```
orchestrator 思考: "这是个中等复杂度的 feature，需要 brainstorm 吗？
                  不需要，需求很明确。直接写 spec。"

orchestrator 执行:
  ag spawn --id spec-writer --system spec-writer.md \
    --input "用户登录支持 OAuth2..."
  ag wait spec-writer
  ag output spec-writer > spec.md

orchestrator 思考: "spec 写好了，我看看质量如何"
  ag spawn --id reviewer --system reviewer.md --input spec.md
  ag wait reviewer
  ... 读取 review 结果 ...

orchestrator 思考: "review 说缺少错误处理部分，让 spec-writer 补充"
  ag spawn --id spec-writer --system spec-writer.md \
    --input "补充错误处理..."
  ...

orchestrator 思考: "spec 过了。拆 plan。这个 feature 可以并行实现 OAuth 部分和 UI 部分"
  ag spawn --id planner --input spec.md
  ...
  # 拿到 plan 后自己拆 task
  ag task create "实现 OAuth2 后端回调"
  ag task create "实现前端 OAuth2 按钮"
  ag task create "集成测试"
  # 并行 spawn
  ag spawn --id worker-1 --input "实现 OAuth2 后端回调" &
  ag spawn --id worker-2 --input "实现前端 OAuth2 按钮" &
```

**Level 2 的优势**：流程是自适应的。不需要 brainstorm 就跳过。需要多轮 review 就多轮。并行度根据任务动态决定。

**Level 2 的风险**：orchestrator 本身是 LLM，不稳定。它可能忘了 kill agent，可能死循环，可能搞错命令语法。

**Level 2 的解法**：给 orchestrator 加约束——

```bash
# 不是直接让 agent 自由操作，而是提供一组"模式"函数
# orchestrator 调用这些函数，而不是直接调 ag CLI

# 模式函数本身就是 bash 脚本，封装了 ag CLI 的用法
worker-judge.sh "spec-writer" "spec-reviewer" "task.md" 3
parallel-explore.sh "topic" 3
fan-out-execute.sh "plan.md" 4
```

这样 orchestrator 的决策空间变小了——它只需要选"用哪个模式"，而不是"怎么组合原语"。

---

## Level 3: 纯 Agent 自治 — 每个 agent 自己调 CLI

这是最灵活但也最不可控的。每个 agent 都是 rpc 模式，自己决定什么时候 recv、什么时候 send、什么时候 claim task。

```bash
# 启动 3 个自治 agent + 1 个 reviewer
ag spawn --id dev-1 --system dev-autonomous.md --mode rpc
ag spawn --id dev-2 --system dev-autonomous.md --mode rpc
ag spawn --id dev-3 --system dev-autonomous.md --mode rpc
ag spawn --id reviewer --system reviewer-autonomous.md --mode rpc

# 把需求发给 reviewer（reviewer 充当 coordinator 角色）
ag send reviewer "Feature: OAuth2 登录，spec 在 spec.md 里"
```

其中 `dev-autonomous.md`:

```markdown
你是一个自治开发 agent。

## 你的工作循环

反复执行：

1. `ag task list --status pending` — 看有没有可做的任务
2. `ag task claim <id> --as <your-id>` — 认领一个
3. 完成任务
4. 如果遇到问题：`ag send reviewer "task xxx 遇到问题: ..."` — 求助
5. `ag task done <id>` — 完成
6. 回到 1

没有 pending task 时：`ag recv self --wait --timeout 300` — 等新消息
```

`reviewer-autonomous.md`:

```markdown
你是一个 reviewer + coordinator。

## 你的工作循环

反复执行：

1. `ag recv self --wait --timeout 60` — 等消息
2. 消息类型：
   - 开发请求 review → review 后 `ag send dev-N verdict`
   - 开发求助 → 分析后拆成新 task → `ag task create ...`
   - 新需求 → 拆成 tasks，逐个 `ag task create ...`
3. 偶尔 `ag task list` 检查进度
4. 所有 task done → `ag send user "feature 完成"` — 通知用户
```

**Level 3 目前不现实**——agent 的稳定性不够，自治能力不足。但是它是目标状态。

---

## 推荐路径

```
现在的你                        目标
───────                        ────

Level 1 写 bash 脚本    →    Level 2 agent + 函数库
用你已经跑稳的套路固化        把套路封装成可调用的函数
                            让 agent 选函数而不是选原语
```

具体步骤：

```
Step 1: 实现最小 ag CLI（Phase 1: spawn/wait/output/kill/ls）
Step 2: 用 bash 写你已有的套路脚本
          feature.sh, bugfix.sh, explore.sh
Step 3: 从脚本中提取可复用的模式函数
          worker-judge.sh, parallel-explore.sh, pipeline.sh
Step 4: 把这些函数变成一个"编排工具箱"技能
          orchestrator agent 通过技能调用这些函数
Step 5: 这时你有了 Level 2 —— agent 决策 + 函数库执行
```

Step 1-3 是纯工程，不需要 LLM 参与。Step 4-5 才引入 agent 决策。
而 Step 1-3 本身就能用——bash 脚本编排 + ag CLI，已经比现在的 subagent skill 强了。