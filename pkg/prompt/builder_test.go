package prompt

import (
	"os"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestThinkingInstruction(t *testing.T) {
	tests := []struct {
		level    string
		contains string
	}{
		{"off", "Thinking level is off"},
		{"minimal", "Thinking level is minimal"},
		{"low", "Thinking level is low"},
		{"medium", "Thinking level is medium"},
		{"high", "Thinking level is high"},
		{"xhigh", "Thinking level is xhigh"},
		{"", "Thinking level is high"},        // default
		{"invalid", "Thinking level is high"}, // default for invalid
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			result := ThinkingInstruction(tt.level)

			if !strings.Contains(result, tt.contains) {
				t.Errorf("ThinkingInstruction(%q) = %q, want to contain %q", tt.level, result, tt.contains)
			}
		})
	}
}

func TestNormalizeThinkingLevel(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{"off", "off"},
		{"OFF", "off"},
		{"minimal", "minimal"},
		{"low", "low"},
		{"medium", "medium"},
		{"high", "high"},
		{"xhigh", "xhigh"},
		{"", "high"},        // default
		{"invalid", "high"}, // default for invalid
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := NormalizeThinkingLevel(tt.input)

			if result != tt.output {
				t.Errorf("NormalizeThinkingLevel(%q) = %q, want %q", tt.input, result, tt.output)
			}
		})
	}
}

func TestBuildSystemPrompt(t *testing.T) {
	result := BuildSystemPrompt(agentctx.ModeNormal)
	if result == "" {
		t.Error("BuildSystemPrompt for normal mode returned empty string")
	}

	result = BuildSystemPrompt(agentctx.ModeContextMgmt)
	if result == "" {
		t.Error("BuildSystemPrompt for context mgmt mode returned empty string")
	}
}

func TestBuildSystemPromptWithThinking(t *testing.T) {
	tests := []struct {
		name        string
		mode        agentctx.AgentMode
		level       string
		wantContain string
		dontContain string
	}{
		{
			name:        "normal off level omits thinking instruction",
			mode:        agentctx.ModeNormal,
			level:       "off",
			dontContain: "thinking_instruction",
		},
		{
			name:        "normal high level includes thinking instruction",
			mode:        agentctx.ModeNormal,
			level:       "high",
			wantContain: "Thinking level is high",
		},
		{
			name:        "normal empty level defaults to high",
			mode:        agentctx.ModeNormal,
			level:       "",
			wantContain: "Thinking level is high",
		},
		{
			name:        "normal medium level",
			mode:        agentctx.ModeNormal,
			level:       "medium",
			wantContain: "Thinking level is medium",
		},
		{
			name:        "context_mgmt mode never gets thinking instruction",
			mode:        agentctx.ModeContextMgmt,
			level:       "high",
			dontContain: "thinking_instruction",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildSystemPromptWithThinking(tt.mode, tt.level)

			if tt.wantContain != "" && !strings.Contains(result, tt.wantContain) {
				t.Errorf("expected result to contain %q", tt.wantContain)
			}
			if tt.dontContain != "" && strings.Contains(result, tt.dontContain) {
				t.Errorf("expected result NOT to contain %q", tt.dontContain)
			}
		})
	}
}

