package session

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// Session represents a conversation session backed by an append-only JSONL file.
type Session struct {
	mu            sync.Mutex
	sessionDir    string // session directory path
	header        SessionHeader
	entries       []*SessionEntry
	byID          map[string]*SessionEntry
	leafID        *string
	flushed       bool
	persist       bool
	workingMemory *agentctx.WorkingMemory // WorkingMemory for saving compaction summaries
}

// ForkMessage represents a user message candidate for forking.
type ForkMessage struct {
	EntryID string
	Text    string
}

// NewSession creates a new session with the given directory path.
func NewSession(sessionDir string, workingMemory *agentctx.WorkingMemory) *Session {
	sess := &Session{
		sessionDir:    sessionDir,
		entries:       make([]*SessionEntry, 0),
		byID:          make(map[string]*SessionEntry),
		persist:       sessionDir != "",
		workingMemory: workingMemory,
	}

	id := sessionIDFromDirPath(sessionDir)
	cwd, _ := os.Getwd()
	sess.header = newSessionHeader(id, cwd, "")
	return sess
}

// LoadSession loads a session from the given directory path.
func LoadSession(sessionDir string, workingMemory *agentctx.WorkingMemory) (*Session, error) {
	sess := &Session{
		sessionDir:    sessionDir,
		entries:       make([]*SessionEntry, 0),
		byID:          make(map[string]*SessionEntry),
		persist:       sessionDir != "",
		workingMemory: workingMemory,
	}

	if sessionDir == "" {
		sess.header = newSessionHeader(uuid.NewString(), "", "")
		return sess, nil
	}

	filePath := filepath.Join(sessionDir, "messages.jsonl")
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			id := sessionIDFromDirPath(sessionDir)
			cwd, _ := os.Getwd()
			sess.header = newSessionHeader(id, cwd, "")
			return sess, nil
		}
		return nil, err
	}

	lines := splitLines(data)
	if len(lines) == 0 {
		id := sessionIDFromDirPath(sessionDir)
		cwd, _ := os.Getwd()
		sess.header = newSessionHeader(id, cwd, "")
		return sess, nil
	}

	firstLine := firstNonEmptyLine(lines)
	header, headerErr := decodeSessionHeader(firstLine)
	if headerErr == nil && header != nil {
		sess.header = *header
		for _, line := range lines {
			if len(line) == 0 {
				continue
			}
			if headerLine(line) {
				continue
			}
			entry, err := decodeSessionEntry(line)
			if err != nil || entry == nil {
				continue
			}
			sess.addEntry(entry)
		}
		sess.flushed = true
		return sess, nil
	}

	// Legacy format: JSONL of AgentMessage objects.
	legacyMessages := make([]agentctx.AgentMessage, 0)
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var msg agentctx.AgentMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		legacyMessages = append(legacyMessages, msg)
	}

	id := sessionIDFromFilePath(filePath)
	cwd, _ := os.Getwd()
	sess.header = newSessionHeader(id, cwd, "")
	var parentID *string
	for _, msg := range legacyMessages {
		ts := time.Now().UTC().Format(time.RFC3339Nano)
		if msg.Timestamp != 0 {
			ts = time.UnixMilli(msg.Timestamp).UTC().Format(time.RFC3339Nano)
		}
		entry := &SessionEntry{
			Type:      EntryTypeMessage,
			ID:        generateEntryID(sess.byID),
			ParentID:  parentID,
			Timestamp: ts,
			Message:   &msg,
		}
		sess.addEntry(entry)
		parentID = &entry.ID
	}
	if err := sess.rewriteFile(); err != nil {
		return nil, err
	}
	return sess, nil
}

// GetMessages returns the current session context messages.
func (s *Session) GetMessages() []agentctx.AgentMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return buildSessionContext(s.entries, s.leafID, s.byID)
}

// GetDir returns the session directory path.
func (s *Session) GetDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionDir
}

