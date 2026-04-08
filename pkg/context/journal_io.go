package context

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Journal handles append-only writes to messages.jsonl.
type Journal struct {
	filePath string
	file     *os.File
	mu       sync.Mutex
}

// OpenJournal opens (or creates) the journal file.
func OpenJournal(sessionDir string) (*Journal, error) {
	journalPath := filepath.Join(sessionDir, "messages.jsonl")

	file, err := os.OpenFile(journalPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open journal file: %w", err)
	}

	return &Journal{
		filePath: journalPath,
		file:     file,
	}, nil
}

// AppendMessage appends a message to the journal.
func (j *Journal) AppendMessage(msg AgentMessage) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	entry := NewMessageEntry(msg)
	return j.appendEntry(entry)
}

// AppendTruncate appends a truncate event to the journal.
func (j *Journal) AppendTruncate(event TruncateEvent) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	entry := NewTruncateEntry(event.ToolCallID, event.Turn, event.Trigger)
	entry.Truncate = &event
	return j.appendEntry(entry)
}

// AppendCompact appends a compact event to the journal.
func (j *Journal) AppendCompact(event CompactEvent) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	entry := NewCompactEntry(event.Summary, event.KeptMessageCount, event.Turn)
	entry.Compact = &event
	return j.appendEntry(entry)
}

// AppendEntry appends a generic journal entry.
func (j *Journal) AppendEntry(entry JournalEntry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.appendEntry(entry)
}

// appendEntry writes a single entry to the journal.
func (j *Journal) appendEntry(entry JournalEntry) error {
	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal journal entry: %w", err)
	}

	data = append(data, '\n')
	if _, err := j.file.Write(data); err != nil {
		return fmt.Errorf("failed to write journal entry: %w", err)
	}

	if err := j.file.Sync(); err != nil {
		return fmt.Errorf("failed to sync journal file: %w", err)
	}

	return nil
}

// ReadAll reads all entries from the journal.
func (j *Journal) ReadAll() ([]JournalEntry, error) {
	return j.readFromIndex(0)
}

// ReadFromIndex reads entries starting from a specific message index.
func (j *Journal) ReadFromIndex(messageIndex int) ([]JournalEntry, error) {
	return j.readFromIndex(messageIndex)
}

// readFromIndex reads entries starting from a specific index.
func (j *Journal) readFromIndex(messageIndex int) ([]JournalEntry, error) {
	file, err := os.Open(j.filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open journal for reading: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	entries := []JournalEntry{}
	currentIndex := 0

	for scanner.Scan() {
		if currentIndex < messageIndex {
			currentIndex++
			continue
		}

		var entry JournalEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}

		entries = append(entries, entry)
		currentIndex++
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading journal: %w", err)
	}

	return entries, nil
}

// GetLength returns the number of entries in the journal.
func (j *Journal) GetLength() int {
	file, err := os.Open(j.filePath)
	if err != nil {
		return 0
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	count := 0
	for scanner.Scan() {
		count++
	}
	return count
}

// Close closes the journal file.
func (j *Journal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if j.file != nil {
		if err := j.file.Close(); err != nil {
			return fmt.Errorf("failed to close journal file: %w", err)
		}
		j.file = nil
	}
	return nil
}