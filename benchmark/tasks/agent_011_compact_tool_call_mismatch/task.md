# Task: Fix Compact Tool Call Pairing Mismatch (Trace-Driven)

## Description
You are given a real trace from `main` branch runtime and a `main` commit hash.
After `context_management` performs `compact`, the next LLM call fails with:

`invalid params, tool call result does not follow tool call`

Your goal is to fix the pairing logic in `compact.go`.

## Inputs
- `trace/main.perfetto.json` (runtime evidence)
- `main_commit.txt` (the main branch commit hash that reproduces this bug)

## Bug Pattern
The current pairing logic hides some `toolResult` messages, but it does NOT remove
stale `toolCall` blocks from assistant messages in recent context.

That leaves unpaired tool calls/results after compaction.

## Requirements
1. Keep tool call/result pairing valid after compaction.
2. Preserve assistant text content while filtering stale tool calls.
3. Do not change tests or input fixtures.

## Files
- `compact.go`
- `compact_test.go`
- `trace/main.perfetto.json`
- `main_commit.txt`