// GetPath returns the messages.jsonl file path of the session.
func (s *Session) GetPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.filePath()
}

// filePath returns the messages.jsonl path (internal, must hold lock).
func (s *Session) filePath() string {
	return filepath.Join(s.sessionDir, "messages.jsonl")
}

// GetID returns the session ID from the header.
func (s *Session) GetID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.header.ID
}

// GetHeader returns a copy of the session header.
func (s *Session) GetHeader() SessionHeader {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.header
}

// GetEntries returns a shallow copy of session entries (excluding header).
func (s *Session) GetEntries() []SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := make([]SessionEntry, 0, len(s.entries))
	for _, entry := range s.entries {
		entries = append(entries, *entry)
	}
	return entries
}

// GetEntry returns a copy of an entry by ID.
func (s *Session) GetEntry(id string) (*SessionEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.byID[id]
	if !ok {
		return nil, false
	}
	copy := *entry
	return &copy, true
}

// GetLeafID returns the current leaf ID.
func (s *Session) GetLeafID() *string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leafID
}

// GetBranch returns entries along the path from the root to the given entry ID.
// If id is empty, it uses the current leaf.
func (s *Session) GetBranch(id string) []SessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getBranchLocked(id)
}

// Branch sets the leaf pointer to the given entry ID.
func (s *Session) Branch(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.byID[id]
	if !ok {
		return fmt.Errorf("entry %s not found", id)
	}
	s.leafID = &entry.ID
	return nil
}

// ResetLeaf clears the leaf pointer (before any entries).
func (s *Session) ResetLeaf() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.leafID = nil
}

// AppendMessage appends a message entry and persists it.
func (s *Session) AppendMessage(message agentctx.AgentMessage) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := &SessionEntry{
		Type:      EntryTypeMessage,
		ID:        generateEntryID(s.byID),
		ParentID:  s.leafID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Message:   &message,
	}

	s.addEntry(entry)
	return entry.ID, s.persistEntry(entry)
}

// AppendSessionInfo appends a session info entry.
func (s *Session) AppendSessionInfo(name, title string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := &SessionEntry{
		Type:      EntryTypeSessionInfo,
		ID:        generateEntryID(s.byID),
		ParentID:  s.leafID,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Name:      strings.TrimSpace(name),
		Title:     strings.TrimSpace(title),
	}

	s.addEntry(entry)
	return entry.ID, s.persistEntry(entry)
}

// GetSessionName returns the latest session name if available.
func (s *Session) GetSessionName() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.entries) - 1; i >= 0; i-- {
		entry := s.entries[i]
		if entry.Type == EntryTypeSessionInfo && strings.TrimSpace(entry.Name) != "" {
			return strings.TrimSpace(entry.Name)
		}
	}
	return ""
}

// GetSessionTitle returns the latest session title if available.
func (s *Session) GetSessionTitle() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.entries) - 1; i >= 0; i-- {
		entry := s.entries[i]
		if entry.Type == EntryTypeSessionInfo && strings.TrimSpace(entry.Title) != "" {
			return strings.TrimSpace(entry.Title)
		}
	}
	return ""
}

// GetLastCompactionSummary returns the summary from the most recent compaction entry
// along the current branch, or empty string if none exists.
func (s *Session) GetLastCompactionSummary() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get the current branch path
	path := s.getBranchLocked("")
	if len(path) == 0 {
		return ""
	}

	// Search backwards for the last compaction entry
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Type == EntryTypeCompaction {
			// Try to read from file first (new format)
			if path[i].SummaryFile != nil {
				if summary, err := readSummaryFromFile(s.sessionDir, *path[i].SummaryFile); err == nil && summary != "" {
					return summary
				}
			}
			// Fall back to inline summary (old format)
			return path[i].Summary
		}
	}

	return ""
}

// SetWorkingMemory sets the working memory instance for the session.
func (s *Session) SetWorkingMemory(wm *agentctx.WorkingMemory) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workingMemory = wm
}

