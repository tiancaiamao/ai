package tools

import (
	"fmt"
	"sync"
	"time"
)

// InterruptManager handles interrupt files for subagent_wait.
// It supports multiple concurrent interrupt files.
type InterruptManager interface {
	// RegisterInterruptFile registers an interrupt file path and returns a unique ID.
	// The ID should be used to unregister the file when the command completes.
	RegisterInterruptFile(path string) string

	// UnregisterInterruptFile removes an interrupt file by ID.
	UnregisterInterruptFile(id string)

	// GetAllInterruptFiles returns all registered interrupt file paths.
	GetAllInterruptFiles() []string
}

// globalInterruptManager is the global interrupt manager instance.
var (
	interruptManagerMu     sync.Mutex
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

// DefaultInterruptManager supports multiple concurrent interrupt files.
type DefaultInterruptManager struct {
	mu       sync.Mutex
	files    map[string]string // id -> path
	nextID   int
}

// NewDefaultInterruptManager creates a new default interrupt manager.
func NewDefaultInterruptManager() *DefaultInterruptManager {
	return &DefaultInterruptManager{
		files: make(map[string]string),
	}
}

// RegisterInterruptFile registers an interrupt file path and returns a unique ID.
func (m *DefaultInterruptManager) RegisterInterruptFile(path string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	id := fmt.Sprintf("interrupt-%d-%d", time.Now().UnixNano(), m.nextID)
	m.files[id] = path
	return id
}

// UnregisterInterruptFile removes an interrupt file by ID.
func (m *DefaultInterruptManager) UnregisterInterruptFile(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, id)
}

// GetAllInterruptFiles returns all registered interrupt file paths.
func (m *DefaultInterruptManager) GetAllInterruptFiles() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	paths := make([]string, 0, len(m.files))
	for _, path := range m.files {
		paths = append(paths, path)
	}
	return paths
}

// GenerateInterruptFilePath generates a unique interrupt file path.
func GenerateInterruptFilePath() string {
	return fmt.Sprintf("/tmp/ai-interrupt-%d", time.Now().UnixNano())
}