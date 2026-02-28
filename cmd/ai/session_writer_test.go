package main

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"testing"

	"github.com/tiancaiamao/ai/pkg/session"
)

func TestSessionWriterReplaceOverridesPreviousAppends(t *testing.T) {
	sess := session.NewSession(t.TempDir(), nil)
	writer := newSessionWriter(16)
	defer writer.Close()

	writer.Append(sess, agentctx.NewUserMessage("before-1"))
	writer.Append(sess, agentctx.NewUserMessage("before-2"))

	replaced := []agentctx.AgentMessage{
		agentctx.NewUserMessage("after"),
	}
	if err := writer.Replace(sess, replaced); err != nil {
		t.Fatalf("replace failed: %v", err)
	}

	messages := sess.GetMessages()
	if len(messages) != 1 {
		t.Fatalf("expected 1 message after replace, got %d", len(messages))
	}
	if got := messages[0].ExtractText(); got != "after" {
		t.Fatalf("expected replaced message content, got %q", got)
	}
}
