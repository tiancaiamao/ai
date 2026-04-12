package session

import (
	"os"
	"path/filepath"
)

const (
	// LLMContextDir is the directory name for llm context
	LLMContextDir = "llm-context"
	// DetailDir is the directory name for detailed content (L2)
	DetailDir = "detail"
)

// GetLLMContextDetailDir returns the path to the detail directory for a session.
func GetLLMContextDetailDir(sessionDir string) string {
	return filepath.Join(sessionDir, LLMContextDir, DetailDir)
}

// ensureLLMContextDetailDir creates the llm-context/detail directory structure.
func ensureLLMContextDetailDir(sessionDir string) error {
	dir := GetLLMContextDetailDir(sessionDir)
	return os.MkdirAll(dir, 0755)
}