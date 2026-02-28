package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SessionMeta contains metadata about a session.
type SessionMeta struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Title        string    `json:"title"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	MessageCount int       `json:"messageCount"`
}

// SessionManager manages multiple sessions.
type SessionManager struct {
	sessionsDir string
	currentID   string
}

// NewSessionManager creates a new session manager.
func NewSessionManager(sessionsDir string) *SessionManager {
	return &SessionManager{
		sessionsDir: sessionsDir,
	}
}

// ListSessions returns all sessions.
func (sm *SessionManager) ListSessions() ([]SessionMeta, error) {
	// Ensure directory exists
	if err := os.MkdirAll(sm.sessionsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sessions directory: %w", err)
	}

	// Read directory entries
	entries, err := os.ReadDir(sm.sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions directory: %w", err)
	}

	var sessions []SessionMeta
	for _, entry := range entries {
		// Only consider directories (new session format)
		if !entry.IsDir() {
			continue
		}

		// Check if it's a valid session directory (has messages.jsonl or meta.json)
		sessionDir := filepath.Join(sm.sessionsDir, entry.Name())
		meta, err := sm.createMetaFromSessionDir(sessionDir)
		if err != nil {
			continue
		}
		sessions = append(sessions, *meta)
	}

	return sessions, nil
}

// CreateSession creates a new session with the given name and title.
func (sm *SessionManager) CreateSession(name, title string) (*Session, error) {
	// Ensure directory exists
	if err := os.MkdirAll(sm.sessionsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sessions directory: %w", err)
	}

	// Generate unique ID
	id := uuid.New().String()
	sessDir := sm.getSessionPath(id)

	// Create session directory
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	// Create session
	sess := NewSession(sessDir, nil)

	// Store session info inside the session file
	if _, err := sess.AppendSessionInfo(name, title); err != nil {
		return nil, fmt.Errorf("failed to write session info: %w", err)
	}

	// Create metadata
	meta := &SessionMeta{
		ID:        id,
		Name:      name,
		Title:     title,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// Save metadata
	if err := sm.saveMeta(id, meta); err != nil {
		return nil, fmt.Errorf("failed to save metadata: %w", err)
	}

	// Create working-memory structure
	if _, err := EnsureWorkingMemory(sessDir); err != nil {
		return nil, fmt.Errorf("failed to create working memory: %w", err)
	}

	return sess, nil
}

// ForkSessionFrom creates a new session from a branch of the source session.
// leafID specifies the entry to use as the branch leaf (nil means current leaf).
func (sm *SessionManager) ForkSessionFrom(source *Session, leafID *string, name, title string) (*Session, error) {
	if source == nil {
		return nil, fmt.Errorf("source session is nil")
	}
	if err := os.MkdirAll(sm.sessionsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create sessions directory: %w", err)
	}

	id := uuid.New().String()
	sessDir := sm.getSessionPath(id)

	// Create session directory
	if err := os.MkdirAll(sessDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create session directory: %w", err)
	}

	newSess := NewSession(sessDir, nil)
	newSess.header.ParentSession = source.GetPath()

	branchEntries := []SessionEntry{}
	if leafID != nil {
		branchEntries = source.GetBranch(*leafID)
	}

	newSess.entries = make([]*SessionEntry, 0, len(branchEntries)+1)
	newSess.byID = make(map[string]*SessionEntry)
	newSess.leafID = nil

	for _, entry := range branchEntries {
		copy := entry
		newSess.addEntry(&copy)
	}

	infoEntry := &SessionEntry{
		Type:      EntryTypeSessionInfo,
		ID:        generateEntryID(newSess.byID),
		ParentID:  newSess.leafID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Name:      strings.TrimSpace(name),
		Title:     strings.TrimSpace(title),
	}
	newSess.addEntry(infoEntry)

	if err := newSess.rewriteFile(); err != nil {
		return nil, err
	}

	meta := &SessionMeta{
		ID:           id,
		Name:         name,
		Title:        title,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		MessageCount: len(newSess.GetMessages()),
	}
	if err := sm.saveMeta(id, meta); err != nil {
		return nil, fmt.Errorf("failed to save metadata: %w", err)
	}

	// Copy working-memory from source session
	sourceDir := source.GetDir()
	srcWM := filepath.Join(sourceDir, WorkingMemoryDir)
	dstWM := filepath.Join(sessDir, WorkingMemoryDir)
	if _, err := os.Stat(srcWM); err == nil {
		if err := copyDir(srcWM, dstWM); err != nil {
			// Log but don't fail - working memory copy is not critical
			fmt.Fprintf(os.Stderr, "warning: failed to copy working memory: %v\n", err)
		}
	} else {
		// Create fresh working-memory if source doesn't have one
		if _, err := EnsureWorkingMemory(sessDir); err != nil {
			fmt.Fprintf(os.Stderr, "warning: failed to create working memory: %v\n", err)
		}
	}

	return newSess, nil
}

// copyDir recursively copies a directory
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dstPath, data, info.Mode())
	})
}

// GetSession retrieves a session by ID.
func (sm *SessionManager) GetSession(id string) (*Session, error) {
	id = normalizeSessionID(id)
	if id == "" {
		return nil, fmt.Errorf("session id is required")
	}
	sessPath := sm.getSessionPath(id)
	return LoadSession(sessPath, nil)
}

// GetMeta retrieves session metadata by ID.
func (sm *SessionManager) GetMeta(id string) (*SessionMeta, error) {
	id = normalizeSessionID(id)
	if id == "" {
		return nil, fmt.Errorf("session id is required")
	}
	metaPath := sm.getMetaPath(id)
	meta, err := sm.loadMeta(metaPath)
	if err == nil {
		return meta, nil
	}

	// Fallback: try to build from session file
	sessPath := sm.getSessionPath(id)
	if _, statErr := os.Stat(sessPath); statErr == nil {
		return sm.createMetaFromSession(sessPath)
	}
	return nil, err
}

// DeleteSession deletes a session by ID.
func (sm *SessionManager) DeleteSession(id string) error {
	id = normalizeSessionID(id)
	if id == "" {
		return fmt.Errorf("session id is required")
	}

	// Don't allow deleting current session
	if sm.currentID == id {
		return fmt.Errorf("cannot delete current session")
	}

	sessPath := sm.getSessionPath(id)
	metaPath := sm.getMetaPath(id)

	// Delete session directory (new format) or file (legacy fallback).
	if info, err := os.Stat(sessPath); err == nil {
		if info.IsDir() {
			if err := os.RemoveAll(sessPath); err != nil {
				return fmt.Errorf("failed to delete session directory: %w", err)
			}
		} else if err := os.Remove(sessPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to delete session file: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to stat session path: %w", err)
	}

	// Delete metadata (for legacy/session-file fallback cases).
	if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete metadata: %w", err)
	}

	legacySessionPath := filepath.Join(sm.sessionsDir, id+".jsonl")
	if err := os.Remove(legacySessionPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete legacy session file: %w", err)
	}

	legacyMetaPath := filepath.Join(sm.sessionsDir, id+".meta.json")
	if err := os.Remove(legacyMetaPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete legacy metadata file: %w", err)
	}

	return nil
}

// SetCurrent sets the current session ID.
func (sm *SessionManager) SetCurrent(id string) error {
	id = normalizeSessionID(id)
	if id == "" {
		return fmt.Errorf("session id is required")
	}
	sm.currentID = id
	return nil
}

// GetCurrentID returns the current session ID.
func (sm *SessionManager) GetCurrentID() string {
	return sm.currentID
}

// GetCurrentSession returns the current session.
func (sm *SessionManager) GetCurrentSession() (*Session, error) {
	if sm.currentID == "" {
		return nil, fmt.Errorf("no current session")
	}

	return sm.GetSession(sm.currentID)
}

// LoadCurrent loads the current session from disk.
func (sm *SessionManager) LoadCurrent() (*Session, string, error) {
	if sm.currentID == "" {
		// No persisted current pointer. Each process starts with a new session.
		return sm.createDefaultSession()
	}

	sess, err := sm.GetSession(sm.currentID)
	if err != nil {
		return nil, "", err
	}

	return sess, sm.currentID, nil
}

// SaveCurrent updates metadata for the current session.
func (sm *SessionManager) SaveCurrent() error {
	if sm.currentID == "" {
		return fmt.Errorf("no current session")
	}

	// Update metadata
	meta, err := sm.GetMeta(sm.currentID)
	if err == nil {
		// Get session to count messages
		sess, err := sm.GetSession(sm.currentID)
		if err == nil {
			meta.MessageCount = len(sess.GetMessages())
			meta.UpdatedAt = time.Now()
			_ = sm.saveMeta(sm.currentID, meta)
		}
	}

	return nil
}

// UpdateSessionName updates the session name/title metadata.
func (sm *SessionManager) UpdateSessionName(id, name, title string) error {
	id = normalizeSessionID(id)
	if id == "" {
		return fmt.Errorf("session id is required")
	}
	meta, err := sm.GetMeta(id)
	if err != nil {
		return err
	}
	trimmedName := strings.TrimSpace(name)
	if trimmedName == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	meta.Name = trimmedName
	trimmedTitle := strings.TrimSpace(title)
	if trimmedTitle != "" {
		meta.Title = trimmedTitle
	} else if strings.TrimSpace(meta.Title) == "" {
		meta.Title = trimmedName
	}
	meta.UpdatedAt = time.Now()
	return sm.saveMeta(id, meta)
}

// getSessionPath returns the session directory path for a given ID.
func (sm *SessionManager) getSessionPath(id string) string {
	return filepath.Join(sm.sessionsDir, normalizeSessionID(id))
}

// getMetaPath returns the metadata file path for a given ID.
func (sm *SessionManager) getMetaPath(id string) string {
	return filepath.Join(sm.sessionsDir, normalizeSessionID(id), "meta.json")
}

// saveMeta saves session metadata.
func (sm *SessionManager) saveMeta(id string, meta *SessionMeta) error {
	metaPath := sm.getMetaPath(id)
	if err := os.MkdirAll(filepath.Dir(metaPath), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(metaPath, data, 0644)
}

// loadMeta loads session metadata.
func (sm *SessionManager) loadMeta(metaPath string) (*SessionMeta, error) {
	data, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, err
	}

	var meta SessionMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}

	return &meta, nil
}

// createMetaFromSessionDir creates metadata from an existing session directory.
func (sm *SessionManager) createMetaFromSessionDir(sessDir string) (*SessionMeta, error) {
	// Try to load metadata file first
	id := filepath.Base(sessDir)
	metaPath := filepath.Join(sessDir, "meta.json")
	if data, err := os.ReadFile(metaPath); err == nil {
		var meta SessionMeta
		if err := json.Unmarshal(data, &meta); err == nil {
			return &meta, nil
		}
	}

	// Fallback: create from session file
	sess, err := LoadSession(sessDir, nil)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(sessDir)
	if err != nil {
		return nil, err
	}

	header := sess.GetHeader()
	createdAt := info.ModTime()
	if ts := strings.TrimSpace(header.Timestamp); ts != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
			createdAt = parsed
		}
	}

	name := sess.GetSessionName()
	if name == "" {
		name = id
	}
	title := sess.GetSessionTitle()
	if title == "" {
		title = "Session"
	}

	return &SessionMeta{
		ID:           id,
		Name:         name,
		Title:        title,
		CreatedAt:    createdAt,
		UpdatedAt:    info.ModTime(),
		MessageCount: len(sess.GetMessages()),
	}, nil
}

// createMetaFromSession creates metadata from an existing session file path (legacy).
func (sm *SessionManager) createMetaFromSession(sessPath string) (*SessionMeta, error) {
	// If it's a directory, use the new method
	if info, err := os.Stat(sessPath); err == nil && info.IsDir() {
		return sm.createMetaFromSessionDir(sessPath)
	}

	// Extract ID from filename (legacy file format).
	id := sessionIDFromFilePath(sessPath)

	info, err := os.Stat(sessPath)
	if err != nil {
		return nil, err
	}

	// Best-effort load from new directory format if it exists.
	var sess *Session
	if dirInfo, dirErr := os.Stat(sm.getSessionPath(id)); dirErr == nil && dirInfo.IsDir() {
		sess, _ = LoadSession(sm.getSessionPath(id), nil)
	}

	messageCount := 0
	createdAt := info.ModTime()
	name := id
	title := "Session"

	if sess != nil {
		header := sess.GetHeader()
		if ts := strings.TrimSpace(header.Timestamp); ts != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, ts); err == nil {
				createdAt = parsed
			}
		}
		messageCount = len(sess.GetMessages())
		if sessionName := sess.GetSessionName(); sessionName != "" {
			name = sessionName
		}
		if sessionTitle := sess.GetSessionTitle(); sessionTitle != "" {
			title = sessionTitle
		}
	} else {
		messageCount = countLegacyMessages(sessPath)
	}

	return &SessionMeta{
		ID:           id,
		Name:         name,
		Title:        title,
		CreatedAt:    createdAt,
		UpdatedAt:    info.ModTime(),
		MessageCount: messageCount,
	}, nil
}

// createDefaultSession creates a default session.
func (sm *SessionManager) createDefaultSession() (*Session, string, error) {
	sess, err := sm.CreateSession("default", "Default Session")
	if err != nil {
		return nil, "", err
	}

	id := normalizeSessionID(sess.GetID())

	// Set current ID to just the ID, not the full path
	sm.currentID = id

	return sess, id, nil
}

func normalizeSessionID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	base := filepath.Base(id)
	if strings.HasSuffix(base, ".jsonl") {
		return strings.TrimSuffix(base, ".jsonl")
	}
	return base
}

func countLegacyMessages(sessPath string) int {
	data, err := os.ReadFile(sessPath)
	if err != nil {
		return 0
	}

	count := 0
	for _, line := range splitLines(data) {
		line = bytesTrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if _, ok := msg["role"]; ok {
			count++
		}
	}
	return count
}

// GetSessionsDir returns the sessions directory path.
func (sm *SessionManager) GetSessionsDir() string {
	return sm.sessionsDir
}
