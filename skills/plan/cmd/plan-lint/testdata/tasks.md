---
version: "1"
metadata:
  spec_file: design.md
groups:
  - name: parser
    title: Markdown Parser
    tasks: [T001, T002]
    commit_message: "feat(plan): add Markdown plan format parser"
  - name: importer
    title: Plan Importer
    tasks: [T003]
    commit_message: "feat(plan): update importer for Markdown format"
group_order: [parser, importer]
risks:
  - area: "Backward compatibility"
    risk: "Existing YAML plans may break"
    mitigation: "Keep YAML parser as fallback, detect format by frontmatter presence"
---

## T001 — Implement Markdown frontmatter parser (3h)

**Dependencies:** none
**Group:** parser

### Goal
Extract YAML frontmatter from Markdown files and parse it into structured metadata.

### Key changes
- Add extractFrontmatter function to split --- delimited sections
- Parse frontmatter YAML into Plan struct (version, metadata, groups, group_order, risks)
- Validate frontmatter structure and required fields

### Files
- MODIFY: skills/plan/cmd/plan-lint/main.go

### Done when
- [ ] Frontmatter extraction correctly splits YAML from Markdown body
- [ ] Missing closing --- produces clear error message
- [ ] Empty frontmatter produces validation error

---

## T002 — Implement Markdown task section parser (4h)

**Dependencies:** T001
**Group:** parser

### Goal
Parse task sections from Markdown body, extracting ID, title, hours, dependencies, group, and body content.

### Key changes
- Split Markdown body on `## Txxx` pattern to extract task sections
- Parse task header with regex: `## T001 — Title (3h)`
- Extract `**Dependencies:**` and `**Group:**` metadata lines
- Collect remaining lines as task description body

### Files
- MODIFY: skills/plan/cmd/plan-lint/main.go

### Done when
- [ ] Task header regex matches `## T001 — Title (3h)` and `## T001 - Title (3h)`
- [ ] Dependencies line parsed correctly: comma-separated IDs or "none"
- [ ] Group line parsed correctly
- [ ] Task body contains Goal, Key changes, Files, Done when sections

---

## T003 — Update ImportPlan for Markdown format (2h)

**Dependencies:** T002
**Group:** importer

### Goal
Update ImportPlan function to read Markdown plan files instead of YAML-only files.

### Key changes
- Parse Markdown frontmatter to extract task metadata
- Parse task sections from Markdown body
- Create tasks using existing CreateWithID and AddDependency functions

### Files
- MODIFY: skills/ag/internal/task/task.go

### Done when
- [ ] ImportPlan reads Markdown files with frontmatter
- [ ] Tasks created with correct ID, title, description, dependencies, and group
- [ ] Import fails gracefully on invalid Markdown format