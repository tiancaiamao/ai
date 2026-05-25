package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// --- Session management handlers --@

func (app *rpcApp) setSession(newSess *session.Session, newID, newName string) {
	app.sess = newSess
	app.sessionComp.Update(app.sess, app.compactor)
	app.setAgentContext(app.createBaseContext())

	if err := app.updateCheckpointManager(); err != nil {
		slog.Warn("Failed to update checkpoint manager", "error", err)
	}

	// Update trace handler to use the new session ID.
	if handler := traceevent.GetHandler(); handler != nil {
		if fh, ok := handler.(*traceevent.FileHandler); ok {
			fh.SetSessionID(newID)
			app.traceOutputPath = fh.TraceFilePath("")
		}
	}

	app.stateMu.Lock()
	app.sessionID = newID
	app.sessionName = newName
	app.stateMu.Unlock()
}

func (app *rpcApp) handleNewSession(args string) (any, error) {
	var name, title string
	var jsonData struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	}
	if app.parseJSONArgs(args, &jsonData) {
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
	newSess, err := app.sessionMgr.CreateSession(name, title)
	if err != nil {
		return nil, err
	}

	newSessionID := newSess.GetID()

	if err := app.sessionMgr.SetCurrent(newSessionID); err != nil {
		return nil, err
	}
	if err := app.sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	app.setSession(newSess, newSessionID, name)

	slog.Info("Created new session", "name", name, "id", newSessionID)
	app.server.EmitEvent(map[string]any{"type": "session_switch", "session": newSessionID, "sessionName": name})
	return map[string]any{"sessionId": newSessionID, "cancelled": false}, nil
}

func (app *rpcApp) handleResume(args string) (any, error) {
	arg := strings.TrimSpace(args)
	if arg == "" {
		sessions, err := app.sessionMgr.ListSessions()
		if err != nil {
			return nil, fmt.Errorf("failed to list sessions: %w", err)
		}
		return map[string]any{"sessions": sessions}, nil
	}

	var targetID string
	sessions, err := app.sessionMgr.ListSessions()
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	if idx, err := strconv.Atoi(arg); err == nil {
		if idx < 0 || idx >= len(sessions) {
			return nil, fmt.Errorf("session index %d out of range (0-%d)", idx, len(sessions)-1)
		}
		targetID = sessions[idx].ID
	} else {
		for _, s := range sessions {
			if s.ID == arg {
				targetID = s.ID
				break
			}
		}
		if targetID == "" {
			for _, s := range sessions {
				if s.Name == arg {
					targetID = s.ID
					break
				}
			}
		}
		if targetID == "" {
			return nil, fmt.Errorf("session not found: %s", arg)
		}
	}

	newSess, err := app.sessionMgr.GetSession(targetID)
	if err != nil {
		return nil, fmt.Errorf("failed to load session %s: %w", targetID, err)
	}

	if err := app.sessionMgr.SetCurrent(targetID); err != nil {
		return nil, err
	}
	if err := app.sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	newSessionName := resolveSessionName(app.sessionMgr, targetID)
	app.setSession(newSess, targetID, newSessionName)

	slog.Info("Switched to session", "id", targetID, "name", newSessionName)
	app.server.EmitEvent(map[string]any{"type": "session_switch", "session": targetID, "sessionName": newSessionName})
	return map[string]any{"sessionId": targetID, "sessionName": newSessionName}, nil
}

func (app *rpcApp) handleRewind(args string) (any, error) {
	var jsonData struct {
		EntryID string `json:"entryId"`
	}
	entryID := strings.TrimSpace(args)
	if app.parseJSONArgs(args, &jsonData) && jsonData.EntryID != "" {
		entryID = jsonData.EntryID
	}
	slog.Info("Received rewind", "entryId", entryID)
	app.stateMu.Lock()
	streaming := app.isStreaming
	app.stateMu.Unlock()
	if streaming {
		return nil, fmt.Errorf("agent is busy")
	}

	if entryID == "" {
		return nil, fmt.Errorf("entryId is required")
	}

	if entryID == "root" {
		app.sess.ResetLeaf()
	} else {
		// Lazy loading may not have all entries in byID (e.g., pre-compaction).
		// Ensure the full session is loaded so Branch can find the entry.
		if err := app.sess.EnsureFullyLoaded(); err != nil {
			return nil, err
		}
		if err := app.sess.Branch(entryID); err != nil {
			return nil, err
		}
	}

	app.setAgentContext(app.createBaseContext())
	app.restoreLLMContextFromCompaction(app.sess)

	if err := app.sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}
	return map[string]any{"switched": true}, nil
}

