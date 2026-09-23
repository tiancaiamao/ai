# Prompt Templates

## Role Mapping

| PGE Role | `--role` 参数 | 说明 |
|---|---|---|
| Generator | `coder` | 实现代码 |
| Evaluator | `validator` | 独立验证 |
| Review | `reviewer` | 代码审查 |
| Orchestrator | 任意（不 spawn 自己） | 加载 pge 技能的 agent 本身，任意 role 均可充当 |

具体的 `ai serve` 参数（`--name`, `--input-file`, `--id-file`, `--timeout` 等）参见 `subagent` 技能。

---

## Generator Prompt 模板

写入 `/tmp/task-{name}.md`，作为 `--input-file` 传入。内容是**纯 wrapper**——task 文件（`.pge/tasks/task-{name}.md`）是唯一内容来源，wrapper 不复述 Goal/Scope/Acceptance，只引用它并附加流程规则，避免两处内容漂移：

```markdown
## Task: {title}

Project: {一句话项目背景 — 框架/语言/关键路径}

**Your single source of truth is `.pge/tasks/task-{name}.md` — read it first: Goal, Scope (Read/Write), Constraints, Stop Conditions, Acceptance.**
**Before starting, also read `.pge/state.md` for context from previous tasks.**

## Rules
1. READ BEFORE WRITE — grep 确认 API 存在再使用
2. STAY IN SCOPE — 只改动 task 文件 Scope.Write 列表内的文件
3. ACCEPTANCE MUST PASS — task 文件 Acceptance 节中的所有命令必须通过
4. Output `DONE: <file list>` when complete (file list: space-separated, relative to project root)
5. **DONE 回传格式** — DONE 之后必须附上结果包（简洁，每项一行）：
   - `Verified:` 本任务实际运行过的验证命令及结果
   - `Risks:` 实现中的风险与限制（如未覆盖的边界条件），无则写 `none`
   - `OpenQuestions:` 仍需 Orchestrator 确认的问题，无则写 `none`
   - 完整执行轨迹不需要回传，但支撑判断的依据（关键决策对应 spec 哪条）必须说明
6. **On DONE, write to `.pge/progress.md`**: `bash -c "mkdir -p .pge && echo \"[$(date '+%Y-%m-%d %H:%M:%S')] GENERATOR | {task-name} DONE. Write: <file list> | Risks: <...> | Open: <...>\" >> .pge/progress.md"`
7. **BLOCKED if stuck** — 命中 task 文件 Stop Conditions 节列出的任一条件（需求矛盾、API/输入不存在、需要改动 Scope.Write 之外的文件）时，输出 `BLOCKED: <reason>`，不要猜测实现。写 `.pge/` 流程日志不算越界
```

---

## Evaluator Prompt 模板

写入 `/tmp/eval-{task}.md`，作为 `--input-file` 传入：

```bash
ai serve --role validator --name eval-{task} --input-file /tmp/eval-{task}.md
```

```markdown
## Task: Evaluate {task-name}

## Spec Acceptance Criteria
{从 spec.md 复制相关 criteria}

## Instructions
1. cd {project_dir}
2. For each criterion, run the verification command YOURSELF
3. For code quality, READ the actual source files
4. Output verdict per the format in `~/.ai/skills/pge/references/eval-report-template.md` (✅/❌ per criterion + PASS/FAIL summary)
5. Write report to `.pge/eval-{task}.md`
6. **Append verdict to `.pge/progress.md`**: `bash -c "mkdir -p .pge && echo \"[$(date '+%Y-%m-%d %H:%M:%S')] EVALUATOR | {task-name} VERDICT: <PASS|FAIL> — <summary>\" >> .pge/progress.md"`
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

```bash
ai serve --role reviewer --name review-{phase} --input-file /tmp/review-{phase}.md
```

```markdown
## Task: Review Phase {N} Code

Review all code changes in this phase:
cd {project_dir} && git diff $(cat .pge/phase-start-commit)

(Step 3 各 task 已独立 commit，`git diff $(cat .pge/phase-start-commit)` 显示相位基线以来的累计变更)

Look for: memory safety, GC correctness, error handling, type safety, dead code.
Write findings to .pge/review-phase{N}.md with priority levels (P0-P3).
After writing the report, append to .pge/progress.md:
`bash -c "mkdir -p .pge && echo \"[$(date '+%Y-%m-%d %H:%M:%S')] REVIEW | Phase {N} done. Issues: P0=<n> P1=<n>\" >> .pge/progress.md"`
```
 -p .pge && echo \"[$(date '+%Y-%m-%d %H:%M:%S')] REVIEW | Phase {N} done. Issues: P0=<n> P1=<n>\" >> .pge/progress.md"`
```
