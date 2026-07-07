# Prompt Templates

## Role Mapping

| PGE Role | `--role` 参数 | 说明 |
|---|---|---|
| Generator | `coder` | 实现代码 |
| Evaluator | `validator` | 独立验证 |
| Review | `coder` | 代码审查 |

具体的 `ai serve` 参数（`--name`, `--input-file`, `--id-file`, `--timeout` 等）参见 `subagent` 技能。

---

## Eval Report Format（Evaluator 输出格式）

Evaluator **必须**将验证结果写入 `.pge/eval-{task}.md`：

```markdown
# Eval Report: {task-name}

**Result: PASS / FAIL**

## Criteria Verification

### L1 — Structural
- [✅/⚠️/❌] <criterion> — Evidence: <actual output or observation>

### L2 — Behavioral
- [✅/⚠️/❌] <criterion> — Evidence: <actual output or observation>

## Issues Found (if any)
- <description of each failure, with enough detail for Generator to fix>
```

**评分规则：**
- ✅ = 全部通过 → 整体 PASS
- 任一 ❌ = 整体 FAIL（即使部分 ✅）
- ⚠️ = 部分达标、无法自动验证、或边界情况（不做为 FAIL 依据，但需要在 Issues 中说明）

---

## Generator Prompt 模板

写入 `/tmp/task-{name}.md`，作为 `--input-file` 传入：

```bash
ai serve --role coder --name gen-{task} --input-file /tmp/task-{name}.md
```

```markdown
## Task: {task-name}

## Context
{简要项目背景，帮助 Generator 理解代码库}
**Before starting, read `.pge/state.md` for context from previous tasks.**

## What to Implement
{具体的实现要求，给 WHAT 不给 HOW}

## Files
### Read (context, do not modify)
{文件列表 — You may grep the entire codebase for API verification}

### Write (expected changes)
{文件列表 — Kitchen Sink 检查会对比此范围}

## Verification
{构建命令 + 测试命令 — 所有命令必须通过}

## Rules
1. READ BEFORE WRITE — grep 确认 API 存在再使用
2. MODIFY ONLY WRITE FILES — 不要改动 Write 列表之外的文件
3. VERIFICATION MUST PASS — Verification 节中的所有命令（build + test）必须通过
4. Output `DONE: <file list>` when complete (file list: space-separated, relative to project root)
5. **On DONE, write to `.pge/progress.md`**: `bash -c 'echo "[$(date "+%Y-%m-%d %H:%M:%S")] GENERATOR | {task-name} DONE. Write: <file list>" >> .pge/progress.md'`
6. **BLOCKED if stuck** — 如果遇到无法解决的问题（API 不存在、需求矛盾、超出 Write 范围的关键依赖缺失），输出 `BLOCKED: <reason>`，不要猜测实现
```

---

## Evaluator Prompt 模板

写入 `/tmp/eval-{task}.md`，作为 `--input-file` 传入：

```markdown
## Task: Evaluate {task-name}

You are an INDEPENDENT evaluator. You did NOT write this code.
Critically and objectively verify each acceptance criterion.

## Spec Acceptance Criteria
{从 spec.md 复制相关 criteria}

## Instructions
1. cd {project_dir}
2. For each criterion, run the verification command YOURSELF
3. For code quality, READ the actual source files
4. Output a structured report with ✅ or ❌ for EVERY criterion, with EVIDENCE
5. For any ❌, explain what failed and what the actual behavior was
6. At the end, give overall PASS/FAIL verdict
7. Write your report to .pge/eval-{task}.md
8. **After writing report, append verdict to `.pge/progress.md`**: `bash -c 'mkdir -p .pge && echo "[$(date "+%Y-%m-%d %H:%M:%S")] EVALUATOR | {task-name} PASS/FAIL: <summary>" >> .pge/progress.md'`
```

---

## Generator Fix Prompt 模板（FAIL 后 ai send 给同一个 Generator）

当 Evaluator 返回 FAIL 时，Orchestrator 通过 `ai send` 发送给同一个 Generator：

```markdown
## Feedback from Evaluator (Round {N})
The evaluator found the following issues:
{paste relevant ❌ items from eval report}
Please fix these issues. The eval report is at .pge/eval-{task}.md.
Output DONE: <file list> when complete.
```

---

## Review Agent Prompt 模板

写入 `/tmp/review-{phase}.md`，作为 `--input-file` 传入：

```markdown
## Task: Review Phase {N} Code

Review all code changes in this phase:
cd {project_dir} && git diff HEAD

(Phase 3 的工作树修改尚未提交，`git diff HEAD` 显示自上次 commit 以来的所有变更)

Look for: memory safety, GC correctness, error handling, type safety, dead code.
Write findings to .pge/review-phase{N}.md with priority levels (P0-P3).
After writing the report, append to .pge/progress.md:
`bash -c 'echo "[$(date "+%Y-%m-%d %H:%M:%S")] REVIEW | Phase {N} done. Issues: P0=<n> P1=<n>" >> .pge/progress.md'`
```