package main

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
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

	registerHeadlessTools(registry, ws, compactor, config.DefaultConfig())

	found := false
	for _, tool := range registry.All() {
		if tool.Name() == "context_management" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected context_management to be registered in headless mode")
	}
}

func TestRegisterHeadlessToolsDisablesTrackingToolsWhenRequested(t *testing.T) {
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

	cfg := config.DefaultConfig()
	cfg.TaskTracking = false
	cfg.ContextManagement = false
	registerHeadlessTools(registry, ws, compactor, cfg)

	for _, tool := range registry.All() {
		if tool.Name() == "task_tracking" {
			t.Fatal("did not expect task_tracking when disabled")
		}
		if tool.Name() == "context_management" {
			t.Fatal("did not expect context_management when disabled")
		}
	}
}

func TestHeadlessEffectiveConfigWithCustomPrompt(t *testing.T) {
	base := config.DefaultConfig()
	base.TaskTracking = true
	base.ContextManagement = true

	effective := headlessEffectiveConfig(base, "custom prompt", false)
	if effective.TaskTracking {
		t.Fatal("expected TaskTracking to be disabled for custom system prompt")
	}
	if effective.ContextManagement {
		t.Fatal("expected ContextManagement to be disabled for custom system prompt")
	}
	if !base.TaskTracking || !base.ContextManagement {
		t.Fatal("expected base config to remain unchanged")
	}
}