// GetCompactionCount returns the number of compaction entries along the current branch.
func (s *Session) GetCompactionCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := s.getBranchLocked("")
	count := 0
	for _, entry := range path {
		if entry.Type == EntryTypeCompaction {
			count++
		}
	}
	return count
}

// GetUserMessagesForForking returns user messages along the current branch.
func (s *Session) GetUserMessagesForForking() []ForkMessage {
	entries := s.GetBranch("")
	results := make([]ForkMessage, 0)
	for _, entry := range entries {
		if entry.Type != EntryTypeMessage || entry.Message == nil {
			continue
		}
		if entry.Message.Role != "user" {
			continue
		}
		results = append(results, ForkMessage{
			EntryID: entry.ID,
			Text:    entry.Message.ExtractText(),
		})
	}
	return results
}

// SaveMessages replaces all messages and saves to disk as a new linear session.
func (s *Session) SaveMessages(messages []agentctx.AgentMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = make([]*SessionEntry, 0, len(messages))
	s.byID = make(map[string]*SessionEntry)
	s.leafID = nil

	var parentID *string
	for _, msg := range messages {
		entry := &SessionEntry{
			Type:      EntryTypeMessage,
			ID:        generateEntryID(s.byID),
			ParentID:  parentID,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Message:   &msg,
		}
		s.addEntry(entry)
		parentID = &entry.ID
	}

	return s.rewriteFile()
}

// AddMessages adds new messages to the session.
func (s *Session) AddMessages(messages ...agentctx.AgentMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, msg := range messages {
		entry := &SessionEntry{
			Type:      EntryTypeMessage,
			ID:        generateEntryID(s.byID),
			ParentID:  s.leafID,
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Message:   &msg,
		}
		s.addEntry(entry)
		if err := s.persistEntry(entry); err != nil {
			return err
		}
	}

	return nil
}

// Clear clears all messages from the session and deletes the file.
func (s *Session) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.entries = make([]*SessionEntry, 0)
	s.byID = make(map[string]*SessionEntry)
	s.leafID = nil
	s.header.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)

	if s.sessionDir == "" {
		return nil
	}

	filePath := s.filePath()
	if err := s.withFileWriteLock(func() error {
		if _, err := os.Stat(filePath); err == nil {
			if err := os.Remove(filePath); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	s.flushed = false
	return nil
}

func (s *Session) addEntry(entry *SessionEntry) {
	s.entries = append(s.entries, entry)
	s.byID[entry.ID] = entry
	s.leafID = &entry.ID
}

func (s *Session) persistEntry(entry *SessionEntry) error {
	if !s.persist || s.sessionDir == "" {
		return nil
	}

	return s.withFileWriteLock(func() error {
		return s.persistEntryLocked(entry)
	})
}

func (s *Session) persistEntryLocked(entry *SessionEntry) error {
	if !s.flushed {
		if err := s.rewriteFileLocked(); err != nil {
			return err
		}
		return nil
	}

	filePath := s.filePath()
	if info, err := os.Stat(filePath); err != nil || info.Size() == 0 {
		if err := s.rewriteFileLocked(); err != nil {
			return err
		}
		return nil
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	data = append(data, '\n')

	file, err := os.OpenFile(filePath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return nil
}

func (s *Session) rewriteFile() error {
	if !s.persist || s.sessionDir == "" {
		s.flushed = true
		return nil
	}

	return s.withFileWriteLock(func() error {
		return s.rewriteFileLocked()
	})
}

func (s *Session) rewriteFileLocked() error {
	filePath := s.filePath()
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmpPath := fmt.Sprintf("%s.tmp-%d-%d", filePath, os.Getpid(), time.Now().UnixNano())
	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(tmpPath)
	}()

	encoder := json.NewEncoder(file)
	if err := encoder.Encode(s.header); err != nil {
		return err
	}

	for _, entry := range s.entries {
		if err := encoder.Encode(entry); err != nil {
			return err
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		return err
	}

	s.flushed = true
	return nil
}

func (s *Session) withFileWriteLock(run func() error) error {
	if !s.persist || s.sessionDir == "" {
		return run()
	}

	filePath := s.filePath()
	lockPath := filePath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return err
	}

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lockFile.Close()

	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() {
		_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	}()

	return run()
}

func (s *Session) getBranchLocked(id string) []SessionEntry {
	var start *SessionEntry
	if id != "" {
		start = s.byID[id]
	} else if s.leafID != nil {
		start = s.byID[*s.leafID]
	}

	path := make([]*SessionEntry, 0)
	current := start
	for current != nil {
		path = append(path, current)
		if current.ParentID == nil {
			break
		}
		current = s.byID[*current.ParentID]
	}

	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}

	entries := make([]SessionEntry, 0, len(path))
	for _, entry := range path {
		entries = append(entries, *entry)
	}
	return entries
}

func generateEntryID(existing map[string]*SessionEntry) string {
	for i := 0; i < 100; i++ {
		candidate := strings.ReplaceAll(uuid.NewString(), "-", "")
		if len(candidate) > 8 {
			candidate = candidate[:8]
		}
		if _, ok := existing[candidate]; !ok {
			return candidate
		}
	}
	return strings.ReplaceAll(uuid.NewString(), "-", "")
}

func decodeSessionEntry(line []byte) (*SessionEntry, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return nil, err
	}
	if probe.Type == EntryTypeSession {
		return nil, nil
	}
	if probe.Type == "" {
		return nil, errors.New("missing type")
	}
	var entry SessionEntry
	if err := json.Unmarshal(line, &entry); err != nil {
		return nil, err
	}
	if entry.ID == "" {
		return nil, errors.New("missing entry id")
	}
	return &entry, nil
}

func headerLine(line []byte) bool {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return false
	}
	return probe.Type == EntryTypeSession
}

