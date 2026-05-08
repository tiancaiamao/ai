# ADR 001: Extract `internal/run` Package

The `~/.ai/runs/` path layout and run lifecycle management (spawn, status, kill) are scattered across 5 files in `cmd/` and `internal/task/scheduler.go`. Extract a dedicated `internal/run` package to be the single source of truth for AI run operations.

This consolidates 9 duplicated path constructions, 3 copies of `isProcessAlive`, and makes `AIAdapter` testable in isolation from CLI wiring.