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
