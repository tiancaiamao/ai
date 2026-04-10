# ADR 001: RPC-First Design

**Status:** Accepted
**Date:** 2024-01-15
**Context:** Initial architecture decision

## Context

When designing the agent system, we needed to choose the interface between the agent and external clients (editors, CLIs, etc.).

Options considered:
1. CLI-based interface (subprocess spawning)
2. RPC-based interface (stdin/stdout JSON-RPC)
3. HTTP API (REST/GraphQL)

## Decision

Chose RPC over stdin/stdout using JSON-RPC protocol.

## Rationale

### Advantages of RPC over stdin/stdout

1. **Standard Interface**
   - JSON-RPC is a well-established protocol
   - Language-agnostic - works with any editor (VS Code, Neovim, JetBrains, etc.)
   - Easy to add new RPC commands without breaking compatibility

2. **Separation of Concerns**
   - Agent core focuses on logic, not communication
   - Client code is separate and independent
   - Clear contract between client and server

3. **Testability**
   - Can mock RPC calls in tests
   - No subprocess spawning complexity
   - Easier to test agent in isolation

4. **Editor Integration**
   - Many editors already support stdin/stdout communication
   - Similar architecture to LSP (Language Server Protocol)
   - Low overhead for real-time interactions

### Disadvantages

1. **Requires External Client**
   - Need separate client binary for direct usage
   - More moving parts in the system

2. **Mixed Output**
   - RPC responses and debug output share stdout/stderr
   - Need careful handling to avoid confusion

### Rejected Alternatives

#### CLI-Based Interface

**Rejected because:**
- Too editor-specific (hard to integrate with different editors)
- Subprocess spawning adds complexity
- Harder to test (need to spawn processes)
- Less clean separation of concerns

#### HTTP API

**Rejected because:**
- Overkill for local tool (adds network overhead)
- Requires port management
- More complex deployment (need to ensure server is running)
- Unnecessary for single-machine use case

## Consequences

### Positive

- Clean protocol (JSON-RPC)
- Easy to add new RPC commands
- Better testability
- Language-agnostic editor support
- Clear separation of concerns

### Negative

- Requires client implementation
- stderr/stdout mixed with RPC output
- More components to maintain

### Mitigations

- Provide simple CLI wrapper for direct usage
- Use structured logging for debug output
- Maintain backwards-compatible RPC protocol
- Document RPC interface clearly

## References

- Related: ADR 002 (Stateful Agent)
- Related: ADR 003 (Tmux for Subagents)
- JSON-RPC 2.0 Specification: https://www.jsonrpc.org/specification
- Language Server Protocol: https://microsoft.github.io/language-server-protocol/