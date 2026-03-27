# Workflow Commands

Command handlers for `/workflow` prefix commands.

## Command Parsing

```
/workflow <action> [args...]

Actions:
  start <template> [description]  Start a workflow
  auto                          Execute current workflow
  status                        Show workflow state
  templates [info <name>]       List or show template info
  pause                         Pause auto mode
  resume                        Resume paused workflow
  stop                          Stop workflow
```

## Template Resolution

### resolveByName(id)

Find template by exact ID or alias:

```typescript
const aliases: Record<string, string> = {
  "bug": "bugfix",
  "fix": "bugfix",
  "hot": "hotfix",
  "feature": "feature",
  "feat": "feature",
  "research": "spike",
  "refactor": "refactor",
  "security": "security",
};
```

### autoDetect(description)

Guess template from keywords:

```typescript
function autoDetect(desc: string): string {
  const lower = desc.toLowerCase();
  
  if (/bug|issue|error|wrong|broken|fix/.test(lower)) return "bugfix";
  if (/hot|emergency|prod|urgent/.test(lower)) return "hotfix";
  if (/security|vulnerability|exploit/.test(lower)) return "security";
  if (/research|explore|spike|investigate/.test(lower)) return "spike";
  if (/refactor|restructure|cleanup|technical.?debt/.test(lower)) return "refactor";
  
  return "feature"; // default
}
```

### loadTemplate(id)

Load template from `templates/<id>.md`:

```markdown
---
id: bugfix
name: Bug Fix
description: Fix bugs with root-cause analysis
phases: [triage, fix, verify, ship]
complexity: low
estimated_tasks: 2-4
---

# Bug Fix Workflow

## Phase 1: Triage

### Goals
- Reproduce the issue
- Identify root cause
- Document findings

### Output
Write findings to triage.md in artifact directory.

## Phase 2: Fix

### Goals
- Implement fix based on triage
- Write or update tests
- Ensure no regressions

## Phase 3: Verify

### Goals
- Run test suite
- Verify fix works
- Check for edge cases

## Phase 4: Ship

### Goals
- Commit changes
- Create PR if applicable
- Notify stakeholders
```

## State Management

### Write State

```typescript
function writeWorkflowState(
  artifactDir: string,
  templateId: string,
  templateName: string,
  phases: string[],
  description: string,
): void {
  const state = {
    template: templateId,
    templateName,
    description,
    phases: phases.map((p, i) => ({
      name: p,
      index: i,
      status: i === 0 ? "active" : "pending",
    })),
    currentPhase: 0,
    startedAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
    artifactDir,
  };
  
  writeFileSync(".workflow/STATE.json", JSON.stringify(state, null, 2));
}
```

### Read State

```typescript
function readWorkflowState(): WorkflowState | null {
  const path = ".workflow/STATE.json";
  if (!existsSync(path)) return null;
  return JSON.parse(readFileSync(path, "utf-8"));
}
```

### Update Phase

```typescript
function advancePhase(): void {
  const state = readWorkflowState();
  if (!state) return;
  
  // Mark current as completed
  state.phases[state.currentPhase].status = "completed";
  
  // Advance to next
  if (state.currentPhase < state.phases.length - 1) {
    state.currentPhase++;
    state.phases[state.currentPhase].status = "active";
  }
  
  state.updatedAt = new Date().toISOString();
  writeWorkflowState(state);
}
```

## Artifact Directory

### Naming Convention

```
.workflow/<category>/<YYMMDD>-<num>-<slug>/

Examples:
  .workflow/bugfixes/250327-1-login-timeout/
  .workflow/features/250326-1-user-auth/
  .workflow/spikes/250325-1-api-research/
```

### Creation

```bash
# Generate slug from description
slug = description.toLowerCase()
       .replace(/[^a-z0-9]+/g, "-")
       .slice(0, 40)

# Get next workflow number for category
num = max(existing nums) + 1
```

## Workflow Start Flow

```
1. Parse command: /workflow start bugfix "login timeout"
   ↓
2. Resolve template: "bugfix" → BugfixTemplate
   ↓
3. Auto-detect if no template: analyze description
   ↓
4. Create artifact directory
   ↓
5. Write STATE.json
   ↓
6. Load workflow-start.md prompt
   ↓
7. Send custom message with workflow prompt
   ↓
8. Main agent begins triage phase
```

## Auto Mode Flow

```
1. Read STATE.json
   ↓
2. Find current active phase
   ↓
3. Execute phase (via subagent or main agent)
   ↓
4. Review output
   ↓
5. If approved: advancePhase() → commit
   ↓
6. If failed: retry (max 3)
   ↓
7. Repeat until all phases done
   ↓
8. Mark workflow complete
```

## Review Checkpoints

After each phase, reviewer checks:

| Phase | Review Criteria |
|-------|----------------|
| triage | Root cause identified? Reproducible? |
| fix | Fix addresses root cause? Tests pass? |
| verify | All tests pass? Edge cases covered? |
| ship | PR ready? Changelog updated? |

## Error Recovery

### Phase Retry

```typescript
const MAX_RETRIES = 3;

async function executePhase(phase: Phase): Promise<boolean> {
  for (let attempt = 1; attempt <= MAX_RETRIES; attempt++) {
    try {
      await runPhase(phase);
      const review = await reviewPhase(phase);
      
      if (review.status === "APPROVED") {
        return true;
      }
      
      // Fix feedback and retry
      await applyFeedback(review.feedback);
    } catch (err) {
      if (attempt === MAX_RETRIES) throw err;
      await delay(1000 * attempt); // backoff
    }
  }
  return false;
}
```

### Abort Workflow

```typescript
function abortWorkflow(reason: string): void {
  const state = readWorkflowState();
  state.status = "aborted";
  state.abortedAt = new Date().toISOString();
  state.abortReason = reason;
  writeWorkflowState(state);
  
  // Cleanup resources
  cleanupTmuxSessions();
  notifyUser(`Workflow aborted: ${reason}`);
}
```

## Integration Points

### With subagent skill

```bash
# Execute phase in isolated subagent
SESSION=$(start_subagent_tmux.sh \
  /tmp/phase-output.txt \
  15m \
  @phase-worker.md \
  "Execute ${phase} phase. Read ${artifactDir}/instructions.md")

tmux_wait.sh "$(echo $SESSION | cut -d: -f1)" 900
```

### With tmux skill

```bash
# Monitor auto mode execution
tmux new -s workflow-monitor
# Panels: status, logs, progress
```

### With review skill

```bash
# Review phase output
# phase-reviewer checks against criteria
```