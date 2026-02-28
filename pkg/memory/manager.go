package memory

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// MemoryManager handles external memory operations using grep-based search
type MemoryManager struct {
	mu           sync.RWMutex
	sessionDir   string
	detailDir    string
	messagesPath string
}

// NewMemoryManager creates a new memory manager
func NewMemoryManager(sessionDir string) (*MemoryManager, error) {
	detailDir := filepath.Join(sessionDir, "working-memory", "detail")
	messagesPath := filepath.Join(sessionDir, "messages.jsonl")

	// Ensure directories exist
	if err := os.MkdirAll(detailDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create detail directory: %w", err)
	}
	if _, err := os.Stat(messagesPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(messagesPath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create messages.jsonl parent directory: %w", err)
		}
	}

	return &MemoryManager{
		sessionDir:   sessionDir,
		detailDir:    detailDir,
		messagesPath: messagesPath,
	}, nil
}

// SetSessionDir updates the session directory for the memory manager.
// This should be called when the active session changes.
func (m *MemoryManager) SetSessionDir(sessionDir string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	detailDir := filepath.Join(sessionDir, "working-memory", "detail")
	messagesPath := filepath.Join(sessionDir, "messages.jsonl")

	// Ensure directories exist
	if err := os.MkdirAll(detailDir, 0755); err != nil {
		return fmt.Errorf("failed to create detail directory: %w", err)
	}
	if _, err := os.Stat(messagesPath); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(messagesPath), 0755); err != nil {
			return fmt.Errorf("failed to create messages.jsonl parent directory: %w", err)
		}
	}

	m.sessionDir = sessionDir
	m.detailDir = detailDir
	m.messagesPath = messagesPath
	return nil
}

// Search retrieves relevant memories using grep
func (m *MemoryManager) Search(ctx context.Context, opts SearchOptions) ([]*SearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Default: search all sources
	sources := opts.Sources
	if len(sources) == 0 {
        sources = []MemorySource{MemorySourceDetail, MemorySourceMessages}
    }
    var results []*SearchResult

    for _, source := range sources {
        switch source {
        case MemorySourceDetail:
            results = append(results, m.grepDetail(ctx, opts.Query)...)
        case MemorySourceMessages:
            results = append(results, m.grepMessages(ctx, opts.Query)...)
        }
    }

    // Limit results
    if len(results) > opts.Limit {
        results = results[:opts.Limit]
    }

    return results, nil
}

// grepDetail searches the detail/ directory
func (m *MemoryManager) grepDetail(ctx context.Context, query string) []*SearchResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Check if detail directory exists
	if _, err := os.Stat(m.detailDir); os.IsNotExist(err) {
        return nil
    }

    cmd := exec.CommandContext(ctx, "grep", "-r", "-i", "-n", "--", query, m.detailDir)
    output, err := cmd.Output()
    if err != nil {
        // grep returns exit code 1 when no matches found
        return nil
    }
    return m.parseGrepOutput(MemorySourceDetail, output)
}

// grepMessages searches messages.jsonl
func (m *MemoryManager) grepMessages(ctx context.Context, query string) []*SearchResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

    cmd := exec.CommandContext(ctx, "grep", "-i", "-n", "--", query, m.messagesPath)
    output, err := cmd.Output()
    if err != nil {
        // grep returns exit code 1 when no matches found
        return nil
    }
    return m.parseMessagesGrep(output)
}

// parseGrepOutput parses grep output format: "file:line:content"
func (m *MemoryManager) parseGrepOutput(source MemorySource, output []byte) []*SearchResult {
    lines := strings.Split(string(output), "\n")
    results := make([]*SearchResult, 0, len(lines))

    for _, line := range lines {
        if line == "" {
            continue
        }

        // Parse "filepath:linenum:content"
        parts := strings.SplitN(line, ":", 3)
        if len(parts) < 3 {
            continue
        }

        filePath := parts[0]
        lineNum, err := strconv.Atoi(parts[1])
        if err != nil {
            continue
        }
        content := parts[2]

        // Truncate long content
        if len(content) > 300 {
            content = content[:300] + "..."
        }

		// Get relative filename
		relPath, err := filepath.Rel(m.detailDir, filePath)
		if err != nil {
			relPath = filepath.Base(filePath)
		}

        results = append(results, &SearchResult{
            Source:     source,
            FilePath:   relPath,
            LineNumber: lineNum,
            Text:       content,
            Citation:   fmt.Sprintf("detail/%s#L%d", relPath, lineNum),
        })
    }

    return results
}

// parseMessagesGrep parses grep output from messages.jsonl
func (m *MemoryManager) parseMessagesGrep(output []byte) []*SearchResult {
	lines := strings.Split(string(output), "\n")
	results := make([]*SearchResult, 0, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}

		// Parse "linenum:json_content"
		parts := strings.SplitN(line, ":", 2)
		if len(parts) < 2 {
			continue
		}

		lineNumStr := parts[0]
		lineNum, err := strconv.Atoi(lineNumStr)
		if err != nil {
			continue
		}
		content := parts[1]

		// Try to extract text from JSON (simple approach: find content field)
		text := extractTextFromJSON(content)
		if text == "" {
			continue
		}

		// Truncate long content
		if len(text) > 300 {
			text = text[:300] + "..."
		}

		results = append(results, &SearchResult{
			Source:     MemorySourceMessages,
			LineNumber: lineNum,
			Text:       text,
			Citation:   fmt.Sprintf("messages.jsonl#L%d", lineNum),
		})
	}

	return results
}

// extractTextFromJSON attempts to extract text from a JSON line
// This is a simple implementation - looks for "content" field
func extractTextFromJSON(jsonContent string) string {
	// Look for "content":"..." pattern
	idx := strings.Index(jsonContent, `"content":"`)
	if idx == -1 {
		return ""
	}

	// Start after "content":"
	start := idx + len(`"content":"`)

	// Find the closing quote, handling escaped quotes
	end := start
	for i := start; i < len(jsonContent); i++ {
		if jsonContent[i] == '"' {
			// Check if it's escaped
			if i > 0 && jsonContent[i-1] == '\\' {
				continue
			}
			end = i
			break
		}
	}

	if end <= start {
		return ""
	}

	return jsonContent[start:end]
}