func TestBuildSystemPromptWithExtras(t *testing.T) {
	tests := []struct {
		name        string
		mode        agentctx.AgentMode
		level       string
		skills      string
		context     string
		wantContain string
		dontContain string
	}{
		{
			name:        "normal mode with skills and project context",
			mode:        agentctx.ModeNormal,
			level:       "high",
			skills:      "## Skills\n- **bash**: Run commands",
			context:     "## Project Context\n### AGENTS.md\n\nBe concise.",
			wantContain: "## Skills",
		},
		{
			name:        "normal mode with project context",
			mode:        agentctx.ModeNormal,
			level:       "high",
			skills:      "",
			context:     "## Project Context\n### AGENTS.md\n\nBe concise.",
			wantContain: "## Project Context",
		},
		{
			name:        "normal mode without skills cleans up empty section",
			mode:        agentctx.ModeNormal,
			level:       "high",
			skills:      "",
			context:     "",
			dontContain: "%SKILLS%",
		},
		{
			name:        "normal mode without project context cleans up",
			mode:        agentctx.ModeNormal,
			level:       "high",
			skills:      "",
			context:     "",
			dontContain: "%PROJECT_CONTEXT%",
		},
		{
			name:        "normal mode with thinking and skills",
			mode:        agentctx.ModeNormal,
			level:       "medium",
			skills:      "## Skills\n- **tmux**: Background tasks",
			context:     "",
			wantContain: "thinking_instruction",
		},
		{
			name:        "normal mode off level no thinking instruction",
			mode:        agentctx.ModeNormal,
			level:       "off",
			skills:      "",
			context:     "",
			dontContain: "thinking_instruction",
		},
		{
			name:        "context_mgmt mode ignores extras",
			mode:        agentctx.ModeContextMgmt,
			level:       "high",
			skills:      "## Skills\n- **bash**: Run commands",
			context:     "## Project Context\n### AGENTS.md\n\nBe concise.",
			dontContain: "## Skills",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildSystemPromptWithExtras(tt.mode, tt.level, tt.skills, tt.context)

			if result == "" {
				t.Error("result is empty")
			}
			if tt.wantContain != "" && !strings.Contains(result, tt.wantContain) {
				t.Errorf("expected result to contain %q", tt.wantContain)
			}
			if tt.dontContain != "" && strings.Contains(result, tt.dontContain) {
				t.Errorf("expected result NOT to contain %q", tt.dontContain)
			}
			// Verify no placeholders remain
			if strings.Contains(result, "%SKILLS%") {
				t.Error("result still contains %SKILLS% placeholder")
			}
			if strings.Contains(result, "%PROJECT_CONTEXT%") {
				t.Error("result still contains %PROJECT_CONTEXT% placeholder")
			}
		})
	}
}

func TestBuildProjectContext(t *testing.T) {
	t.Run("empty directory returns empty string", func(t *testing.T) {
		result := BuildProjectContext(t.TempDir())
		if result != "" {
			t.Errorf("expected empty string, got %q", result)
		}
	})

	t.Run("reads AGENTS.md from cwd", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(dir+"/AGENTS.md", []byte("Be concise."), 0644)
		result := BuildProjectContext(dir)
		if !strings.Contains(result, "Be concise.") {
			t.Errorf("expected to contain 'Be concise.', got %q", result)
		}
		if !strings.Contains(result, "## Project Context") {
			t.Errorf("expected to contain '## Project Context', got %q", result)
		}
	})

	t.Run("reads AGENTS.md from .ai directory", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(dir+"/.ai", 0755)
		os.WriteFile(dir+"/.ai/AGENTS.md", []byte("AI agent rules."), 0644)
		result := BuildProjectContext(dir)
		if !strings.Contains(result, "AI agent rules.") {
			t.Errorf("expected to contain 'AI agent rules.', got %q", result)
		}
	})

	t.Run("prefers .ai/AGENTS.md over root AGENTS.md", func(t *testing.T) {
		dir := t.TempDir()
		os.MkdirAll(dir+"/.ai", 0755)
		os.WriteFile(dir+"/AGENTS.md", []byte("root version"), 0644)
		os.WriteFile(dir+"/.ai/AGENTS.md", []byte("ai version"), 0644)
		result := BuildProjectContext(dir)
		if !strings.Contains(result, "ai version") {
			t.Errorf("expected to prefer .ai/AGENTS.md, got %q", result)
		}
	})

	t.Run("skips CLAUDE.md when AGENTS.md exists", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(dir+"/AGENTS.md", []byte("agents"), 0644)
		os.WriteFile(dir+"/CLAUDE.md", []byte("claude"), 0644)
		result := BuildProjectContext(dir)
		if !strings.Contains(result, "agents") {
			t.Errorf("expected to contain 'agents', got %q", result)
		}
		if strings.Contains(result, "claude") {
			t.Errorf("expected NOT to contain 'claude' when AGENTS.md exists, got %q", result)
		}
	})

	t.Run("uses CLAUDE.md when AGENTS.md does not exist", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(dir+"/CLAUDE.md", []byte("claude rules"), 0644)
		result := BuildProjectContext(dir)
		if !strings.Contains(result, "claude rules") {
			t.Errorf("expected to contain 'claude rules', got %q", result)
		}
	})
}
