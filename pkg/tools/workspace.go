package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Workspace represents a dynamic workspace that can track directory changes.
// It is used by tools to maintain a consistent working directory across tool invocations,
// especially when working with git worktrees where the agent may change directories.
type Workspace struct {
	mu           sync.RWMutex
	cwd          string
	gitRoot      string
	initialCwd   string
	gitRootDirty bool // flag to avoid repeated git calls
}

// NewWorkspace creates a new Workspace with the specified initial working directory.
// It will attempt to detect the git root directory for session storage.
func NewWorkspace(initialCwd string) (*Workspace, error) {
	ws := &Workspace{
		initialCwd: initialCwd,
		cwd:        initialCwd,
	}
	// Initialize git root
	ws.detectGitRoot()
	return ws, nil
}

// MustNewWorkspace is like NewWorkspace but panics on error. Useful for initialization.
func MustNewWorkspace(initialCwd string) *Workspace {
	ws, err := NewWorkspace(initialCwd)
	if err != nil {
		panic(err)
	}
	return ws
}

// GetCWD returns the current working directory.
func (w *Workspace) GetCWD() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.cwd
}

// SetCWD updates the current working directory.
// This is called when the agent changes directories (e.g., via change_workspace tool).
func (w *Workspace) SetCWD(cwd string) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Resolve to absolute path
	absPath, err := filepath.Abs(cwd)
	if err != nil {
		return fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// Verify the directory exists
	if _, err := os.Stat(absPath); err != nil {
		return fmt.Errorf("directory does not exist: %w", err)
	}

	w.cwd = absPath
	w.gitRootDirty = true // Mark git root as dirty, re-detect on next access
	return nil
}

// GetInitialCWD returns the initial working directory when the workspace was created.
func (w *Workspace) GetInitialCWD() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.initialCwd
}

// GetGitRoot returns the git repository root directory.
// If not in a git repository, it returns the initial working directory.
// This is used for session storage to share sessions across worktrees.
func (w *Workspace) GetGitRoot() string {
	w.mu.RLock()
	if !w.gitRootDirty {
		gitRoot := w.gitRoot
		w.mu.RUnlock()
		if gitRoot != "" {
			return gitRoot
		}
		// gitRoot is empty, need to detect
		w.mu.RLock() // re-acquire write lock
	}

	w.mu.RUnlock()
	w.mu.Lock()
	defer w.mu.Unlock()

	// Double-check after acquiring write lock
	if !w.gitRootDirty && w.gitRoot != "" {
		return w.gitRoot
	}

	w.detectGitRoot()
	return w.gitRoot
}

// detectGitRoot attempts to detect the git repository root.
// It sets w.gitRoot and w.gitRootDirty = false.
func (w *Workspace) detectGitRoot() {
	defer func() { w.gitRootDirty = false }()

	// Try git rev-parse --show-toplevel
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = w.cwd
	output, err := cmd.Output()
	if err == nil {
		gitRoot := strings.TrimSpace(string(output))
		if gitRoot != "" {
			w.gitRoot = gitRoot
			return
		}
	}

	// Not in a git repo, use initial cwd as fallback
	w.gitRoot = w.initialCwd
}

// ResolvePath resolves a relative path against the current working directory.
// If the path is already absolute, it is returned as-is.
func (w *Workspace) ResolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(w.GetCWD(), path)
}

// IsGitRepository returns true if the current workspace is inside a git repository.
func (w *Workspace) IsGitRepository() bool {
	gitRoot := w.GetGitRoot()
	return gitRoot != w.initialCwd || w.isGitRepoDirect()
}

// isGitRepoDirect checks if cwd is directly in a git repo.
func (w *Workspace) isGitRepoDirect() bool {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = w.cwd
	err := cmd.Run()
	return err == nil
}

// GetRelativePath returns the path relative to the current working directory.
// Useful for displaying paths to the user.
func (w *Workspace) GetRelativePath(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(w.GetCWD(), absPath)
	if err != nil {
		return "", err
	}
	return relPath, nil
}
