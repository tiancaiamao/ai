package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// LoadOptions controls session loading behavior.
type LoadOptions struct {
	// MaxMessages limits how many messages to load (0 = auto based on compaction, -1 = all)
	MaxMessages int
	// IncludeSummary includes compaction summary as history
	IncludeSummary bool
	// Lazy enables lazy loading (only load recent entries + compaction summary)
	Lazy bool
}

// DefaultLoadOptions returns the default load options for lazy loading.
func DefaultLoadOptions() LoadOptions {
	return LoadOptions{
		MaxMessages:    0,    // auto
		IncludeSummary: true, // include summary
		Lazy:           true, // lazy load
	}
}

// FullLoadOptions returns options for loading everything (non-lazy).
func FullLoadOptions() LoadOptions {
	return LoadOptions{
		MaxMessages:    -1,  // all
		IncludeSummary: true,
		Lazy:           false,
	}
}

// LoadSessionLazy loads a session with lazy loading support.
// It reads the session file efficiently by:
// 1. Using ResumeOffset if available (fast path)
// 2. Scanning from end to find compaction entry (fallback)
// 3. Only loading recent messages + compaction summary
func LoadSessionLazy(sessionDir string, opts LoadOptions) (*Session, error) {
	if sessionDir == "" {
		sess := &Session{
			entries: make([]*SessionEntry, 0),
			byID:    make(map[string]*SessionEntry),
		}
		sess.header = newSessionHeader(uuid.NewString(), "", "")
		return sess, nil
	}

	// Non-lazy mode: use original LoadSession
	if !opts.Lazy {
		return LoadSession(sessionDir)
	}

	filePath := filepath.Join(sessionDir, "messages.jsonl")
	f, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			id := sessionIDFromDirPath(sessionDir)
			cwd, _ := os.Getwd()
			sess := &Session{
				sessionDir: sessionDir,
				entries:    make([]*SessionEntry, 0),
				byID:       make(map[string]*SessionEntry),
				persist:    true,
			}
			sess.header = newSessionHeader(id, cwd, "")
			return sess, nil
		}
		return nil, err
	}
	defer f.Close()

	// Read header first
	header, err := readHeaderFromFile(f)
	if err != nil {
		// Fallback to full load if header parsing fails
		return LoadSession(sessionDir)
	}

	sess := &Session{
		sessionDir: sessionDir,
		entries:    make([]*SessionEntry, 0),
		byID:       make(map[string]*SessionEntry),
		header:     *header,
		persist:    true,
	}

	// Fast path: use ResumeOffset
	if header.ResumeOffset > 0 {
		_, err = f.Seek(header.ResumeOffset, io.SeekStart)
		if err == nil {
			if err := loadEntriesFromPosition(f, sess, opts); err == nil {
				sess.flushed = true
				return sess, nil
			}
		}
		// If fast path fails, fall through to scanning
	}

	// Fallback: scan from end to find compaction entry
	if err := loadFromEnd(f, sess, opts); err != nil {
		// If lazy loading fails, fall back to full load
		return LoadSession(filePath)
	}

	sess.flushed = true
	return sess, nil
}

// readHeaderFromFile reads the session header from the beginning of the file.
func readHeaderFromFile(f *os.File) (*SessionHeader, error) {
	_, err := f.Seek(0, io.SeekStart)
	if err != nil {
		return nil, err
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		header, err := decodeSessionHeader(line)
		if err != nil {
			return nil, err
		}
		if header != nil {
			return header, nil
		}

		// First non-header line found but no valid header
		break
	}

	return nil, errors.New("no valid session header found")
}

// loadEntriesFromPosition loads entries starting from a file position.
func loadEntriesFromPosition(f *os.File, sess *Session, opts LoadOptions) error {
	scanner := bufio.NewScanner(f)
	messageCount := 0

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		entry, err := decodeSessionEntry(line)
		if err != nil || entry == nil {
			continue
		}

		// Add entry
		sess.addEntry(entry)

		// Count messages for max limit
		if entry.Type == EntryTypeMessage {
			messageCount++
			if opts.MaxMessages > 0 && messageCount >= opts.MaxMessages {
				break
			}
		}
	}

	return scanner.Err()
}

// loadFromEnd scans the file from the end to find the most recent compaction entry
// and loads only the relevant entries.
// Uses a simple approach: read all lines into memory, then scan backwards.
func loadFromEnd(f *os.File, sess *Session, opts LoadOptions) error {
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	size := stat.Size()
	if size == 0 {
		return nil
	}

	// Read entire file into memory (simple but effective for session files)
	_, err = f.Seek(0, io.SeekStart)
	if err != nil {
		return err
	}

	// First pass: read all lines
	var lines [][]byte
	scanner := bufio.NewScanner(f)
	// Increase buffer size for long lines
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		// Make a copy since scanner reuses the buffer
		lineCopy := make([]byte, len(line))
		copy(lineCopy, line)
		lines = append(lines, lineCopy)
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	// Skip header line (first line with type="session")
	startIdx := 0
	for i, line := range lines {
		var peek struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &peek); err == nil && peek.Type == EntryTypeSession {
			startIdx = i + 1
			break
		}
	}

	// Scan backwards to find compaction entry and collect recent messages
	var compactionEntry *SessionEntry
	var recentEntries []*SessionEntry
	messageCount := 0
	maxMessages := opts.MaxMessages
	if maxMessages == 0 {
		maxMessages = 50 // default recent messages to keep
	}

	for i := len(lines) - 1; i >= startIdx; i-- {
		line := lines[i]

		entry, err := decodeSessionEntry(line)
		if err != nil || entry == nil {
			continue
		}

		// Found compaction entry - this is our starting point
		if entry.Type == EntryTypeCompaction {
			compactionEntry = entry
			break
		}

		// Collect recent entries (in reverse order)
		switch entry.Type {
		case EntryTypeMessage:
			recentEntries = append([]*SessionEntry{entry}, recentEntries...)
			messageCount++
		case EntryTypeBranchSummary:
			recentEntries = append([]*SessionEntry{entry}, recentEntries...)
		}

		// Stop if we have enough messages
		if messageCount >= maxMessages {
			break
		}
	}

	// Build final entries list
	if compactionEntry != nil && opts.IncludeSummary {
		sess.addEntry(compactionEntry)
	}

	for _, entry := range recentEntries {
		sess.addEntry(entry)
	}

	return nil
}