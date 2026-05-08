# ADR 003: WorkerBackend Interface for Scheduler

The scheduler's `spawnWorker` and `checkAIServeRun` directly construct `exec.Command("ai", "serve", ...)` and read `~/.ai/runs/` files. This couples the scheduler to the `ai serve` backend specifically.

Introduce a `WorkerBackend` interface with `Spawn` and `IsDone` methods. The current `ai serve` implementation becomes `AIServeBackend`. This allows future backends (codex, local subprocess) to be plugged into the scheduler without modifying its core loop.