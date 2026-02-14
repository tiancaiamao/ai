package agent

import (
	"context"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestBuildLLMRequestSnapshotStableForSameInput(t *testing.T) {
	ctx := context.WithValue(context.Background(), llmAttemptKey, 2)
	model := llm.Model{ID: "test-model"}
	llmCtx := llm.LLMContext{
		SystemPrompt: "system prompt",
		Messages: []llm.LLMMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "working..."},
			{Role: "tool", Content: "tool ok", ToolCallID: "call_1"},
		},
		Tools: []llm.LLMTool{
			{
				Type: "function",
				Function: llm.ToolFunction{
					Name:        "read",
					Description: "read file",
					Parameters: map[string]any{
						"type": "object",
					},
				},
			},
		},
	}

	s1 := buildLLMRequestSnapshot(ctx, model, llmCtx)
	s2 := buildLLMRequestSnapshot(ctx, model, llmCtx)

	if s1.RequestHash == "" {
		t.Fatal("expected non-empty request hash")
	}
	if s1.RequestHash != s2.RequestHash {
		t.Fatalf("expected stable request hash, got %q vs %q", s1.RequestHash, s2.RequestHash)
	}
	if s1.MessagesHash != s2.MessagesHash {
		t.Fatalf("expected stable messages hash, got %q vs %q", s1.MessagesHash, s2.MessagesHash)
	}
	if s1.ToolsHash != s2.ToolsHash {
		t.Fatalf("expected stable tools hash, got %q vs %q", s1.ToolsHash, s2.ToolsHash)
	}
	if s1.Attempt != 2 {
		t.Fatalf("expected attempt=2, got %d", s1.Attempt)
	}
	if s1.MessageCount != 3 || s1.UserMessages != 1 || s1.AssistantMessages != 1 || s1.ToolMessages != 1 {
		t.Fatalf("unexpected role counts: %+v", s1)
	}
	if s1.LastRole != "tool" {
		t.Fatalf("expected last role tool, got %q", s1.LastRole)
	}
	if s1.LastUserHash == "" {
		t.Fatal("expected non-empty last user hash")
	}
}

func TestBuildLLMRequestSnapshotChangesWhenInputChanges(t *testing.T) {
	ctx := context.Background()
	model := llm.Model{ID: "test-model"}
	base := llm.LLMContext{
		SystemPrompt: "system prompt",
		Messages: []llm.LLMMessage{
			{Role: "user", Content: "hello"},
		},
	}

	changed := llm.LLMContext{
		SystemPrompt: "system prompt v2",
		Messages: []llm.LLMMessage{
			{Role: "user", Content: "hello"},
		},
	}

	sBase := buildLLMRequestSnapshot(ctx, model, base)
	sChanged := buildLLMRequestSnapshot(ctx, model, changed)

	if sBase.RequestHash == sChanged.RequestHash {
		t.Fatalf("expected request hash to change when system prompt changes, got %q", sBase.RequestHash)
	}
	if sBase.SystemPromptHash == sChanged.SystemPromptHash {
		t.Fatalf("expected system prompt hash to change, got %q", sBase.SystemPromptHash)
	}
}
