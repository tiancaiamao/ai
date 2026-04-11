package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
// If evolve-baseline branch exists, create from it; otherwise create from HEAD.
func (wm *WorktreeManager) CreateWorktree(gen int) (*GenerationWorkspace, error) {
	genDir := filepath.Join(wm.Generations, fmt.Sprintf("gen_%d", gen))
	if err := os.RemoveAll(genDir); err != nil {
		return nil, fmt.Errorf("reset generation dir: %w", err)
	}
	if err := os.MkdirAll(genDir, 0o755); err != nil {
		return nil, fmt.Errorf("create generation dir: %w", err)
	}

	// Prune old worktrees
	pruneCmd := exec.Command("git", "worktree", "prune")
	pruneCmd.Dir = wm.BaseRepo
	_, _ = pruneCmd.CombinedOutput()

	// Check if baseline branch exists
	cmd := exec.Command("git", "rev-parse", "--verify", "refs/heads/evolve-baseline")
	cmd.Dir = wm.BaseRepo
	var baseRef string = "HEAD"
	if _, err := cmd.CombinedOutput(); err == nil {
		baseRef = "evolve-baseline"
		fmt.Println("  Creating from evolve-baseline branch")
	} else {
		fmt.Println("  Creating from HEAD (baseline not yet established)")
	}

	// Create worktree for this generation
	cmd2 := exec.Command("git", "worktree", "add", "--detach", genDir, baseRef)
	cmd2.Dir = wm.BaseRepo
	if output, err := cmd2.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("create worktree: %w\n%s", err, output)
	}

	wm.Current = gen
	return &GenerationWorkspace{
		Generation: gen,
		Path:       genDir,
		Worktree:   genDir,
		Src:        genDir,
	}, nil
}

// BuildWorker compiles the worker binary in a generation workspace.
func (wm *WorktreeManager) BuildWorker(ws *GenerationWorkspace) (string, error) {
	// Stage changes before building (go build should see uncommitted files)
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = ws.Worktree
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("stage changes: %w\n%s", err, output)
	}

	// Build the worker
	cmd = exec.Command("go", "build", "-o", filepath.Join(ws.Path, "worker"), "./cmd/evolve-mini-worker/")
	cmd.Dir = ws.Src
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build worker: %w\n%s", err, output)
	}

	return filepath.Join(ws.Path, "worker"), nil
}

// CommitBaseline commits worktree changes to evolve-baseline branch for future inheritance.
func (wm *WorktreeManager) CommitBaseline(ws *GenerationWorkspace) error {
	// Commit changes in the worktree (detached HEAD → temporary commit)
	cmd := exec.Command("git", "add", "-A")
	cmd.Dir = ws.Worktree
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stage in worktree: %w\n%s", err, output)
	}

	cmd = exec.Command("git", "commit", "-m", fmt.Sprintf("evolve gen %d (%s)", ws.Generation, time.Now().Format("2006-01-02")))
	cmd.Dir = ws.Worktree
	// Allow empty commits (in case nothing changed)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=evolve", "GIT_AUTHOR_EMAIL=evolve@local", "GIT_COMMITTER_NAME=evolve", "GIT_COMMITTER_EMAIL=evolve@local")
	if output, err := cmd.CombinedOutput(); err != nil {
		// If nothing to commit, that's ok
		if !strings.Contains(string(output), "nothing to commit") {
			return fmt.Errorf("commit in worktree: %w\n%s", err, output)
		}
		return nil
	}

	// Get the commit hash from worktree
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = ws.Worktree
	hashBytes, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("get worktree HEAD: %w", err)
	}
	commitHash := strings.TrimSpace(string(hashBytes))

	// Update or create evolve-baseline branch to point at this commit
	cmd = exec.Command("git", "branch", "-f", "evolve-baseline", commitHash)
	cmd.Dir = wm.BaseRepo
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("update evolve-baseline branch: %w\n%s", err, output)
	}

	fmt.Printf("  evolve-baseline branch updated to %s\n", commitHash[:8])
	return nil
}

// Cleanup removes a generation worktree.
func (wm *WorktreeManager) Cleanup(gen int) error {
	genDir := filepath.Join(wm.Generations, fmt.Sprintf("gen_%d", gen))
	cmd := exec.Command("git", "worktree", "prune")
	cmd.Dir = wm.BaseRepo
	_, _ = cmd.CombinedOutput()
	return os.RemoveAll(genDir)
}

// GenerationWorkspace represents a single generation's workspace.
type GenerationWorkspace struct {
	Generation int
	Path       string
	Worktree   string
	Src        string
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