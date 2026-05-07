# Plan

**Source:** docs/design/claw-rpc-refactor.md

**Created:** 2025-07-11

**Progress:** 0/9 tasks (0%)
**Total Effort:** 25 hours

---

## Groups

### RPC Client Infrastructure

Build the RPCConn and ConnManager that manage ai subprocess connections

**Commit:** `feat(claw): add RPC client for ai subprocess communication`

- [ ] **T001**: Implement RPCConn — single subprocess connection manager (4h) `claw/pkg/adapter/rpc_client.go` [HIGH]
  - Create claw/pkg/adapter/rpc_client.go with RPCConn struct.
Manages a single ai --mode rpc subprocess: start, stdin write, stdout scan, request-response matching, event channel.
StartRPC(sessionKey, sessionsDir, systemPromptFile) launches ai subprocess with --session and --system-prompt flags.
Close() sends quit and kills process.
Prompt(ctx, message) sends prompt command, collects turn_end text until agent_end, returns concatenated response.

- [ ] **T002**: Implement ConnManager — multi-connection pool (2h) `claw/pkg/adapter/rpc_client.go` [HIGH]
  - Depends on: T001
  - Add ConnManager to rpc_client.go (same file).
Manages map[sessionKey]*RPCConn with lazy creation.
Prompt(ctx, sessionKey, message) delegates to getOrCreateConn, handles restart-on-failure with single retry.
CloseAll() shuts down all connections.

- [ ] **T003**: Write unit tests for RPCConn protocol parsing (3h) `claw/pkg/adapter/rpc_client_test.go` [HIGH]
  - Depends on: T001
  - Create claw/pkg/adapter/rpc_client_test.go.
Test RPCConn with mock stdin/stdout pipes:
- Test StartRPC constructs correct command args
- Test sendRequest writes valid JSON to stdin
- Test readLoop dispatches response by ID and events by type
- Test Prompt collects turn_end text and returns on agent_end
- Test error detection (stdout EOF) triggers reconnect


### Replace AgentLoop Core with RPC Client

Rewrite processMessage, remove duplicate agent/session/compact code, implement command routing

**Commit:** `feat(claw): replace agent loop with RPC client, remove duplicate code`

- [ ] **T004**: Rewrite AgentLoop.processMessage to use ConnManager (4h) `claw/pkg/adapter/adapter.go` [HIGH]
  - Depends on: T002
  - Replace agent.Agent call chain in processMessage with connManager.Prompt().
Remove getOrCreateSession, createSession, loopConfig methods.
AgentLoop holds *ConnManager instead of map[string]*Session.
Voice transcription remains in claw layer, passes text to connManager.Prompt().
Event collection moves into RPCConn (buffer to agent_end).

- [ ] **T005**: Remove claw-side agent/session/compact code (3h) `claw/pkg/adapter/adapter.go` [HIGH]
  - Depends on: T004
  - Delete from adapter.go:
- clawCompactor struct and methods (ShouldCompact, Compact, CalculateDynamicThreshold, EstimateContextTokens)
- resolveModel function and models.json loading
- createSession, getOrCreateSession
- Session struct (replaced by conn-per-sessionKey)
- All cmd* methods that duplicate ai slash commands (cmdHelp, cmdModel, cmdSession, cmdHistory, cmdClear, cmdTraceevent, cmdShow, cmdThinking, SwitchModel, listModels, saveModelConfig, etc.)
- loopConfig
- ReloadSkills, GetSkills, RefreshAPIKey

- [ ] **T006**: Implement slash command routing in claw (2h) `claw/pkg/adapter/adapter.go` [MEDIUM]
  - Depends on: T004
  - Claw registers only local commands: /cron, /skills reload, /help.
processMessage checks whitelist first, then forwards everything else as connManager.Prompt(sessionKey, message).
/help in claw outputs text format listing local commands + note that other commands go to ai.
Update registerBuiltinCommands to only register local set.

- [ ] **T007**: Update main.go for new AgentLoop wiring (3h) `claw/cmd/aiclaw/main.go` [HIGH]
  - Depends on: T004, T005
  - Update claw/cmd/aiclaw/main.go:
- Replace agent/session creation with ConnManager initialization
- Simplify buildSystemPrompt to identity-only (no skills list embedding)
- Skills registration moves to binary layer as tool registration (future), remove from system prompt
- Cron handler uses connManager.Prompt instead of agentLoop.ProcessDirect
- Remove model/config loading that's now handled by ai


### Cleanup and Integration Verification

Clean imports/dependencies, verify end-to-end functionality

**Commit:** `chore(claw): clean up imports and verify integration`

- [ ] **T008**: Clean up imports and dependencies (2h) `claw/go.mod` [MEDIUM]
  - Depends on: T005, T007
  - After all adapter changes:
- Remove agent, session, compact, agentctx imports from adapter.go
- Remove modelselect, llm config imports
- Clean go.mod if possible (remove unused direct deps)
- Verify claw compiles and basic tests pass

- [ ] **T009**: Integration test — end-to-end claw → ai subprocess (2h) `claw/pkg/adapter/rpc_client_test.go` [HIGH]
  - Depends on: T006, T007, T008
  - Manual integration verification:
- Start claw, send message to a session, verify response comes back
- Test /cron trigger sends message through ai subprocess
- Kill ai subprocess, verify retry on next message
- Verify session persistence (restart ai subprocess, check history)
- Verify voice transcription still works (transcribe in claw, text to ai)


## Risks

### 1. Protocol mismatch

**Risk:** ai RPC protocol may have edge cases not covered by mock tests

**Mitigation:** Integration test T009 verifies real subprocess communication

### 2. Session state loss

**Risk:** Switching from in-memory Session to subprocess-based may lose state during migration

**Mitigation:** ai handles session persistence to disk; restart recovers from disk

### 3. Breaking claw functionality

**Risk:** Large adapter.go rewrite may break existing claw features

**Mitigation:** Incremental groups; rpc-infra tested independently before adapter-rewrite

### 4. Subprocess startup latency

**Risk:** First message to each session has ~100-200ms subprocess startup overhead

**Mitigation:** Accepted per design decision Q2; lazy start + never close

