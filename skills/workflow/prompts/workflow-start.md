# Workflow Start Prompt

This prompt is sent when a workflow is initiated.

---

## Template Variables

| Variable | Description |
|----------|-------------|
| `{{templateId}}` | Template ID (e.g., bugfix, feature) |
| `{{templateName}}` | Human-readable name |
| `{{templateDescription}}` | Brief description |
| `{{phases}}` | Phase names joined with → |
| `{{complexity}}` | Estimated complexity |
| `{{artifactDir}}` | Artifact directory path |
| `{{description}}` | User-provided description |
| `{{issueRef}}` | Issue reference number |
| `{{date}}` | Current date |
| `{{workflowContent}}` | Full template content |

---

## Generated Prompt

```
# Workflow Started: {{templateName}}

## Summary
{{templateDescription}}

## Description
{{description}}

{{#if issueRef}}
## Issue Reference
{{issueRef}}
{{/if}}

## Phases
{{phases}}

**Complexity:** {{complexity}}
**Started:** {{date}}
**Artifacts:** {{artifactDir}}

---

## Your Task

Begin **Phase 1** of the workflow. Read the template below for instructions.

{{workflowContent}}

---

## Guidelines

1. Read the phase instructions carefully
2. Create required outputs in {{artifactDir}}/
3. Update .workflow/STATE.json as you advance
4. Commit after each phase completion
5. Keep the user informed of progress

## Current State

Check .workflow/STATE.json for:
- Current phase
- Completed phases
- Next actions

Let's begin Phase 1: [First Phase Name]
```