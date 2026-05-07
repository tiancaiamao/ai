# Plan

**Source:** design.md

**Created:** 2025-05-08

**Progress:** 0/17 tasks (0%)
**Total Effort:** 38 hours

---

## Groups

### ToolExecutor Interface (Phase 1)

**Commit:** `refactor(agent): replace ExecutorPool fake seam with ToolExecutor interface`

- [ ] **T001**: Define ToolExecutor interface and refactor executor.go (2h) `pkg/agent/executor.go` [HIGH]
  - Rewrite pkg/agent/executor.go:
- Define ToolExecutor interface with Execute(ctx, tool, args) method
- Rename current ToolExecutor struct to concurrentToolExecutor (unexported)
- Rename ExecutorPool to ConcurrentToolExecutor (exported) wrapping concurrentToolExecutor
- Or simplify: make concurrentToolExecutor the sole implementation, delete ExecutorPool wrapper
- Ensure NewToolExecutor / NewExecutorPool constructors return ToolExecutor interface

- [ ] **T002**: Update LoopConfig and all callers to use ToolExecutor interface (1h) `pkg/agent/loop.go` [HIGH]
  - Depends on: T001
  - Update LoopConfig.Executor field type from *ExecutorPool to ToolExecutor interface.
Update DefaultLoopConfig to construct the concrete executor and return it as interface.
Update all call sites in loop.go (executeToolCalls etc.) that reference ExecutorPool.
Fix any compile errors from the type change.

