package agentconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Criterion 1: version=2 报错 "unsupported agent config version" ---

func TestVersionValidation(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.yaml")
	err := os.WriteFile(cfgPath, []byte("version: 2\nsystem_prompt: sp.md\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for version=2, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported agent config version") {
		t.Fatalf("error should mention 'unsupported agent config version', got: %v", err)
	}
}

// --- Criterion 2: system_prompt 指向不存在文件时报错退出 ---

func TestSystemPromptNotFound(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.yaml")
	err := os.WriteFile(cfgPath, []byte("version: 1\nsystem_prompt: nonexistent.md\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = cfg.ResolveSystemPrompt()
	if err == nil {
		t.Fatal("expected error when system_prompt file does not exist, got nil")
	}
}

// --- Criterion 3: memory 指向不存在文件时静默跳过，正常启动 ---

func TestMemoryOptional(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.yaml")
	spPath := filepath.Join(dir, "sp.md")
	err := os.WriteFile(spPath, []byte("you are a helper"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(cfgPath, []byte("version: 1\nsystem_prompt: sp.md\nmemory: nonexistent_memory.md\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := cfg.ResolveSystemPrompt()
	if err != nil {
		t.Fatalf("expected no error when memory file does not exist, got: %v", err)
	}
	if sp != "you are a helper" {
		t.Fatalf("expected system prompt 'you are a helper', got %q", sp)
	}
}

// --- Criterion 4: memory 有内容时追加到 system prompt 末尾 ---

func TestMemoryAppended(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.yaml")
	spPath := filepath.Join(dir, "sp.md")
	memPath := filepath.Join(dir, "mem.md")
	err := os.WriteFile(spPath, []byte("you are a helper"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(memPath, []byte("remember the user likes Go"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(cfgPath, []byte("version: 1\nsystem_prompt: sp.md\nmemory: mem.md\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := cfg.ResolveSystemPrompt()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sp, "you are a helper") {
		t.Fatalf("system prompt should start with the system_prompt content, got: %q", sp)
	}
	if !strings.HasSuffix(sp, "remember the user likes Go") {
		t.Fatalf("system prompt should end with memory content, got: %q", sp)
	}
	if !strings.Contains(sp, "you are a helper\nremember the user likes Go") {
		t.Fatalf("memory should be appended after system prompt with newline, got: %q", sp)
	}
}

// --- Criterion 5: 路径为相对路径时相对于 agent.yaml 所在目录解析 ---

func TestRelativePath(t *testing.T) {
	dir := t.TempDir()
	subdir := filepath.Join(dir, "config")
	err := os.MkdirAll(subdir, 0755)
	if err != nil {
		t.Fatal(err)
	}

	// Put files in a sibling dir (not the YAML dir) to prove relative resolution
	cfgPath := filepath.Join(subdir, "agent.yaml")
	spPath := filepath.Join(subdir, "prompt.md") // relative: prompt.md → subdir/prompt.md
	memPath := filepath.Join(subdir, "notes.md") // relative: notes.md → subdir/notes.md

	err = os.WriteFile(spPath, []byte("hello from subdir"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(memPath, []byte("memory from subdir"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(cfgPath, []byte("version: 1\nsystem_prompt: prompt.md\nmemory: notes.md\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := cfg.ResolveSystemPrompt()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sp, "hello from subdir") {
		t.Fatalf("expected resolved system prompt to contain 'hello from subdir', got: %q", sp)
	}
	if !strings.Contains(sp, "memory from subdir") {
		t.Fatalf("expected resolved system prompt to contain 'memory from subdir', got: %q", sp)
	}
}

// --- Additional: version=0 (missing) also rejected ---

func TestVersionMissing(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.yaml")
	err := os.WriteFile(cfgPath, []byte("system_prompt: sp.md\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(cfgPath)
	if err == nil {
		t.Fatal("expected error for missing version (defaults to 0), got nil")
	}
	if !strings.Contains(err.Error(), "unsupported agent config version") {
		t.Fatalf("error should mention 'unsupported agent config version', got: %v", err)
	}
}

// --- Additional: version=1 is accepted ---

func TestVersion1Accepted(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.yaml")
	err := os.WriteFile(cfgPath, []byte("version: 1\nsystem_prompt: sp.md\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Load(cfgPath)
	if err != nil {
		t.Fatalf("version=1 should be accepted, got: %v", err)
	}
}

func TestGetEnabledTools(t *testing.T) {
	// nil map -> nil result
	c := &AgentConfig{}
	if got := c.GetEnabledTools(); got != nil {
		t.Errorf("GetEnabledTools with nil Tools = %v, want nil", got)
	}

	// Mixed enabled/disabled
	c.Tools = []ToolEntry{
		{Name: "bash", Enabled: true},
		{Name: "edit", Enabled: false},
		{Name: "grep", Enabled: true},
		{Name: "read", Enabled: false},
		{Name: "write", Enabled: true},
		{Name: "global", Enabled: true},
	}
	got := c.GetEnabledTools()
	if len(got) != 4 {
		t.Fatalf("expected 4 enabled tools, got %d: %v", len(got), got)
	}

	// All disabled -> empty (not nil)
	c2 := &AgentConfig{Tools: []ToolEntry{{Name: "x", Enabled: false}}}
	if got := c2.GetEnabledTools(); len(got) != 0 {
		t.Errorf("expected empty slice for all-disabled, got %v", got)
	}
}

func TestResolveSystemPrompt_MemoryFallback(t *testing.T) {
	// Cover the "memory file missing -> skip" branch in ResolveSystemPrompt.
	dir := t.TempDir()
	sp := "system prompt body"
	spPath := filepath.Join(dir, "sp.txt")
	if err := os.WriteFile(spPath, []byte(sp), 0644); err != nil {
		t.Fatal(err)
	}

	c := &AgentConfig{
		SystemPrompt: spPath,
		Memory:       filepath.Join(dir, "missing-memory.md"), // does not exist
	}
	got, err := c.ResolveSystemPrompt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != sp {
		t.Errorf("expected just system prompt when memory missing, got %q", got)
	}

	// With memory present -> concatenated
	memPath := filepath.Join(dir, "mem.txt")
	if err := os.WriteFile(memPath, []byte("memory content"), 0644); err != nil {
		t.Fatal(err)
	}
	c.Memory = memPath
	got, err = c.ResolveSystemPrompt()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "system prompt body") || !strings.Contains(got, "memory content") {
		t.Errorf("expected both prompts concatenated, got %q", got)
	}
}

func TestLoadErrors(t *testing.T) {
	// Nonexistent file
	_, err := Load("/no/such/file.yaml")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}

	// Invalid YAML
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(bad, []byte("::: not valid yaml :::\n  - foo: [unclosed"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = Load(bad)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}

	// Wrong version
	dir2 := t.TempDir()
	v0 := filepath.Join(dir2, "v0.yaml")
	if err := os.WriteFile(v0, []byte("version: 0\nsystem_prompt: none\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = Load(v0)
	if err == nil {
		t.Error("expected error for version != 1, got nil")
	}
}
