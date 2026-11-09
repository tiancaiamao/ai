package app

import (
	"log/slog"
	"time"

	"github.com/tiancaiamao/ai/pkg/compact"
)

// Idle compaction
//
// A provider's prompt cache expires after a short idle period. The next user
// turn then re-reads the whole conversation at full price (a cache miss).
// Compacting while the cache is still warm turns that miss into a cheap cached
// read of the old context plus a small context for every later turn, so the
// first request after a long pause costs a fraction of what it would have.
//
// The timer therefore fires just *before* the provider TTL (see
// compact.IdleCompactDelay), and only once the conversation has already reached
// the "ask for compact" threshold — compacting a short context saves nothing.

// idleCompactionRun performs the compaction for the idle path; tests replace it
// to exercise the scheduling logic without an LLM call.
var idleCompactionRun = func(app *App, trigger string) (*CompactResult, error) {
	return app.compactNow(trigger, true)
}

// scheduleIdleCompaction arms the idle timer after a turn completes.
func (app *App) scheduleIdleCompaction() {
	delay := compact.IdleCompactDelay(app.model.Provider)

	app.idleMu.Lock()
	defer app.idleMu.Unlock()

	app.idleEpoch++
	app.stopIdleTimerLocked()
	if delay <= 0 {
		return
	}

	epoch := app.idleEpoch
	app.idleTimer = time.AfterFunc(delay, func() { app.runIdleCompaction(epoch) })
	slog.Info("[IdleCompact] Scheduled", "delay", delay, "provider", app.model.Provider)
}

// cancelIdleCompaction drops a pending idle compaction. Any user input
// invalidates the idle period the timer was scheduled for.
func (app *App) cancelIdleCompaction() {
	app.idleMu.Lock()
	app.idleEpoch++
	app.stopIdleTimerLocked()
	app.idleMu.Unlock()
}

// stopIdleTimerLocked stops the pending timer. Callers must hold idleMu.
func (app *App) stopIdleTimerLocked() {
	if app.idleTimer != nil {
		app.idleTimer.Stop()
		app.idleTimer = nil
	}
}

// waitForIdleCompaction blocks until an in-flight idle compaction finishes, so a
// user prompt is queued behind it instead of racing with it on the agent
// context. Turns are short-lived; the wait is bounded by the compaction itself.
func (app *App) waitForIdleCompaction() {
	app.idleCompactMu.Lock()
	app.idleCompactMu.Unlock()
}

// runIdleCompaction is the timer callback. It compacts at most once per idle
// period: after a successful compaction the context is small, so a later timer
// would find it below the threshold anyway.
func (app *App) runIdleCompaction(epoch uint64) {
	app.idleCompactMu.Lock()
	defer app.idleCompactMu.Unlock()

	app.idleMu.Lock()
	current := app.idleEpoch
	app.idleMu.Unlock()
	// Superseded by user input that arrived between the timer firing and this
	// goroutine acquiring the gate.
	if current != epoch {
		return
	}

	app.stateMu.Lock()
	busy := app.isStreaming || app.isCompacting
	autoEnabled := app.autoCompactionEnabled
	app.stateMu.Unlock()
	if busy {
		slog.Info("[IdleCompact] Skipped: agent busy")
		return
	}
	// Honor the user's opt-out: /set auto_compaction false must also stop the
	// idle trigger, not only the in-loop one.
	if !autoEnabled {
		slog.Info("[IdleCompact] Skipped: automatic compaction is disabled")
		return
	}
	if app.ag == nil || app.compactor == nil {
		return
	}
	ag, compactor := app.ag, app.compactor

	agentCtx := ag.GetContext()
	if agentCtx == nil {
		return
	}
	tokens := agentCtx.EstimateTokens()
	threshold := compactor.IdleCompactThreshold()
	if tokens < threshold {
		slog.Info("[IdleCompact] Skipped: context below compaction threshold",
			"tokens", tokens, "threshold", threshold)
		return
	}

	slog.Info("[IdleCompact] Compacting ahead of prompt-cache expiry",
		"tokens", tokens, "threshold", threshold, "provider", app.model.Provider)
	if _, err := idleCompactionRun(app, "idle"); err != nil {
		// No retry: a failed compaction leaves the context untouched, and the
		// trace span (source=idle) plus the compaction_end event carry the error.
		slog.Warn("[IdleCompact] Idle compaction failed", "error", err)
	}
}
