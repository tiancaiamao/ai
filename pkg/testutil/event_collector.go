package testutil

import (
	"sync"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// EventCollector collects agent events for assertion in tests.
type EventCollector struct {
	mu     sync.Mutex
	events []agent.AgentEvent
}

// NewEventCollector creates a new EventCollector.
func NewEventCollector() *EventCollector {
	return &EventCollector{
		events: make([]agent.AgentEvent, 0),
	}
}

// Record adds an event to the collector. Safe to call from multiple goroutines.
func (c *EventCollector) Record(ev agent.AgentEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, ev)
}

// All returns all collected events.
func (c *EventCollector) All() []agent.AgentEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]agent.AgentEvent, len(c.events))
	copy(out, c.events)
	return out
}

// EventsOfType returns events matching the given type.
func (c *EventCollector) EventsOfType(typ string) []agent.AgentEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var result []agent.AgentEvent
	for _, ev := range c.events {
		if ev.Type == typ {
			result = append(result, ev)
		}
	}
	return result
}

// HasEvent returns true if at least one event of the given type was collected.
func (c *EventCollector) HasEvent(typ string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ev := range c.events {
		if ev.Type == typ {
			return true
		}
	}
	return false
}

// CountEvent returns the number of events of the given type.
func (c *EventCollector) CountEvent(typ string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, ev := range c.events {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

// Len returns the total number of collected events.
func (c *EventCollector) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

// Reset clears all collected events.
func (c *EventCollector) Reset() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = c.events[:0]
}

// Subscribe drains the agent event channel into this collector.
// Returns a stop function that should be called to end collection.
// The caller must ensure the agent is running before calling Subscribe.
func (c *EventCollector) Subscribe(ch <-chan agent.AgentEvent) func() {
	done := make(chan struct{})
	go func() {
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				c.Record(ev)
			case <-done:
				return
			}
		}
	}()
	return func() { close(done) }
}

// CollectAll reads all events from the channel until it is closed or the
// timeout fires. Returns collected events. Fatals the test on timeout.
func CollectAll(t *testing.T, ch <-chan agent.AgentEvent, timeout time.Duration) []agent.AgentEvent {
	t.Helper()
	var events []agent.AgentEvent
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
		case <-timer.C:
			t.Fatal("CollectAll: timed out waiting for events")
			return events
		}
	}
}
