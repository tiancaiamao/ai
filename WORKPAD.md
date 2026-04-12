## Workpad

```
genius:/Users/genius/.symphony/workspaces/fbbbc76a-0fdb-4d84-a58d-372f5bfbf382@ed501b3
```

### Plan

- [x] 1. Create `pkg/version/version.go` with git version info via `runtime/debug`
- [x] 2. Modify `pkg/session/entries.go` — add `GitCommit` field to `SessionHeader`
- [x] 3. Modify `pkg/session/entries.go` — populate field in `newSessionHeader`
- [x] 4. Build and test pass
- [x] 5. Commit and push
- [x] 6. Create PR #132
- [x] 7. Self-review PR
- [ ] 8. Fix P1 finding: dirty suffix lost due to Settings iteration order
- [ ] 9. Re-test, commit, push
- [ ] 10. Move to Self Review (re-validate) or Human Review

### Acceptance Criteria

- [ ] `go build ./...` compiles
- [ ] `go test ./pkg/session/... -v` passes
- [ ] `go test ./pkg/version/... -v` passes
- [ ] New session `messages.jsonl` first line contains `gitCommit` field
- [ ] `omitempty` for backward compatibility
- [ ] `-dirty` suffix is correctly appended when tree is dirty

### Validation

- [x] targeted tests: `go build ./... && go test ./pkg/session/... ./pkg/version/... -v`

### Review Findings (Self Review)

**P1**: `pkg/version/version.go` init() — `vcs.modified` is processed before `vcs.revision` in alphabetical Settings order. When dirty, `GitCommit` becomes `"-dirty"`, then gets overwritten by `vcs.revision` assignment, losing the dirty suffix.

**Fix**: Two-pass approach — first collect revision, then check modified.

### Notes

- PR #132 is open: https://github.com/tiancaiamao/ai/pull/132
- Status: Moving to Address Comment to fix P1 finding