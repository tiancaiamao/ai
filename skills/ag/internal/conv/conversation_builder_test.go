package conv

import (
	"strings"
	"testing"
)

func TestConversationBuilderStreamingDelta(t *testing.T) {
	events := []string{
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"Hello "}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"World"}}`,
		`{"type":"turn_end"}`,
	}
	builder := NewConversationBuilder()
	for _, e := range events {
		builder.ProcessEvent(e)
	}
	texts := builder.AssistantTexts()
	if len(texts) != 1 {
		t.Fatalf("expected 1 message, got %d", len(texts))
	}
	if texts[0] != "Hello World" {
		t.Fatalf("expected 'Hello World', got '%s'", texts[0])
	}
}

func TestConversationBuilderMultiTurn(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"ping"}]}}`,
		`{"type":"message_end"}`,
		`{"type":"turn_start"}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"pong"}}`,
		`{"type":"turn_end"}`,
	}
	builder := NewConversationBuilder()
	for _, e := range events {
		builder.ProcessEvent(e)
	}
	msgs := builder.Messages()
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" {
		t.Errorf("first message role = %q, want user", msgs[0].Role)
	}
	if msgs[0].Content != "ping" {
		t.Errorf("first message content = %q, want ping", msgs[0].Content)
	}
	foundAssistant := false
	for _, m := range msgs {
		if m.Role == "assistant" && m.Content == "pong" {
			foundAssistant = true
		}
	}
	if !foundAssistant {
		t.Errorf("no assistant pong message found in %+v", msgs)
	}
}

func TestConversationBuilderAgentEnd(t *testing.T) {
	events := []string{
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"thinking..."}}`,
		`{"type":"agent_end","messages":[{"role":"assistant","content":[{"type":"text","text":"Final answer"}]}]}`,
	}
	builder := NewConversationBuilder()
	for _, e := range events {
		builder.ProcessEvent(e)
	}
	texts := builder.AssistantTexts()
	// agent_end should flush the streaming text and add its own messages
	if len(texts) != 2 {
		t.Fatalf("expected 2 messages, got %d: %v", len(texts), texts)
	}
	if texts[0] != "thinking..." {
		t.Errorf("texts[0] = %q, want thinking...", texts[0])
	}
	if texts[1] != "Final answer" {
		t.Errorf("texts[1] = %q, want Final answer", texts[1])
	}
}

func TestBuildAssistantTexts(t *testing.T) {
	data := strings.Join([]string{
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hello"}}`,
		`{"type":"turn_end"}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"world"}}`,
		`{"type":"turn_end"}`,
	}, "\n")
	texts := BuildAssistantTexts([]byte(data))
	if len(texts) != 2 {
		t.Fatalf("expected 2, got %d", len(texts))
	}
}

func TestBuildConversation(t *testing.T) {
	data := `{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
{"type":"message_end"}`
	msgs := BuildConversation([]byte(data))
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hi" {
		t.Errorf("unexpected: %+v", msgs[0])
	}
}

func TestConversationBuilderEmpty(t *testing.T) {
	builder := NewConversationBuilder()
	if len(builder.Messages()) != 0 {
		t.Error("expected empty messages")
	}
	if len(builder.AssistantTexts()) != 0 {
		t.Error("expected empty texts")
	}
	builder.ProcessEvent("")
	builder.ProcessEvent("not json")
	builder.ProcessEvent(`{"type":"unknown"}`)
	if len(builder.Messages()) != 0 {
		t.Error("expected still empty after unknown events")
	}
}