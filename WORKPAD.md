## Workpad

```text
geniusdeMBP:/Users/genius/.symphony/workspaces/d02bcbf7-ac49-4fd6-9196-143bb04b2bd8@53c5a37
```

### Plan

- [ ] 1. Remove config fields (`TaskTracking`, `ContextManagement`) and test
  - [ ] 1.1 `pkg/config/config.go`: Remove fields, defaults, `ToLoopConfig` references
  - [ ] 1.2 `pkg/config/config_test.go`: Remove `TestTaskTrackingContextManagementDefaults`
- [ ] 2. Remove prompt files and embeddings
  - [ ] 2.1 Delete `pkg/prompt/context_management.md`
  - [ ] 2.2 Delete `pkg/prompt/task_tracking.md`
  - [ ] 2.3 Delete `pkg/prompt/context_mgmt.go`
  - [ ] 2.4 `pkg/prompt/builder.go`: Remove embed vars, `LLMContextInfo`, setters, task tracking content, placeholders
  - [ ] 2.5 `pkg/prompt/prompt.md`: Remove `%TASK_TRACKING_CONTENT%` and `%CONTEXT_MANAGEMENT_CONTENT%`
- [ ] 3. Delete `pkg/tools/context_mgmt/` directory
- [ ] 4. Remove agent loop config and methods
  - [ ] 4.1 `pkg/agent/loop.go`: Remove `TaskTrackingEnabled`/`ContextManagementEnabled` from `LoopConfig` and `DefaultLoopConfig`
  - [ ] 4.2 `pkg/agent/agent.go`: Remove `SetTaskTrackingEnabled()` and `SetContextManagementEnabled()`
- [ ] 5. Remove session llm-context helpers
  - [ ] 5.1 `pkg/session/llm_context.go`: Remove entire file
  - [ ] 5.2 `pkg/session/manager.go`: Remove `EnsureLLMContext` call and llm-context copy logic
  - [ ] 5.3 `pkg/session/compaction.go`: Remove `backupPreCompact` if it references llm-context
- [ ] 6. Remove tag parser test cases for `context_management` and `task_tracking`
  - [ ] 6.1 `pkg/agent/tool_tag_parser.go`: Remove from `supportedToolTags`, parse cases, validation cases
  - [ ] 6.2 `pkg/agent/tool_tag_parser_test.go`: Remove test cases
- [ ] 7. Remove entry point references
  - [ ] 7.1 `cmd/ai/rpc_handlers.go`: Remove `SetTaskTrackingEnabled`/`SetContextManagementEnabled` calls
  - [ ] 7.2 `cmd/ai/json_mode.go`: Same
  - [ ] 7.3 `cmd/ai/headless_mode.go`: Same + effective config logic
- [ ] 8. Clean up context/journal references
  - [ ] 8.1 `pkg/context/journal.go`: Update `Trigger` comment (keep field, it's used by other callers)
  - [ ] 8.2 `pkg/context/memory_manager.go`: Keep as-is (detail/ is still used)
- [ ] 9. Clean up trace events
  - [ ] 9.1 `pkg/traceevent/config.go`: Remove legacy events from defaults (keep bit positions)
- [ ] 10. Build & test verification

### Acceptance Criteria

- [ ] `go build ./...` compiles without error
- [ ] All targeted tests pass
- [ ] `go vet ./...` passes
- [ ] No references to removed code remain

### Validation

- [ ] targeted tests: `go build ./... && go vet ./... && go test ./pkg/config/... ./pkg/prompt/... ./pkg/agent/... ./pkg/session/... ./cmd/ai/... -v`

### Notes

- Started: 2025-01-XX