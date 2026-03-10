---
name: orchestrate
description: Automatically analyze tasks, decompose complex ones, and coordinate subagents to complete them. Users only see final results.
tools: [bash]
---

# Orchestrate Skill

Automatically analyze user tasks, determine if decomposition is needed, spawn appropriate subagents with suitable personas, and aggregate results back to the user.

## ⚠️ CRITICAL RULES (READ FIRST)

```
1. NEVER use --no-session by default
   → Sessions are needed for debugging subagent behavior

2. ALWAYS use --subagent-timeout to prevent runaway subagents
   → Recommended: 5-10 minutes for most tasks

3. ALWAYS use --system-prompt @{persona-path} with appropriate persona
   → Personas are in: skills/orchestrate/references/

4. ALWAYS use absolute paths for persona files
   → /Users/genius/.ai/skills/orchestrate/references/{persona}.md
```

## Subagent 调用

**重要**: 所有 subagent 调用请参考 `/skill:subagent` 技能的最佳实践。

核心要点：
- **必须**使用 persona：`--system-prompt @{persona-path}`
- **必须**设置 timeout：`--subagent-timeout 10m`
- **必须**后台运行并收集结果（bash 工具有 30s 超时限制）
- **不要**使用 `--no-session`

Persona 路径: `/Users/genius/.ai/skills/orchestrate/references/{persona}.md`

详细调试、监控、结果收集方法见 subagent 技能的 **Debugging & Monitoring** 章节。

## Persona Selection (MANDATORY)

**Always load a persona.** Choose based on task type:

| Persona | File | Use When | Keywords |
|---------|------|----------|----------|
| **Explorer** | `explorer.md` | Understanding code, finding patterns | analyze, explore, understand, find |
| **Researcher** | `researcher.md` | Gathering info, investigating | research, investigate, compare |
| **Implementer** | `implementer.md` | Building features, writing code | implement, build, create, fix |
| **Reviewer** | `reviewer.md` | Validating, checking quality | review, check, validate, audit |

## Task Decomposition

### Complexity Analysis

**Simple task** (execute directly, no decomposition):
- Single file modification
- Quick lookup or question
- Straightforward implementation

**Complex task** (decompose into subagents):
- Multiple files/directories
- Multi-step process (research → implement → review)
- Parallel analysis of independent targets
- Requires different expertise phases

### Decomposition Patterns

**Pattern A: Sequential Phases**
```
Task: "Add user authentication"

Phase 1 (researcher): Research auth best practices
Phase 2 (implementer): Implement based on research
Phase 3 (reviewer): Review for security issues
```

**Pattern B: Parallel Analysis**
```
Task: "Compare three projects' architecture"

Subagent 1 (explorer): Analyze project A
Subagent 2 (explorer): Analyze project B
Subagent 3 (explorer): Analyze project C
→ Aggregate and compare
```

**Pattern C: Explore + Fix**
```
Task: "Fix the memory leak"

Phase 1 (explorer): Find leak sources
Phase 2 (implementer): Fix identified issues
```

## Parallel Execution

For independent tasks, run subagents in parallel (参考 `/skill:subagent`):

关键点：
- 最大并行数: 2 subagents（API rate limit 保护）
- 启动间隔: 5s delay（防止请求突发）
- 后台运行: `(...) &` + `> /tmp/out.txt`
- 收集结果: `wait` + `cat /tmp/*.txt`

## Result Aggregation

After subagents complete, synthesize results:

```markdown
## Summary
<Brief overall summary>

## Detailed Results

### Phase 1: Research
<researcher output>

### Phase 2: Implementation
<implementer output>

### Phase 3: Review
<reviewer output>

## Next Steps
<If any follow-up needed>
```

## Complete Examples

### Example 1: Parallel Project Analysis

```bash
# User: "Compare mission-control and oh-my-openagent's agent orchestration"

# Decomposition: 2 parallel explorers + aggregate

# Launch parallel analysis
(ai --mode headless --subagent \
  --subagent-timeout 10m \
  --system-prompt @/Users/genius/.ai/skills/orchestrate/references/explorer.md \
  "Analyze mission-control's agent orchestration. Find: scheduler, dispatcher, task queue, concurrency handling." \
  > /tmp/mc.txt) &

sleep 5

(ai --mode headless --subagent \
  --subagent-timeout 10m \
  --system-prompt @/Users/genius/.ai/skills/orchestrate/references/explorer.md \
  "Analyze oh-my-openagent's agent orchestration. Find: delegate-task, background-task, sync-task, model fallback." \
  > /tmp/omo.txt) &

wait

# Aggregate and compare
echo "## mission-control\n$(cat /tmp/mc.txt)"
echo "## oh-my-openagent\n$(cat /tmp/omo.txt)"
echo "## Comparison\n<Key differences and similarities>"
```

### Example 2: Feature Implementation Pipeline

```bash
# User: "Add OAuth2 login to the app"

# Phase 1: Research
ai --mode headless --subagent \
  --subagent-timeout 10m \
  --system-prompt @/Users/genius/.ai/skills/orchestrate/references/researcher.md \
  "Research OAuth2 implementation for Go web apps. Find: libraries, flows, security considerations." \
  > /tmp/research.txt

# Phase 2: Implement (pass research findings)
ai --mode headless --subagent \
  --subagent-timeout 15m \
  --system-prompt @/Users/genius/.ai/skills/orchestrate/references/implementer.md \
  "Implement OAuth2 login. Research findings: $(cat /tmp/research.txt)" \
  > /tmp/implement.txt

# Phase 3: Review
ai --mode headless --subagent \
  --subagent-timeout 10m \
  --system-prompt @/Users/genius/.ai/skills/orchestrate/references/reviewer.md \
  "Review OAuth2 implementation for security issues" \
  > /tmp/review.txt

# Return aggregated result
```

## Best Practices

- ✅ **Always** use persona with `--system-prompt`
- ✅ **Always** add `--subagent-timeout` (5-15m typical)
- ✅ **Never** use `--no-session` (lose debugging info)
- ✅ Use absolute paths for persona files
- ✅ Run independent tasks in parallel with 5s delay
- ✅ Pass context between sequential phases
- ✅ Aggregate results into coherent summary
- ❌ Don't decompose trivial tasks
- ❌ Don't spawn >2 parallel subagents
- ❌ Don't launch multiple subagents without delay (rate limit risk)
- ❌ Don't forget to aggregate results

## Configuration

| Setting | Value |
|---------|-------|
| Persona directory | `/Users/genius/.ai/skills/orchestrate/references/` |
| Max parallel subagents | 2 (rate limit protection) |
| Launch delay | 5s between subagents |
| Default timeout | 10m per subagent |
| Session | Enabled (for debugging) |

## Troubleshooting

| Problem | Solution |
|---------|----------|
| Can't find session for debugging | Check `--no-session` not used. Sessions: `~/.ai/sessions/--<cwd>--/subagents/<id>/messages.jsonl` |
| Subagent hangs/timeout | Add `--subagent-timeout 10m` to prevent runaway |
| Poor quality output | Ensure persona is loaded via `--system-prompt` |
| Inconsistent results | Check persona matches task type |
| Resource exhaustion | Reduce parallel subagents to 2, ensure 5s delay |