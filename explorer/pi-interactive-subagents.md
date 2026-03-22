# Explorer: pi-interactive-subagents

**Date:** 2025-03-22
**Target:** https://github.com/HazAT/pi-interactive-subagents

## Overview

This package extends the `pi` coding agent with interactive subagent orchestration capabilities. It enables spawning specialized agents (scout, planner, worker, reviewer, visual-tester) in dedicated multiplexer panes (cmux/tmux/zellij), allowing users to observe progress in real-time, collaborate with agents, and manage multi-phase workflows like `/plan` from initial brainstorming through implementation to code review.

## Tech Stack

- **Language:** TypeScript
- **Target:** Node.js with ESM modules
- **Dependencies:**
  - `@mariozechner/pi-coding-agent` — core agent framework
  - `@mariozechner/pi-tui` — terminal UI components (Box, Text)
  - `@sinclair/typebox` — schema validation
- **Terminal Multiplexers:** cmux, tmux, zellij (runtime detection)
- **External Tools:** Git (for session management)

## Project Structure

```
pi-interactive-subagents/
├── agents/                    # Bundled agent definitions (YAML frontmatter + markdown body)
│   ├── planner.md            # Interactive brainstorming agent (Opus medium)
│   ├── scout.md               # Fast reconnaissance agent (Haiku)
│   ├── worker.md              # Implementation agent (Sonnet)
│   ├── reviewer.md            # Code review agent (Opus medium)
│   └── visual-tester.md       # Visual QA via Chrome CDP (Sonnet)
├── pi-extension/
│   ├── subagents/
│   │   ├── index.ts           # Main extension: tools + commands (subagent, parallel_subagents, etc.)
│   │   ├── cmux.ts            # Multiplexer abstraction layer (cmux/tmux/zellij)
│   │   ├── session.ts         # Session file manipulation (JSONL read/write/merge)
│   │   ├── subagent-done.ts   # Subagent-side extension (tools widget, status bar, self-terminate)
│   │   └── plan-skill.md      # /plan skill content
│   └── session-artifacts/
│       └── index.ts           # write_artifact / read_artifact tools
├── test/
│   └── test.ts
├── package.json
└── README.md
```

## Core Components

### 1. Multiplexer Abstraction (`cmux.ts`)
- **File:** `pi-extension/subagents/cmux.ts`
- **Responsibility:** Unified API over cmux, tmux, and zellij for terminal pane management
- **Key APIs:**
  - `getMuxBackend()` — detect available multiplexer (env vars + command availability)
  - `createSurface(name)` / `createSurfaceSplit(name, direction)` — spawn new panes
  - `sendCommand(surface, command)` — send keystrokes to a pane
  - `readScreen(surface, lines)` / `readScreenAsync()` — capture pane contents
  - `pollForExit(surface, signal, options)` — wait for `__SUBAGENT_DONE_N__` sentinel
  - `closeSurface(surface)` — terminate a pane
  - `renameCurrentTab(title)` / `renameWorkspace(title)` — update tab/window titles

### 2. Main Extension — Tools (`index.ts`)
- **File:** `pi-extension/subagents/index.ts`
- **Responsibility:** Registers all subagent tools and commands with the pi extension API

**Tools registered:**
| Tool | Description |
|------|-------------|
| `subagent` | Spawn a sub-agent in a dedicated multiplexer pane |
| `parallel_subagents` | Run multiple autonomous agents with tiled layout |
| `subagents_list` | List available agent definitions |
| `subagent_resume` | Resume a previous session |
| `set_tab_title` | Update the current tab title |

**Commands registered:**
| Command | Description |
|---------|-------------|
| `/plan` | Start full planning workflow |
| `/iterate` | Fork current session into interactive subagent |
| `/subagent <name> <task>` | Spawn a named agent directly |

### 3. Subagent Session Management (`session.ts`)
- **File:** `pi-extension/subagents/session.ts`
- **Responsibility:** Read, write, merge, and copy JSONL session files for context sharing and session resumption
- **Key APIs:**
  - `getNewEntries(sessionFile, afterLine)` — read entries added since last checkpoint
  - `findLastAssistantMessage(entries)` — extract summary text for caller
  - `mergeNewEntries(source, target, afterLine)` — sync worker output back to main session
  - `copySessionFile(sessionFile, destDir)` — isolate parallel workers with their own session copy
  - `appendBranchSummary()` — write branch summary entries to session

