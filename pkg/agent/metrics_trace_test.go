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
