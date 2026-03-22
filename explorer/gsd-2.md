# Explorer: GSD-2

**Date:** 2026-03-22
**Target:** ~/project/gsd-2

## Overview

GSD-2 (Get Shit Done v2) is an **AI coding agent orchestration layer** built on the Pi SDK. It handles planning, execution, verification, and shipping — letting developers focus on what to build, not how to wrangle tools.

## Tech Stack

- **Language:** TypeScript (monorepo)
- **Core:** Pi SDK (`@gsd/pi-coding-agent`)
- **Packages:**
  - `@gsd/pi-coding-agent` - Main CLI
  - `@gsd/pi-agent-core` - Agent harness
  - `@gsd/pi-ai` - LLM API abstraction
  - `@gsd/pi-tui` - Terminal UI
  - `@gsd/native` - Rust N-API bindings

## Project Structure

```
gsd-2/
├── packages/
│   ├── pi-coding-agent/   # Main CLI entry
│   ├── pi-agent-core/     # Agent core
│   ├── pi-ai/            # LLM API
│   ├── pi-tui/           # TUI components
│   └── native/           # Rust bindings
├── src/                   # GSD extensions
│   └── resources/extensions/gsd/
├── docs/                  # Documentation
└── vscode-extension/      # VS Code plugin
```

## Core Components

### 1. pi-coding-agent
- Main CLI entry (`pi` command)
- Handles session management, tool execution
- Auto-mode for milestone completion

### 2. pi-agent-core
- Vendored from pi-mono
- Generic agent harness
- Tool registry, context management

### 3. Extensions (src/resources/extensions/gsd/)
- GSD-specific commands: `/gsd`, `/plan`, `/worktree`
- Doctor: health checks and cleanup
- Auto-mode execution

## Key Patterns

### 1. Extension-First
Core stays lean; capabilities in extensions unless requiring core integration.

### 2. Provider-Agnostic
LLM abstraction via pi-ai works with any provider.

### 3. Auto-Mode
One command to complete entire milestone without intervention.

### 4. Worktree Lifecycle
Git worktree per milestone for isolation.

## Key Findings

1. **Monorepo with vendored deps** - pi-* packages vendored from pi-mono
2. **Extension system** - GSD commands via `/gsd` extension
3. **Auto-mode** - Full milestone automation
4. **Data loss prevention** - 7 critical fixes in latest release
5. **Worktree per milestone** - Isolated git branches

## Gotchas

- Requires Node 24 LTS on Mac (Homebrew Node may be dev release)
- Root `.gsd/` files sync to worktrees on teardown
- Merge anchor verification before milestone closeout
