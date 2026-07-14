package session

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// loadSessionLazy is the internal implementation of lazy loading.
// It reads the session file efficiently by scanning from end to find compaction entry.
// Only loads recent messages + compaction summary.
func loadSessionLazy(sessionDir string) (*Session, error) {
	if sessionDir == "" {
		sess := &Session{
			entries: make([]*SessionEntry, 0),
			byID:    make(map[string]*SessionEntry),
		}
		sess.header = newSessionHeader(uuid.NewString(), "", "")
		return sess, nil
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
		return loadSessionFull(sessionDir)
	}

	sess := &Session{
		sessionDir: sessionDir,
		entries:    make([]*SessionEntry, 0),
		byID:       make(map[string]*SessionEntry),
		header:     *header,
		persist:    true,
	}

	// Load from end to find compaction entry
	if err := loadFromEnd(f, sess); err != nil {
		// If lazy loading fails, fall back to full load
		return loadSessionFull(sessionDir)
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

// loadFromEnd scans the file from the end to find the most recent compaction entry
// and loads only the relevant entries.
// Starts with a 256KB tail scan; if no compaction entry is found, progressively
// doubles the scan window until one is found or the entire file is scanned.
func loadFromEnd(f *os.File, sess *Session) error {
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	size := stat.Size()
	if size == 0 {
		return nil
	}

	const minScanSize = 256 * 1024 // 256KB initial scan
	scanSize := int64(minScanSize)

	for {
		if scanSize > size {
			scanSize = size
		}
		startOffset := size - scanSize
		if startOffset < 0 {
			startOffset = 0
		}

		_, err = f.Seek(startOffset, io.SeekStart)
		if err != nil {
			return err
		}

		// Read tail data
		tailData := make([]byte, size-startOffset)
		if _, err := io.ReadFull(f, tailData); err != nil && err != io.ErrUnexpectedEOF {
			return err
		}

		// Split lines, skipping the first line if it's incomplete (we started mid-line)
		lines := splitLines(tailData)
		if len(lines) > 0 && startOffset > 0 {
			// First line might be incomplete (we started mid-file), skip it
			var test map[string]any
			if json.Unmarshal(lines[0], &test) != nil {
				lines = lines[1:]
			}
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

			// Collect entries after the most recent compaction
			switch entry.Type {
			case EntryTypeMessage:
				recentEntries = append([]*SessionEntry{entry}, recentEntries...)
			case EntryTypeBranchSummary:
				recentEntries = append([]*SessionEntry{entry}, recentEntries...)
			}
		}

		if compactionEntry != nil {
			// Found compaction entry — process it and remaining recent entries
			// Load compressed messages from snapshot file
			if compactionEntry.SnapshotRef != "" && sess.sessionDir != "" {
				snapshotPath := filepath.Join(sess.sessionDir, compactionEntry.SnapshotRef)
				loadedMessages, err := loadSnapshotMessages(snapshotPath)
				if err == nil {
					// Add compressed messages as entries
					var parentID *string
					for i := range loadedMessages {
						ts := time.Now().UTC().Format(time.RFC3339Nano)
						if loadedMessages[i].Timestamp != 0 {
							ts = time.UnixMilli(loadedMessages[i].Timestamp).UTC().Format(time.RFC3339Nano)
						}
						entry := &SessionEntry{
							Type:      EntryTypeMessage,
							ID:        generateEntryID(sess.byID),
							ParentID:  parentID,
							Timestamp: ts,
							Message:   &loadedMessages[i],
						}
						sess.addEntry(entry)
						pid := entry.ID
						parentID = &pid
					}
				}
			}

			// Build final entries list
			if compactionEntry != nil {
				// Compaction entry's parent is the last compressed message
				if len(sess.entries) > 0 {
					lastID := sess.entries[len(sess.entries)-1].ID
					compactionEntry.ParentID = &lastID
				} else {
					compactionEntry.ParentID = nil
				}
				sess.addEntry(compactionEntry)
			}

			// Fix message chain: if the first entry's parent is not in byID, set it to nil
			// or point to compaction entry if available
			if len(recentEntries) > 0 {
				firstEntry := recentEntries[0]
				if firstEntry.ParentID != nil {
					_, parentExists := sess.byID[*firstEntry.ParentID]
					if !parentExists {
						// Parent not loaded, break the chain here
						// If we have a compaction entry, link to it; otherwise set to nil
						if compactionEntry != nil {
							firstEntry.ParentID = &compactionEntry.ID
						} else {
							firstEntry.ParentID = nil
						}
					}
				}
			}

			for _, entry := range recentEntries {
				sess.addEntry(entry)
			}

			return nil
		}

		// No compaction found in this chunk
		if startOffset == 0 {
			// Scanned entire file without finding a compaction entry.
			// Process all remaining lines as a full session load
			// to avoid re-reading the file in loadSessionFull.
			for _, line := range lines[startIdx:] {
				entry, err := decodeSessionEntry(line)
				if err != nil || entry == nil {
					continue
				}
				sess.addEntry(entry)
			}
			return nil
		}

		// Double scan size and retry
		scanSize *= 2
	}

	return nil
}

// scanSessionInfoFromTail scans the tail of a JSONL file for the most recent
// SessionInfo entries to extract the session name and title.
// Lightweight: reads only the last 64KB, doesn't load all entries.
func scanSessionInfoFromTail(f *os.File) (name, title string) {
	stat, err := f.Stat()
	if err != nil || stat.Size() == 0 {
		return "", ""
	}

	size := stat.Size()
	const scanSize int64 = 64 * 1024 // 64KB tail scan
	startOffset := size - scanSize
	if startOffset < 0 {
		startOffset = 0
	}

	_, err = f.Seek(startOffset, io.SeekStart)
	if err != nil {
		return "", ""
	}

	data := make([]byte, size-startOffset)
	n, _ := f.Read(data)
	data = data[:n]

	lines := splitLines(data)
	if len(lines) > 0 && startOffset > 0 {
		var test map[string]any
		if json.Unmarshal(lines[0], &test) != nil {
			lines = lines[1:]
		}
	}

	// Scan backward for SessionInfo entries
	for i := len(lines) - 1; i >= 0; i-- {
		var entry struct {
			Type  string `json:"type"`
			Name  string `json:"name"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal(lines[i], &entry); err != nil {
			continue
		}
		if entry.Type == EntryTypeSessionInfo {
			if name == "" && strings.TrimSpace(entry.Name) != "" {
				name = strings.TrimSpace(entry.Name)
			}
			if title == "" && strings.TrimSpace(entry.Title) != "" {
				title = strings.TrimSpace(entry.Title)
			}
			if name != "" && title != "" {
				break
			}
		}
	}

	return name, title
}
