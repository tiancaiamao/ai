package agent

import (
	"testing"
)

func TestFollowUpQueue(t *testing.T) {
	agent := &AgentNew{
		followUpQueue: make(chan string, 100),
	}

	// Initially empty
	if count := agent.PendingFollowUpCount(); count != 0 {
		t.Fatalf("expected 0 pending, got %d", count)
	}

	// Drain empty queue returns nil
	msgs := agent.DrainFollowUps()
	if len(msgs) != 0 {
		t.Fatalf("expected empty drain, got %v", msgs)
	}

	// Queue a message
	if err := agent.QueueFollowUp("hello"); err != nil {
		t.Fatalf("QueueFollowUp failed: %v", err)
	}
	if count := agent.PendingFollowUpCount(); count != 1 {
		t.Fatalf("expected 1 pending, got %d", count)
	}

	// Queue another message
	if err := agent.QueueFollowUp("world"); err != nil {
		t.Fatalf("QueueFollowUp failed: %v", err)
	}
	if count := agent.PendingFollowUpCount(); count != 2 {
		t.Fatalf("expected 2 pending, got %d", count)
	}

	// Drain returns all in FIFO order
	msgs = agent.DrainFollowUps()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0] != "hello" {
		t.Fatalf("expected first message 'hello', got %q", msgs[0])
	}
	if msgs[1] != "world" {
		t.Fatalf("expected second message 'world', got %q", msgs[1])
	}

	// Queue is now empty
	if count := agent.PendingFollowUpCount(); count != 0 {
		t.Fatalf("expected 0 pending after drain, got %d", count)
	}
}

func TestFollowUpQueueFull(t *testing.T) {
	agent := &AgentNew{
		followUpQueue: make(chan string, 2),
	}

	// Fill the queue
	if err := agent.QueueFollowUp("msg1"); err != nil {
		t.Fatalf("QueueFollowUp msg1 failed: %v", err)
	}
	if err := agent.QueueFollowUp("msg2"); err != nil {
		t.Fatalf("QueueFollowUp msg2 failed: %v", err)
	}

	// Third message should fail
	if err := agent.QueueFollowUp("msg3"); err == nil {
		t.Fatal("expected queue full error, got nil")
	}

	// Drain and verify
	msgs := agent.DrainFollowUps()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}

func TestDrainFollowUpsPartialDrain(t *testing.T) {
	agent := &AgentNew{
		followUpQueue: make(chan string, 100),
	}

	// Queue messages
	if err := agent.QueueFollowUp("a"); err != nil {
		t.Fatalf("QueueFollowUp 'a' failed: %v", err)
	}
	if err := agent.QueueFollowUp("b"); err != nil {
		t.Fatalf("QueueFollowUp 'b' failed: %v", err)
	}
	if err := agent.QueueFollowUp("c"); err != nil {
		t.Fatalf("QueueFollowUp 'c' failed: %v", err)
	}

	// First drain gets all of them
	msgs := agent.DrainFollowUps()
	if len(msgs) != 3 {
		t.Fatalf("expected 3, got %d", len(msgs))
	}

	// Queue more
	if err := agent.QueueFollowUp("d"); err != nil {
		t.Fatalf("QueueFollowUp 'd' failed: %v", err)
	}

	// Second drain only gets new one
	msgs = agent.DrainFollowUps()
	if len(msgs) != 1 || msgs[0] != "d" {
		t.Fatalf("expected ['d'], got %v", msgs)
	}

	// Third drain is empty
	msgs = agent.DrainFollowUps()
	if len(msgs) != 0 {
		t.Fatalf("expected empty, got %v", msgs)
	}
}