func (app *rpcApp) handleFork(args string) (any, error) {
	var jsonData struct {
		EntryID string `json:"entryId"`
	}
	entryID := strings.TrimSpace(args)
	if app.parseJSONArgs(args, &jsonData) && jsonData.EntryID != "" {
		entryID = jsonData.EntryID
	}
	slog.Info("Received fork: entryId=", "value", entryID)
	// Lazy loading may not have all entries in byID (e.g., pre-compaction).
	// Ensure the full session is loaded so GetEntry can find the entry.
	if err := app.sess.EnsureFullyLoaded(); err != nil {
		return nil, err
	}
	entry, ok := app.sess.GetEntry(entryID)
	if !ok || entry.Type != session.EntryTypeMessage || entry.Message == nil || entry.Message.Role != "user" {
		return nil, fmt.Errorf("invalid entryId: %s", entryID)
	}

	text := entry.Message.ExtractText()
	name := fmt.Sprintf("fork-%s", time.Now().Format("20060102-150405"))
	title := "Forked Session"
	newSess, err := app.sessionMgr.ForkSessionFrom(app.sess, entry.ParentID, name, title)
	if err != nil {
		return nil, err
	}

	newSessionID := newSess.GetID()

	if err := app.sessionMgr.SetCurrent(newSessionID); err != nil {
		return nil, err
	}
	if err := app.sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	app.setSession(newSess, newSessionID, name)

	slog.Info("Forked to new session", "name", name, "id", newSessionID)
	return &rpc.ForkResult{Cancelled: false, Text: text}, nil
}

func (app *rpcApp) handleSessionGetState() (any, error) {
	slog.Info("Received get_state")
	compactionState := buildCompactionState(app.compactorConfig, app.compactor)
	app.stateMu.Lock()
	currentSessionID := app.sessionID
	currentSessionName := app.sessionName
	streaming := app.isStreaming
	compacting := app.isCompacting
	thinkingLevel := app.currentThinkingLevel
	autoCompact := app.autoCompactionEnabled
	currentSteeringMode := app.steeringMode
	currentFollowUpMode := app.followUpMode
	modelInfo := app.currentModelInfo
	app.stateMu.Unlock()

	aiLogPath := app.getCurrentAILogPath()

	return &rpc.SessionState{
		Model:                 &modelInfo,
		ThinkingLevel:         thinkingLevel,
		IsStreaming:           streaming,
		IsCompacting:          compacting,
		SteeringMode:          currentSteeringMode,
		FollowUpMode:          currentFollowUpMode,
		SessionFile:           app.sess.GetPath(),
		SessionID:             currentSessionID,
		SessionName:           currentSessionName,
		AIPid:                 os.Getpid(),
		AILogPath:             aiLogPath,
		AIWorkingDir:          app.ws.GetCWD(),
		AIStartupPath:         app.ws.GetGitRoot(),
		AutoCompactionEnabled: autoCompact,
		MessageCount:          len(app.ag.GetMessages()),
		PendingMessageCount:   app.ag.GetPendingFollowUps(),
		Compaction:            compactionState,
	}, nil
}

func (app *rpcApp) handleGetForkMessages(args string) (any, error) {
	_ = args
	slog.Info("Received get_fork_messages")
	forkMessages := app.sess.GetUserMessagesForForking()
	result := make([]rpc.ForkMessage, 0, len(forkMessages))
	for _, msg := range forkMessages {
		result = append(result, rpc.ForkMessage{
			EntryID: msg.EntryID,
			Text:    msg.Text,
		})
	}
	return map[string]any{"messages": result}, nil
}

func (app *rpcApp) handleGetTree(args string) (any, error) {
	_ = args
	slog.Info("Received get_tree")
	entries := app.sess.GetEntries()
	tree := buildTreeEntries(entries, app.sess.GetLeafID())
	return map[string]any{"entries": tree}, nil
}

// registerSessionHandlers registers session-related slash commands.
func (app *rpcApp) registerSessionHandlers() {
	app.server.RegisterSlash("new", "Create a new session and switch to it", func(args string) (any, error) {
		return app.handleNewSession(args)
	})

	app.server.RegisterSlash("session", "Get the current agent state (model, session, streaming status)", func(args string) (any, error) {
		return app.handleSessionGetState()
	})

	app.server.RegisterSlash("rewind", "Resume generation on a specific branch", func(args string) (any, error) {
		return app.handleRewind(args)
	})

	app.server.RegisterSlash("fork", "Fork the conversation at a specific entry point", func(args string) (any, error) {
		return app.handleFork(args)
	})

	app.server.RegisterSlash("resume", "List sessions or resume a session by ID/name", func(args string) (any, error) {
		return app.handleResume(args)
	})

	app.server.RegisterHiddenSlash("get_fork_messages", "Get messages for a fork point (internal)", func(args string) (any, error) {
		return app.handleGetForkMessages(args)
	})

	app.server.RegisterHiddenSlash("get_tree", "Get the conversation tree structure (internal)", func(args string) (any, error) {
		return app.handleGetTree(args)
	})
}
