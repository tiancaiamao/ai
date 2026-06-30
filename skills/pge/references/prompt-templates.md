# Prompt Templates

## Role Mapping

| PGE Role | `--role` 参数 | 说明 |
|----------|--------------|------|
| Generator | `coder` | 实现代码 |
| Evaluator | `validator` | 独立验证 |
| Review | `coder` | 代码审查 |

具体的 `ai serve` 参数（`--name`, `--input-file`, `--id-file`, `--timeout` 等）参见 `subagent` 技能。

---

## Generator Prompt 模板

写入 `/tmp/task-{name}.md`，作为 `--input-file` 传入：

```markdown
## Task: {title}

## Context
{简要项目背景，帮助 Generator 理解代码库}

## What to Implement
{具体的实现要求，给 WHAT 不给 HOW}

## Files to Modify/Create
{明确的文件列表}

## Verification
{构建命令 + 测试命令}

## Rules
1. READ BEFORE WRITE — grep 确认 API 存在再使用
2. BUILD MUST PASS — 实现后必须构建成功
3. Output DONE: <file list> when complete
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
cd {project_dir} && git diff {start_commit}..HEAD -- '*.c' '*.h' (adapt extensions)

Look for: memory safety, GC correctness, error handling, type safety, dead code.
Write findings to .pge/review-phase{N}.md with priority levels (P0-P3).
```