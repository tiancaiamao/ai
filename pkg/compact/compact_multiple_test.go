package compact

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// TestCompactionSummaryMessageKind verifies that compaction summary
// messages have the correct Kind field.
func TestCompactionSummaryMessageKind(t *testing.T) {
	summary := "Test summary"
	msg := agentctx.NewCompactionSummaryMessage(summary)

	// Verify the message has the correct Kind
	if msg.Metadata == nil {
		t.Fatal("Expected message to have metadata")
	}

	if msg.Metadata.Kind != "compactionSummary" {
		t.Errorf("Expected Kind='compactionSummary', got '%s'", msg.Metadata.Kind)
	}

	// Verify the role is 'user' so it's visible to the agent
	if msg.Role != "user" {
		t.Errorf("Expected Role='user', got '%s'", msg.Role)
	}

	// Verify the content includes the prefix
	text := msg.ExtractText()
	if text == "" {
		t.Error("Expected non-empty content")
	}

	// Should contain the "[Previous conversation summary]" prefix
	if !containsSubstring(text, "[Previous conversation summary]") {
		t.Errorf("Expected content to contain '[Previous conversation summary]', got: %s", text)
	}
}

// TestCompactionSummaryVsRegularUserMessage verifies that compaction
// summary messages are distinct from regular user messages.
func TestCompactionSummaryVsRegularUserMessage(t *testing.T) {
	regularMsg := agentctx.NewUserMessage("Regular user message")
	summaryMsg := agentctx.NewCompactionSummaryMessage("Summary text")

	// Both should have role "user"
	if regularMsg.Role != "user" {
		t.Errorf("Regular message should have role 'user', got '%s'", regularMsg.Role)
	}

	if summaryMsg.Role != "user" {
		t.Errorf("Summary message should have role 'user', got '%s'", summaryMsg.Role)
	}

	// But they should have different Kinds
	if regularMsg.Metadata == nil {
		t.Fatal("Regular message should have metadata")
	}

	if summaryMsg.Metadata == nil {
		t.Fatal("Summary message should have metadata")
	}

	if regularMsg.Metadata.Kind == summaryMsg.Metadata.Kind {
		t.Errorf("Expected different Kind values: regular=%s, summary=%s",
			regularMsg.Metadata.Kind, summaryMsg.Metadata.Kind)
	}

	if regularMsg.Metadata.Kind != "user" {
		t.Errorf("Regular message should have Kind='user', got '%s'", regularMsg.Metadata.Kind)
	}

	if summaryMsg.Metadata.Kind != "compactionSummary" {
		t.Errorf("Summary message should have Kind='compactionSummary', got '%s'", summaryMsg.Metadata.Kind)
	}
}

func containsSubstring(text, substr string) bool {
	for i := 0; i <= len(text)-len(substr); i++ {
		if text[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
