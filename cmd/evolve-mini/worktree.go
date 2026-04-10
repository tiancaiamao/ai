package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tiancaiamao/ai/internal/evolvemini"
)

// WorktreeManager manages git worktrees for each evolution generation.
type WorktreeManager struct {
	BaseRepo    string
	Generations string
	Current     int
}

// NewWorktreeManager creates a new manager.
func NewWorktreeManager(baseRepo, generationsDir string) *WorktreeManager {
	return &WorktreeManager{
		BaseRepo:    baseRepo,
		Generations: generationsDir,
		Current:     0,
	}
}

// CreateWorktree creates a new worktree for a given generation.
func (wm *WorktreeManager) CreateWorktree(gen int) (*GenerationWorkspace, error) {
	genDir := filepath.Join(wm.Generations, fmt.Sprintf("gen_%d", gen))
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		return nil, fmt.Errorf("create generation dir: %w", err)
	}

	// Create git worktree at generation dir
	worktreePath := genDir
	cmd := exec.Command("git", "worktree", "add", worktreePath)
	cmd.Dir = wm.BaseRepo
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("create worktree: %w\n%s", err, output)
	}

	// Create src symlink pointing to worktree
	srcPath := filepath.Join(genDir, "src")
	if err := os.Symlink(worktreePath, srcPath); err != nil {
		return nil, fmt.Errorf("create src symlink: %w", err)
	}

	wm.Current = gen
	return &GenerationWorkspace{
		Generation: gen,
		Path:      genDir,
		Worktree:  worktreePath,
		Src:       srcPath,
	}, nil
}

// BuildWorker compiles the worker binary in a generation workspace.
func (wm *WorktreeManager) BuildWorker(ws *GenerationWorkspace) (string, error) {
	srcPath := ws.Src

	// Build the worker
	cmd := exec.Command("go", "build", "-o", filepath.Join(ws.Path, "worker"), "./cmd/evolve-mini-worker/")
	cmd.Dir = srcPath
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build worker: %w\n%s", err, output)
	}

	return filepath.Join(ws.Path, "worker"), nil
}

// Cleanup removes a generation worktree.
func (wm *WorktreeManager) Cleanup(gen int) error {
	cmd := exec.Command("git", "worktree", "remove",
		fmt.Sprintf("gen_%d", gen))
	cmd.Dir = wm.BaseRepo
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("remove worktree: %w\n%s", err, output)
	}

	// Also remove the generation directory
	genDir := filepath.Join(wm.Generations, fmt.Sprintf("gen_%d", gen))
	if err := os.RemoveAll(genDir); err != nil {
		return fmt.Errorf("remove generation dir: %w", err)
	}

	return nil
}

// GenerationWorkspace represents a single generation's workspace.
type GenerationWorkspace struct {
	Generation int
	Path       string
	Worktree  string
	Src        string // symlink to worktree
}

// InitializeWorktreeManager sets up the workspace for evolution.
func InitializeWorktreeManager(baseRepo string) (*WorktreeManager, error) {
	absRepo, err := filepath.Abs(baseRepo)
	if err != nil {
		return nil, fmt.Errorf("resolve base repo: %w", err)
	}

	// Check if we're in a git repo
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = absRepo
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("not a git repo: %w", err)
	}

	generationsDir := filepath.Join(absRepo, "data", "generations")
	if err := os.MkdirAll(generationsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create generations dir: %w", err)
	}

	return NewWorktreeManager(absRepo, generationsDir), nil
}

// SaveGenerationRecord saves a generation record to disk.
func SaveGenerationRecord(genDir string, record evolvemini.GenerationRecord) error {
	recordPath := filepath.Join(genDir, "generation_record.json")
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal record: %w", err)
	}
	return os.WriteFile(recordPath, data, 0o644)
}