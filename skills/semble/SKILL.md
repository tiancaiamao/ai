---
name: semble
description: Fast code search using Semble. Use when you need to understand a codebase, find implementations, locate symbols, or explore unfamiliar code. Replaces grep+read for exploratory searches with ~98% fewer tokens. Works fully offline after first model download.
categories: [code-search, exploration, tools]
triggers:
  - search code
  - find implementation
  - understand codebase
  - explore code
  - where is something implemented
  - how does something work
  - semble search
---

# Semble: Fast Code Search for Agents

Use Semble to search code by natural language or symbol name instead of grep+read. It indexes in ~250ms, queries in ~1.5ms, all on CPU.

## Prerequisites

Semble must be installed: `pip install semble`

If behind a proxy, set `ALL_PROXY` before running (e.g. `export ALL_PROXY=socks5h://127.0.0.1:1180`).

First run downloads the embedding model (~16MB) from HuggingFace. Subsequent runs are fully offline.

## Commands

### Search

```bash
# Natural language search
semble search "authentication flow" /path/to/project

# Symbol/identifier search
semble search "CheckpointManager" /path/to/project

# More results
semble search "compaction trigger" /path/to/project --top-k 10
```

### Find Related Code

After a search returns results, use `find-related` to discover similar code:

```bash
semble find-related <file_path> <line_number> /path/to/project
```

### Content Types

```bash
# Default: code only
semble search "database config" /path/to/project

# Search docs (markdown, rst, etc.)
semble search "deployment guide" /path/to/project --content docs

# Search config files (yaml, toml, json, etc.)
semble search "database host port" /path/to/project --content config

# Search everything
semble search "authentication" /path/to/project --content all
```

## When to Use Semble vs grep

| Use Semble | Use grep |
|---|---|
| "Where is authentication handled?" | Find all occurrences of exact string `oauth_callback` |
| "How does compaction work?" | Count occurrences of a pattern |
| "Find the tool execution dispatch" | Quick confirmation of exact literal |
| "Locate session persistence logic" | Exhaustive literal matching across all files |

**Rule of thumb**: If you're asking "where" or "how" → Semble. If you need "every occurrence of X" → grep.

## Workflow for Exploring a New Codebase

1. **Start broad**: `semble search "main entry point" /path/to/project` or `semble search "project structure" /path/to/project --content docs`
2. **Drill down**: `semble search "specific feature" /path/to/project` based on initial findings
3. **Find related**: `semble find-related <file> <line> /path/to/project` to discover connected code
4. **Read files**: Only read full files when the snippet doesn't give enough context
5. **Use grep**: For exhaustive literal matches or exact pattern confirmation

## Integration Tips

### For Sub-agents

Sub-agents cannot call MCP tools. Use the CLI commands above directly via bash.

### Proxy Setup

If the HuggingFace model hasn't been cached yet, set proxy before running:

```bash
export ALL_PROXY=socks5h://127.0.0.1:1180
semble search "query" /path/to/project
```

After the model is cached (~/.cache/huggingface/hub/models--minishlab--potion-code-16M/), no proxy needed.

### Performance Notes

- Tree-sitter language warnings ("Language X not found, falling back to line chunking") are harmless — search still works via line-based chunking.
- The first search on a project indexes it; subsequent searches reuse the in-memory index.
- No persistent index in CLI mode (unlike MCP server mode which caches). Each `semble search` call re-indexes, but it's fast (~250ms for average repos).

### Large Repository Handling

**Problem**: In CLI mode, every `semble search` call re-indexes from scratch (no persistent index). For a huge repo, indexing alone can take many minutes and exceed the default 2-minute bash timeout. The command will appear stuck with only language fallback warnings in the output.

Even in MCP server mode, the initial index build for a large repo can be very slow if it contains large binary files (e.g. `.pt`, `.tar.gz`, `.bin` in `benchmark/` dirs) that aren't in `.gitignore`.

**Solution for CLI mode**: Search targeted subdirectories instead of the entire repo root:

```bash
# ❌ WRONG — entire repo, will timeout on large projects
semble search "join execution" ~/project/tidb

# ✅ CORRECT — target the relevant subdirectory
semble search "join execution" ~/project/tidb/pkg/executor/
```

**CLI mode guidelines**:
- Estimate repo size first: `du -sh <path>` — if > 100MB, prefer subdirectory search.
- Subdirectories under ~50MB index in seconds and return results reliably within default timeout.
- Use `ls` or `find ... -name "*.go" | wc -l` to identify relevant subdirectories before searching.

**Solution for large repos: MCP server mode (recommended)**

Semble has a built-in MCP stdio server that **caches indexes in memory** for the lifetime of the process. This avoids re-indexing on every call.

```bash
# Start MCP server (pre-indexes a project at startup)
semble ~/project/tidb/pkg/executor

# Or without pre-indexing (indexes on first query)
semble
```

Key features of MCP server mode:
- **Persistent cache**: Indexes are built once and reused across queries (up to 10 cached repos via LRU).
- **File watching**: For local paths, uses `watchfiles` to detect changes and auto re-index when files change.
- **Git URL support**: Can index remote repos directly: `semble https://github.com/org/repo --ref main`
- **Pre-warming**: Optionally pre-loads the embedding model and indexes at startup via `_load_and_prewarm()`.

**MCP server measured performance** (on ai/pkg, ~7MB, 50+ Go files):
- First search (cold index): ~2s
- Subsequent searches (cached): ~1-3s
- Index is reused instantly across queries

**Integration with mcporter**:
```bash
# Add semble as an MCP server in mcporter config (home scope)
mcporter config add semble --command semble --scope home \
  --description "Semble code search (cached index with file watching)"

# List tools
mcporter list semble --schema

# Call search
mcporter call semble.search query="join execution" repo="/path/to/project" top_k:3

# Note: mcporter default call timeout is 60s, use --timeout for large repos
mcporter call semble.search query="join execution" repo="/path/to/project" --timeout 300000
```

MCP server dependencies (check if installed):
```bash
pip show mcp watchfiles  # Both needed for MCP server mode
```

**Important: binary files and `.gitignore`**
- Semble respects `.gitignore`, but if your repo has large binary files (models, archives) in tracked directories, indexing will be extremely slow.
- Before searching the entire repo root, check for large dirs: `du -sh */ | sort -rh | head -10`
- If there are >100MB of non-code files, prefer subdirectory search or add them to `.gitignore`.