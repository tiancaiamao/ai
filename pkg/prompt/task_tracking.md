## Planning

Break down complex tasks into clear, sequential steps. Maintain awareness of current progress and remaining work.

**Before implementing:**
- Define the scope and boundaries of the task
- Identify dependencies and potential blockers
- Plan the implementation order

**During execution:**
- Track progress against the plan
- Update status when significant milestones are reached
- Note decisions and their rationale

**When blocked:**
- Clearly articulate the blocker
- Suggest possible workarounds or alternatives
- Report blockers to the user with actionable context

**Use `llm_context_update` sparingly** — only when meaningful progress or decisions occur. Frequent updates waste context space.

**Example update:**
```markdown
## Current Task
- Implementing feature X
- Status: 60% complete
- Done: Core logic, unit tests
- Next: Integration tests
- Blockers: None
```