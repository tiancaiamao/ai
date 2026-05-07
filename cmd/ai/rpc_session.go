package main

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// HandleNewSession creates a new session and switches to it.
func (c *RPCCore) HandleNewSession(args string) (any, error) {
	var name, title string
	var jsonData struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	}
	if parseJSONArgs(args, &jsonData) {
		name, title = jsonData.Name, jsonData.Title
	} else {
		parts := strings.SplitN(args, " ", 2)
		name = strings.TrimSpace(parts[0])
		if len(parts) > 1 {
			title = strings.TrimSpace(parts[1])
		}
	}
	slog.Info("Received new_session", "name", name, "title", title)
	if strings.TrimSpace(name) == "" {
		name = time.Now().Format("20060102-150405")
	}
	if strings.TrimSpace(title) == "" {
		title = name
	}
	newSess, err := c.SessionMgr.CreateSession(name, title)
	if err != nil {
		return nil, err
	}

	newSessionID := newSess.GetID()

	// Update session manager's current ID
	if err := c.SessionMgr.SetCurrent(newSessionID); err != nil {
		return nil, err
	}

	// Update current session metadata
	if err := c.SessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	c.Sess = newSess
	c.SessionComp.Update(c.Sess, c.Compactor)
	c.SetAgentContext(c.CreateBaseContext())

	// Update checkpoint manager for new session
	if err := c.UpdateCheckpointManager(); err != nil {
		slog.Warn("Failed to update checkpoint manager for new session", "error", err)
	}

	c.StateMu.Lock()
	c.SessionID = newSessionID
	c.SessionName = name
	c.StateMu.Unlock()

	slog.Info("Created new session", "name", name, "id", newSessionID)
	c.Server.EmitEvent(map[string]any{"type": "session_switch", "session": newSessionID, "sessionName": name})
	return map[string]any{"sessionId": newSessionID, "cancelled": false}, nil
}

// HandleListSessions lists all sessions with their metadata.
func (c *RPCCore) HandleListSessions(args string) (any, error) {
	slog.Info("Received list_sessions")
	sessions, err := c.SessionMgr.ListSessions()
	if err != nil {
		return nil, err
	}

	// Get workspace and current directory info
	startupPath := c.Cfg.Workspace  // This is the initial working directory (git root or cwd at startup)
	currentWorkdir := c.Ws.GetCWD() // This is the current working directory

	result := make([]any, len(sessions))
	// Reverse the order so newest (index 0) appears at the bottom
	for i, s := range sessions {
		// Calculate reversed index: 0->len-1, 1->len-2, etc.
		reversedIdx := len(sessions) - 1 - i
		s.Workspace = startupPath
		s.CurrentWorkdir = currentWorkdir
		result[reversedIdx] = s
	}
	return map[string]any{"sessions": result}, nil
}

// HandleSwitchSession switches to a session by name, ID, index, or path.
func (c *RPCCore) HandleSwitchSession(args string) (any, error) {
	var jsonData struct {
		ID          string `json:"id"`
		SessionPath string `json:"sessionPath"`
	}
	id := strings.TrimSpace(args)
	if parseJSONArgs(args, &jsonData) {
		id = jsonData.ID
		if jsonData.SessionPath != "" {
			id = jsonData.SessionPath
		}
	}
	slog.Info("Received switch_session: id=", "id", id)
	if id == "" {
		return nil, fmt.Errorf("session id is required")
	}

	// Resolve numeric index to session ID
	if idx, err := strconv.Atoi(id); err == nil {
		sessions, listErr := c.SessionMgr.ListSessions()
		if listErr != nil {
			return nil, fmt.Errorf("failed to list sessions: %w", listErr)
		}
		// Reverse to match display order (index 0 = oldest, as shown in renderSessions)
		// ListSessions returns newest-first; renderSessions shows index 0 = oldest
		reversed := make([]session.SessionMeta, len(sessions))
		for i, s := range sessions {
			reversed[len(sessions)-1-i] = s
		}
		if idx < 0 || idx >= len(reversed) {
			return nil, fmt.Errorf("invalid session index %d (valid range: 0-%d)", idx, len(reversed)-1)
		}
		id = reversed[idx].ID
		slog.Info("Resolved session index to ID", "index", idx, "id", id)
	}

	// Treat absolute or relative path as session file
	if strings.Contains(id, string(os.PathSeparator)) || strings.HasSuffix(id, ".jsonl") {
		return c.switchSessionByPath(id)
	}

	return c.switchSessionByID(id)
}

