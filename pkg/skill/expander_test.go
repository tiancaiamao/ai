package skill

import (
	"path/filepath"
	"testing"
)

func TestGetSource(t *testing.T) {
	l := NewLoader(t.TempDir())
	base := t.TempDir()
	userSkills := filepath.Join(base, "agent", "skills")
	projectSkills := filepath.Join(base, "project", ".ai", "skills")

	opts := &LoadOptions{
		CWD:             filepath.Join(base, "project"),
		AgentDir:        filepath.Join(base, "agent"),
		IncludeDefaults: false,
	}

	cases := []struct {
		path string
		want string
	}{
		{filepath.Join(userSkills, "my-skill", "SKILL.md"), "user"},
		{filepath.Join(projectSkills, "other", "SKILL.md"), "project"},
		{filepath.Join(base, "elsewhere", "SKILL.md"), "path"},
	}
	for _, c := range cases {
		if got := l.getSource(c.path, opts); got != c.want {
			t.Errorf("getSource(%q) = %q; want %q", c.path, got, c.want)
		}
	}

	// With defaults included every path is treated as an explicit path.
	opts.IncludeDefaults = true
	if got := l.getSource(filepath.Join(userSkills, "x.md"), opts); got != "path" {
		t.Errorf("IncludeDefaults should bypass source detection, got %q", got)
	}
}
