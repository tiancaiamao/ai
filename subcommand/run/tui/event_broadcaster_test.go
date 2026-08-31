package tui

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestEventBroadcaster_PushAndSubscribe(t *testing.T) {
	b := NewEventBroadcaster()

	// Subscribe first to receive live events.
	c := b.Subscribe(0)
	defer b.Unsubscribe(c)

	event := []byte(`{"type":"text_delta","data":{"text_delta":"hello"}}`)
	b.Push(event)

	select {
	case e := <-c.Events():
		if string(e) != string(event) {
			t.Errorf("expected %s, got %s", event, e)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBroadcaster_SubscribeBeforePush(t *testing.T) {
	b := NewEventBroadcaster()

	c := b.Subscribe(0)
	defer b.Unsubscribe(c)

	event := []byte(`{"type":"text_delta","data":{"text_delta":"world"}}`)
	b.Push(event)

	select {
	case e := <-c.Events():
		if string(e) != string(event) {
			t.Errorf("expected %s, got %s", event, e)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBroadcaster_MultipleConsumers(t *testing.T) {
	b := NewEventBroadcaster()

	c1 := b.Subscribe(0)
	c2 := b.Subscribe(0)
	defer b.Unsubscribe(c1)
	defer b.Unsubscribe(c2)

	event := []byte(`{"type":"agent_start"}`)
	b.Push(event)

	for i, c := range []*Consumer{c1, c2} {
		select {
		case e := <-c.Events():
			if string(e) != string(event) {
				t.Errorf("consumer %d: expected %s, got %s", i, event, e)
			}
		case <-time.After(time.Second):
			t.Fatalf("consumer %d timed out", i)
		}
	}
}

func TestEventBroadcaster_ReplayFromRing(t *testing.T) {
	b := NewEventBroadcaster()

	// Push some events.
	for i := 0; i < 10; i++ {
		data, _ := json.Marshal(map[string]any{"type": "test", "seq": i})
		b.Push(data)
	}

	// Subscribe with fromSeq=0 means "replay everything from the beginning".
	// Should replay all 10 existing events.
	c := b.Subscribe(0)
	defer b.Unsubscribe(c)

	// Expect 10 replayed events + 5 live events = 15 total.
	expectedReplay := 10
	for i := 10; i < 15; i++ {
		data, _ := json.Marshal(map[string]any{"type": "test", "seq": i})
		b.Push(data)
	}

	total := expectedReplay + 5
	count := 0
	timeout := time.After(2 * time.Second)
	for count < total {
		select {
		case <-c.Events():
			count++
		case <-timeout:
			t.Fatalf("only received %d/5 live events", count)
		}
	}
}

func TestEventBroadcaster_ReplayWithFromSeq(t *testing.T) {
	b := NewEventBroadcaster()

	// Push 20 events.
	for i := 0; i < 20; i++ {
		data, _ := json.Marshal(map[string]any{"type": "test", "seq": i})
		b.Push(data)
	}

	// Subscribe from seq 10 — should replay events 11-20 and then go live.
	c := b.Subscribe(10)
	defer b.Unsubscribe(c)

	count := 0
	expectedReplay := 10 // events 11-20
	timeout := time.After(2 * time.Second)
	for count < expectedReplay {
		select {
		case <-c.Events():
			count++
		case <-timeout:
			t.Fatalf("only received %d/%d replayed events", count, expectedReplay)
		}
	}
}

func TestEventBroadcaster_FilterEmptyThinkingDeltas(t *testing.T) {
	b := NewEventBroadcaster()

	c := b.Subscribe(0)
	defer b.Unsubscribe(c)

	// Push an empty thinking delta — should be filtered.
	emptyThinking := []byte(`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":""}}`)
	b.Push(emptyThinking)

	// Push a non-empty event — should not be filtered.
	realEvent := []byte(`{"type":"text_delta","data":{"text_delta":"hello"}}`)
	b.Push(realEvent)

	// Should only get the real event.
	select {
	case e := <-c.Events():
		if string(e) != string(realEvent) {
			t.Errorf("expected real event, got %s", e)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for real event")
	}

	// Should not have any more events (empty thinking was filtered).
	select {
	case e := <-c.Events():
		t.Errorf("unexpected event after filter: %s", e)
	case <-time.After(100 * time.Millisecond):
		// Expected — no more events.
	}
}

func TestEventBroadcaster_NonEmptyThinkingNotFiltered(t *testing.T) {
	b := NewEventBroadcaster()

	c := b.Subscribe(0)
	defer b.Unsubscribe(c)

	// Push a non-empty thinking delta — should NOT be filtered.
	thinking := []byte(`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"I am thinking..."}}`)
	b.Push(thinking)

	select {
	case e := <-c.Events():
		if string(e) != string(thinking) {
			t.Errorf("expected thinking event, got %s", e)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for thinking event")
	}
}

func TestEventBroadcaster_Close(t *testing.T) {
	b := NewEventBroadcaster()

	c := b.Subscribe(0)
	b.Close()

	// Channel should be closed.
	_, ok := <-c.Events()
	if ok {
		t.Error("expected channel to be closed")
	}
}

func TestEventBroadcaster_CloseUnsubscribesAll(t *testing.T) {
	b := NewEventBroadcaster()

	var consumers []*Consumer
	for i := 0; i < 5; i++ {
		c := b.Subscribe(0)
		consumers = append(consumers, c)
	}

	b.Close()

	for i, c := range consumers {
		_, ok := <-c.Events()
		if ok {
			t.Errorf("consumer %d: expected channel to be closed", i)
		}
	}
}

func TestEventBroadcaster_PushAfterClose(t *testing.T) {
	b := NewEventBroadcaster()
	b.Close()

	c := b.Subscribe(0)
	if c != nil {
		t.Error("expected nil consumer after close")
	}
}

func TestEventBroadcaster_EmptyEventIgnored(t *testing.T) {
	b := NewEventBroadcaster()

	c := b.Subscribe(0)
	defer b.Unsubscribe(c)

	b.Push([]byte{})
	b.Push(nil)

	// Push a real event to verify.
	realEvent := []byte(`{"type":"test"}`)
	b.Push(realEvent)

	select {
	case e := <-c.Events():
		if string(e) != string(realEvent) {
			t.Errorf("expected real event, got %s", e)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestEventBroadcaster_ConcurrentPush(t *testing.T) {
	b := NewEventBroadcaster()
	c := b.Subscribe(0)
	defer b.Unsubscribe(c)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			data, _ := json.Marshal(map[string]any{"type": "test", "n": n})
			b.Push(data)
		}(i)
	}
	wg.Wait()

	// Should receive all 100 events.
	count := 0
	timeout := time.After(2 * time.Second)
	for count < 100 {
		select {
		case <-c.Events():
			count++
		case <-timeout:
			t.Fatalf("only received %d/100 events", count)
		}
	}
}

func TestIsEmptyThinkingDelta(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		expected bool
	}{
		{
			name:     "empty thinking delta",
			data:     `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":""}}`,
			expected: true,
		},
		{
			name:     "whitespace-only thinking delta",
			data:     `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"   "}}`,
			expected: true,
		},
		{
			name:     "non-empty thinking delta",
			data:     `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"I am thinking..."}}`,
			expected: false,
		},
		{
			name:     "text delta (not thinking)",
			data:     `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":""}}`,
			expected: false,
		},
		{
			name:     "unrelated event",
			data:     `{"type":"agent_start"}`,
			expected: false,
		},
		{
			name:     "invalid JSON",
			data:     `not json`,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEmptyThinkingDelta([]byte(tt.data))
			if got != tt.expected {
				t.Errorf("isEmptyThinkingDelta(%q) = %v, want %v", tt.data, got, tt.expected)
			}
		})
	}
}