// switchSessionByPath switches to a session loaded from a file path.
func (c *RPCCore) switchSessionByPath(id string) (any, error) {
	sessionPath, err := normalizeSessionPath(id)
	if err != nil {
		return nil, err
	}
	// LoadSessionLazy expects session directory, not file path
	// Extract directory if sessionPath points to messages.jsonl
	sessionDir := sessionPath
	if strings.HasSuffix(sessionPath, ".jsonl") {
		info, err := os.Stat(sessionPath)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			sessionDir = filepath.Dir(sessionPath)
		}
	}
	opts := session.DefaultLoadOptions()
	newSess, err := session.LoadSessionLazy(sessionDir, opts)
	if err != nil {
		return nil, err
	}
	newSessionID := newSess.GetID()
	c.SessionsDir = sessionDir
	c.SessionMgr = session.NewSessionManager(c.SessionsDir)
	_ = c.SessionMgr.SetCurrent(newSessionID)
	if err := c.SessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	// Clear agent context and load new messages
	c.Sess = newSess
	c.SessionComp.Update(c.Sess, c.Compactor)
	c.SetAgentContext(c.CreateBaseContext())
	// Restore last compaction summary if available
	c.Ag.GetContext().LastCompactionSummary = newSess.GetLastCompactionSummary()

	// Update checkpoint manager for new session
	if err := c.UpdateCheckpointManager(); err != nil {
		slog.Warn("Failed to update checkpoint manager for new session", "error", err)
	}

	c.StateMu.Lock()
	c.SessionID = newSessionID
	c.SessionName = resolveSessionName(c.SessionMgr, newSessionID)
	c.StateMu.Unlock()

	// Update trace handler session ID
	if handler := traceevent.GetHandler(); handler != nil {
		if fh, ok := handler.(*traceevent.FileHandler); ok {
			fh.SetSessionID(newSessionID)
		}
	}

	slog.Info("Switched to session", "id", newSessionID, "count", len(newSess.GetMessages()))
	c.Server.EmitEvent(map[string]any{"type": "session_switch", "session": newSessionID, "sessionName": resolveSessionName(c.SessionMgr, newSessionID)})
	return map[string]any{"switched": true, "cancelled": false}, nil
}

// switchSessionByID switches to an existing session by its ID.
func (c *RPCCore) switchSessionByID(id string) (any, error) {
	if err := c.SessionMgr.SetCurrent(id); err != nil {
		return nil, err
	}

	// Load the new session
	newSess, err := c.SessionMgr.GetSession(id)
	if err != nil {
		return nil, err
	}
	newSessionID := newSess.GetID()
	if err := c.SessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	// Clear agent context and load new messages
	c.Sess = newSess
	c.SessionComp.Update(c.Sess, c.Compactor)
	c.SetAgentContext(c.CreateBaseContext())
	// Restore last compaction summary if available
	c.Ag.GetContext().LastCompactionSummary = newSess.GetLastCompactionSummary()

	// Update checkpoint manager for new session
	if err := c.UpdateCheckpointManager(); err != nil {
		slog.Warn("Failed to update checkpoint manager for new session", "error", err)
	}

	c.StateMu.Lock()
	c.SessionID = newSessionID
	c.SessionName = resolveSessionName(c.SessionMgr, newSessionID)
	c.StateMu.Unlock()

	// Update trace handler session ID
	if handler := traceevent.GetHandler(); handler != nil {
		if fh, ok := handler.(*traceevent.FileHandler); ok {
			fh.SetSessionID(newSessionID)
		}
	}

	slog.Info("Switched to session", "id", newSessionID, "count", len(newSess.GetMessages()))
	c.Server.EmitEvent(map[string]any{"type": "session_switch", "session": newSessionID, "sessionName": resolveSessionName(c.SessionMgr, newSessionID)})
	return map[string]any{"switched": true, "cancelled": false}, nil
}

