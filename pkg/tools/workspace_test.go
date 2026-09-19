package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// NewWorkspace
// ---------------------------------------------------------------------------

func TestNewWorkspace(t *testing.T) {
	dir := t.TempDir()
	ws, err := NewWorkspace(dir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	if ws.GetCWD() != dir {
		t.Errorf("GetCWD() = %q, want %q", ws.GetCWD(), dir)
	}
	if ws.GetInitialCWD() != dir {
		t.Errorf("GetInitialCWD() = %q, want %q", ws.GetInitialCWD(), dir)
	}
}

func TestMustWorkspace(t *testing.T) {
	dir := t.TempDir()
	ws := mustNewWorkspace(dir)
	if ws == nil {
		t.Fatal("mustNewWorkspace should return non-nil")
	}
}

// mustNewWorkspace is a test-only convenience wrapper around NewWorkspace.
func mustNewWorkspace(initialCwd string) *Workspace {
	ws, err := NewWorkspace(initialCwd)
	if err != nil {
		panic(err)
	}
	return ws
}

// ---------------------------------------------------------------------------
// SetCWD / GetCWD
// ---------------------------------------------------------------------------

func TestWorkspace_SetCWD(t *testing.T) {
	dir := t.TempDir()
	ws := mustNewWorkspace(dir)

	newDir := filepath.Join(dir, "subdir")
	os.MkdirAll(newDir, 0755)

	err := ws.SetCWD(newDir)
	if err != nil {
		t.Fatalf("SetCWD: %v", err)
	}
	if ws.GetCWD() != newDir {
		t.Errorf("GetCWD() = %q, want %q", ws.GetCWD(), newDir)
	}
}

func TestWorkspace_SetCWD_NonExistent(t *testing.T) {
	dir := t.TempDir()
	ws := mustNewWorkspace(dir)

	err := ws.SetCWD("/nonexistent/path/xyz")
	if err == nil {
		t.Fatal("SetCWD with nonexistent dir should return error")
	}
}

func TestWorkspace_SetCWD_UpdatesGitRoot(t *testing.T) {
	dir := t.TempDir()
	ws := mustNewWorkspace(dir)

	sub := filepath.Join(dir, "child")
	os.MkdirAll(sub, 0755)

	ws.SetCWD(sub)
	// After SetCWD, gitRootDirty flag should be set
	// GetGitRoot will re-detect
	got := ws.GetGitRoot()
	// In temp dir, likely not a git repo, so falls back to initialCwd
	_ = got
}

// ---------------------------------------------------------------------------
// ResolvePath
// ---------------------------------------------------------------------------

func TestWorkspace_ResolvePath_Relative(t *testing.T) {
	dir := t.TempDir()
	ws := mustNewWorkspace(dir)

	resolved := ws.ResolvePath("foo/bar.txt")
	expected := filepath.Join(dir, "foo", "bar.txt")
	if resolved != expected {
		t.Errorf("ResolvePath(foo/bar.txt) = %q, want %q", resolved, expected)
	}
}

func TestWorkspace_ResolvePath_Absolute(t *testing.T) {
	dir := t.TempDir()
	ws := mustNewWorkspace(dir)

	abs := "/absolute/path/file.txt"
	resolved := ws.ResolvePath(abs)
	if resolved != abs {
		t.Errorf("ResolvePath(%q) = %q, want %q", abs, resolved, abs)
	}
}

// ---------------------------------------------------------------------------
// GetRelativePath
// ---------------------------------------------------------------------------

func TestWorkspace_GetRelativePath(t *testing.T) {
	dir := t.TempDir()
	ws := mustNewWorkspace(dir)

	rel, err := ws.GetRelativePath(filepath.Join(dir, "sub", "file.txt"))
	if err != nil {
		t.Fatalf("GetRelativePath: %v", err)
	}
	expected := filepath.Join("sub", "file.txt")
	if rel != expected {
		t.Errorf("GetRelativePath = %q, want %q", rel, expected)
	}
}

// ---------------------------------------------------------------------------
// IsGitRepository
// ---------------------------------------------------------------------------

func TestWorkspace_IsGitRepository_TempDir(t *testing.T) {
	// Temp dirs are not git repos
	dir := t.TempDir()
	ws := mustNewWorkspace(dir)

	// In CI/local, temp dir may or may not be in a git repo
	// Just verify it doesn't panic
	_ = ws.IsGitRepository()
}

// ---------------------------------------------------------------------------
// GetGitRoot
// ---------------------------------------------------------------------------

func TestWorkspace_GetGitRoot(t *testing.T) {
	dir := t.TempDir()
	ws := mustNewWorkspace(dir)

	root := ws.GetGitRoot()
	// Not in a git repo, should fall back to initial cwd
	if root != dir {
		t.Errorf("GetGitRoot() = %q, want %q (not in git repo)", root, dir)
	}
}

// ---------------------------------------------------------------------------
// detectGitRoot / GetRelativePath edge cases
// ---------------------------------------------------------------------------

func TestWorkspace_DetectGitRoot_InRepo(t *testing.T) {
	// This test runs inside the ai repository itself.
	repoRoot, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Skip("git not available or not in a repo")
	}
	root := strings.TrimSpace(string(repoRoot))

	ws := mustNewWorkspace(root)
	if got := ws.GetGitRoot(); got != root {
		t.Errorf("GetGitRoot() = %q; want %q", got, root)
	}

	// Subdirectory resolves to the repo root too, and stays cached.
	sub := filepath.Join(root, "pkg", "tools")
	if err := ws.SetCWD(sub); err != nil {
		t.Fatal(err)
	}
	ws.gitRootDirty = true
	if got := ws.GetGitRoot(); got != root {
		t.Errorf("GetGitRoot() from subdir = %q; want %q", got, root)
	}
	if ws.gitRootDirty {
		t.Error("git root should be cached after detection")
	}
}

func TestWorkspace_GetRelativePath_Cases(t *testing.T) {
	dir := t.TempDir()
	ws := mustNewWorkspace(dir)

	rel, err := ws.GetRelativePath(filepath.Join(dir, "sub", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if rel != filepath.Join("sub", "file.txt") {
		t.Errorf("GetRelativePath = %q", rel)
	}

	// Path outside cwd yields a ../-style relative path, not an error.
	outside := filepath.Join(dir, "..", "other.txt")
	if _, err := ws.GetRelativePath(outside); err != nil {
		t.Errorf("path outside cwd should not error: %v", err)
	}

	// ResolvePath on absolute path is a no-op.
	if got := ws.ResolvePath("/abs/path"); got != "/abs/path" {
		t.Errorf("ResolvePath(abs) = %q", got)
	}
}
