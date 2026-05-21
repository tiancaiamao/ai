package tools

import (
	"context"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"

	"github.com/tiancaiamao/ai/pkg/skill"
)

// LoadSkillTool loads a skill's content and returns it formatted as XML.
// This tool is used internally when users invoke /skill:xxx commands.
type LoadSkillTool struct {
	skills []skill.Skill
}

// NewLoadSkillTool creates a new LoadSkill tool.
func NewLoadSkillTool(skills []skill.Skill) *LoadSkillTool {
	return &LoadSkillTool{skills: skills}
}

// Name returns the tool name.
func (t *LoadSkillTool) Name() string {
	return "load_skill"
}

// Description returns the tool description.
func (t *LoadSkillTool) Description() string {
	return "Load a skill by name and return its content formatted as XML."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *LoadSkillTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Name of the skill to load",
			},
		},
		"required": []string{"name"},
	}
}

// Execute loads the skill and returns its content.
func (t *LoadSkillTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	name, ok := args["name"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid name argument")
	}

	// Find the skill
	var foundSkill *skill.Skill
	for i := range t.skills {
		if t.skills[i].Name == name {
			foundSkill = &t.skills[i]
			break
		}
	}

	if foundSkill == nil {
		return nil, fmt.Errorf("skill not found: %s", name)
	}

	// Build skill block in XML format (same format as ExpandCommand)
	skillBlock := fmt.Sprintf(`<skill name="%s" location="%s">
References are relative to %s.

%s
</skill>`,
		skill.EscapeXML(foundSkill.Name),
		skill.EscapeXML(foundSkill.FilePath),
		skill.EscapeXML(foundSkill.BaseDir),
		foundSkill.Content,
	)

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: skillBlock,
		},
	}, nil
}