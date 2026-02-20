package agent

import (
	"context"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/llm"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

type noopChunkTraceHandler struct{}

func (noopChunkTraceHandler) Handle(_ context.Context, _ []byte, _ []traceevent.TraceEvent) error {
	return nil
}

func (noopChunkTraceHandler) HandleChunk(_ context.Context, _ []byte, _ []traceevent.TraceEvent, _ bool) error {
	return nil
}

func TestAgentMetricsSurvivePrimaryTraceFlush(t *testing.T) {
	traceevent.SetHandler(noopChunkTraceHandler{})
	defer traceevent.ClearHandler()

	ag := NewAgent(llm.Model{}, "test-key", "test")
	defer ag.Shutdown()

	now := time.Now()
	ag.traceBuf.Record(traceevent.TraceEvent{
		Timestamp: now,
		Name:      "llm_call",
		Phase:     traceevent.PhaseEnd,
		Fields: []traceevent.Field{
			{Key: "input_tokens", Value: 10},
			{Key: "output_tokens", Value: 20},
			{Key: "duration_ms", Value: int64(12)},
		},
	})

	if err := ag.traceBuf.Flush(context.Background()); err != nil {
		t.Fatalf("flush primary trace buffer: %v", err)
	}

	llmMetrics := ag.GetMetrics().GetLLMMetrics()
	if llmMetrics.CallCount != 1 {
		t.Fatalf("expected llm call count 1 after primary flush, got %d", llmMetrics.CallCount)
	}
	if llmMetrics.TokenTotal != 30 {
		t.Fatalf("expected llm token total 30, got %d", llmMetrics.TokenTotal)
	}
}
