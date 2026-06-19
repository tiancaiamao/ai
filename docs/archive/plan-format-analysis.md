# Plan Format Analysis: YAML vs Single-File Markdown

## Decision

After a structured 3-round debate (proposer vs opposer), the conclusion is: **single-file Markdown** is the optimal plan format.

## Evaluation Criteria

Ranked by priority:
1. Modification pass rate (LLM edit accuracy)
2. Generation pass rate (LLM generates valid format)
3. Lint pass rate (validator catches errors)
4. Import pass rate (correct task creation)
5. Implementation cost
6. Human readability

## Why Single-File Markdown Wins

### Pain Points with YAML

1. **`description: |` block scalar** requires precise indentation across dozens of lines
2. A single indentation error corrupts the entire file
3. LLM edit (find-replace) in a 1100-line YAML file frequently damages adjacent tasks
4. `commit_message: feat(plan): add feature` fails YAML parsing (unquoted colons)

### Advantages of Markdown

1. **Zero indentation sensitivity** — task bodies are pure Markdown
2. **LLM-friendly editing** — `## T001` section boundaries are natural edit anchors
3. **Human-readable** — no YAML syntax noise in task descriptions
4. **Horizontal rule separator** — `---` visually separates tasks
5. **Frontmatter for metadata** — groups, risks, version stay in YAML (where they work well)

### Why Not Multi-File

The current `pair.sh` architecture spawns a fresh planner each round. Multi-file's "edit isolation" advantage (each task in its own file) doesn't apply when the planner regenerates the entire plan each round.

Multi-file would add:
- File creation/deletion complexity
- Cross-file reference validation
- Directory management overhead
- No benefit over single-file for the current architecture

## New Format

```markdown
---
version: "1"
metadata:
  spec_file: design.md
groups:
  - name: agent
    title: Agent loop
    tasks: [T001, T002, T003]
    commit_message: "feat(agent): description"
group_order: [agent, storage]
risks:
  - area: "..."
    risk: "..."
    mitigation: "..."
---

## T001 — Task title (3h)

**Dependencies:** none
**Group:** agent

### Goal
One concrete sentence.

### Key changes
- Specific change

### Files
- CREATE: pkg/context/types.go
- MODIFY: pkg/agent/agent.go

### Done when
- [ ] Observable behavior 1
```

## Implementation

- `plan-lint` — parses frontmatter (YAML) + task sections (Markdown)
- `ImportPlan` — reads Markdown format, falls back to legacy YAML
- `planner.md` — instructs output in Markdown format
- `reviewer.md` — validates Markdown structure and content

## Debate Context

This decision was made through a structured 3-round debate between a proposer and opposer, evaluating YAML, single-file Markdown, and multi-file Markdown approaches. The debate transcript is available in the task history.