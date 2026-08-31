package rpc

import (
	"bytes"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/transport"
)

func TestACPClientLoadSessionSignalsReplayCompletionAfterUpdates(t *testing.T) {
	input := bytes.NewBufferString(
		`{"jsonrpc":"2.0","method":"session/update","params":{"sessionId":"sess","update":{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"replayed"}}}}` + "\n" +
			`{"jsonrpc":"2.0","id":2,"result":{}}` + "\n",
	)
	client := NewACPClient(transport.NewStdio(input, &bytes.Buffer{}))
	defer client.Close()

	if err := client.LoadSession("sess"); err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	updates := client.Updates()
	select {
	case update := <-updates:
		if update.SessionUpdate != "agent_message_chunk" {
			t.Fatalf("expected replay update first, got %q", update.SessionUpdate)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replay update")
	}
	select {
	case update := <-updates:
		if update.SessionUpdate != ACPUpdateSessionLoadEnd {
			t.Fatalf("expected replay completion marker, got %q", update.SessionUpdate)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for replay completion")
	}
}

func TestACPClientLoadSessionDoesNotSignalReplayCompletionOnError(t *testing.T) {
	input := bytes.NewBufferString(`{"jsonrpc":"2.0","id":2,"error":{"code":-32000,"message":"not found"}}` + "\n")
	client := NewACPClient(transport.NewStdio(input, &bytes.Buffer{}))
	defer client.Close()

	if err := client.LoadSession("sess"); err == nil {
		t.Fatal("LoadSession unexpectedly succeeded")
	}

	select {
	case update := <-client.Updates():
		if update.SessionUpdate == ACPUpdateSessionLoadEnd {
			t.Fatal("session/load error emitted replay completion marker")
		}
	case <-time.After(100 * time.Millisecond):
	}
}
