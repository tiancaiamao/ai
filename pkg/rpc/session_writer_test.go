package app

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
)

func TestSessionWriterAppend(t *testing.T) {
	sess := session.NewSession(t.TempDir())
	writer := newSessionWriter(16)
	defer writer.Close()

	writer.Append(sess, agentctx.NewUserMessage("msg-1"))
	writer.Append(sess, agentctx.NewUserMessage("msg-2"))

	// Drain channel synchronously (Close waits for pending writes).
	writer.Close()

	messages := sess.GetMessages()
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
}
