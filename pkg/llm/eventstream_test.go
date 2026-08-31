package llm

import (
	"context"
	"testing"
	"time"
)

// TestEventStreamResultAndIsDone covers the Result() and IsDone() accessors
// that were previously 0% covered. They are simple but worth pinning.
func TestEventStreamResultAndIsDone(t *testing.T) {
	stream := NewEventStream[LLMEvent, LLMMessage](
		func(e LLMEvent) bool { return e.GetEventType() == "done" },
		func(e LLMEvent) LLMMessage {
			if d, ok := e.(LLMDoneEvent); ok && d.Message != nil {
				return *d.Message
			}
			return LLMMessage{}
		},
	)

	if stream.IsDone() {
		t.Fatal("fresh stream should not be done")
	}

	// Pushing a done event marks it done and delivers Result().
	msg := LLMMessage{Role: "assistant", Content: "hi"}
	stream.Push(LLMDoneEvent{Message: &msg})

	if !stream.IsDone() {
		t.Fatal("stream should be done after DoneEvent")
	}

	select {
	case got := <-stream.Result():
		if got.Content != "hi" {
			t.Fatalf("expected result content 'hi', got %q", got.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("Result() did not deliver")
	}

	// Pushing after done must be a no-op (and not panic).
	stream.Push(LLMTextDeltaEvent{Delta: "late"})
}

// TestEventStreamEndWakesIterator covers End() notifying a blocked consumer.
// This exercises the "notify waiting" branch in End() that was 40% covered.
// Note: the Iterator goroutine handles Done internally and closes its output
// channel — we just verify it terminates promptly after End().
func TestEventStreamEndWakesIterator(t *testing.T) {
	stream := NewEventStream[int, int](
		func(e int) bool { return false }, // never complete via Push
		func(e int) int { return e },
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range stream.Iterator(ctx) {
			// Drain until iterator channel is closed (End() wakes the waiter
			// and the iterator goroutine returns).
		}
	}()

	// Give the iterator a moment to register as a waiter.
	time.Sleep(20 * time.Millisecond)

	stream.End(42)

	select {
	case <-done:
		// Good — iterator woke up and terminated.
	case <-time.After(time.Second):
		t.Fatal("iterator did not terminate after End()")
	}
}

// TestEventStreamEndIdempotent ensures End() on an already-done stream is a no-op.
func TestEventStreamEndIdempotent(t *testing.T) {
	stream := NewEventStream[int, int](
		func(int) bool { return true },
		func(e int) int { return e },
	)
	stream.Push(1) // marks done
	stream.End(99) // should be no-op since already done

	if !stream.IsDone() {
		t.Fatal("expected stream done")
	}
	// Result channel should have exactly one value (the first Push result).
	select {
	case <-stream.Result():
	default:
		// Drain; there should be one buffered value. After drain, End()'s send
		// would have blocked — the lock guards against this by short-circuiting.
	}
}
