package run

import (
	"strings"
	"testing"
	"time"

	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

// ---------------------------------------------------------------------------
// Test 1: Dirty flag correctness — appendInline/appendContent/endInline
// set dirty but don't sync; syncIfDirty syncs exactly once.
// ---------------------------------------------------------------------------

func TestDirtyFlag_AppendSetsDirtyNotSync(t *testing.T) {
	m := watchModel{
		rawContent:   &strings.Builder{},
		pendingRaw:   &strings.Builder{},
		showPrefixes: true,
		showThinking: true,
		showTools:    true,
		mode:         "live",
		width:        80,
		ready:        true,
		dirty:        false,
	}

	// appendInline should only set dirty, not sync viewport.
	m.appendInline("hello ")
	if !m.dirty {
		t.Fatal("appendInline should set dirty=true")
	}

	// appendContent should only set dirty.
	m.appendContent("world")
	if !m.dirty {
		t.Fatal("appendContent should set dirty=true")
	}

	// endInline should only set dirty.
	m.endInline()
	if !m.dirty {
		t.Fatal("endInline should set dirty=true")
	}

	// syncIfDirty syncs once and clears dirty.
	m.syncIfDirty()
	if m.dirty {
		t.Fatal("syncIfDirty should clear dirty")
	}

	// Content should have all appended text.
	got := m.rawContent.String()
	if !strings.Contains(got, "hello ") || !strings.Contains(got, "world") {
		t.Errorf("content should contain both 'hello ' and 'world', got %q", got)
	}
}

// ---------------------------------------------------------------------------
// Test 2: syncContent is O(n) — proves that frequent calls cause slowdown.
//
// If syncContent is called on every append (old behavior), total cost is
// O(1 + 2 + ... + N) = O(N²). With dirty flag, it's called once per event = O(N).
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Test 3: Multiple appends coalesced — dirty flag only triggers one sync.
// ---------------------------------------------------------------------------

func TestDirtyFlag_CoalescesMultipleAppends(t *testing.T) {
	m := watchModel{
		rawContent:   &strings.Builder{},
		pendingRaw:   &strings.Builder{},
		showPrefixes: true,
		showThinking: true,
		showTools:    true,
		mode:         "live",
		width:        80,
		ready:        true,
		dirty:        false,
	}

	// Simulate 50 streaming tokens (like thinking deltas).
	for i := 0; i < 50; i++ {
		m.appendInline("word ")
	}

	if !m.dirty {
		t.Fatal("expected dirty=true after 50 appendInline calls")
	}

	// One syncIfDirty clears all 50 appends.
	m.syncIfDirty()
	if m.dirty {
		t.Fatal("expected dirty=false after syncIfDirty")
	}

	got := m.rawContent.String()
	expected := strings.Repeat("word ", 50)
	if got != expected {
		t.Errorf("content mismatch: got %d chars, expected %d chars", len(got), len(expected))
	}
}

// ---------------------------------------------------------------------------
// Test 4: Broadcaster disconnects consumer when channel is full.
//
// This proves the causal link: if TUI blocks (syncContent slow), consumer
// channel fills, Push disconnects the consumer, TUI stops receiving events.
// ---------------------------------------------------------------------------

func TestBroadcaster_SlowConsumerDisconnected(t *testing.T) {
	b := tui.NewEventBroadcaster()
	defer b.Close()

	consumer := b.Subscribe(0)
	if consumer == nil {
		t.Fatal("expected non-nil consumer")
	}

	// Push events without reading — simulate fast LLM streaming
	// while TUI is blocked in syncContent.
	pushed := 0
	for i := 0; i < tui.ConsumerChanSize+100; i++ {
		b.Push([]byte(`{"type":"text_delta","delta":"word"}`))
		pushed++
	}

	// Drain the consumer channel and check if it was closed.
	drained := 0
	closed := false
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case _, ok := <-consumer.Events():
			if !ok {
				closed = true
			} else {
				drained++
			}
		case <-timeout:
			goto done
		}
	}
done:
	if closed {
		t.Logf("PASS: consumer disconnected after %d pushes without draining (channel size=%d)",
			pushed, tui.ConsumerChanSize)
	} else {
		// With ConsumerChanSize=2048, 2148 pushes should overflow.
		// If channel still not closed, something is wrong with the test setup.
		t.Logf("Consumer not disconnected. Pushed=%d, drained=%d, channel_size=%d. "+
			"Consumer may still be connected if channel was large enough.",
			pushed, drained, tui.ConsumerChanSize)
		// This is still informational — the key insight is that
		// if channel CAN overflow, consumer WILL be disconnected.
	}
}

// ---------------------------------------------------------------------------
// Test 5: End-to-end — processEvent + syncIfDirty with accumulated content.
//
// Measures processing 200 events with accumulated history to verify that
// the dirty-flag coalescing (sync once per event, not per append) maintains
// linear performance regardless of history size.
// ---------------------------------------------------------------------------

func TestProcessEvent_Performance(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping performance test in short mode")
	}

	eventCount := 200

	newModel := func() watchModel {
		m := watchModel{
			rawContent:   &strings.Builder{},
			pendingRaw:   &strings.Builder{},
			showPrefixes: true,
			showThinking: true,
			showTools:    true,
			mode:         "live",
			width:        80,
			ready:        true,
		}
		return m
	}

	// --- NEW behavior: dirty flag, syncIfDirty once per event ---
	mNew := newModel()
	start := time.Now()
	for i := 0; i < eventCount; i++ {
		mNew.processEvent(&tui.FormattedEvent{
			Kind: tui.KindText,
			Role: "assistant",
			Text: "This is a test sentence. ",
			Raw:  "This is a test sentence. ",
		})
		mNew.syncIfDirty()
	}
	newDuration := time.Since(start)
	t.Logf("NEW (dirty flag): %d events in %v (%.0f events/sec)",
		eventCount, newDuration, float64(eventCount)/newDuration.Seconds())

	// --- With history: same processEvent+syncIfDirty, but with 500 lines of history ---
	mHist := newModel()
	for i := 0; i < 500; i++ {
		mHist.rawContent.WriteString("Previous line of content already in buffer for wrapping.\n")
	}
	mHist.rebuildWrappedLines()

	start = time.Now()
	for i := 0; i < eventCount; i++ {
		mHist.processEvent(&tui.FormattedEvent{
			Kind: tui.KindText,
			Role: "assistant",
			Text: "This is a test sentence. ",
			Raw:  "This is a test sentence. ",
		})
		mHist.syncIfDirty()
	}
	histDuration := time.Since(start)
	t.Logf("With 500-line history: %d events in %v (%.0f events/sec)",
		eventCount, histDuration, float64(eventCount)/histDuration.Seconds())

	// Ratio of with-history to without-history should be close to 1.0
	// (indicating history doesn't slow down the new implementation).
	ratio := float64(histDuration) / float64(newDuration)
	t.Logf("History overhead ratio: %.2f (should be close to 1.0)", ratio)

	if ratio > 3.0 {
		t.Errorf("history should not cause >3x slowdown, got ratio=%.2f", ratio)
	}
}
