package agent

import (
	"testing"
	"time"

	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

func TestGetFullMetricsDoesNotDeadlock(t *testing.T) {
	buf := traceevent.NewTraceBuf()
	m := NewMetrics(buf)
	now := time.Now()

	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now,
		Name:      "llm_call",
		Phase:     traceevent.PhaseEnd,
		Fields: []traceevent.Field{
			{Key: "input_tokens", Value: 5},
			{Key: "output_tokens", Value: 7},
			{Key: "duration_ms", Value: int64(10)},
		},
	})

	done := make(chan struct{})
	go func() {
		_ = m.GetFullMetrics()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("GetFullMetrics timed out, possible lock deadlock")
	}
}

func TestGetFullMetricsUnderConcurrentRecording(t *testing.T) {
	buf := traceevent.NewTraceBuf()
	m := NewMetrics(buf)
	// Mirror production wiring: every trace write invalidates the metrics cache.
	buf.AddSink(func(_ traceevent.TraceEvent) {
		m.InvalidateCache()
	})

	stop := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		now := time.Now()
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			buf.Record(traceevent.TraceEvent{
				Timestamp: now.Add(time.Duration(i) * time.Millisecond),
				Name:      "llm_call",
				Phase:     traceevent.PhaseEnd,
				Fields: []traceevent.Field{
					{Key: "input_tokens", Value: 4},
					{Key: "output_tokens", Value: 6},
					{Key: "duration_ms", Value: int64(5)},
				},
			})
			i++
		}
	}()
	defer func() {
		close(stop)
		<-writerDone
	}()

	for i := 0; i < 10; i++ {
		done := make(chan struct{})
		go func() {
			_ = m.GetFullMetrics()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(1 * time.Second):
			t.Fatalf("GetFullMetrics blocked under concurrent recording (iteration %d)", i)
		}
	}
}

func TestRecordTraceEventCanonicalSpanNames(t *testing.T) {
	buf := traceevent.NewTraceBuf()
	m := NewMetrics(buf)
	now := time.Now()

	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now,
		Name:      "prompt",
		Phase:     traceevent.PhaseBegin,
	})
	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now.Add(25 * time.Millisecond),
		Name:      "prompt",
		Phase:     traceevent.PhaseEnd,
		Fields: []traceevent.Field{
			{Key: "duration_ms", Value: int64(25)},
			{Key: "error", Value: false},
		},
	})

	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now,
		Name:      "llm_call",
		Phase:     traceevent.PhaseBegin,
	})
	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now.Add(10 * time.Millisecond),
		Name:      "llm_call",
		Phase:     traceevent.PhaseEnd,
		Fields: []traceevent.Field{
			{Key: "input_tokens", Value: 10},
			{Key: "output_tokens", Value: 20},
			{Key: "duration_ms", Value: int64(10)},
			{Key: "first_token_ms", Value: int64(2)},
		},
	})

	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now,
		Name:      "tool_execution",
		Phase:     traceevent.PhaseBegin,
		Fields: []traceevent.Field{
			{Key: "tool", Value: "read"},
		},
	})
	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now.Add(5 * time.Millisecond),
		Name:      "tool_execution",
		Phase:     traceevent.PhaseEnd,
		Fields: []traceevent.Field{
			{Key: "tool", Value: "read"},
			{Key: "duration_ms", Value: int64(5)},
			{Key: "error", Value: false},
		},
	})

	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now,
		Name:      "message_end",
		Phase:     traceevent.PhaseInstant,
		Fields: []traceevent.Field{
			{Key: "role", Value: "assistant"},
		},
	})

	prompt := m.GetPromptMetrics()
	if prompt.CallCount != 1 {
		t.Fatalf("expected prompt call count 1, got %d", prompt.CallCount)
	}
	if prompt.ErrorCount != 0 {
		t.Fatalf("expected prompt error count 0, got %d", prompt.ErrorCount)
	}

	llm := m.GetLLMMetrics()
	if llm.CallCount != 1 {
		t.Fatalf("expected llm call count 1, got %d", llm.CallCount)
	}
	if llm.TokenInput != 10 || llm.TokenOutput != 20 {
		t.Fatalf("expected llm tokens 10/20, got %d/%d", llm.TokenInput, llm.TokenOutput)
	}
	if llm.TokenTotal != 30 {
		t.Fatalf("expected llm total tokens 30, got %d", llm.TokenTotal)
	}
	if llm.ActiveTotalTokensPerSec <= 0 {
		t.Fatalf("expected active token rate > 0, got %f", llm.ActiveTotalTokensPerSec)
	}
	if llm.LastTotalTokensPerSec <= 0 {
		t.Fatalf("expected last-call token rate > 0, got %f", llm.LastTotalTokensPerSec)
	}

	msg := m.GetMessageCounts()
	if msg.ToolCalls != 1 || msg.ToolResults != 1 {
		t.Fatalf("expected tool counts 1/1, got %d/%d", msg.ToolCalls, msg.ToolResults)
	}
	if msg.AssistantMessages != 1 {
		t.Fatalf("expected assistant messages 1, got %d", msg.AssistantMessages)
	}
}

