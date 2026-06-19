package app

import (
	"context"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// Session helpers moved to pkg/session. Thin wrappers for cmd/ai callers.

func normalizeSessionPath(sessionPath string) (string, error) {
	return session.NormalizeSessionPath(sessionPath)
}

func resolveSessionName(sessionMgr *session.SessionManager, sessionID string) string {
	return session.ResolveSessionName(sessionMgr, sessionID)
}

// Model spec helpers moved to pkg/config and pkg/rpc.
// Thin wrappers kept for cmd/ai callers.

func modelInfoFromSpec(spec config.ModelSpec) rpc.ModelInfo {
	return config.ModelInfoFromSpec(spec)
}

func applyModelOverride(cfg *config.Config, modelOverride string) {
	config.ApplyModelOverride(cfg, modelOverride)
}

func modelSpecFromConfig(cfg *config.Config) config.ModelSpec {
	return config.ModelSpecFromConfig(cfg)
}

func applyModelLimitsFromSpec(model llm.Model, spec config.ModelSpec) llm.Model {
	return config.ApplyModelLimitsFromSpec(model, spec)
}

func resolveActiveModelSpec(cfg *config.Config) (config.ModelSpec, error) {
	return config.ResolveActiveModelSpec(cfg)
}

func loadModelSpecs(cfg *config.Config) ([]config.ModelSpec, string, error) {
	return config.LoadModelSpecsFromConfig(cfg)
}

func filterModelSpecsWithKeys(specs []config.ModelSpec) []config.ModelSpec {
	return config.FilterModelSpecsWithKeys(specs)
}

func findModelSpec(specs []config.ModelSpec, provider, modelID string) (config.ModelSpec, bool) {
	return config.FindModelSpec(specs, provider, modelID)
}

// buildCompactionState delegates to compact.BuildCompactionState.
func buildCompactionState(cfg *compact.Config, compactor *compact.Compactor) *rpc.CompactionState {
	return compact.BuildCompactionState(cfg, compactor)
}

// loadModelSpecs/filterModelSpecsWithKeys/findModelSpec moved to pkg/config.

func initTraceFileHandler(sessionID string) (*traceevent.FileHandler, string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, "", err
	}
	tracesDir := filepath.Join(homeDir, ".ai", "traces")
	handler, err := traceevent.NewFileHandler(tracesDir)
	if err != nil {
		return nil, tracesDir, err
	}
	traceevent.SetHandler(handler)

	// Set the session ID for meaningful trace file names
	if sessionID != "" {
		handler.SetSessionID(sessionID)
	}

	traceFilePath := handler.TraceFilePath("")
	return handler, traceFilePath, nil
}

// runDetachedTraceSpan executes a non-prompt operation as an independent trace segment.
// It creates a fresh trace ID, records a span, and flushes events before returning.
func runDetachedTraceSpan(
	name string,
	category traceevent.TraceCategory,
	fields []traceevent.Field,
	run func(ctx context.Context, span *traceevent.Span) error,
) error {
	ctx := context.Background()
	tb := traceevent.GetTraceBuf(ctx)
	if tb != nil {
		if err := tb.DiscardOrFlush(ctx); err != nil {
			slog.Warn("Failed to finalize previous trace segment", "error", err)
		}
		tb.SetTraceID(traceevent.GenerateTraceID("session", 0))
		// Explicitly attach trace buf to context so downstream LLM calls
		// find it via context value, not just the global activeTraceBuf fallback.
		ctx = traceevent.WithTraceBuf(ctx, tb)
		defer func() {
			if err := tb.Flush(ctx); err != nil {
				slog.Warn("Failed to flush detached trace segment", "error", err)
			}
		}()
	}

	span := traceevent.StartSpan(ctx, name, category, fields...)
	defer span.End()

	if run == nil {
		return nil
	}

	err := run(span.Context(), span)
	if err != nil {
		err = agent.WithErrorStack(err)
		span.AddField("error", true)
		span.AddField("error_message", err.Error())
		if stack := agent.ErrorStack(err); stack != "" {
			span.AddField("error_stack", stack)
		}
	}
	return err
}

func buildTreeEntries(entries []session.SessionEntry, leafID *string) []rpc.TreeEntry {
	return session.BuildTreeEntries(entries, leafID)
}

func treeEntryLabel(entry session.SessionEntry) (string, string) {
	return session.TreeEntryLabel(entry)
}

// truncateText delegates to rpc.TruncateText.
func truncateText(text string, limit int) string { return rpc.TruncateText(text, limit) }

func formatIntOrUnknown(value int) string                { return rpc.FormatIntOrUnknown(value) }
func formatLimit(value int) string                       { return rpc.FormatLimit(value) }
func formatTokenLimit(state *rpc.CompactionState) string { return rpc.FormatTokenLimit(state) }
func formatTokenLimitSource(value string) string         { return rpc.FormatTokenLimitSource(value) }

func collectSessionUsage(messages []agentctx.AgentMessage) (int, int, int, int, rpc.SessionTokenStats, float64) {
	u := session.CollectSessionUsage(messages)
	return u.UserCount, u.AssistantCount, u.ToolCalls, u.ToolResults, u.Tokens, u.Cost
}

// format functions moved to pkg/rpc/format.go
