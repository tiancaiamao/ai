package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ----- BashTool metadata + SetTimeout ---------------------------------------

func TestBashTool_Metadata(t *testing.T) {
	ws, err := NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	tool := NewBashTool(ws)

	if tool.Name() != "bash" {
		t.Errorf("Name = %q, want %q", tool.Name(), "bash")
	}
	if tool.Description() == "" {
		t.Error("Description should not be empty")
	}
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("Parameters type = %v, want object", params["type"])
	}
}

// TestBashTool_SetTimeout ensures SetTimeout updates the per-command timeout.
func TestBashTool_SetTimeout(t *testing.T) {
	ws, _ := NewWorkspace(t.TempDir())
	tool := NewBashTool(ws)
	original := tool.execTimeout
	tool.SetTimeout(7 * time.Second)
	if tool.execTimeout != 7*time.Second {
		t.Errorf("expected 7s, got %v", tool.execTimeout)
	}
	if tool.execTimeout == original {
		t.Error("expected timeout to change")
	}
}

// TestIsBareCDCommand_AllBrands exercises all branches of isBareCDCommand.
func TestIsBareCDCommand_AllBrands(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		// Plain "cd" returns true.
		{"cd", true},
		{"  cd  ", true},
		{"cd\t", true},
		{"cd\n", true},

		// cd with target — still considered bare (no shell operators).
		{"cd /tmp", true},
		{"cd .", true},
		{"cd ..", true},

		// Compound commands with shell operators return false.
		{"cd /tmp && make", false},
		{"cd /tmp || exit", false},
		{"cd /tmp; make", false},
		{"cd /tmp | head", false},
		{"cd /tmp\necho", false},

		// Non-cd commands.
		{"echo cd", false},
		{"ls", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isBareCDCommand(c.cmd); got != c.want {
			t.Errorf("isBareCDCommand(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

// ----- WriteTool Execute edge cases -----------------------------------------

func TestWriteTool_TildeExpansion(t *testing.T) {
	tool, _ := newWriteToolInTempDir(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	rel := filepath.Join("~/", "ai_test_file.txt")
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    rel,
		"content": "tilde-content",
	})
	if err != nil {
		t.Fatalf("Execute with ~ expansion failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, "ai_test_file.txt"))
	if err != nil {
		t.Fatalf("expected file in $HOME: %v", err)
	}
	if string(data) != "tilde-content" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

// TestWriteTool_RelativePathCreatesDir exercises the MkdirAll branch via a relative
// path that needs a parent directory created inside the workspace tempdir.
func TestWriteTool_RelativePathCreatesDir(t *testing.T) {
	tool, _ := newWriteToolInTempDir(t)
	rel := filepath.Join("subdir", "nested", "file.txt")
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    rel,
		"content": "nested",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
}

// TestWriteTool_MkdirAllFailure triggers the MkdirAll error path by writing to
// a subdirectory of a regular file (which can't contain children).
func TestWriteTool_MkdirAllFailure(t *testing.T) {
	tool, dir := newWriteToolInTempDir(t)
	// Create a regular file. Try to write to "<that file>/nested/file.txt".
	regular := filepath.Join(dir, "regular_file")
	if err := os.WriteFile(regular, []byte("x"), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	absTarget := filepath.Join(regular, "nested", "file.txt")
	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    absTarget,
		"content": "fail",
	})
	if err == nil {
		t.Error("expected error when MkdirAll fails")
	}
}

// ----- ReadTool: Name/Description/Parameters + parsePositiveIntArg ---------

func TestReadTool_Metadata(t *testing.T) {
	ws, _ := NewWorkspace(t.TempDir())
	tool := NewReadTool(ws)
	if tool.Name() != "read" {
		t.Errorf("Name = %q, want %q", tool.Name(), "read")
	}
	if tool.Description() == "" {
		t.Error("Description should not be empty")
	}
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("Parameters type = %v, want object", params["type"])
	}
}

// TestParsePositiveIntArg_Branches systematically exercises every branch of
// parsePositiveIntArg's type switch.
func TestParsePositiveIntArg_Branches(t *testing.T) {
	def := 99

	// Missing key returns default.
	got, err := parsePositiveIntArg(map[string]any{}, "n", def)
	if err != nil || got != def {
		t.Errorf("missing key: got (%d, %v), want (%d, nil)", got, err, def)
	}

	type testCase struct {
		name    string
		value   any
		wantOK  bool
		wantVal int
	}

	// Float64
	floatCases := []testCase{
		{"float64 valid", float64(5.0), true, 5},
		{"float64 zero", float64(0.0), false, 0},
		{"float64 negative", float64(-1.0), false, 0},
		{"float64 non-integer", float64(1.5), false, 0},
	}
	for _, c := range floatCases {
		got, err := parsePositiveIntArg(map[string]any{"n": c.value}, "n", def)
		if c.wantOK && (err != nil || got != c.wantVal) {
			t.Errorf("%s: got (%d, %v), want (%d, nil)", c.name, got, err, c.wantVal)
		}
		if !c.wantOK && err == nil {
			t.Errorf("%s: expected error, got (%d, nil)", c.name, got)
		}
	}

	// Float32
	if got, err := parsePositiveIntArg(map[string]any{"n": float32(3.0)}, "n", def); err != nil || got != 3 {
		t.Errorf("float32 valid: got (%d, %v), want (3, nil)", got, err)
	}
	if got, err := parsePositiveIntArg(map[string]any{"n": float32(-1.0)}, "n", def); err == nil {
		t.Errorf("float32 negative: expected error, got %d", got)
	}

	// All int types
	intCases := []struct {
		name  string
		value any
		want  int
	}{
		{"int", 7, 7},
		{"int8", int8(8), 8},
		{"int16", int16(16), 16},
		{"int32", int32(32), 32},
		{"int64", int64(64), 64},
	}
	for _, c := range intCases {
		got, err := parsePositiveIntArg(map[string]any{"n": c.value}, "n", def)
		if err != nil || got != c.want {
			t.Errorf("%s: got (%d, %v), want (%d, nil)", c.name, got, err, c.want)
		}
	}

	// Negative ints fail
	for _, c := range []struct {
		name  string
		value any
	}{
		{"int<1", 0},
		{"int8<1", int8(0)},
		{"int16<1", int16(-1)},
		{"int32<1", int32(-2)},
		{"int64<1", int64(-3)},
	} {
		_, err := parsePositiveIntArg(map[string]any{"n": c.value}, "n", def)
		if err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}

	// All uint types
	uintCases := []struct {
		name  string
		value any
		want  int
	}{
		{"uint", uint(1), 1},
		{"uint8", uint8(2), 2},
		{"uint16", uint16(3), 3},
		{"uint32", uint32(4), 4},
		{"uint64", uint64(5), 5},
	}
	for _, c := range uintCases {
		got, err := parsePositiveIntArg(map[string]any{"n": c.value}, "n", def)
		if err != nil || got != c.want {
			t.Errorf("%s: got (%d, %v), want (%d, nil)", c.name, got, err, c.want)
		}
	}

	// uint zero fails
	for _, c := range []struct {
		name  string
		value any
	}{
		{"uint<1", uint(0)},
		{"uint8<1", uint8(0)},
		{"uint16<1", uint16(0)},
		{"uint32<1", uint32(0)},
		{"uint64<1", uint64(0)},
	} {
		_, err := parsePositiveIntArg(map[string]any{"n": c.value}, "n", def)
		if err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}

	// String parsing
	got, err = parsePositiveIntArg(map[string]any{"n": " 42 "}, "n", def)
	if err != nil || got != 42 {
		t.Errorf("string valid: got (%d, %v), want (42, nil)", got, err)
	}
	for _, c := range []struct {
		name  string
		value any
	}{
		{"string invalid", "abc"},
		{"string zero", "0"},
		{"string negative", "-5"},
		{"string empty", "  "},
	} {
		_, err := parsePositiveIntArg(map[string]any{"n": c.value}, "n", def)
		if err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}

	// Default branch: unknown type (e.g. a struct{}) must error.
	_, err = parsePositiveIntArg(map[string]any{"n": struct{}{}}, "n", def)
	if err == nil {
		t.Error("unknown type: expected error")
	}
}

// TestParsePositiveIntArg_IntOverflow exercises the int64 overflow branch.
func TestParsePositiveIntArg_IntOverflow(t *testing.T) {
	// uint64 = maxInt+1 should error. Use a sufficiently large value.
	big := uint64(^uint(0))
	_, err := parsePositiveIntArg(map[string]any{"n": big}, "n", 0)
	if err == nil {
		t.Error("expected overflow error for uint64(maxUint)")
	}

	// uint32 overflow only triggers on 32-bit platforms; on 64-bit, maxInt > uint32 max,
	// so we skip that case.

	// int64 overflow: only triggers when int is 32-bit. Skip — we can't simulate portably.
}

// TestDetectFileType_Branches covers all three branches of DetectFileType.
func TestDetectFileType_Branches(t *testing.T) {
	// Text extensions.
	for _, ext := range []string{".txt", ".md", ".go", ".py", ".json"} {
		if got := DetectFileType("foo" + ext); got != "text" {
			t.Errorf("DetectFileType(%q) = %q, want %q", "foo"+ext, got, "text")
		}
	}
	// Unknown extension -> binary.
	if got := DetectFileType("foo.unknownext"); got != "binary" {
		t.Errorf("DetectFileType(unknown) = %q, want %q", got, "binary")
	}
	// No extension -> binary.
	if got := DetectFileType("README"); got != "binary" {
		t.Errorf("DetectFileType(README) = %q, want %q", got, "binary")
	}
	// Mixed case should still match.
	if got := DetectFileType("foo.JSON"); got != "text" {
		t.Errorf("DetectFileType(.JSON uppercase) = %q, want %q", got, "text")
	}
}

// TestGetReadLimit_EnvOverride covers the env-override branch of getReadLimit.
func TestGetReadLimit_EnvOverride(t *testing.T) {
	t.Setenv("AI_READ_MAX_LINES", "1234")
	if got := getReadLimit(); got != 1234 {
		t.Errorf("expected 1234, got %d", got)
	}

	t.Setenv("AI_READ_MAX_LINES", "")
	if got := getReadLimit(); got != defaultReadLimit {
		t.Errorf("expected default %d, got %d", defaultReadLimit, got)
	}

	t.Setenv("AI_READ_MAX_LINES", "not-a-number")
	if got := getReadLimit(); got != defaultReadLimit {
		t.Errorf("invalid env should fall back to default %d, got %d", defaultReadLimit, got)
	}

	t.Setenv("AI_READ_MAX_LINES", "-5")
	if got := getReadLimit(); got != defaultReadLimit {
		t.Errorf("negative env should fall back to default %d, got %d", defaultReadLimit, got)
	}
}

// TestGetReadMaxBytes_EnvOverride covers the env-override branch of getReadMaxBytes.
func TestGetReadMaxBytes_EnvOverride(t *testing.T) {
	t.Setenv("AI_READ_MAX_BYTES", "9999")
	if got := getReadMaxBytes(); got != 9999 {
		t.Errorf("expected 9999, got %d", got)
	}

	t.Setenv("AI_READ_MAX_BYTES", "")
	if got := getReadMaxBytes(); got != defaultReadMaxBytes {
		t.Errorf("expected default %d, got %d", defaultReadMaxBytes, got)
	}

	t.Setenv("AI_READ_MAX_BYTES", "garbage")
	if got := getReadMaxBytes(); got != defaultReadMaxBytes {
		t.Errorf("invalid env should fall back to default %d, got %d", defaultReadMaxBytes, got)
	}
}

// ----- GrepTool helpers ----------------------------------------------------

// TestGetBoolArg covers both branches of getBoolArg.
func TestGetBoolArg(t *testing.T) {
	args := map[string]any{
		"yes":   true,
		"strT":  "true",
		"strF":  "false",
		"other": "maybe",
		"int":   1,
		"nil":   nil,
	}
	if !getBoolArg(args, "yes") {
		t.Error("yes: expected true")
	}
	if !getBoolArg(args, "strT") {
		t.Error("strT: expected true")
	}
	if getBoolArg(args, "strF") {
		t.Error("strF: expected false")
	}
	if getBoolArg(args, "other") {
		t.Error("other: expected false")
	}
	if getBoolArg(args, "int") {
		t.Error("int: expected false (only bool/string)")
	}
	if getBoolArg(args, "nil") {
		t.Error("nil: expected false")
	}
	if getBoolArg(args, "missing") {
		t.Error("missing: expected false")
	}
}

// TestGetIntArg covers all branches of getIntArg.
func TestGetIntArg(t *testing.T) {
	args := map[string]any{
		"f64":   float64(12),
		"int":   42,
		"str":   "100",
		"bad":   "abc",
		"other": []int{1, 2}, // unsupported type
	}
	if got := getIntArg(args, "f64"); got != 12 {
		t.Errorf("f64: got %d", got)
	}
	if got := getIntArg(args, "int"); got != 42 {
		t.Errorf("int: got %d", got)
	}
	if got := getIntArg(args, "str"); got != 100 {
		t.Errorf("str: got %d", got)
	}
	if got := getIntArg(args, "bad"); got != 0 {
		t.Errorf("bad string should fall to 0, got %d", got)
	}
	if got := getIntArg(args, "other"); got != 0 {
		t.Errorf("other type should fall to 0, got %d", got)
	}
	if got := getIntArg(args, "missing"); got != 0 {
		t.Errorf("missing key should return 0, got %d", got)
	}
}

// TestGetIntArgDefault covers both branches.
func TestGetIntArgDefault(t *testing.T) {
	if got := getIntArgDefault(map[string]any{"n": 5}, "n", 99); got != 5 {
		t.Errorf("valid: got %d, want 5", got)
	}
	if got := getIntArgDefault(map[string]any{"n": -5}, "n", 99); got != 99 {
		t.Errorf("non-positive: got %d, want default 99", got)
	}
	if got := getIntArgDefault(map[string]any{}, "n", 99); got != 99 {
		t.Errorf("missing: got %d, want default 99", got)
	}
}

// TestLimitMatches_Trunctation covers the truncation branch.
func TestLimitMatches_Trunctation(t *testing.T) {
	// 5 match lines, limit 2 → should keep first 2, truncate remaining 3.
	input := "f1.go:1:line1\nf1.go:2:line2\nf2.go:1:line3\nf2.go:2:line4\nf3.go:1:line5"
	output := limitMatches(input, 2)
	if !strings.Contains(output, "[3 matches truncated") {
		t.Errorf("expected truncation message, got: %s", output)
	}
	// Original first 2 matches preserved.
	if !strings.Contains(output, "line1") || !strings.Contains(output, "line2") {
		t.Errorf("expected first 2 matches preserved, got: %s", output)
	}
	// Remaining matches not present.
	if strings.Contains(output, "line3") {
		t.Errorf("expected line3 to be truncated, got: %s", output)
	}
}

// TestLimitMatches_NoTruncation covers the under-limit path.
func TestLimitMatches_NoTruncation(t *testing.T) {
	input := "f1.go:1:line1\nf1.go:2:line2"
	output := limitMatches(input, 10)
	if output != input {
		t.Errorf("expected input unchanged, got: %s", output)
	}
}

// TestLimitMatches_ContextLinesCoverTruncation verifies context lines are
// preserved correctly even when truncation kicks in.
func TestLimitMatches_ContextLinesCoverTruncation(t *testing.T) {
	// 3 match lines, 1 context line between line1 and line2.
	// Limit = 1 → first line kept, context for line2 + line2 + line3 truncated.
	input := "f1.go:1:match1\nf1.go-2-context\nf1.go:3:match3\nf1.go:4:match4"
	output := limitMatches(input, 1)
	if !strings.Contains(output, "match1") {
		t.Errorf("first match should be kept: %s", output)
	}
	if strings.Contains(output, "match3") {
		t.Errorf("subsequent matches should be truncated: %s", output)
	}
}

// ----- ChangeWorkspaceTool metadata -----------------------------------------

func TestChangeWorkspaceTool_Metadata(t *testing.T) {
	ws, _ := NewWorkspace(t.TempDir())
	tool := NewChangeWorkspaceTool(ws)
	if tool.Name() != "change_workspace" {
		t.Errorf("Name = %q, want %q", tool.Name(), "change_workspace")
	}
	if tool.Description() == "" {
		t.Error("Description should not be empty")
	}
	if tool.Parameters()["type"] != "object" {
		t.Error("expected object parameters")
	}
}

// TestChangeWorkspaceTool_InvalidPathType exercises the !ok branch.
func TestChangeWorkspaceTool_InvalidPathType(t *testing.T) {
	ws, _ := NewWorkspace(t.TempDir())
	tool := NewChangeWorkspaceTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{"path": 123})
	if err == nil {
		t.Error("expected error for non-string path")
	}
}

// ----- Workspace: MustNewWorkspace + GetRelativePath + edge cases ----------

func TestMustNewWorkspace_Success(t *testing.T) {
	ws := MustNewWorkspace(t.TempDir())
	if ws == nil {
		t.Fatal("expected non-nil workspace")
	}
}

func TestGetRelativePath_AbsAndRel(t *testing.T) {
	ws, _ := NewWorkspace(t.TempDir())

	// Absolute path under cwd.
	abs := filepath.Join(ws.GetCWD(), "nested", "file.go")
	rel, err := ws.GetRelativePath(abs)
	if err != nil {
		t.Fatalf("GetRelativePath(abs): %v", err)
	}
	if rel != filepath.Join("nested", "file.go") {
		t.Errorf("expected nested/file.go, got %q", rel)
	}

	// Absolute path outside cwd: should produce ".." prefixed path.
	parent := filepath.Dir(ws.GetCWD())
	sibling := filepath.Join(parent, "sibling.txt")
	rel, err = ws.GetRelativePath(sibling)
	if err != nil {
		t.Fatalf("GetRelativePath(sibling): %v", err)
	}
	if !strings.HasPrefix(rel, "..") {
		t.Errorf("expected ..-prefixed path, got %q", rel)
	}
}

func TestSetCWD_Relative(t *testing.T) {
	ws, _ := NewWorkspace(t.TempDir())
	parent := ws.GetCWD()

	// SetCWD resolves relative paths against the OS cwd, not against ws.GetCWD().
	// We chdir to parent so the relative "sub" path resolves into our tempdir.
	sub := filepath.Join(parent, "sub")
	if err := os.Mkdir(sub, 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	defer func() {
		_ = os.Chdir(origCwd)
	}()
	if err := os.Chdir(parent); err != nil {
		t.Fatalf("Chdir: %v", err)
	}
	if err := ws.SetCWD("sub"); err != nil {
		t.Fatalf("SetCWD: %v", err)
	}
	// On macOS, /var is symlinked to /private/var. Compare via EvalSymlinks
	// to be insensitive to that symlink resolution.
	got, err := filepath.EvalSymlinks(ws.GetCWD())
	if err != nil {
		t.Fatalf("EvalSymlinks got: %v", err)
	}
	want, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatalf("EvalSymlinks want: %v", err)
	}
	if got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}

func TestSetCWD_NotADirectory(t *testing.T) {
	ws, _ := NewWorkspace(t.TempDir())
	if err := ws.SetCWD(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("expected error when SetCWD points to nonexistent dir")
	}
}

// ----- TildeExpansion in GrepTool -------------------------------------------

// TestGrepTool_TildeExpansion covers the ~/ expansion branch of Execute.
// This needs $HOME to exist, so we set HOME to a tempdir.
func TestGrepTool_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "hi.txt"), []byte("tilde hello\n"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	tool, _ := setupGrepTest(t)
	// Use ~ to point to a file outside the cwd.
	_, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "tilde",
		"path":    "~/hi.txt",
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
}

// ----- FindSkillTool metadata -----------------------------------------------

func TestFindSkillTool_Metadata(t *testing.T) {
	tool := NewFindSkillTool(testSkills(), nil)
	if tool.Name() != "find_skill" {
		t.Errorf("Name = %q, want %q", tool.Name(), "find_skill")
	}
	if tool.Description() == "" {
		t.Error("Description should not be empty")
	}
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("Parameters type = %v, want object", params["type"])
	}
}

// ----- GrepTool Parameters --------------------------------------------------

func TestGrepTool_Parameters(t *testing.T) {
	tool, _ := setupGrepTest(t)
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("Parameters type = %v, want object", params["type"])
	}
}

// ----- context_mgmt tools ---------------------------------------------------

// These are in a separate package, so we use a small _test.go in that subpackage
// (created separately).
