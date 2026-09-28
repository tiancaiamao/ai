package app

import (
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestHandleSetThinkingLevelUsesModelNativeEfforts(t *testing.T) {
	app := &App{
		ag: agent.NewAgent(llm.Model{}, "", ""),
		model: llm.Model{
			Reasoning:              true,
			ReasoningEfforts:       []string{"depth-1", "depth-3"},
			DefaultReasoningEffort: "depth-1",
		},
	}

	got, err := app.handleSetThinkingLevel("")
	if err != nil {
		t.Fatalf("listing efforts: %v", err)
	}
	settings := got.(map[string]any)
	if settings["value"] != "depth-1" || strings.Join(settings["options"].([]string), ",") != "depth-1,depth-3" {
		t.Fatalf("listed settings = %#v", settings)
	}

	if _, err := app.handleSetThinkingLevel("medium"); err == nil {
		t.Fatal("expected unsupported effort to be rejected")
	}
	if _, err := app.handleSetThinkingLevel("depth-3"); err != nil {
		t.Fatalf("setting native effort: %v", err)
	}
	if app.currentThinkingLevel != "depth-3" {
		t.Fatalf("current effort = %q, want depth-3", app.currentThinkingLevel)
	}

}