### 4. Subagent-Side Extension (`subagent-done.ts`)
- **File:** `pi-extension/subagents/subagent-done.ts`
- **Responsibility:** Loaded into subagent processes; renders tools widget and handles self-termination
- **Key APIs:**
  - `subagent_done` tool — calls `ctx.shutdown()` to close session and return results
  - Tools widget (collapsible, shows available + denied tools)
  - Status bar integration showing agent identity

### 5. Session Artifacts (`session-artifacts/index.ts`)
- **File:** `pi-extension/session-artifacts/index.ts`
- **Responsibility:** Session-scoped file storage accessible across subagents
- **Key APIs:**
  - `write_artifact` — write to `~/.pi/history/<project>/artifacts/<session-id>/`
  - `read_artifact` — read by name, searching current session then historical sessions (mtime-sorted)

### 6. Bundled Agents (`.md` files in `agents/`)

Each agent definition uses YAML frontmatter for configuration + markdown body for system prompt:

| Agent | Model | Key Config |
|-------|-------|------------|
| planner | Opus 4 (medium) | spawning: default (can spawn scouts) |
| scout | Haiku 4 | tools: read,bash; spawning: false; output: context.md |
| worker | Sonnet 4-6 | tools: read,bash,write,edit; spawning: false; thinking: minimal |
| reviewer | Opus 4 (medium) | tools: read,bash; spawning: false |
| visual-tester | Sonnet 4-6 | spawning: false |

### 7. Plan Skill (`plan-skill.md`)
- **File:** `pi-extension/subagents/plan-skill.md`
- **Responsibility:** The `/plan` command skill content — orchestrates the full planning workflow
- **Flow:** Quick investigation → Spawn interactive planner → Review plan/todos → Execute sequentially → Review

## Key Patterns

### Pattern 1: Multiplexer-Backed Agent Spawning
**Location:** `index.ts` — `subagent` tool handler
```typescript
// Spawn pi in a new multiplexer pane
const surface = createSurface(name);
sendCommand(surface, `PI_SUBAGENT_NAME="${name}" PI_DENY_TOOLS="${[...denied].join(",")}" PI_SESSION_FILE="${params.sessionPath}" PI_SESSION_ID="${sessionId}" ${launchCmd}\n`);
// Poll for completion, extract summary
```
**Usage:** Every subagent runs in its own terminal pane, visible side-by-side. Parent polls screen for sentinel.

### Pattern 2: Denied Tools via Environment Variable
**Location:** `index.ts:resolveDenyTools()`, `subagent-done.ts`
```typescript
// Parent sets denied tools as comma-separated env var
PI_DENY_TOOLS="subagent,parallel_subagents,..."

// Subagent reads at startup
denied = process.env.PI_DENY_TOOLS?.split(",").filter(Boolean) ?? [];
```
**Usage:** `spawning: false` in agent frontmatter expands to all spawning tools (5 total) automatically.

### Pattern 3: Agent Discovery with Priority Chain
**Location:** `index.ts:loadAgentDefaults()`
```typescript
const paths = [
  join(process.cwd(), ".pi", "agents", `${agentName}.md`),       // 1. project-local
  join(homedir(), ".pi", "agent", "agents", `${agentName}.md`), // 2. user-global
  join(dirname(import.meta.url), "../../agents", `${agentName}.md`), // 3. package-bundled
];
```
**Usage:** Users can override any bundled agent by placing a higher-priority definition.

### Pattern 4: Session File Checkpointing
**Location:** `session.ts`
```typescript
// Before spawning worker, record line count
const beforeCount = getEntryCount(sessionPath);

// Worker appends entries to shared session file
appendFileSync(sessionFile, JSON.stringify(entry) + "\n");

// Parent reads only new entries after spawn
const newEntries = getNewEntries(sessionFile, beforeCount);
```
**Usage:** Main orchestrator reads worker output from shared JSONL file without re-parsing entire history.

