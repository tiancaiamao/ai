package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	LLMContextDir = "llm-context"
	OverviewFile  = "overview.md"
	DetailDir     = "detail"
)

// ContextMeta contains metadata about the current context state.
type ContextMeta struct {
	TokensUsed        int     `json:"tokens_used"`
	TokensMax         int     `json:"tokens_max"`
	TokensPercent     float64 `json:"tokens_percent"`
	MessagesInHistory int     `json:"messages_in_history"`
	LLMContextSize    int     `json:"llm_context_size"` // bytes
}

// LLMContextWriter defines the interface for writing LLM context.
type LLMContextWriter interface {
	WriteContent(content string) error
}

// LLMContext manages the agent's llm context file (overview.md).
// Task tracking state is managed separately by TaskTrackingState.
type LLMContext struct {
	mu sync.RWMutex

	// Paths
	sessionDir   string
	overviewPath string
	detailPath   string

	// Cache
	overviewContent string
	overviewModTime time.Time

	// Meta
	tokensUsed    int
	tokensMax     int
	messagesCount int

	// Runtime decision pressure signal
	staleToolOutputs int
}

// NewLLMContext creates a new LLMContext for the given session directory.
func NewLLMContext(sessionDir string) *LLMContext {
	return &LLMContext{
		sessionDir:   sessionDir,
		overviewPath: filepath.Join(sessionDir, LLMContextDir, OverviewFile),
		detailPath:   filepath.Join(sessionDir, LLMContextDir, DetailDir),
	}
}

// GetOverviewTemplate returns the default template for overview.md.
func GetOverviewTemplate(overviewPath, detailPath string) string {
	return fmt.Sprintf(`# LLM Context

<!--
This is your external memory.
This file's content will be injected into your prompt for context recovery.

This is YOUR memory. You control what you see.
-->

## Current Task
<!-- What did the user ask you to do? Current progress? -->


## Key Decisions
<!-- Important decisions made and why? -->


## Known Information
<!-- Project structure, tech stack, key files, etc. -->


## Pending
<!-- Issues or blockers to resolve -->


<!--
Tip:
- Write detailed content to the %s directory when needed
-->
`, detailPath)
}

// ensureLLMContext creates the llm-context directory structure if needed.
func (wm *LLMContext) ensureLLMContext() error {
	wmDir := filepath.Join(wm.sessionDir, LLMContextDir)
	if err := os.MkdirAll(wmDir, 0755); err != nil {
		return fmt.Errorf("failed to create llm-context directory: %w", err)
	}

	detailDir := filepath.Join(wmDir, DetailDir)
	if err := os.MkdirAll(detailDir, 0755); err != nil {
		return fmt.Errorf("failed to create detail directory: %w", err)
	}

	if _, err := os.Stat(wm.overviewPath); os.IsNotExist(err) {
		template := GetOverviewTemplate(wm.overviewPath, wm.detailPath)
		if err := os.WriteFile(wm.overviewPath, []byte(template), 0644); err != nil {
			return fmt.Errorf("failed to write overview template: %w", err)
		}
	}

	return nil
}

// Load loads the overview.md content with mtime-based caching.
func (wm *LLMContext) Load() (string, error) {
	return wm.loadContent()
}

func (wm *LLMContext) loadContent() (string, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if err := wm.ensureLLMContext(); err != nil {
		return "", err
	}

	info, err := os.Stat(wm.overviewPath)
	if err != nil {
		if os.IsNotExist(err) {
			return GetOverviewTemplate(wm.overviewPath, wm.detailPath), nil
		}
		return "", err
	}

	if info.ModTime().Equal(wm.overviewModTime) && wm.overviewContent != "" {
		return wm.overviewContent, nil
	}

	content, err := os.ReadFile(wm.overviewPath)
	if err != nil {
		return "", err
	}

	wm.overviewContent = string(content)
	wm.overviewModTime = info.ModTime()
	return wm.overviewContent, nil
}

// GetPath returns the path to overview.md.
func (wm *LLMContext) GetPath() string {
	return wm.overviewPath
}

// GetDetailDir returns the path to the detail directory.
func (wm *LLMContext) GetDetailDir() string {
	return wm.detailPath
}

// GetSessionDir returns the session directory path.
func (wm *LLMContext) GetSessionDir() string {
	return wm.sessionDir
}

// WriteContent writes content to overview.md.
func (wm *LLMContext) WriteContent(content string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if err := wm.ensureLLMContext(); err != nil {
		return err
	}

	if err := os.WriteFile(wm.overviewPath, []byte(content), 0644); err != nil {
		return err
	}

	wm.overviewContent = content
	wm.overviewModTime = time.Now()
	return nil
}

// SetMeta updates the token and message count metadata.
func (wm *LLMContext) SetMeta(tokensUsed, tokensMax, messagesCount int) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.tokensUsed = tokensUsed
	wm.tokensMax = tokensMax
	wm.messagesCount = messagesCount
}

// GetMeta returns the current metadata.
func (wm *LLMContext) GetMeta() ContextMeta {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	var tokensPercent float64
	if wm.tokensMax > 0 {
		tokensPercent = float64(wm.tokensUsed) / float64(wm.tokensMax) * 100
	}

	var llmContextSize int
	if info, err := os.Stat(wm.overviewPath); err == nil {
		llmContextSize = int(info.Size())
	}

	return ContextMeta{
		TokensUsed:        wm.tokensUsed,
		TokensMax:         wm.tokensMax,
		TokensPercent:     tokensPercent,
		MessagesInHistory: wm.messagesCount,
		LLMContextSize:    llmContextSize,
	}
}

// SetStaleToolCount sets the number of stale tool outputs.
func (wm *LLMContext) SetStaleToolCount(count int) {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.staleToolOutputs = count
}

// GetStaleToolCount returns the number of stale tool outputs.
func (wm *LLMContext) GetStaleToolCount() int {
	wm.mu.RLock()
	defer wm.mu.RUnlock()
	return wm.staleToolOutputs
}

// InvalidateCache clears the content cache.
func (wm *LLMContext) InvalidateCache() {
	wm.mu.Lock()
	defer wm.mu.Unlock()
	wm.overviewContent = ""
	wm.overviewModTime = time.Time{}
}

// SaveCompactionSummary saves a compaction summary to the detail directory.
// Returns the path to the saved summary file.
func (wm *LLMContext) SaveCompactionSummary(summary string) (string, error) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if err := wm.ensureLLMContext(); err != nil {
		return "", err
	}

	// Create compaction summaries directory
	summariesDir := filepath.Join(wm.sessionDir, LLMContextDir, "summaries")
	if err := os.MkdirAll(summariesDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create summaries directory: %w", err)
	}

	// Generate filename with timestamp
	filename := fmt.Sprintf("compact-%s.md", time.Now().Format("20060102-150405"))
	summaryPath := filepath.Join(summariesDir, filename)

	if err := os.WriteFile(summaryPath, []byte(summary), 0644); err != nil {
		return "", fmt.Errorf("failed to write compaction summary: %w", err)
	}

	return summaryPath, nil
}

