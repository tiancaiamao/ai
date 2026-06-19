package main

import (
	"context"
	"errors"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

func normalizeSessionPath(sessionPath string) (string, error) {
	if sessionPath == "" {
		return "", nil
	}
	if sessionPath == "~" || strings.HasPrefix(sessionPath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if sessionPath == "~" {
			sessionPath = homeDir
		} else {
			sessionPath = filepath.Join(homeDir, sessionPath[2:])
		}
	}
	absPath, err := filepath.Abs(sessionPath)
	if err != nil {
		return "", err
	}
	return absPath, nil
}

func resolveSessionName(sessionMgr *session.SessionManager, sessionID string) string {
	if sessionMgr == nil || sessionID == "" {
		return sessionID
	}
	meta, err := sessionMgr.GetMeta(sessionID)
	if err != nil || meta == nil || meta.Name == "" {
		return sessionID
	}
	return meta.Name
}

func modelInfoFromSpec(spec config.ModelSpec) rpc.ModelInfo {
	name := spec.Name
	if name == "" {
		name = spec.ID
	}
	input := spec.Input
	if len(input) == 0 {
		input = []string{"text"}
	}
	return rpc.ModelInfo{
		ID:            spec.ID,
		Name:          name,
		Provider:      spec.Provider,
		API:           spec.API,
		Reasoning:     spec.Reasoning,
		Input:         input,
		ContextWindow: spec.ContextWindow,
		MaxTokens:     spec.MaxTokens,
	}
}

// applyModelOverride sets the model ID from the CLI --model flag.
// If the model ID is found in models.json, Provider/BaseURL/API are auto-filled.
// If not found, a warning is logged and the raw ID is used with existing config.
func applyModelOverride(cfg *config.Config, modelOverride string) {
	cfg.Model.ID = modelOverride
	specs, _, specErr := loadModelSpecs(cfg)
	if specErr == nil {
		found := false
		for _, spec := range specs {
			if spec.ID == modelOverride {
				cfg.Model.Provider = spec.Provider
				cfg.Model.BaseURL = spec.BaseURL
				cfg.Model.API = spec.API
				slog.Info("Model override applied", "id", modelOverride, "provider", spec.Provider)
				found = true
				break
			}
		}
		if !found {
			slog.Warn("Model override: model ID not found in models.json, using raw ID with existing config", "id", modelOverride)
		}
	} else {
		slog.Warn("Model override: could not load model specs, using raw ID", "id", modelOverride, "error", specErr)
	}
}

func modelSpecFromConfig(cfg *config.Config) config.ModelSpec {
	return config.ModelSpec{
		ID:        cfg.Model.ID,
		Name:      cfg.Model.ID,
		Provider:  cfg.Model.Provider,
		BaseURL:   cfg.Model.BaseURL,
		API:       cfg.Model.API,
		Input:     []string{"text"},
		MaxTokens: cfg.Model.MaxTokens,
	}
}

func applyModelLimitsFromSpec(model llm.Model, spec config.ModelSpec) llm.Model {
	if model.ContextWindow <= 0 && spec.ContextWindow > 0 {
		model.ContextWindow = spec.ContextWindow
	}
	if model.MaxTokens <= 0 && spec.MaxTokens > 0 {
		model.MaxTokens = spec.MaxTokens
	}
	if spec.Reasoning {
		model.Reasoning = true
	}
	return model
}

func resolveActiveModelSpec(cfg *config.Config) (config.ModelSpec, error) {
	specs, modelsPath, err := loadModelSpecs(cfg)
	if err != nil {
		return modelSpecFromConfig(cfg), fmt.Errorf("load models from %s: %w", modelsPath, err)
	}
	if spec, ok := findModelSpec(specs, cfg.Model.Provider, cfg.Model.ID); ok {
		return spec, nil
	}
	return modelSpecFromConfig(cfg), nil
}

func buildCompactionState(cfg *compact.Config, compactor *compact.Compactor) *rpc.CompactionState {
	if cfg == nil || compactor == nil {
		return nil
	}
	limit, source := compactor.EffectiveTokenLimit()
	return &rpc.CompactionState{
		MaxMessages:           cfg.MaxMessages,
		MaxTokens:             cfg.MaxTokens,
		KeepRecent:            cfg.KeepRecent,
		KeepRecentTokens:      cfg.KeepRecentTokens,
		ReserveTokens:         compactor.ReserveTokens(),
		ToolCallCutoff:        cfg.ToolCallCutoff,
		ToolSummaryStrategy:   cfg.ToolSummaryStrategy,
		ToolSummaryAutomation: cfg.ToolSummaryAutomation,
		ContextWindow:         compactor.ContextWindow(),
		TokenLimit:            limit,
		TokenLimitSource:      source,
	}
}

func loadModelSpecs(cfg *config.Config) ([]config.ModelSpec, string, error) {
	modelsPath, err := config.ResolveModelsPath()
	if err != nil {
		return nil, "", err
	}

	specs, err := config.LoadModelSpecs(modelsPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []config.ModelSpec{modelSpecFromConfig(cfg)}, modelsPath, nil
		}
		return nil, modelsPath, err
	}

	if len(specs) == 0 {
		return nil, modelsPath, fmt.Errorf("no models defined in %s", modelsPath)
	}

	return specs, modelsPath, nil
}

