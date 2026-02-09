package agent

import (
	"context"
	"sync"
)

// IterResult represents a single iteration result.
type IterResult[T any] struct {
	Value T
	Done  bool
}

// EventStream is a generic async event stream with dynamic expansion.
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
	maxSize       int // Maximum queue size before expansion
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
		maxSize:       100, // Default max queue size
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

	// Check if queue is full and needs expansion
	if len(es.queue) >= es.maxSize && !es.done {
		// Expand queue: double the max size
		es.maxSize *= 2
		// Allocate new queue
		newQueue := make([]T, 0, es.maxSize)
		newQueue = append(newQueue, es.queue...)
		es.queue = newQueue
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
	es.queue = nil
}

// Iterator returns a channel that iterates over events.
// The channel will be closed when the stream is complete or context is cancelled.
func (es *EventStream[T, R]) Iterator(ctx context.Context) <-chan IterResult[T] {
	ch := make(chan IterResult[T])

	go func() {
		defer close(ch)
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
				ch <- result
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch
}

// Result returns a channel that delivers the final result.
func (es *EventStream[T, R]) Result() <-chan R {
	return es.finalResultCh
}

// IsDone returns true if the stream is complete.
func (es *EventStream[T, R]) IsDone() bool {
	es.mu.Lock()
	defer es.mu.Unlock()
	return es.done
}

// SetMaxSize sets the maximum queue size before expansion.
func (es *EventStream[T, R]) SetMaxSize(size int) {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.maxSize = size
}

// QueueSize returns the current queue size.
func (es *EventStream[T, R]) QueueSize() int {
	es.mu.Lock()
	defer es.mu.Unlock()
	return len(es.queue)
}