func sessionIDFromFilePath(path string) string {
	if path == "" {
		return uuid.NewString()
	}
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	if ext != "" {
		base = strings.TrimSuffix(base, ext)
	}
	if base == "" {
		return uuid.NewString()
	}
	return base
}

// sessionIDFromDirPath extracts session ID from directory path.
func sessionIDFromDirPath(path string) string {
	if path == "" {
		return uuid.NewString()
	}
	base := filepath.Base(path)
	if base == "" || base == "." || base == "/" {
		return uuid.NewString()
	}
	return base
}

func firstNonEmptyLine(lines [][]byte) []byte {
	for _, line := range lines {
		if len(bytesTrimSpace(line)) == 0 {
			continue
		}
		return line
	}
	return nil
}

func bytesTrimSpace(data []byte) []byte {
	return []byte(strings.TrimSpace(string(data)))
}

// GetDefaultSessionsDir returns the default sessions directory for a working directory.
func GetDefaultSessionsDir(cwd string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	safePath := sanitizeSessionPath(cwd)
	return filepath.Join(homeDir, ".ai", "sessions", safePath), nil
}

// GetDefaultSessionPath returns the default session file path for a working directory.
func GetDefaultSessionPath(cwd string) (string, error) {
	dir, err := GetDefaultSessionsDir(cwd)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "session.jsonl"), nil
}

func sanitizeSessionPath(cwd string) string {
	clean := filepath.Clean(cwd)
	trimmed := strings.TrimPrefix(clean, string(os.PathSeparator))
	replaced := strings.NewReplacer(
		string(os.PathSeparator), "-",
		"\\", "-",
		":", "-",
	).Replace(trimmed)
	return fmt.Sprintf("--%s--", replaced)
}

// splitLines splits data into lines.
func splitLines(data []byte) [][]byte {
	lines := make([][]byte, 0)
	start := 0

	for i, b := range data {
		if b == '\n' {
			lines = append(lines, data[start:i])
			start = i + 1
		}
	}

	if start < len(data) {
		lines = append(lines, data[start:])
	}

	return lines
}