func TestRecordTraceEventLegacySpanAliases(t *testing.T) {
	buf := traceevent.NewTraceBuf()
	m := NewMetrics(buf)
	now := time.Now()

	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now,
		Name:      "prompt_start",
		Phase:     traceevent.PhaseBegin,
	})
	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now.Add(7 * time.Millisecond),
		Name:      "prompt_end",
		Phase:     traceevent.PhaseEnd,
		Fields: []traceevent.Field{
			{Key: "duration_ms", Value: int64(7)},
			{Key: "error", Value: true},
		},
	})

	prompt := m.GetPromptMetrics()
	if prompt.CallCount != 1 {
		t.Fatalf("expected prompt call count 1, got %d", prompt.CallCount)
	}
	if prompt.ErrorCount != 1 {
		t.Fatalf("expected prompt error count 1, got %d", prompt.ErrorCount)
	}
}

func TestIncrementalAggregation(t *testing.T) {
	buf := traceevent.NewTraceBuf()
	m := NewMetrics(buf)
	now := time.Now()

	// Record initial batch of events
	for i := 0; i < 100; i++ {
		m.RecordTraceEvent(traceevent.TraceEvent{
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
			Name:      "llm_call",
			Phase:     traceevent.PhaseEnd,
			Fields: []traceevent.Field{
				{Key: "input_tokens", Value: 10},
				{Key: "output_tokens", Value: 20},
				{Key: "duration_ms", Value: int64(10)},
			},
		})
	}

	// First metrics call - should aggregate all 100 events
	metrics1 := m.GetLLMMetrics()
	if metrics1.CallCount != 100 {
		t.Fatalf("expected 100 calls after first aggregation, got %d", metrics1.CallCount)
	}

	// Record more events
	for i := 0; i < 50; i++ {
		m.RecordTraceEvent(traceevent.TraceEvent{
			Timestamp: now.Add(time.Duration(100+i) * time.Millisecond),
			Name:      "llm_call",
			Phase:     traceevent.PhaseEnd,
			Fields: []traceevent.Field{
				{Key: "input_tokens", Value: 10},
				{Key: "output_tokens", Value: 20},
				{Key: "duration_ms", Value: int64(10)},
			},
		})
	}

	// Second metrics call - should only aggregate the 50 new events (incremental)
	metrics2 := m.GetLLMMetrics()
	if metrics2.CallCount != 150 {
		t.Fatalf("expected 150 calls after incremental aggregation, got %d", metrics2.CallCount)
	}

	// Simulate buffer flush by creating a new buffer (similar to what happens after prompt completes)
	// In real usage, agent calls buf.Flush() which clears events when there's a handler
	newBuf := traceevent.NewTraceBuf()
	m.buf = newBuf // Replace the buffer (simulating flush)

	// Record events after buffer flush
	for i := 0; i < 10; i++ {
		m.RecordTraceEvent(traceevent.TraceEvent{
			Timestamp: now.Add(time.Duration(200+i) * time.Millisecond),
			Name:      "llm_call",
			Phase:     traceevent.PhaseEnd,
			Fields: []traceevent.Field{
				{Key: "input_tokens", Value: 10},
				{Key: "output_tokens", Value: 20},
				{Key: "duration_ms", Value: int64(10)},
			},
		})
	}

	// Third metrics call - should only have 10 calls (buffer was replaced)
	metrics3 := m.GetLLMMetrics()
	if metrics3.CallCount != 10 {
		t.Fatalf("expected 10 calls after buffer flush, got %d", metrics3.CallCount)
	}
}

func TestRecentWindowMetricsDoNotDoubleCountOnRefresh(t *testing.T) {
	buf := traceevent.NewTraceBuf()
	m := NewMetrics(buf)
	now := time.Now()

	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now,
		Name:      "llm_call",
		Phase:     traceevent.PhaseEnd,
		Fields: []traceevent.Field{
			{Key: "input_tokens", Value: 10},
			{Key: "output_tokens", Value: 20},
			{Key: "duration_ms", Value: int64(10)},
		},
	})

	first := m.GetLLMMetrics()
	if first.RecentWindowTotalTokens != 30 {
		t.Fatalf("expected first recent-window total 30, got %d", first.RecentWindowTotalTokens)
	}

	// Invalidate cache with a non-LLM event; recomputing should not re-add old LLM samples.
	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now.Add(1 * time.Millisecond),
		Name:      "message_end",
		Phase:     traceevent.PhaseInstant,
		Fields: []traceevent.Field{
			{Key: "role", Value: "assistant"},
		},
	})

	second := m.GetLLMMetrics()
	if second.RecentWindowTotalTokens != 30 {
		t.Fatalf("recent-window total should stay 30 after refresh, got %d", second.RecentWindowTotalTokens)
	}
	if second.TokenTotal != 30 {
		t.Fatalf("total tokens should stay 30 after refresh, got %d", second.TokenTotal)
	}
}

