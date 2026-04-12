package channel

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/genius/ag/internal/storage"
)

func setupTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Init storage
	agentsDir, channelsDir, tasksDir := storage.Paths()
	os.MkdirAll(agentsDir, 0755)
	os.MkdirAll(channelsDir, 0755)
	os.MkdirAll(tasksDir, 0755)

	return dir
}

func TestCreateAndList(t *testing.T) {
	setupTest(t)

	if err := Create("test-ch"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Create("test-ch"); err == nil {
		t.Fatal("expected error on duplicate create")
	}

	channels, err := List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(channels) != 1 || channels[0].Name != "test-ch" {
		t.Fatalf("unexpected channels: %+v", channels)
	}
}

func TestSendRecv(t *testing.T) {
	setupTest(t)
	Create("q")

	// Send two messages
	if err := Send("q", []byte("msg1"), false); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if err := Send("q", []byte("msg2"), false); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Recv first
	data, err := Recv("q", false, 0, false)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(data) != "msg1" {
		t.Fatalf("expected msg1, got %s", data)
	}

	// Recv second
	data, err = Recv("q", false, 0, false)
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if string(data) != "msg2" {
		t.Fatalf("expected msg2, got %s", data)
	}

	// Empty queue
	_, err = Recv("q", false, 0, false)
	if err == nil {
		t.Fatal("expected error on empty queue")
	}
}

func TestRecvAll(t *testing.T) {
	setupTest(t)
	Create("q")

	Send("q", []byte("a"), false)
	Send("q", []byte("b"), false)
	Send("q", []byte("c"), false)

	data, err := Recv("q", false, 0, true)
	if err != nil {
		t.Fatalf("Recv all: %v", err)
	}
	expected := "a\nb\nc\n"
	if string(data) != expected {
		t.Fatalf("expected %q, got %q", expected, string(data))
	}

	// Queue should be empty now
	msgs, _ := filepath.Glob(filepath.Join(storage.ChannelDir("q"), "*.msg"))
	if len(msgs) != 0 {
		t.Fatalf("queue not empty: %v", msgs)
	}
}

func TestSendToAgent(t *testing.T) {
	dir := setupTest(t)

	// Create a fake agent directory
	agentDir := filepath.Join(dir, ".ag", "agents", "w1")
	os.MkdirAll(filepath.Join(agentDir, "inbox"), 0755)
	os.WriteFile(filepath.Join(agentDir, "status"), []byte("running"), 0644)

	if err := Send("w1", []byte("hello worker"), false); err != nil {
		t.Fatalf("Send to agent: %v", err)
	}

	// Verify message was written to inbox
	msgs, _ := filepath.Glob(filepath.Join(agentDir, "inbox", "*.msg"))
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
}

func TestRemove(t *testing.T) {
	setupTest(t)
	Create("del-me")

	if err := Remove("del-me"); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	channels, _ := List()
	if len(channels) != 0 {
		t.Fatalf("expected no channels, got %d", len(channels))
	}
}