### Pattern 5: Fork — Full Context Inheritance
**Location:** `index.ts` — fork handling in subagent spawn
```typescript
if (params.fork) {
  sendCommand(surface, `${cmdPrefix} pi --session ${currentSession}\n`);
} else {
  sendCommand(surface, `${cmdPrefix} pi\n`);
}
```
**Usage:** `/iterate` forks the session; subagent gets all prior conversation context.

### Pattern 6: Role-Specific Working Directories
**Location:** `index.ts` — `cwd` parameter resolution
```typescript
// cwd resolves relative to project root, pi auto-discovers .pi/ config in that folder
sendCommand(surface, `cd ${resolvedCwd} && ${launchCmd}\n`);
```
**Usage:** Each subagent can start with different system prompts, skills, and extensions based on folder.

## Key Findings

1. **Multiplexer-agnostic design**: The `cmux.ts` abstraction handles three different multiplexers via unified APIs. Backend is auto-detected but can be forced via `PI_SUBAGENT_MUX` env var.

2. **Sequential worker execution required**: The `/plan` skill explicitly notes that parallel workers will conflict on git commits. Workers must run one at a time in the same repo.

3. **Session isolation for parallel agents**: `parallel_subagents` copies the session file to each worker directory, giving each its own isolated session to avoid conflicts.

4. **Agent definitions are markdown with YAML frontmatter**: Simple and human-editable. Frontmatter keys: `name`, `description`, `model`, `tools`, `skills`, `thinking`, `deny-tools`, `spawning`, `cwd`, `output`.

5. **Artifacts are cross-session and cross-agent**: `write_artifact`/`read_artifact` persist to `~/.pi/history/<project>/artifacts/`, searchable across all historical sessions for the same project.

6. **Subagent self-termination**: Autonomous subagents call `subagent_done` tool which invokes `ctx.shutdown()`. The `__SUBAGENT_DONE_N__` sentinel (N = exit code) is written, and the parent detects it via screen polling.

7. **Tab title tracking**: `set_tab_title` updates the multiplexer tab to show phase progress (e.g., "🔍 Scout", "🔨 Worker 1/3", "🔎 Reviewer").

8. **Tools widget for subagent transparency**: Subagent-side extension renders a collapsible widget showing available and denied tools, keeping the user informed of agent capabilities.

9. **Extension discovery**: Registered in `package.json` under `pi.extensions` array, loaded automatically by pi on startup.

10. **Fish shell compatibility**: `isFishShell()` / `exitStatusVar()` handle fish's `$status` vs bash/zsh's `$?`.

## Gotchas

- **Requires a multiplexer at runtime**: Subagents will not work outside cmux/tmux/zellij. The `isMuxAvailable()` check gates all operations.

- **`spawning: false` is not default**: Only agents that shouldn't delegate need this explicitly set. Planner defaults to spawning scouts.

- **Session file path must be absolute**: The session path is passed as an env var (`PI_SESSION_FILE`) to subagents; relative paths would break.

- **Artifacts search is exhaustive**: `read_artifact` searches all historical sessions for a project (sorted by mtime) if not found in current session — can be slow for projects with many sessions.

- **Zellij pane ID detection**: Uses a file-wait loop (`waitForFile`) to get the pane ID from a temp file path — potential race condition if timing is tight.

- **Tools widget only works in subagent sessions**: The `subagent-done.ts` extension is loaded via `PI_SUBAGENT_EXTENSIONS` env var only in spawned subagents, not the main session.

- **Fork inherits full conversation context**: Using `fork: true` on a large session can create very long context, potentially hitting token limits.

- **No authentication on artifact paths**: While there's a path-escape check (`!filePath.startsWith(artifactDir)`), artifact directory is world-readable in `~/.pi/history/`.

## Relevance to Task

This repository demonstrates a complete implementation of multi-agent orchestration within a coding agent framework. Key patterns to understand:

- **Agent spawning** via multiplexer panes with environment-based configuration
- **Sequential coordination** with checkpointed session file reading
- **Parallel isolation** via session file copying
- **Role-based agents** with YAML-defined configurations and markdown system prompts
- **Cross-agent communication** via session artifacts
- **Interactive collaboration** via forked sessions where users work directly with specialized agents
- **Self-termination** with result extraction via screen polling

The `/plan` workflow is the primary demonstration: investigation → interactive planning → plan review → sequential execution → review — a full pipeline that other orchestration systems can reference.