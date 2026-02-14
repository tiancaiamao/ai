package main

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/tiancaiamao/ai/pkg/skill"
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

func sessionIDFromPath(path string) string {
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	if strings.HasSuffix(base, ".jsonl") {
		return strings.TrimSuffix(base, ".jsonl")
	}
	ext := filepath.Ext(base)
	if ext != "" {
		return strings.TrimSuffix(base, ext)
	}
	return base
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

func modelInfoFromModel(model llm.Model) rpc.ModelInfo {
	return rpc.ModelInfo{
		ID:       model.ID,
		Name:     model.ID,
		Provider: model.Provider,
		API:      model.API,
		Input:    []string{"text"},
	}
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

func modelSpecFromConfig(cfg *config.Config) config.ModelSpec {
	return config.ModelSpec{
		ID:       cfg.Model.ID,
		Name:     cfg.Model.ID,
		Provider: cfg.Model.Provider,
		BaseURL:  cfg.Model.BaseURL,
		API:      cfg.Model.API,
		Input:    []string{"text"},
	}
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
		MaxMessages:         cfg.MaxMessages,
		MaxTokens:           cfg.MaxTokens,
		KeepRecent:          cfg.KeepRecent,
		KeepRecentTokens:    cfg.KeepRecentTokens,
		ReserveTokens:       compactor.ReserveTokens(),
		ToolCallCutoff:      cfg.ToolCallCutoff,
		ToolSummaryStrategy: cfg.ToolSummaryStrategy,
		ContextWindow:       compactor.ContextWindow(),
		TokenLimit:          limit,
		TokenLimitSource:    source,
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

func buildSkillCommands(skills []skill.Skill) []rpc.SlashCommand {
	commands := make([]rpc.SlashCommand, 0, len(skills))
	for _, s := range skills {
		name := s.Name
		if name == "" {
			continue
		}
		commands = append(commands, rpc.SlashCommand{
			Name:        "skill:" + name,
			Description: s.Description,
			Source:      "skill",
			Location:    s.Source,
			Path:        s.FilePath,
		})
	}
	return commands
}

func initTraceFileHandler() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	tracesDir := filepath.Join(homeDir, ".ai", "traces")
	handler, err := traceevent.NewFileHandler(tracesDir)
	if err != nil {
		return tracesDir, err
	}
	traceevent.SetHandler(handler)
	return tracesDir, nil
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
		span.AddField("error", true)
		span.AddField("error_message", err.Error())
	}
	return err
}

func forkEntryID(msg agent.AgentMessage, index int) string {
	if msg.Timestamp != 0 {
		return fmt.Sprintf("msg-%d", msg.Timestamp)
	}
	return fmt.Sprintf("idx-%d", index)
}

func buildForkMessages(messages []agent.AgentMessage) []rpc.ForkMessage {
	results := make([]rpc.ForkMessage, 0)
	for i, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		results = append(results, rpc.ForkMessage{
			EntryID: forkEntryID(msg, i),
			Text:    msg.ExtractText(),
		})
	}
	return results
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

func resolveForkEntry(entryID string, messages []agent.AgentMessage) (int, string, error) {
	if entryID == "" {
		return -1, "", fmt.Errorf("entryId is required")
	}
	if strings.HasPrefix(entryID, "msg-") {
		raw := strings.TrimPrefix(entryID, "msg-")
		ts, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return -1, "", fmt.Errorf("invalid entryId: %s", entryID)
		}
		for i, msg := range messages {
			if msg.Timestamp == ts {
				return i, msg.ExtractText(), nil
			}
		}
		return -1, "", fmt.Errorf("entryId not found: %s", entryID)
	}
	if strings.HasPrefix(entryID, "idx-") {
		raw := strings.TrimPrefix(entryID, "idx-")
		index, err := strconv.Atoi(raw)
		if err != nil {
			return -1, "", fmt.Errorf("invalid entryId: %s", entryID)
		}
		if index < 0 || index >= len(messages) {
			return -1, "", fmt.Errorf("entryId out of range: %s", entryID)
		}
		return index, messages[index].ExtractText(), nil
	}
	return -1, "", fmt.Errorf("unknown entryId format: %s", entryID)
}

func collectSessionUsage(messages []agent.AgentMessage) (int, int, int, int, rpc.SessionTokenStats, float64) {
	var userCount int
	var assistantCount int
	var toolCalls int
	var toolResults int
	var tokens rpc.SessionTokenStats
	var cost float64

	for _, msg := range messages {
		switch msg.Role {
		case "user":
			userCount++
		case "assistant":
			assistantCount++
			toolCalls += len(msg.ExtractToolCalls())
			if msg.Usage != nil {
				tokens.Input += msg.Usage.InputTokens
				tokens.Output += msg.Usage.OutputTokens
				tokens.CacheRead += msg.Usage.CacheRead
				tokens.CacheWrite += msg.Usage.CacheWrite
				tokens.Total += msg.Usage.TotalTokens
				cost += msg.Usage.Cost.Total
			}
		case "toolResult":
			toolResults++
		}
	}

	if tokens.Total == 0 {
		tokens.Total = tokens.Input + tokens.Output + tokens.CacheRead + tokens.CacheWrite
	}

	return userCount, assistantCount, toolCalls, toolResults, tokens, cost
}