- [ ] **T003**: Fix executor-related tests after interface refactor (1h) `pkg/agent/executor_test.go` [HIGH]
  - Depends on: T002
  - Update any tests in pkg/agent/*_test.go that reference *ExecutorPool or *ToolExecutor concrete types.
Ensure all existing tests compile and pass with the new interface.
Run: go test ./pkg/agent/... ./cmd/ai/... -v


### loop.go Concern Split (Phase 2)

**Commit:** `refactor(agent): split loop.go by concern layer into separate files`

- [ ] **T004**: Extract LLM retry logic into llm_retry.go (2h) `pkg/agent/llm_retry.go` [HIGH]
  - Depends on: T003
  - Create pkg/agent/llm_retry.go containing:
- streamAssistantResponseWithRetry
- shouldRetryLLMError
- jitterDelay
- classifyLLMError
- llmErrorMeta type
- Constants: defaultLLMMaxRetries, defaultRateLimitMaxRetries, defaultRetryBaseDelay, defaultRateLimitBaseDelay
All functions stay in package agent. Delete them from loop.go.
Run go build ./... to verify compilation.

- [ ] **T005**: Extract tool execution logic into tool_exec.go (2h) `pkg/agent/tool_exec.go` [HIGH]
  - Depends on: T003
  - Create pkg/agent/tool_exec.go containing:
- executeToolCalls
- buildInvalidToolArgsMessage
- isLikelyTruncatedToolArguments
- buildTruncatedToolArgsMessage
- hasToolResultNamed
All functions stay in package agent. Delete them from loop.go.
Run go build ./... to verify.

- [ ] **T006**: Extract runtime telemetry functions into runtime_meta.go (2h) `pkg/agent/runtime_meta.go` [MEDIUM]
  - Depends on: T003
  - Move all runtime* functions from loop.go into the existing pkg/agent/runtime_meta.go (or create/extend it):
- buildRuntimeUserAppendix, buildRuntimeSystemAppendix, updateRuntimeMetaSnapshot
- runtimeTokenBand, runtimeMessageBucket, runtimeSizeBucket, runtimeCountBucket
- runtimeToolOutputSizeBucket, runtimeYAMLString, collectRuntimeToolPressure
- yesNo, normalizeApprox
- extractRecentMessages, extractActiveTurnMessages, EstimateConversationTokens
- selectMessagesForLLM, insertBeforeLastUserMessage
Delete all moved functions from loop.go. Run go build ./... to verify.

- [ ] **T007**: Extract LLM streaming logic into llm_stream.go (3h) `pkg/agent/llm_stream.go` [HIGH]
  - Depends on: T004
  - Create pkg/agent/llm_stream.go containing:
- streamAssistantResponse (the ~390 line function)
This is the largest single extraction. After this, loop.go should only contain:
LoopConfig, DefaultLoopConfig, RunLoop, runInnerLoop, getEffectiveModel,
getEffectiveAPIKey, EstimateMessageTokens, isEmptyActionableResponse,
hashAny, llmAttemptFromContext, randFloat64
Run go build ./... to verify.

- [ ] **T008**: Verify loop.go split: run full test suite (1h) `pkg/agent/loop.go` [HIGH]
  - Depends on: T004, T005, T006, T007
  - After all extractions (T004-T007), verify:
- loop.go is under 700 lines (was 1981)
- All files compile: go build ./...
- All tests pass: go test ./... -v
- No behavioral regression in agent loop tests
If tests fail, fix import issues or missed references.


### Token Estimation Extraction (Phase 3)

**Commit:** `refactor(context): extract token estimation into standalone functions`

- [ ] **T009**: Extract token estimation into pkg/context/token_estimation.go (2h) `pkg/context/token_estimation.go` [MEDIUM]
  - Depends on: T008
  - Create pkg/context/token_estimation.go with package-level functions:
- EstimateTokens(systemPrompt string, tools []Tool, messages []AgentMessage) int
- EstimateToolsTokens(tools []Tool) int
- EstimateMessageTokens(msg AgentMessage) int
- EstimateTokenPercent(used, total int) int
- estimateMessageChars(msg AgentMessage) int
Add thin method wrappers on AgentContext that delegate to the new functions.
Delete original method bodies from context.go.

- [ ] **T010**: Update callers of token estimation methods (1h) `pkg/context/context.go` [MEDIUM]
  - Depends on: T009
  - Verify all callers of AgentContext.EstimateTokens() etc. still compile.
Callers: pkg/agent/loop.go, pkg/compact/compact.go, cmd/ai/rpc_handlers.go.
The thin wrappers preserve the method signature, so most callers need no change.
Run go test ./pkg/context/... ./pkg/agent/... ./pkg/compact/... -v


### Compaction Controller (Phase 4)

**Commit:** `refactor(agent): encapsulate compaction logic in CompactionController`

- [ ] **T011**: Create CompactionController in pkg/agent/compaction_controller.go (3h) `pkg/agent/compaction_controller.go` [MEDIUM]
  - Depends on: T008
  - Create CompactionController struct encapsulating:
- compactor *compact.Compactor
- agent *Agent
- onStateChange func(isCompacting bool)
Methods:
- NewCompactionController(compactor, agent, opts...) *CompactionController
- MaybeCompact(trigger string) error  — encapsulates trigger logic
- RestoreContext(sess *session.Session) error — restores from compaction summary
Move the compaction decision logic from rpc_handlers.go closures into MaybeCompact.

- [ ] **T012**: Wire CompactionController into rpc_handlers.go (2h) `cmd/ai/rpc_handlers.go` [MEDIUM]
  - Depends on: T011
  - Replace the compactBeforeRequest closure and restoreLLMContextFromCompaction closure
in rpc_handlers.go with CompactionController method calls.
Construct CompactionController in runRPC setup phase.
Verify: go test ./cmd/ai/... -v


### RPCCore Struct Extraction (Phase 5)

**Commit:** `refactor(rpc): extract runRPC god function into RPCCore struct with methods`

- [ ] **T013**: Define RPCCore struct and extract constructor + Run method (4h) `cmd/ai/rpc_core.go` [HIGH]
  - Depends on: T012
  - Create cmd/ai/rpc_core.go with:
- rpcCoreConfig struct (all params currently passed to runRPC)
- RPCCore struct with fields for agent, session, registry, server, compactionCtrl, etc.
- NewRPCCore(cfg rpcCoreConfig) *RPCCore
- Run() error — the main event loop, currently the tail of runRPC
Move stateMu, streaming state, wg, cancelFunc from local vars to struct fields.

- [ ] **T014**: Extract RPC prompt/steer/abort handlers into rpc_prompt.go (4h) `cmd/ai/rpc_prompt.go` [HIGH]
  - Depends on: T013
  - Create cmd/ai/rpc_prompt.go with methods on RPCCore:
- handlePrompt(cmd rpc.RPCCommand) error
- handleSteer(cmd rpc.RPCCommand) error
- handleAbort(cmd rpc.RPCCommand) error
- handleFollowUp(cmd rpc.RPCCommand) error
Convert closures to methods that access state via RPCCore struct fields.

- [ ] **T015**: Extract session lifecycle into rpc_session.go (3h) `cmd/ai/rpc_session.go` [MEDIUM]
  - Depends on: T013
  - Create cmd/ai/rpc_session.go with methods on RPCCore:
- createSession() error
- restoreSession(id string) error
- forkSession(forkPoint string) error
- resumeBranch(branchID string) error
- updateCheckpointManager() error
Move session-related logic from runRPC into these methods.

- [ ] **T016**: Extract slash commands into rpc_commands.go (3h) `cmd/ai/rpc_commands.go` [MEDIUM]
  - Depends on: T013
  - Create cmd/ai/rpc_commands.go with methods on RPCCore:
- handleSlashCommand(input string) error
- Individual command handler methods (compact, skills, model, etc.)
Move slash command dispatch from runRPC into these methods.

- [ ] **T017**: Slim down rpc_handlers.go to entry point only (2h) `cmd/ai/rpc_handlers.go` [HIGH]
  - Depends on: T014, T015, T016
  - After all extractions (T013-T016), rpc_handlers.go should only contain:
- runRPC() function that calls NewRPCCore + core.Run()
- bashRunner helper (or move to separate file)
- getWorkflowStatus helper
Target: rpc_handlers.go under 100 lines.
Final verification: go test ./... -v


## Risks

### 1. Compilation

**Risk:** Function moves between files may miss references or cause import cycles

**Mitigation:** Each task runs go build ./... immediately after move; all functions stay in same package

### 2. Test regression

**Risk:** Existing integration tests may break due to type changes (ExecutorPool → ToolExecutor)

**Mitigation:** T003 specifically addresses test fixes; full suite run at T008 gate

### 3. Behavior regression

**Risk:** Subtle behavior change during RPCCore extraction (closure capture vs struct field access)

**Mitigation:** Phase 5 is last; phases 1-4 are zero-behavior-change; each group has its own test gate

### 4. Scope creep

**Risk:** RPCCore extraction (Phase 5) may reveal additional coupling not visible in design

**Mitigation:** Can stop after any phase; each group is independently valuable and committable