func filterModelSpecsWithKeys(specs []config.ModelSpec) []config.ModelSpec {
	available := make(map[string]bool)
	filtered := make([]config.ModelSpec, 0, len(specs))
	for _, spec := range specs {
		provider := strings.TrimSpace(spec.Provider)
		if provider == "" || strings.TrimSpace(spec.ID) == "" {
			continue
		}
		ok, seen := available[provider]
		if !seen {
			if _, err := config.ResolveAPIKey(provider); err == nil {
				ok = true
			} else {
				ok = false
			}
			available[provider] = ok
		}
		if ok {
			filtered = append(filtered, spec)
		}
	}
	return filtered
}

func findModelSpec(specs []config.ModelSpec, provider, modelID string) (config.ModelSpec, bool) {
	provider = strings.TrimSpace(provider)
	modelID = strings.TrimSpace(modelID)
	for _, spec := range specs {
		if strings.EqualFold(spec.Provider, provider) && spec.ID == modelID {
			return spec, true
		}
	}
	return config.ModelSpec{}, false
}

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

type treeNode struct {
	entry    session.SessionEntry
	children []*treeNode
}

func buildTreeEntries(entries []session.SessionEntry, leafID *string) []rpc.TreeEntry {
	if len(entries) == 0 {
		return nil
	}

	nodeMap := make(map[string]*treeNode, len(entries))
	order := make(map[string]int, len(entries))
	for i, entry := range entries {
		node := &treeNode{entry: entry}
		nodeMap[entry.ID] = node
		order[entry.ID] = i
	}

	roots := make([]*treeNode, 0)
	for _, node := range nodeMap {
		parentID := node.entry.ParentID
		if parentID == nil || *parentID == node.entry.ID {
			roots = append(roots, node)
			continue
		}
		parent := nodeMap[*parentID]
		if parent != nil {
			parent.children = append(parent.children, node)
			continue
		}
		roots = append(roots, node)
	}

	sort.Slice(roots, func(i, j int) bool {
		return order[roots[i].entry.ID] < order[roots[j].entry.ID]
	})

	var result []rpc.TreeEntry
	var walk func(nodes []*treeNode, depth int)
	walk = func(nodes []*treeNode, depth int) {
		for _, node := range nodes {
			if len(node.children) > 0 {
				sort.Slice(node.children, func(i, j int) bool {
					return order[node.children[i].entry.ID] < order[node.children[j].entry.ID]
				})
			}
			role, text := treeEntryLabel(node.entry)
			if text != "" {
				text = truncateText(text, 120)
			}
			isLeaf := false
			if leafID != nil && *leafID == node.entry.ID {
				isLeaf = true
			}
			result = append(result, rpc.TreeEntry{
				EntryID:   node.entry.ID,
				ParentID:  node.entry.ParentID,
				Type:      node.entry.Type,
				Role:      role,
				Text:      text,
				Timestamp: node.entry.Timestamp,
				Depth:     depth,
				Leaf:      isLeaf,
			})
			if len(node.children) > 0 {
				walk(node.children, depth+1)
			}
		}
	}

	walk(roots, 0)
	return result
}

