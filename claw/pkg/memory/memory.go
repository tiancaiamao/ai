// Package memory provides persistent memory for the claw agent.
// - Long-term memory: memory/MEMORY.md
// - Daily notes: memory/YYYYMM/YYYYMMDD.md
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store manages persistent memory for the agent.
type Store struct {
	memoryDir  string
	memoryFile string
}

// NewStore creates a new Store with the given base path.
// It ensures the memory directory exists.
func NewStore(basePath string) *Store {
	memoryDir := filepath.Join(basePath, "memory")
	memoryFile := filepath.Join(memoryDir, "MEMORY.md")

	// Ensure memory directory exists
	os.MkdirAll(memoryDir, 0755)

	return &Store{
		memoryDir:  memoryDir,
		memoryFile: memoryFile,
	}
}

// getTodayFile returns the path to today's daily note file (memory/YYYYMM/YYYYMMDD.md).
func (s *Store) getTodayFile() string {
	now := time.Now()
	yearMonth := now.Format("200601")
	today := now.Format("2006-01-02")
	return filepath.Join(s.memoryDir, yearMonth, today+".md")
}

// ReadLongTerm reads the long-term memory (MEMORY.md).
func (s *Store) ReadLongTerm() string {
	data, err := os.ReadFile(s.memoryFile)
	if err != nil {
		return ""
	}
	return string(data)
}

// ReadTodayNote reads today's daily note.
func (s *Store) ReadTodayNote() string {
	todayFile := s.getTodayFile()
	data, err := os.ReadFile(todayFile)
	if err != nil {
		return ""
	}
	return string(data)
}

// GetMemoryContext returns the memory context for inclusion in system prompt.
// It combines long-term memory and today's note.
func (s *Store) GetMemoryContext() string {
	var parts []string

	// Add long-term memory
	if longTerm := s.ReadLongTerm(); longTerm != "" {
		parts = append(parts, fmt.Sprintf("# Memory\n\n%s", longTerm))
	}

	// Add today's note
	if todayNote := s.ReadTodayNote(); todayNote != "" {
		parts = append(parts, fmt.Sprintf("# Today's Note\n\n%s", todayNote))
	}

	if len(parts) == 0 {
		return ""
	}

	return strings.Join(parts, "\n\n---\n\n")
}

// WriteToTodayNote appends content to today's daily note.
func (s *Store) WriteToTodayNote(content string) error {
	todayFile := s.getTodayFile()

	// Ensure directory exists
	dir := filepath.Dir(todayFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Append to file
	file, err := os.OpenFile(todayFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	if _, err := file.WriteString(content); err != nil {
		return fmt.Errorf("failed to write: %w", err)
	}

	return nil
}

// UpdateLongTerm updates the long-term memory file.
func (s *Store) UpdateLongTerm(content string) error {
	if err := os.WriteFile(s.memoryFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write MEMORY.md: %w", err)
	}
	return nil
}
