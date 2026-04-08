# Plan: Backport to Stable + Strip Old Context Management

## Strategy
1. Base: e9b1a039 (already in worktree)
2. Strip old context management (hint-based system via system prompt)
3. Strip task tracking (overview.md-based)
4. Keep compact mechanism (it's the foundation for future mini compact)
5. Leave clean base for new mini compact implementation

## Steps

### STEP-1: Strip context_management tool + prompt
- Remove `pkg/tools/context_management.go` and test
- Remove `pkg/prompt/context_management.md`
- Remove `ContextManagementEnabled` config
- Remove all reminder injection in loop.go

### STEP-2: Strip task_tracking tool + prompt  
- Remove `pkg/tools/task_tracking.go`
- Remove `pkg/prompt/task_tracking.md`
- Remove `TaskTrackingEnabled` config
- Remove task tracking reminder in loop.go

### STEP-3: Strip ContextMgmtState + related from AgentContext
- Remove `ContextMgmtState`, `ContextMgmtSnapshot` from `pkg/context/context.go`
- Remove `AllowReminders`, `TaskTrackingState`
- Remove `runtimeMetaSnapshot` and reminder injection in loop.go

### STEP-4: Clean up loop.go
- Remove reminder-related code paths
- Remove compliance/penalty logic
- Keep compact as-is (safety net compaction)
- Simplify `buildRuntimeUserAppendix` / `buildRuntimeSystemAppendix`

### STEP-5: Clean up prompt builder
- Remove context_management and task_tracking from prompt assembly
- Verify system prompt still works

### STEP-6: Verify
- All tests pass
- Binary builds
- No broken references

## What we KEEP
- `pkg/compact/` — compact mechanism (foundation for mini compact)
- `pkg/tools/recall.go` — llm_context_recall (useful standalone tool)
- `pkg/context/` — basic context structure
- `LLMContext` (overview.md) — can be repurposed for mini compact context
- All the core agent loop logic
