package tools

import (
	"fmt"
	"sync"
	"time"
)

// InterruptManager handles interrupt files for subagent_wait.
// It's a global singleton that can be set from cmd/ai.
type InterruptManager interface {
	// SetInterruptFile sets the current interrupt file path.
	// Returns the path that was set (useful for appending to command).
	SetInterruptFile(path string)
	
	// ClearInterruptFile clears the current interrupt file path.
	ClearInterruptFile()
	
	// GetInterruptFile returns the current interrupt file path.
	GetInterruptFile() string
}

// globalInterruptManager is the global interrupt manager instance.
var (
	interruptManagerMu   sync.Mutex
	globalInterruptManager InterruptManager
)

// SetGlobalInterruptManager sets the global interrupt manager.
// This should be called once during initialization from cmd/ai.
func SetGlobalInterruptManager(im InterruptManager) {
	interruptManagerMu.Lock()
	globalInterruptManager = im
	interruptManagerMu.Unlock()
}

// getGlobalInterruptManager returns the global interrupt manager.
func getGlobalInterruptManager() InterruptManager {
	interruptManagerMu.Lock()
	im := globalInterruptManager
	interruptManagerMu.Unlock()
	return im
}

// DefaultInterruptManager is a simple in-memory implementation.
type DefaultInterruptManager struct {
	mu             sync.Mutex
	currentFile    string
}

// NewDefaultInterruptManager creates a new default interrupt manager.
func NewDefaultInterruptManager() *DefaultInterruptManager {
	return &DefaultInterruptManager{}
}

// SetInterruptFile sets the current interrupt file path.
func (m *DefaultInterruptManager) SetInterruptFile(path string) {
	m.mu.Lock()
	m.currentFile = path
	m.mu.Unlock()
}

// ClearInterruptFile clears the current interrupt file path.
func (m *DefaultInterruptManager) ClearInterruptFile() {
	m.mu.Lock()
	m.currentFile = ""
	m.mu.Unlock()
}

// GetInterruptFile returns the current interrupt file path.
func (m *DefaultInterruptManager) GetInterruptFile() string {
	m.mu.Lock()
	path := m.currentFile
	m.mu.Unlock()
	return path
}

// GenerateInterruptFilePath generates a unique interrupt file path.
func GenerateInterruptFilePath() string {
	return fmt.Sprintf("/tmp/ai-interrupt-%d", time.Now().UnixNano())
}