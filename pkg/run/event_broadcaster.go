package run

import (
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
)

const (
	// RingSize is the number of entries in the fixed-size ring buffer.
	RingSize = 4096

	// ConsumerChanSize is the buffered channel size for each consumer.
	// Larger buffer absorbs burst events when the TUI event loop is busy
	// (e.g. wrapping large content), preventing slow-consumer disconnect.
	ConsumerChanSize = 2048
)

// ringEntry holds a single event in the ring buffer.
type ringEntry struct {
	seq   uint64
	event []byte
}

// Consumer represents a connected client that receives events from the broadcaster.
type Consumer struct {
	id     uint64
	ch     chan []byte
	cursor uint64 // last seq consumed
}

// Events returns a read-only channel for consuming events.
func (c *Consumer) Events() <-chan []byte {
	return c.ch
}

// EventBroadcaster implements a ring buffer broadcast pattern.
// It receives events via Push, stores them in a fixed-size ring,
// and fans out to all subscribed consumers.
type EventBroadcaster struct {
	mu        sync.Mutex
	seq       uint64
	ring      [RingSize]ringEntry
	consumers map[uint64]*Consumer
	nextID    uint64
	closed    bool
}

// NewEventBroadcaster creates a new broadcaster ready to receive events.
func NewEventBroadcaster() *EventBroadcaster {
	return &EventBroadcaster{
		consumers: make(map[uint64]*Consumer),
	}
}

// Push adds an event to the ring buffer and broadcasts it to all consumers.
// Empty thinking deltas are filtered at this layer — they are the primary
// source of disk bloat (99% of events.jsonl volume).
func (b *EventBroadcaster) Push(event []byte) {
	if len(event) == 0 {
		return
	}

	// Filter empty thinking deltas at the broadcast layer.
	if isEmptyThinkingDelta(event) {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return
	}

	b.seq++
	seq := b.seq
	idx := seq % RingSize
	b.ring[idx] = ringEntry{seq: seq, event: event}

	// Fan out to all consumers. Drop slow consumers.
	for id, c := range b.consumers {
		select {
		case c.ch <- event:
			c.cursor = seq
		default:
			// Consumer too slow — disconnect it.
			slog.Warn("event broadcaster: disconnecting slow consumer", "consumer_id", id)
			close(c.ch)
			delete(b.consumers, id)
		}
	}
}

// Subscribe creates a new consumer. If the ring still has entries from
// the requested sequence, the consumer's channel will be pre-loaded
// with replayed events. fromSeq=0 means "replay everything from the beginning".
func (b *EventBroadcaster) Subscribe(fromSeq uint64) *Consumer {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed {
		return nil
	}

	b.nextID++
	id := b.nextID
	ch := make(chan []byte, ConsumerChanSize)

	// Replay from ring if possible.
	// fromSeq=0 means "replay everything from the beginning".
	// fromSeq>0 means "I've seen up to fromSeq, give me everything after".
	if fromSeq < b.seq {
		// Check if the oldest entry in the ring is still available.
		var oldest uint64
		if b.seq >= RingSize {
			oldest = b.seq - RingSize + 1
		} else {
			oldest = 1
		}

		replayAll := fromSeq == 0
		if fromSeq < oldest {
			// Client is too far behind — start from oldest available.
			fromSeq = oldest
		}

		// fromSeq>0 means "I've seen up to fromSeq", so start from fromSeq+1.
		// fromSeq=0 means "replay from beginning", so include the oldest entry.
		startSeq := fromSeq + 1
		if replayAll {
			startSeq = oldest
		}
		for seq := startSeq; seq <= b.seq; seq++ {
			idx := seq % RingSize
			if b.ring[idx].seq == seq {
				select {
				case ch <- b.ring[idx].event:
				default:
					// Replay buffer full — start live from current position.
					break
				}
			}
		}
	}

	c := &Consumer{
		id:     id,
		ch:     ch,
		cursor: b.seq,
	}
	b.consumers[id] = c
	return c
}

// Unsubscribe removes a consumer and closes its channel.
func (b *EventBroadcaster) Unsubscribe(c *Consumer) {
	if c == nil {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.consumers[c.id]; ok {
		delete(b.consumers, c.id)
		close(c.ch)
	}
}

// Close shuts down the broadcaster, disconnecting all consumers.
func (b *EventBroadcaster) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.closed = true
	for id, c := range b.consumers {
		close(c.ch)
		delete(b.consumers, id)
	}
}

// Seq returns the current sequence number.
func (b *EventBroadcaster) Seq() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.seq
}

// isEmptyThinkingDelta checks if a raw JSON event line is a message_update
// with an empty thinking delta. These events constitute the bulk of events.jsonl
// volume and provide no useful information.
func isEmptyThinkingDelta(data []byte) bool {
	// Fast path: check for common pattern before full JSON parse.
	s := string(data)
	if !strings.Contains(s, `"message_update"`) {
		return false
	}
	if !strings.Contains(s, `"thinking_delta"`) {
		return false
	}

	var evt struct {
		Type                string `json:"type"`
		AssistantMessageEvt struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		} `json:"assistantMessageEvent"`
	}
	if err := json.Unmarshal(data, &evt); err != nil {
		return false
	}
	if evt.Type != "message_update" {
		return false
	}
	if evt.AssistantMessageEvt.Type != "thinking_delta" {
		return false
	}
	return strings.TrimSpace(evt.AssistantMessageEvt.Delta) == ""
}
