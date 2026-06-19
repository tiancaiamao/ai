# Changelog

Functional code changes per commit. Newest first.

Docs-only changes are not tracked here — see git log for those.

## Unreleased

### Docs
- Full documentation sync: rewrote root README, all pkg/*/README.md, docs/*.md to match actual code
- Restructured docs: live docs vs archive, added docs/README.md index
- Condensed CLAUDE.md (242→76 lines)

## 2026-06-19

### Changed
- `a92277f` Archive old messages on compact for agent accessibility (#306)
- `b28a112` Remove context management system (LLMContext, CacheMode, ContextManager) (#305)
- `8b8cb75` Append-only compaction with reasoning_content fallback (#304)

## 2026-06-18

### Added
- `bfe22e7` Thinking level support in config file (#303)
- `6f4623a` LLMDecideCompactor for large context window models (#302)
- `01504cf` Thread context.Context through Compactor for /compact trace coverage (#301)
- `017919d` Reuse prefix cache in compaction via structured messages (#300)

### Changed
- `b6545ad` Unify compaction through compactor.Compact(ctx), remove session-layer compaction logic (#299)

## 2026-06-17

### Added
- `a8fb2a3` Emit cache_read in llm_call trace events (#297)
- `3736519` Thinking API parameters for ZAI/DeepSeek models (#296)

### Removed
- `f6dd3d5` Dead hashline feature (#294)
- `c88ff73` Dead code cleanup

## 2026-06-15

### Removed
- `9b893b6` Remove claw/aiclaw, fix abort event flaky tests (#293)
- `687e3c6` Move benchmark out of repo

## 2026-06-14

### Changed
- `b652e3d` Dead code cleanup, dedup, file splits, God Object treatment (#291)

## 2026-06-13

### Fixed
- `ff65a65` Improve find_skill search with query tokenization (#290)
- `9f30881` /fork and /rewind index resolution uses agent messages (#289)

### Changed
- `a48758d` Extract startServeProcess to deduplicate run/serve subprocess logic (#288)

## 2026-06-09

### Changed
- `e9d94a7` Merge skills+instructions into single prefix user message (#287)

## 2026-06-08

### Added
- `454d85a` Move skills from system prompt to user message injection (#286)

### Changed
- `00dc702` Simplify runtime_state snapshot, inline Workspace section (#285)

## 2026-06-06

### Added
- `218620d` Inject AGENTS.md as user message, not system prompt (#282)
- `120ab85` CI: 80% coverage gate for pkg/ (#281)
- `3ea8baa` Autonomous prompt-optimization loop (57% → 93% in 4 iterations) (#279)

### Fixed
- `8218e28` Resume replays journal entries after checkpoint (#283)

### Changed
- `c6a5763` Remove PROJECT_CONTEXT, drop self-improving-agent skill (#284)

## 2026-06-04

### Added
- `10529ea` Evolve planner pipeline with attribution and tool filtering (#278)

## 2026-05-28

### Added
- `a75d013` Subagent isolation via run ID tracking (#276)

### Fixed
- `45a9fb4` Compaction fallback blocked by no-op context manager (#277)

## 2026-05-27

### Added
- `30665de` Block tmux kill-server at tool level to prevent agent self-destruction

### Changed
- `e819b4b` Slim down orchestrator system prompt, delegate to skills (#275)

## 2026-05-26

### Added
- `d3c9162` Cache-friendly message architecture (#273)
- `283417f` -model CLI flag to override model at startup (#270)
- `4437d11` send --wait and /rewind by index (#266)

### Fixed
- `622d5be` Defer syncContent to prevent TUI freeze (#274)
- `fa0e29f` Loop guard display, hard abort recovery, polling detection (#271)
- `25de837` ai serve messages never reach subprocess (#268)
- `88f83a7` ai serve status always failed, bridge goroutine leak (#267)
- `ed5fe14` /rewind slash command always returns entry not found (#265)
- `3011a9d` Skill loader skips skills without frontmatter description (#264)
- `003b52c` Data race, CI fmt-check (#263)

## Earlier

Pre-2026-05 commits not individually tracked. See `git log` for full history.