func TestLLMErrorBreakdownMetrics(t *testing.T) {
	buf := traceevent.NewTraceBuf()
	m := NewMetrics(buf)
	now := time.Now()

	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now,
		Name:      "llm_call",
		Phase:     traceevent.PhaseEnd,
		Fields: []traceevent.Field{
			{Key: "attempt", Value: int64(1)},
			{Key: "error", Value: "API error (429): rate limit reached"},
			{Key: "error_type", Value: "rate_limit"},
			{Key: "error_status_code", Value: int64(429)},
			{Key: "retry_after_ms", Value: int64(3000)},
			{Key: "duration_ms", Value: int64(120000)},
		},
	})

	m.RecordTraceEvent(traceevent.TraceEvent{
		Timestamp: now.Add(1 * time.Second),
		Name:      "llm_call",
		Phase:     traceevent.PhaseEnd,
		Fields: []traceevent.Field{
			{Key: "attempt", Value: int64(0)},
			{Key: "error", Value: "llm request timeout after 2m0s: context deadline exceeded"},
			{Key: "error_type", Value: "timeout"},
			{Key: "duration_ms", Value: int64(120000)},
		},
	})

	llm := m.GetLLMMetrics()
	if llm.CallCount != 2 {
		t.Fatalf("expected 2 llm calls, got %d", llm.CallCount)
	}
	if llm.ErrorCount != 2 {
		t.Fatalf("expected 2 llm errors, got %d", llm.ErrorCount)
	}
	if llm.RetryCount != 1 {
		t.Fatalf("expected retry count 1, got %d", llm.RetryCount)
	}
	if llm.ErrorRateLimitCount != 1 {
		t.Fatalf("expected rate-limit count 1, got %d", llm.ErrorRateLimitCount)
	}
	if llm.ErrorTimeoutCount != 1 {
		t.Fatalf("expected timeout count 1, got %d", llm.ErrorTimeoutCount)
	}
	if llm.LastErrorType != llmErrorTypeTimeout {
		t.Fatalf("expected last error type %q, got %q", llmErrorTypeTimeout, llm.LastErrorType)
	}
	if llm.LastErrorStatusCode != 0 {
		t.Fatalf("expected last error status code 0 for timeout, got %d", llm.LastErrorStatusCode)
	}
	if llm.LastRetryAfter != 3*time.Second {
		t.Fatalf("expected last retry-after 3s, got %v", llm.LastRetryAfter)
	}
}

func TestIncrementalAggregationPerformance(t *testing.T) {
	buf := traceevent.NewTraceBuf()
	buf.SetMaxEvents(10000) // Increase max events for this test
	m := NewMetrics(buf)
	now := time.Now()

	// Record large number of events to simulate real usage
	numEvents := 5000
	for i := 0; i < numEvents; i++ {
		m.buf.Record(traceevent.TraceEvent{
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
			Name:      "llm_call",
			Phase:     traceevent.PhaseEnd,
			Fields: []traceevent.Field{
				{Key: "input_tokens", Value: 10},
				{Key: "output_tokens", Value: 20},
				{Key: "duration_ms", Value: int64(10)},
			},
		})
	}

	// First call - should process all events
	start := time.Now()
	metrics1 := m.GetLLMMetrics()
	firstDuration := time.Since(start)
	t.Logf("First aggregation (5000 events): %v", firstDuration)
	if metrics1.CallCount != int64(numEvents) {
		t.Fatalf("expected %d calls, got %d", numEvents, metrics1.CallCount)
	}

	// Record a few more events (use RecordTraceEvent to trigger InvalidateCache)
	for i := 0; i < 10; i++ {
		m.RecordTraceEvent(traceevent.TraceEvent{
			Timestamp: now.Add(time.Duration(numEvents+i) * time.Millisecond),
			Name:      "llm_call",
			Phase:     traceevent.PhaseEnd,
			Fields: []traceevent.Field{
				{Key: "input_tokens", Value: 10},
				{Key: "output_tokens", Value: 20},
				{Key: "duration_ms", Value: int64(10)},
			},
		})
	}

	// Second call - should be much faster (incremental)
	start = time.Now()
	metrics2 := m.GetLLMMetrics()
	secondDuration := time.Since(start)
	t.Logf("Incremental aggregation (10 events): %v", secondDuration)
	if metrics2.CallCount != int64(numEvents+10) {
		t.Fatalf("expected %d calls, got %d", numEvents+10, metrics2.CallCount)
	}

	// Log the speedup
	speedup := float64(firstDuration) / float64(secondDuration)
	t.Logf("Speedup: %.1fx faster", speedup)

	// Incremental should be significantly faster
	if secondDuration > firstDuration {
		t.Logf("Warning: incremental (%v) was slower than first aggregation (%v)", secondDuration, firstDuration)
	}
}