func treeEntryLabel(entry session.SessionEntry) (string, string) {
	switch entry.Type {
	case session.EntryTypeMessage:
		if entry.Message == nil {
			return "message", ""
		}
		role := entry.Message.Role
		text := strings.TrimSpace(entry.Message.ExtractText())
		if text == "" {
			switch role {
			case "toolResult":
				if strings.TrimSpace(entry.Message.ToolName) != "" {
					text = fmt.Sprintf("%s result", entry.Message.ToolName)
				} else {
					text = "tool result"
				}
			case "assistant":
				if len(entry.Message.ExtractToolCalls()) > 0 {
					text = "tool call"
				}
			}
		}
		return role, text
	case session.EntryTypeCompaction:
		return "compaction", strings.TrimSpace(entry.Summary)
	case session.EntryTypeBranchSummary:
		return "branch summary", strings.TrimSpace(entry.Summary)
	case session.EntryTypeSessionInfo:
		label := strings.TrimSpace(entry.Name)
		if label == "" {
			label = strings.TrimSpace(entry.Title)
		}
		return "session info", label
	default:
		return entry.Type, ""
	}
}

func truncateText(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

func collectSessionUsage(messages []agentctx.AgentMessage) (int, int, int, int, rpc.SessionTokenStats, float64) {
	var userCount int
	var assistantCount int
	var toolCalls int
	var toolResults int
	var tokens rpc.SessionTokenStats
	var cost float64

	// Track the last prompt tokens (includes system prompt + tools + conversation history)
	var lastPromptTokens int

	for _, msg := range messages {
		switch msg.Role {
		case "user":
			userCount++
		case "assistant":
			assistantCount++
			toolCalls += len(msg.ExtractToolCalls())
			if msg.Usage != nil {
				// Accumulate output tokens (these are always new)
				tokens.Output += msg.Usage.OutputTokens
				cost += msg.Usage.Cost.Total

				// Track last prompt tokens (includes system + tools + history)
				if msg.Usage.InputTokens > 0 {
					lastPromptTokens = msg.Usage.InputTokens
				}
			}
		case "toolResult":
			toolResults++
		}
	}

	// Calculate total tokens:
	// - All output tokens (unique, no duplication)
	// - Last prompt tokens (includes system prompt + tools + full conversation history)
	// Note: Last prompt tokens already includes system prompt and tools, so we DON'T
	// add them again here. The GetSessionStatsHandler will add estimates instead.
	tokens.Total = tokens.Output + lastPromptTokens

	return userCount, assistantCount, toolCalls, toolResults, tokens, cost
}

func formatIntOrUnknown(value int) string {
	if value <= 0 {
		return "unknown"
	}
	return strconv.Itoa(value)
}

func formatLimit(value int) string {
	if value <= 0 {
		return "disabled"
	}
	return strconv.Itoa(value)
}

func formatTokenLimit(state *rpc.CompactionState) string {
	if state == nil || state.TokenLimit <= 0 {
		return "unknown"
	}
	source := formatTokenLimitSource(state.TokenLimitSource)
	if source == "" {
		return strconv.Itoa(state.TokenLimit)
	}
	return fmt.Sprintf("%d (%s)", state.TokenLimit, source)
}

func formatTokenLimitSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "context_window":
		return "context-window"
	case "max_tokens":
		return "max-tokens"
	case "none":
		return ""
	default:
		return strings.TrimSpace(value)
	}
}
