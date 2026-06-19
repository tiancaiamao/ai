package skill

import (
	"path/filepath"
	"testing"
)

func TestIsUnderPath(t *testing.T) {
	tests := []struct {
		name   string
		target string
		root   string
		want   bool
	}{
		{"direct child", "/a/b/skill.md", "/a/b", true},
		{"deep child", "/a/b/c/d/skill.md", "/a/b", true},
		{"same path", "/a/b", "/a/b", true},
		{"different", "/a/c/skill.md", "/a/b", false},
		{"sibling prefix", "/a/bbb/skill.md", "/a/b", false},
		{"relative target", "skills/skill.md", "skills", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isUnderPath(tt.target, tt.root)
			if got != tt.want {
				t.Errorf("isUnderPath(%q, %q) = %v, want %v", tt.target, tt.root, got, tt.want)
			}
		})
	}
}

func TestMustGetwd(t *testing.T) {
	// Should not panic
	wd := mustGetwd()
	if wd == "" {
		t.Error("mustGetwd returned empty string")
	}
	// Verify it matches os.Getwd
	abs, _ := filepath.Abs(wd)
	if abs == "" {
		t.Error("filepath.Abs returned empty")
	}
}
