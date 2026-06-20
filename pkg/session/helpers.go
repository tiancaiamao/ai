package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// truncateText truncates text to at most limit bytes, appending "..." if truncation occurs.
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

// NormalizeSessionPath expands ~ and converts to absolute path.
// Returns "" for empty input.
func NormalizeSessionPath(sessionPath string) (string, error) {
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

// ResolveSessionName returns the human-readable name for a session ID.
// Falls back to the session ID if no name is set or the manager is nil.
func ResolveSessionName(sessionMgr *SessionManager, sessionID string) string {
	if sessionMgr == nil || sessionID == "" {
		return sessionID
	}
	meta, err := sessionMgr.GetMeta(sessionID)
	if err != nil || meta == nil || meta.Name == "" {
		return sessionID
	}
	return meta.Name
}

// --- Tree entry building ---

type treeNode struct {
	entry    SessionEntry
	children []*treeNode
}

// BuildTreeEntries builds a depth-ordered tree of TreeEntries from flat session entries.
// Returns session.TreeEntry (internal type) which is different from RPC TreeEntry in pkg/app.
func BuildTreeEntries(entries []SessionEntry, leafID *string) []TreeEntry {
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

	sortRoots(roots, order)

	var result []TreeEntry
	var walk func(nodes []*treeNode, depth int)
	walk = func(nodes []*treeNode, depth int) {
		for _, node := range nodes {
			if len(node.children) > 0 {
				sortRoots(node.children, order)
			}
			role, text := TreeEntryLabel(node.entry)
			if text != "" {
				text = truncateText(text, 120)
			}
			isLeaf := false
			if leafID != nil && *leafID == node.entry.ID {
				isLeaf = true
			}
			result = append(result, TreeEntry{
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

// TreeEntryLabel extracts a display role and text from a session entry.
func TreeEntryLabel(entry SessionEntry) (string, string) {
	switch entry.Type {
	case EntryTypeMessage:
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
	case EntryTypeCompaction:
		return "compaction", strings.TrimSpace(entry.Summary)
	case EntryTypeBranchSummary:
		return "branch summary", strings.TrimSpace(entry.Summary)
	case EntryTypeSessionInfo:
		label := strings.TrimSpace(entry.Name)
		if label == "" {
			label = strings.TrimSpace(entry.Title)
		}
		return "session info", label
	default:
		return entry.Type, ""
	}
}

// --- Session usage stats ---

// SessionUsage holds message counts and token statistics.
type SessionUsage struct {
	UserCount      int
	AssistantCount int
	ToolCalls      int
	ToolResults    int
	Tokens         SessionTokenStats
	Cost           float64
}

// CollectSessionUsage scans messages and returns aggregated counts and token stats.
func CollectSessionUsage(messages []agentctx.AgentMessage) SessionUsage {
	var u SessionUsage
	var lastPromptTokens int

	for _, msg := range messages {
		switch msg.Role {
		case "user":
			u.UserCount++
		case "assistant":
			u.AssistantCount++
			u.ToolCalls += len(msg.ExtractToolCalls())
			if msg.Usage != nil {
				u.Tokens.Output += msg.Usage.OutputTokens
				u.Cost += msg.Usage.Cost.Total
				if msg.Usage.InputTokens > 0 {
					lastPromptTokens = msg.Usage.InputTokens
				}
			}
		case "toolResult":
			u.ToolResults++
		}
	}

	u.Tokens.Total = u.Tokens.Output + lastPromptTokens
	return u
}

// --- internal helpers ---

func sortRoots(nodes []*treeNode, order map[string]int) {
	sort.Slice(nodes, func(i, j int) bool {
		return order[nodes[i].entry.ID] < order[nodes[j].entry.ID]
	})
}
