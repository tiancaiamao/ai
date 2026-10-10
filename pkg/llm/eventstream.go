package llm

import (
	"context"
	"sync"
	"time"
)

// IterResult represents a single iteration result.
type IterResult[T any] struct {
	Value T
	Done  bool
}

// EventStream is a generic async event stream.
// T is the event type, R is the final result type.
type EventStream[T any, R any] struct {
	mu            sync.Mutex
	queue         []T
	waiting       []chan<- IterResult[T]
	done          bool
	finalResult   R
	finalResultCh chan R
	isComplete    func(T) bool
	extractResult func(T) R
}

// NewEventStream creates a new EventStream.
func NewEventStream[T any, R any](
	isComplete func(T) bool,
	extractResult func(T) R,
) *EventStream[T, R] {
	return &EventStream[T, R]{
		queue:         make([]T, 0),
		waiting:       make([]chan<- IterResult[T], 0),
		finalResultCh: make(chan R, 1),
		isComplete:    isComplete,
		extractResult: extractResult,
	}
}

// Push pushes an event to the stream.
// If the event is complete, it marks the stream as done and stores the final result.
func (es *EventStream[T, R]) Push(event T) {
	es.mu.Lock()
	defer es.mu.Unlock()

	if es.done {
		return
	}

	// Check if this event completes the stream
	if es.isComplete(event) {
		es.done = true
		es.finalResult = es.extractResult(event)
		es.finalResultCh <- es.finalResult
	}

	// Deliver to waiting consumer or add to queue
	if len(es.waiting) > 0 {
		waiter := es.waiting[0]
		es.waiting = es.waiting[1:]
		waiter <- IterResult[T]{Value: event, Done: false}
	} else {
		es.queue = append(es.queue, event)
	}
}

// End marks the stream as complete with the given result.
func (es *EventStream[T, R]) End(result R) {
	es.mu.Lock()
	defer es.mu.Unlock()

	if es.done {
		return
	}

	es.done = true
	es.finalResult = result
	es.finalResultCh <- result

	// Notify all waiting goroutines that stream is done
	for _, waiter := range es.waiting {
		select {
		case waiter <- IterResult[T]{Done: true}:
		default:
			// Channel full or closed, skip
		}
	}
	es.waiting = nil
}

// Iterator returns a channel that iterates over events.
// The channel will be closed when the stream is complete or context is cancelled.
func (es *EventStream[T, R]) Iterator(ctx context.Context) <-chan IterResult[T] {
	ch := make(chan IterResult[T])

	go func() {
		defer close(ch)
		defer func() {
			if r := recover(); r != nil {
				// Forward panic as a done signal so the consumer doesn't block.
				// This should be extremely rare — the iterator goroutine only
				// performs mutex operations and channel sends.
				select {
				case ch <- IterResult[T]{Done: true}:
				default:
				}
			}
		}()
		for {
			es.mu.Lock()

			// Check if there are queued events
			if len(es.queue) > 0 {
				event := es.queue[0]
				es.queue = es.queue[1:]
				es.mu.Unlock()
				ch <- IterResult[T]{Value: event, Done: false}
				continue
			}

			// Check if stream is done
			if es.done {
				es.mu.Unlock()
				return
			}

			// Wait for new events
			waiter := make(chan IterResult[T], 1)
			es.waiting = append(es.waiting, waiter)
			es.mu.Unlock()

			select {
			case result := <-waiter:
				if result.Done {
					return
				}
				if ctx.Err() != nil {
					// Canceled while parked: park the delivered event for a
					// late DrainQueued instead of handing it to a consumer
					// that is about to exit, then exit so the drain processes
					// it exactly once.
					es.parkResult(result)
					return
				}
				ch <- result
			case <-ctx.Done():
				es.abandonWaiter(waiter)
				return
			}
		}
	}()

	return ch
}

// abandonWaiter deregisters the iterator's waiter when the iterator exits via
// ctx cancellation, and rescues an event Push already delivered into it.
// Without this, the next Push pops the orphaned waiter and the event is lost
// to DrainQueued.
func (es *EventStream[T, R]) abandonWaiter(waiter chan IterResult[T]) {
	es.mu.Lock()
	defer es.mu.Unlock()

	for i, w := range es.waiting {
		if w == waiter {
			es.waiting = append(es.waiting[:i], es.waiting[i+1:]...)
			break
		}
	}
	select {
	case result := <-waiter:
		if !result.Done {
			es.queue = append([]T{result.Value}, es.queue...)
		}
	default:
	}
}

// parkResult re-queues an event the iterator received after its context was
// canceled, preserving push order (the event was delivered before any
// subsequently pushed event reaches the queue). Done markers are discarded:
// End() already recorded the final state.
func (es *EventStream[T, R]) parkResult(result IterResult[T]) {
	es.mu.Lock()
	defer es.mu.Unlock()
	if !result.Done {
		es.queue = append([]T{result.Value}, es.queue...)
	}
}

// Result returns a channel that delivers the final result.
func (es *EventStream[T, R]) Result() <-chan R {
	return es.finalResultCh
}

// WaitDone blocks until the stream reaches the done state or timeout
// elapses, returning whether the done state was observed. Producers signal
// done via a completing Push or End; after that no further events can be
// queued, so the queue is stable for draining.
func (es *EventStream[T, R]) WaitDone(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		es.mu.Lock()
		done := es.done
		es.mu.Unlock()
		if done {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// DrainQueued removes and returns all events still sitting in the queue.
// A consumer whose Iterator returned early (e.g. on ctx cancellation) uses
// this to observe tail events the producer pushed afterwards.
func (es *EventStream[T, R]) DrainQueued() []T {
	es.mu.Lock()
	defer es.mu.Unlock()
	if len(es.queue) == 0 {
		return nil
	}
	queued := es.queue
	es.queue = nil
	return queued
}

// IsDone returns true if the stream is complete.
func (es *EventStream[T, R]) IsDone() bool {
	es.mu.Lock()
	defer es.mu.Unlock()
	return es.done
}
