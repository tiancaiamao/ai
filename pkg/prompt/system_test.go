package prompt

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/truncate"
)

type abTool struct {
	name string
	desc string
}

func (t abTool) Name() string        { return t.name }
func (t abTool) Description() string { return t.desc }

func TestBasePromptsAreDefined(t *testing.T) {
	if strings.TrimSpace(CompactorBasePrompt()) == "" {
		t.Fatal("CompactorBasePrompt should not be empty")
	}
	if strings.TrimSpace(RPCBasePrompt()) == "" {
		t.Fatal("RPCBasePrompt should not be empty")
	}
	if strings.TrimSpace(HeadlessBasePrompt(false)) == "" {
		t.Fatal("HeadlessBasePrompt(false) should not be empty")
	}
	if strings.TrimSpace(HeadlessBasePrompt(true)) == "" {
		t.Fatal("HeadlessBasePrompt(true) should not be empty")
	}
	if strings.TrimSpace(JSONModeBasePrompt()) == "" {
		t.Fatal("JSONModeBasePrompt should not be empty")
	}
}

func TestRPCBasePromptNoForcedJSON(t *testing.T) {
	p := RPCBasePrompt()
	if strings.Contains(strings.ToLower(p), "return an empty json with error field") {
		t.Fatalf("RPC base prompt still forces legacy JSON fallback: %q", p)
	}
	if !strings.Contains(p, "If the user explicitly requires a JSON schema") {
		t.Fatalf("RPC base prompt should keep conditional JSON rule: %q", p)
	}
}

func TestPromptABMetricsSmoke(t *testing.T) {
	legacyRPCBasePrompt := strings.TrimSpace(`You are a helpful AI coding assistant.
- If you cannot answer the request, return an empty JSON with error field.
- Do not hallucinate or add unnecessary commentary.
- Respect facts and be critical in your thinking. Don't simply agree with everything the user says.`)

	tools := []ToolInfo{
		abTool{name: "read", desc: "Read files"},
		abTool{name: "write", desc: "Write files"},
		abTool{name: "bash", desc: "Execute commands"},
		abTool{name: "grep", desc: "Search file contents"},
		abTool{name: "edit", desc: "Edit file content"},
		abTool{name: "change_workspace", desc: "Switch workspace directory"},
	}

	skills := make([]skill.Skill, 0, 24)
	for i := 0; i < 24; i++ {
		skills = append(skills, skill.Skill{
			Name:        fmt.Sprintf("skill-%02d", i),
			Description: "Long description for AB prompt benchmark with enough content to reflect realistic skill metadata and token cost.",
			FilePath:    fmt.Sprintf("/tmp/skills/skill-%02d/SKILL.md", i),
		})
	}

	oldPrompt := NewBuilder(legacyRPCBasePrompt, "/workspace").SetTools(tools).SetSkills(skills).Build()
	newPrompt := NewBuilder(RPCBasePrompt(), "/workspace").SetTools(tools).SetSkills(skills).Build()

	oldChars := len(oldPrompt)
	newChars := len(newPrompt)
	oldTokens := truncate.ApproxTokenCount(oldPrompt)
	newTokens := truncate.ApproxTokenCount(newPrompt)

	t.Logf("AB prompt metrics: old chars=%d tokens=%d | new chars=%d tokens=%d", oldChars, oldTokens, newChars, newTokens)

	if oldTokens <= 0 || newTokens <= 0 {
		t.Fatalf("expected positive token estimates, old=%d new=%d", oldTokens, newTokens)
	}
	if !strings.Contains(newPrompt, "- **skill-00**:") {
		t.Fatalf("expected full skill entries in prompt, got: %s", newPrompt)
	}
}
