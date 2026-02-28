# External Memory System - Design Document

## 1. Background and Goals

### 1.1 The Problem

Current coding agents typically use a "linear conversation history + summary compression" approach:

```
[System Prompt] + [Full History or Compressed Summary] + [Recent N turns]
```

This approach has several issues:
- **Context window pressure**: Even with compression, history grows unbounded
- **Information loss**: Summaries lose details and nuance
- **No selective recall**: Cannot retrieve specific past information on demand
- **Passive memory**: Agent cannot actively query its own memory

### 1.2 The Goal

Transform the traditional model into an "external memory" system:

```
[System Prompt] + [Working Memory Overview] + [Recent N turns]
                                    ↓
                          recall_memory tool
                                    ↓
         ┌──────────────────────────┴──────────────────────────┐
         ↓                                                    ↓
    detail/ (summaries + notes)                      messages.jsonl (raw)
```

**Three Layers Only**:
1. **overview.md** - Always in context window
2. **detail/** - External memory (summaries, key points, compaction results)
3. **messages.jsonl** - Raw conversation log

**Scope Clarification**:
- Working memory manages **agent context** only
- Project code, design docs, plans → **project directory** (not working memory)
- Agent's summaries and notes → working memory

### 1.3 Design Constraints

Based on our discussions:
- **No time-based indexing**: LLM recalls by relevance, not chronology
- **No temporal decay**: Technical knowledge doesn't "expire"
- **Simple first**: Start with keyword indexing, add vectors optionally
- **File-based**: Leverage existing filesystem, no heavy database dependencies

---

## 2. Architecture Overview

### 2.1 Memory Layers

```
┌─────────────────────────────────────────────────────────────┐
│                     CONTEXT WINDOW                          │
│  ┌─────────────┐  ┌─────────────────┐  ┌────────────────┐  │
│  │ System      │  │ Working Memory  │  │ Recent Turns   │  │
│  │ Prompt      │  │ Overview.md     │  │ (N messages)   │  │
│  └─────────────┘  └─────────────────┘  └────────────────┘  │
└─────────────────────────────────────────────────────────────┘
                           │
                           │ recall_memory tool
                           ↓
┌─────────────────────────────────────────────────────────────┐
│                    EXTERNAL MEMORY                          │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ detail/                                              │   │
│  │ ├── 2024-01-summary.md     (Compaction summaries)   │   │
│  │ ├── auth-design.md         (LLM-written notes)      │   │
│  │ ├── key-decisions.md       (Important decisions)    │   │
│  │ └── ...                                             │   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ messages.jsonl              (Raw conversation log)   │   │
│  └─────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
```

**Layer Responsibilities**:
- **overview.md**: Current task state, key context (always loaded)
- **detail/**: Summaries, notes, anything agent wants to remember
- **messages.jsonl**: Raw history (never loaded, only searched)

### 2.2 Data Flow

```
1. LLM Request Cycle:
   ┌─────────┐    ┌──────────────────┐    ┌─────────────┐
   │ User    │ → │ Build Context:   │ → │ Send to LLM │
   │ Message │    │ - System Prompt  │    │             │
   └─────────┘    │ - Overview.md    │    └──────┬──────┘
                  │ - Recent N turns │           │
                  └──────────────────┘           │
                                                 ↓
   ┌─────────────────────────────────────────────────────┐
   │ LLM Response                                        │
   │ - May call recall_memory tool                       │
   │ - May update overview.md via write tool             │
   │ - May write to detail/ via write tool               │
   └─────────────────────────────────────────────────────┘

2. Memory Retrieval Flow:
   LLM calls recall_memory(query)
        ↓
   Search: detail/ + messages.jsonl (via grep)
        ↓
   Return: Top-K relevant snippets with citations
        ↓
   LLM uses retrieved context for response
```

### 2.3 Scope Boundary

```
┌─────────────────────────────────────────────────────────────┐
│                   WORKING MEMORY SCOPE                      │
│                                                             │
│  ~/.ai/sessions/--<cwd>--/<session-id>/working-memory/      │
│  ├── overview.md      ← Agent context (this session)        │
│  └── detail/          ← Summaries, notes (this session)     │
│                                                             │
└─────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────┐
│                   PROJECT SCOPE (NOT working memory)        │
│                                                             │
│  <project-dir>/                                             │
│  ├── docs/              ← Design docs, architecture         │
│  ├── plans/             ← Implementation plans              │
│  ├── CLAUDE.md          ← Project instructions              │
│  └── ...                                                    │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

---

## 3. Directory Structure

### 3.1 Session Directory Layout

```
~/.ai/sessions/--<cwd>--/<session-id>/
├── messages.jsonl              # Raw conversation log
│
└── working-memory/
    ├── overview.md             # Always in context (agent state)
    └── detail/                 # External memory
        ├── 2024-01-summary.md  # Compaction summaries
        ├── auth-design.md      # LLM-written notes
        └── key-decisions.md    # Key decisions
```

**That's it. Three levels:**
1. `overview.md` - in memory
2. `detail/` - external, searchable
3. `messages.jsonl` - raw, searchable

### 3.2 File Formats

#### overview.md

```markdown
# Working Memory

## Current Task
<!-- What are you working on? -->

## Key Decisions
<!-- Important decisions made and why -->

## Known Context
<!-- Project structure, tech stack, key files -->

## Pending Issues
<!-- Blockers or open questions -->

## Recent Operations
<!-- Quick summary of recent actions -->
```

#### detail/*.md

All external memory goes here - summaries, notes, decisions:

```markdown
# [Topic or Date]

<!--
META:
- created: 2024-01-15T10:30:00Z
- keywords: ["authentication", "jwt"]
-->

## Content
(Summaries, decisions, notes - anything agent wants to remember)
```

---

## 4. Data Structures (Go)

### 4.1 Core Types

```go
// pkg/memory/types.go

package memory

// MemorySource identifies where a memory entry originates
type MemorySource string

const (
    MemorySourceDetail   MemorySource = "detail"   // Summaries + notes
    MemorySourceMessages MemorySource = "messages" // Raw conversation
)

// SearchResult represents a retrieved memory
type SearchResult struct {
    Source     MemorySource `json:"source"`
    FilePath   string       `json:"file_path,omitempty"`   // For detail
    LineNumber int          `json:"line_number,omitempty"`
    Text       string       `json:"text"`                  // Matched snippet
    Citation   string       `json:"citation"`              // Human-readable reference
}

// SearchOptions configures memory retrieval
type SearchOptions struct {
    Query   string         `json:"query"`
    Limit   int            `json:"limit"`
    Sources []MemorySource `json:"sources,omitempty"`     // Filter by source
}
```

### 4.2 Manager Interface

```go
// pkg/memory/manager.go

package memory

import "context"

// MemoryManager handles external memory operations using grep-based search
type MemoryManager struct {
    sessionDir  string
    detailDir   string
    messagesPath string
}

// NewMemoryManager creates a new memory manager
func NewMemoryManager(sessionDir string) (*MemoryManager, error) {
    return &MemoryManager{
        sessionDir:   sessionDir,
        detailDir:    filepath.Join(sessionDir, "working-memory", "detail"),
        messagesPath: filepath.Join(sessionDir, "messages.jsonl"),
    }, nil
}

// Search retrieves relevant memories using grep
func (m *MemoryManager) Search(ctx context.Context, opts SearchOptions) ([]*SearchResult, error) {
    var results []*SearchResult

    // Default: search both sources
    sources := opts.Sources
    if len(sources) == 0 {
        sources = []MemorySource{MemorySourceDetail, MemorySourceMessages}
    }

    for _, source := range sources {
        switch source {
        case MemorySourceDetail:
            results = append(results, m.grepDetail(ctx, opts.Query)...)
        case MemorySourceMessages:
            results = append(results, m.grepMessages(ctx, opts.Query)...)
        }
    }

    // Limit results
    if len(results) > opts.Limit {
        results = results[:opts.Limit]
    }

    return results, nil
}
```

---

## 5. recall_memory Tool Design

### 5.1 Tool Definition

```go
// pkg/tools/recall.go

var recallMemoryToolDef = map[string]interface{}{
    "name": "recall_memory",
    "description": `Search external memory for relevant information.

Searches:
- detail/: Summaries and notes you've written
- messages.jsonl: Raw conversation history

Returns: Matching entries with citations.`,
    "parameters": map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "query": map[string]interface{}{
                "type":        "string",
                "description": "Search query",
            },
            "scope": map[string]interface{}{
                "type":        "string",
                "enum":        []string{"all", "detail", "messages"},
                "default":     "all",
                "description": "Which sources to search",
            },
            "limit": map[string]interface{}{
                "type":        "integer",
                "default":     5,
                "description": "Maximum results",
            },
        },
        "required": []string{"query"},
    },
}
```

### 5.2 Tool Output Format

```
Found 2 result(s) for: "authentication"

[1] Source: detail
    File: detail/auth-design.md:10
    Content: Decided to use JWT tokens with refresh rotation...
    Citation: detail/auth-design.md#L10

[2] Source: messages
    Line: 45
    Content: User asked about auth flow, discussed OAuth2 vs JWT...
    Citation: messages.jsonl#L45
```

---

## 6. Search Implementation

### 6.1 Grep-Based Search

Simple grep-based search - no index maintenance needed.

```go
// pkg/memory/search.go

// grepDetail searches the detail/ directory
func (m *MemoryManager) grepDetail(ctx context.Context, query string) []*SearchResult {
    cmd := exec.CommandContext(ctx, "grep", "-r", "-i", "-n", "--", query, m.detailDir)
    output, err := cmd.Output()
    if err != nil {
        return nil
    }
    return m.parseGrepOutput(MemorySourceDetail, output)
}

// grepMessages searches messages.jsonl
func (m *MemoryManager) grepMessages(ctx context.Context, query string) []*SearchResult {
    cmd := exec.CommandContext(ctx, "grep", "-i", "-n", "--", query, m.messagesPath)
    output, err := cmd.Output()
    if err != nil {
        return nil
    }
    return m.parseMessagesGrep(output)
}

// parseGrepOutput parses "file:line:content" format
func (m *MemoryManager) parseGrepOutput(source MemorySource, output []byte) []*SearchResult {
    lines := strings.Split(string(output), "\n")
    results := make([]*SearchResult, 0)

    for _, line := range lines {
        if line == "" {
            continue
        }
        parts := strings.SplitN(line, ":", 3)
        if len(parts) < 3 {
            continue
        }

        filePath := parts[0]
        lineNum, _ := strconv.Atoi(parts[1])
        content := truncate(parts[2], 300)

        results = append(results, &SearchResult{
            Source:     source,
            FilePath:   filepath.Base(filePath),
            LineNumber: lineNum,
            Text:       content,
            Citation:   fmt.Sprintf("detail/%s#L%d", filepath.Base(filePath), lineNum),
        })
    }
    return results
}
```

### 6.2 Why Grep Works

For coding agents:
- **Exact term matching**: "authentication", "JWT", "goroutine" - grep is perfect
- **Zero write overhead**: No index to update
- **Real-time**: File changes are immediately searchable
- **Simple & reliable**: No index sync issues

### 6.3 Optional: Use ripgrep

If performance becomes an issue:

```go
cmd := exec.CommandContext(ctx, "rg", "-n", "--no-heading", "--", query, dir)
```

---

## 7. Integration Points

### 7.1 Agent Loop Integration

```go
// pkg/agent/loop.go (modifications)

type Agent struct {
    // ... existing fields
    memoryManager *memory.MemoryManager
}

func (a *Agent) buildContext() ([]Message, error) {
    var ctx []Message

    // 1. System prompt
    ctx = append(ctx, Message{Role: "system", Content: a.systemPrompt})

    // 2. Working memory overview (always loaded)
    overview, _ := a.workingMemory.Load()
    ctx = append(ctx, Message{Role: "system", Content: overview})

    // 3. Recent N turns only (no full history)
    recent := a.getRecentMessages(a.config.RecentTurnsLimit)
    ctx = append(ctx, recent...)

    return ctx, nil
}
```

### 7.2 Tool Registry Integration

```go
// pkg/tools/registry.go

func DefaultRegistry(memoryMgr *memory.MemoryManager) *Registry {
    r := NewRegistry()
    // ... existing tools
    r.Register(NewRecallMemoryTool(memoryMgr))
    return r
}
```

### 7.3 No Changes Needed

- `session.go`: messages.jsonl is already written, grep can search it
- `working_memory.go`: detail/ is already written, grep can search it

---

## 8. Implementation Phases

### Phase 1: Core (1-2 hours)

1. Create `pkg/memory/types.go` - MemorySource, SearchResult, SearchOptions
2. Create `pkg/memory/manager.go` - MemoryManager, Search method
3. Create `pkg/tools/recall.go` - recall_memory tool
4. Wire into agent

### Phase 2: Polish (optional)

1. Use ripgrep if available
2. Better snippet extraction
3. Context around matches

---

## 9. Configuration

```yaml
# config.yaml (minimal)

memory:
  enabled: true
  default_limit: 5
```

---

## 10. References

### 10.1 Projects Analyzed

| Project | Approach | Key Learnings |
|---------|----------|---------------|
| **letta-code** | File-based Markdown + Git | Simple, transparent, LLM-friendly |
| **beads** | Dolt SQL database | Version control for memory |
| **goclaw** | SQLite + sqlite-vec + embeddings | Production-ready vector search |

### 10.2 GCC Paper Insights

From "Git-Context-Controller" (arxiv:2508.00031):
- **Multi-level context**: Overview → Detail → Raw data
- **Agent-driven organization**: Let LLM decide what to remember
- **Explicit context commands**: COMMIT, BRANCH, MERGE, CONTEXT
- **Case Study 3.2.2**: Agent explored RAG but decided it wasn't necessary

Key insight: For coding agents, simple keyword-based retrieval often suffices. Complex RAG systems may be overkill.

### 10.3 What We're NOT Doing

Based on user feedback:
- ❌ Time-based indexing (LLM recalls by relevance, not time)
- ❌ Temporal decay (technical knowledge doesn't expire)
- ❌ MMR re-ranking (over-engineering for this use case)
- ❌ Complex caching (simple mtime-based caching is enough)
- ❌ Access counting (no benefit for retrieval quality)
- ❌ Keyword index files (grep is sufficient)
- ❌ Vector database (over-engineering)
- ❌ Embedding API (latency + cost)

---

## 11. Open Questions

1. **Cross-session memory**: Should memories be searchable across sessions?
   - Recommendation: Start with per-session, add cross-session later

2. **Snippet extraction**: How much context to include?
   - Recommendation: 300 chars, with line numbers for full context

---

## 12. Success Metrics

1. **Context window usage**: Should decrease as agent offloads to external memory
2. **Retrieval relevance**: Manual testing of recall_memory results
3. **Agent autonomy**: Agent successfully recalls information without hints
4. **Session continuity**: Agent maintains context across long sessions

---

## Appendix A: Detail File Example

```markdown
# Authentication Design

<!--
META:
created: 2024-01-15T10:30:00Z
keywords: ["authentication", "jwt"]
-->

## Decision

Using JWT (JSON Web Tokens) for authentication with refresh token rotation.

## Rationale

- Stateless: No server-side session storage needed
- Scalable: Works across multiple servers
- Standard: Well-understood security model
```

---

## Appendix B: Comparison

| Feature | Linear History | External Memory |
|---------|----------------|-----------------|
| Context window | Full history or summary | Overview + recent N |
| Information access | Sequential scan | Random access via grep |
| Memory persistence | Compressed summaries | Full detail preserved |
| LLM control | Passive recipient | Active retriever |
| Scalability | Degrades with length | Constant overhead |
| Index overhead | None | None (grep-based) |
| Layers | 1-2 | 3 (overview/detail/raw) |

---

*Document Version: 1.2*
*Updated: 2024-01-15*
*Changes: Simplified to 3 layers, removed archive, grep-based search*
