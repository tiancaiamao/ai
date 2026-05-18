package tools

import (
	"os"
	"path/filepath"
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

func TestMustNewWorkspace(t *testing.T) {
	dir := t.TempDir()
	ws := MustNewWorkspace(dir)
	if ws == nil {
		t.Fatal("MustNewWorkspace should return non-nil")
	}
}

// ---------------------------------------------------------------------------
// SetCWD / GetCWD
// ---------------------------------------------------------------------------

func TestWorkspace_SetCWD(t *testing.T) {
	dir := t.TempDir()
	ws := MustNewWorkspace(dir)

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
	ws := MustNewWorkspace(dir)

	err := ws.SetCWD("/nonexistent/path/xyz")
	if err == nil {
		t.Fatal("SetCWD with nonexistent dir should return error")
	}
}

func TestWorkspace_SetCWD_UpdatesGitRoot(t *testing.T) {
	dir := t.TempDir()
	ws := MustNewWorkspace(dir)

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
	ws := MustNewWorkspace(dir)

	resolved := ws.ResolvePath("foo/bar.txt")
	expected := filepath.Join(dir, "foo", "bar.txt")
	if resolved != expected {
		t.Errorf("ResolvePath(foo/bar.txt) = %q, want %q", resolved, expected)
	}
}

func TestWorkspace_ResolvePath_Absolute(t *testing.T) {
	dir := t.TempDir()
	ws := MustNewWorkspace(dir)

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
	ws := MustNewWorkspace(dir)

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
	ws := MustNewWorkspace(dir)

	// In CI/local, temp dir may or may not be in a git repo
	// Just verify it doesn't panic
	_ = ws.IsGitRepository()
}

// ---------------------------------------------------------------------------
// GetGitRoot
// ---------------------------------------------------------------------------

func TestWorkspace_GetGitRoot(t *testing.T) {
	dir := t.TempDir()
	ws := MustNewWorkspace(dir)

	root := ws.GetGitRoot()
	// Not in a git repo, should fall back to initial cwd
	if root != dir {
		t.Errorf("GetGitRoot() = %q, want %q (not in git repo)", root, dir)
	}
}