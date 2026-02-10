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
		if entry.IsDir() {
			continue
		}

		// Only consider session files
		if filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}

		sessionPath := filepath.Join(sm.sessionsDir, entry.Name())
		meta, err := sm.createMetaFromSession(sessionPath)
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
	sessPath := sm.getSessionPath(id)

	// Create session
	sess := NewSession(sessPath)

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
	sessPath := sm.getSessionPath(id)
	newSess := NewSession(sessPath)
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

	return newSess, nil
}

// GetSession retrieves a session by ID.
func (sm *SessionManager) GetSession(id string) (*Session, error) {
	sessPath := sm.getSessionPath(id)
	return LoadSession(sessPath)
}

// GetMeta retrieves session metadata by ID.
func (sm *SessionManager) GetMeta(id string) (*SessionMeta, error) {
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
	// Don't allow deleting current session
	if sm.currentID == id {
		return fmt.Errorf("cannot delete current session")
	}

	sessPath := sm.getSessionPath(id)
	metaPath := sm.getMetaPath(id)

	// Delete session file
	if err := os.Remove(sessPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete session file: %w", err)
	}

	// Delete metadata
	if err := os.Remove(metaPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete metadata: %w", err)
	}

	return nil
}

// SetCurrent sets the current session ID.
func (sm *SessionManager) SetCurrent(id string) error {
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
		// Try to load from pointer file
		ptrPath := sm.getPointerPath()
		data, err := os.ReadFile(ptrPath)
		if err != nil {
			// No pointer file, create default session
			return sm.createDefaultSession()
		}

		var ptr struct {
			CurrentID string `json:"current"`
		}
		if err := json.Unmarshal(data, &ptr); err != nil {
			return sm.createDefaultSession()
		}

		sm.currentID = ptr.CurrentID
	}

	sess, err := sm.GetSession(sm.currentID)
	if err != nil {
		return nil, "", err
	}

	return sess, sm.currentID, nil
}

// SaveCurrent saves the current session pointer.
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

	// Save pointer
	ptr := struct {
		Current string `json:"current"`
	}{
		Current: sm.currentID,
	}

	data, err := json.Marshal(ptr)
	if err != nil {
		return err
	}

	ptrPath := sm.getPointerPath()
	if err := os.WriteFile(ptrPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write pointer: %w", err)
	}

	return nil
}

// getSessionPath returns the session file path for a given ID.
func (sm *SessionManager) getSessionPath(id string) string {
	return filepath.Join(sm.sessionsDir, id+".jsonl")
}

// getMetaPath returns the metadata file path for a given ID.
func (sm *SessionManager) getMetaPath(id string) string {
	// Extract ID from full path if needed
	if filepath.Ext(id) == ".jsonl" {
		id = filepath.Base(id) // Extract just the filename
		id = id[:len(id)-6]    // Remove .jsonl extension
	}
	return filepath.Join(sm.sessionsDir, id+".meta.json")
}

// getPointerPath returns the current session pointer file path.
func (sm *SessionManager) getPointerPath() string {
	return filepath.Join(sm.sessionsDir, "current.json")
}

// saveMeta saves session metadata.
func (sm *SessionManager) saveMeta(id string, meta *SessionMeta) error {
	metaPath := sm.getMetaPath(id)
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

// createMetaFromSession creates metadata from an existing session file.
func (sm *SessionManager) createMetaFromSession(sessPath string) (*SessionMeta, error) {
	sess, err := LoadSession(sessPath)
	if err != nil {
		return nil, err
	}

	// Extract ID from filename
	id := filepath.Base(sessPath)
	id = id[:len(id)-6] // Remove .jsonl extension

	info, err := os.Stat(sessPath)
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

// createDefaultSession creates a default session.
func (sm *SessionManager) createDefaultSession() (*Session, string, error) {
	sess, err := sm.CreateSession("default", "Default Session")
	if err != nil {
		return nil, "", err
	}

	// Extract ID from session file path
	sessPath := sess.GetPath()
	id := filepath.Base(sessPath)
	id = id[:len(id)-6] // Remove .jsonl extension

	// Set current ID to just the ID, not the full path
	sm.currentID = id
	_ = sm.SaveCurrent()

	return sess, id, nil
}

// GetSessionsDir returns the sessions directory path.
func (sm *SessionManager) GetSessionsDir() string {
	return sm.sessionsDir
}
