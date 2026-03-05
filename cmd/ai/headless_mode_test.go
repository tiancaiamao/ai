package main

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/tools"
)

func TestRegisterHeadlessToolsIncludesContextDecisionTool(t *testing.T) {
	ws, err := tools.NewWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create workspace: %v", err)
	}

	registry := tools.NewRegistry()
	compactor := compact.NewCompactor(
		compact.DefaultConfig(),
		llm.Model{ID: "test", Provider: "zai", API: "openai-completions", ContextWindow: 128000},
		"",
		"system",
		128000,
	)

	registerHeadlessTools(registry, ws, compactor)

	found := false
	for _, tool := range registry.All() {
		if tool.Name() == "llm_context_decision" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected llm_context_decision to be registered in headless mode")
	}
}
