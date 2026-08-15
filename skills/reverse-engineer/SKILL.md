---
name: reverse-engineer
description: Reverse engineer desktop applications, binaries, and software packages. Use when the user asks to analyze, reverse engineer, or understand how an application works internally. Targets include macOS .app bundles, Electron apps, Tauri apps, native binaries, npm/pip packages, CLI tools, and any software the user wants to understand the architecture and implementation of.
---

# Reverse Engineer

Systematically explore and analyze software to understand its architecture, protocols, and implementation without source code.

## Workflow

### Phase 1: Locate & Identify

```
# macOS app
mdfind "kMDItemFSName == '<AppName>*'"
ls /Applications/ | grep -i <name>
mdfind "kMDItemFSName == '<name>*'"

# CLI tool
which <tool>
file $(which <tool>)
```

Identify: app type (native/Electron/Tauri/Java), binary format (Mach-O/ELF), architecture (arm64/x86_64).

### Phase 2: Structure Survey

**macOS .app bundle:**
```
ls -la <App>.app/Contents/
cat <App>.app/Contents/Info.plist        # bundle ID, version, executable name
ls -la <App>.app/Contents/MacOS/         # main binary
ls -laR <App>.app/Contents/Resources/    # configs, assets, bundled tools
ls -la <App>.app/Contents/Frameworks/    # shared libraries
```

**Electron app (check for app.asar):**
```
npx asar extract <App>.app/Contents/Resources/app.asar /tmp/<name>-inspect
find /tmp/<name>-inspect -name "package.json" -exec cat {} \;
```

**Tauri app:**
Look for Rust binary + web assets in Resources. Check Frameworks/ for libwebkit.

**CLI/npm global:**
```
npm list -g --depth=0
readlink -f $(which <tool>)
cat $(dirname $(readlink -f $(which <tool>)))/package.json
```

### Phase 3: String Extraction (Binary Analysis)

Extract strings from native binaries to find protocols, APIs, endpoints, key logic:

```bash
# Core protocol/API keywords
strings <binary> | grep -iE "protocol|api|http|ws://|grpc|json-rpc|endpoint"
strings <binary> | grep -iE "claude|openai|anthropic|gemini|model|provider|proxy"

# Architecture keywords
strings <binary> | grep -iE "plugin|extension|module|adapter|handler|middleware"
strings <binary> | grep -iE "config|setting|preference|secret|token|key"

# Version/build info
strings <binary> | grep -iE "version|v[0-9]+\.[0-9]+|build|commit"
```

**Tip**: Pipe large output through `head -50` or `grep` to avoid flooding context.

### Phase 4: Config & Data Analysis

```
# Runtime config directories
ls -la ~/Library/Application\ Support/<bundle-id>/
ls -la ~/Library/Preferences/ | grep <name>
ls -la ~/.config/<name>/
ls -la ~/.<name>/

# Databases
sqlite3 <db-path> ".tables"
sqlite3 <db-path> ".schema"

# JSON/YAML config
cat <config-file> | python3 -m json.tool
cat <config-file> | head -100

# Environment and logs
ls -la ~/Library/Logs/<name>/
cat ~/Library/Logs/<name>/*.log | tail -100
```

### Phase 5: Bundled Components Analysis

Many apps bundle sub-tools, agents, or plugins:

```bash
# Check bundled binaries
ls -la <App>.app/Contents/Resources/binaries/
file <App>.app/Contents/Resources/binaries/*

# Check bundled agents/configs
ls -la <App>.app/Contents/Resources/subagents/
ls -la <App>.app/Contents/Resources/configs/
cat <App>.app/Contents/Resources/bundled-agents/manifest.json

# Check npm packages bundled
find <App>.app/Contents/Resources -name "package.json" -exec cat {} \;
```

For each bundled component, repeat Phase 3 (string extraction).

### Phase 6: Protocol & API Inference

From collected strings and configs, infer:

1. **Communication protocols**: REST, WebSocket, JSON-RPC, gRPC, stdio?
2. **Authentication**: API keys, OAuth, local tokens?
3. **Data flow**: How does the main binary interact with bundled components?
4. **Network endpoints**: Local proxy? Remote API? Both?

Key patterns to look for:
- `127.0.0.1:<port>` → local proxy/server
- Environment variable injection (`BASE_URL`, `API_KEY`) → API interception
- ndjson/JSON-RPC over stdio → agent communication protocol

### Phase 7: Synthesize & Report

Write findings to `docs/<app-name>-reverse-engineering.md` with:

```markdown
# <App Name> Reverse Engineering Report

## Architecture Overview
<One-paragraph summary of the whole system>

## Key Findings
### 1. App Type & Build System
### 2. Core Architecture
### 3. Protocols & APIs
### 4. Bundled Components
### 5. Configuration & Data Storage
### 6. Security Observations

## Source Map
<Path-to-feature mapping from analysis>

## Open Questions
<Things we couldn't determine>
```

## Efficiency Tips

1. **Batch independent reads** — Read multiple config files in one tool call round
2. **Target string extraction** — Use specific grep patterns, not broad `strings | head`
3. **Size-aware reading** — Use `head` or `read` with limits for large files
4. **Don't re-read** — Track what you've already seen, don't cat the same file twice
5. **Focus on architecture** — Don't get lost in string details; build the big picture first

## Common Patterns

| App Type | Key Indicators | Analysis Focus |
|----------|---------------|----------------|
| Electron | `app.asar`, Frameworks/`Electron Framework.framework` | Unpack asar, read JS source |
| Tauri | Rust binary + web assets, `libwebkit` in Frameworks | String extraction, web assets |
| Native (Swift/ObjC) | No web framework, `.nib`/`.storyboard` resources | String extraction, class-dump |
| Java/JVM | `.jar` files, `java` in process | Decompile with `cfr` or `jadx` |
| Python package | `site-packages/<name>/`, `__init__.py` | Read source directly |