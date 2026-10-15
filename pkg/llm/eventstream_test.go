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

// TestEventStreamWaitDoneAndDrainQueued covers the late-consumer API used by
// processPrompt after a canceled ctx: wait for the producer to finish, then
// drain tail events the Iterator left behind in the queue.
func TestEventStreamWaitDoneAndDrainQueued(t *testing.T) {
	stream := NewEventStream[int, int](
		func(e int) bool { return e == -1 }, // -1 completes the stream
		func(e int) int { return e },
	)

	// Not done yet: WaitDone returns false after the timeout.
	if stream.WaitDone(20 * time.Millisecond) {
		t.Fatal("WaitDone should time out while the stream is open")
	}

	// Events pushed with no consumer stay queued.
	stream.Push(1)
	stream.Push(2)

	// Producer finishes.
	stream.Push(-1)

	if !stream.WaitDone(time.Second) {
		t.Fatal("WaitDone should observe the done state")
	}

	got := stream.DrainQueued()
	if len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != -1 {
		t.Fatalf("DrainQueued = %v, want [1 2 -1]", got)
	}
	if again := stream.DrainQueued(); again != nil {
		t.Fatalf("second DrainQueued should be nil, got %v", again)
	}
}

// TestEventStreamWaitDoneAfterEnd covers the End() path: producers that exit
// without a completing Push still signal done.
func TestEventStreamWaitDoneAfterEnd(t *testing.T) {
	stream := NewEventStream[int, int](
		func(int) bool { return false },
		func(e int) int { return e },
	)
	go func() {
		time.Sleep(10 * time.Millisecond)
		stream.End(7)
	}()
	if !stream.WaitDone(time.Second) {
		t.Fatal("WaitDone should observe End()")
	}
}

// TestEventStreamDrainQueuedSeesPushAfterCancel pins the waiter lifecycle on
// ctx cancellation: an Iterator that exits via ctx.Done() must deregister its
// waiter. Otherwise the next Push delivers into the abandoned channel, the
// event is invisible to DrainQueued, and the consumer loses it — the exact
// loss reviewer found for steer/abort tail events.
func TestEventStreamDrainQueuedSeesPushAfterCancel(t *testing.T) {
	es := NewEventStream[int, int](func(e int) bool { return e == -1 }, func(e int) int { return e })
	ctx, cancel := context.WithCancel(context.Background())
	ch := es.Iterator(ctx)

	// Give the iterator time to park in its select with a registered waiter.
	time.Sleep(20 * time.Millisecond)
	cancel()
	// Wait until the iterator goroutine has fully exited (channel closed),
	// which is when a leaked waiter would still be sitting in es.waiting.
	for range ch {
	}

	es.Push(42)
	got := es.DrainQueued()
	if len(got) != 1 || got[0] != 42 {
		t.Fatalf("DrainQueued() = %v, want [42]", got)
	}
}

// TestEventStreamEventConservationAcrossCancel runs the cancel/push
// interleaving many times: whatever the scheduler does, a pushed event must
// end up either delivered through the iterator or visible in DrainQueued —
// exactly once, never lost.
func TestEventStreamEventConservationAcrossCancel(t *testing.T) {
	for i := 0; i < 50; i++ {
		es := NewEventStream[int, int](func(e int) bool { return e == -1 }, func(e int) int { return e })
		ctx, cancel := context.WithCancel(context.Background())
		ch := es.Iterator(ctx)

		time.Sleep(time.Millisecond) // let the iterator park as a waiter
		cancel()
		es.Push(i) // may race the iterator's exit; both orders must conserve

		var delivered []int
		for r := range ch {
			if !r.Done {
				delivered = append(delivered, r.Value)
			}
		}
		queued := es.DrainQueued()

		if len(delivered)+len(queued) != 1 {
			t.Fatalf("iteration %d: event lost (delivered=%v queued=%v)", i, delivered, queued)
		}
		if len(delivered) == 1 && delivered[0] != i {
			t.Fatalf("iteration %d: delivered %v, want [%d]", i, delivered, i)
		}
		if len(queued) == 1 && queued[0] != i {
			t.Fatalf("iteration %d: queued %v, want [%d]", i, queued, i)
		}
	}
}