// HandleDeleteSession deletes a session by ID.
func (c *RPCCore) HandleDeleteSession(args string) (any, error) {
	var jsonData struct {
		ID string `json:"id"`
	}
	id := strings.TrimSpace(args)
	if parseJSONArgs(args, &jsonData) && jsonData.ID != "" {
		id = jsonData.ID
	}
	slog.Info("Received delete_session: id=", "id", id)
	if err := c.SessionMgr.DeleteSession(id); err != nil {
		return nil, err
	}
	return map[string]any{"deleted": true}, nil
}

// HandleSetSessionName sets a human-readable name for the current session.
func (c *RPCCore) HandleSetSessionName(args string) (any, error) {
	name := strings.TrimSpace(args)
	var jsonData struct {
		Name string `json:"name"`
	}
	if parseJSONArgs(args, &jsonData) {
		name = jsonData.Name
	}
	slog.Info("Received set_session_name", "name", name)
	if name == "" {
		return nil, fmt.Errorf("session name cannot be empty")
	}
	if _, err := c.Sess.AppendSessionInfo(name, ""); err != nil {
		return nil, err
	}
	if err := c.SessionMgr.UpdateSessionName(c.SessionID, name, ""); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}
	if err := c.SessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}
	c.StateMu.Lock()
	c.SessionName = name
	c.StateMu.Unlock()
	return nil, nil
}

// HandleResumeOnBranch resumes generation on a specific branch.
func (c *RPCCore) HandleResumeOnBranch(args string) (any, error) {
	var jsonData struct {
		EntryID string `json:"entryId"`
	}
	entryID := strings.TrimSpace(args)
	if parseJSONArgs(args, &jsonData) && jsonData.EntryID != "" {
		entryID = jsonData.EntryID
	}
	slog.Info("Received resume_on_branch", "entryId", entryID)
	c.StateMu.Lock()
	streaming := c.IsStreaming
	c.StateMu.Unlock()
	if streaming {
		return nil, fmt.Errorf("agent is busy")
	}

	if entryID == "" {
		return nil, fmt.Errorf("entryId is required")
	}

	if entryID == "root" {
		c.Sess.ResetLeaf()
	} else {
		if err := c.Sess.Branch(entryID); err != nil {
			return nil, err
		}
	}

	c.SetAgentContext(c.CreateBaseContext())

	// Restore llm context from the latest compaction summary on this branch
	c.CompactionCtrl.RestoreContext(c.Sess)

	// Update checkpoint manager (session might have changed due to branch switch)
	if err := c.UpdateCheckpointManager(); err != nil {
		slog.Warn("Failed to update checkpoint manager for branch resume", "error", err)
	}

	if err := c.SessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}
	return map[string]any{"switched": true}, nil
}

// HandleFork forks the conversation at a specific entry point into a new session.
func (c *RPCCore) HandleFork(args string) (any, error) {
	var jsonData struct {
		EntryID string `json:"entryId"`
	}
	entryID := strings.TrimSpace(args)
	if parseJSONArgs(args, &jsonData) && jsonData.EntryID != "" {
		entryID = jsonData.EntryID
	}
	slog.Info("Received fork: entryId=", "value", entryID)
	entry, ok := c.Sess.GetEntry(entryID)
	if !ok || entry.Type != session.EntryTypeMessage || entry.Message == nil || entry.Message.Role != "user" {
		return nil, fmt.Errorf("invalid entryId: %s", entryID)
	}

	text := entry.Message.ExtractText()
	name := fmt.Sprintf("fork-%s", time.Now().Format("20060102-150405"))
	title := "Forked Session"
	newSess, err := c.SessionMgr.ForkSessionFrom(c.Sess, entry.ParentID, name, title)
	if err != nil {
		return nil, err
	}

	newSessionID := newSess.GetID()
	if err := c.SessionMgr.SetCurrent(newSessionID); err != nil {
		return nil, err
	}
	if err := c.SessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	c.Sess = newSess
	c.SessionComp.Update(c.Sess, c.Compactor)
	c.SetAgentContext(c.CreateBaseContext())

	// Update checkpoint manager for new session
	if err := c.UpdateCheckpointManager(); err != nil {
		slog.Warn("Failed to update checkpoint manager for fork", "error", err)
	}

	c.StateMu.Lock()
	c.SessionID = newSessionID
	c.SessionName = name
	c.StateMu.Unlock()

	slog.Info("Forked to new session", "name", name, "id", newSessionID)
	return &rpc.ForkResult{Cancelled: false, Text: text}, nil
}
