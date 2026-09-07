package llm

import (
	"context"
	"strings"
	"testing"
)

func TestProcessResponsesSSEEmptyErrorIncludesDiagnostics(t *testing.T) {
	stream := NewEventStream[LLMEvent, LLMMessage](
		func(e LLMEvent) bool { return e.GetEventType() == "done" || e.GetEventType() == "error" },
		func(LLMEvent) LLMMessage { return LLMMessage{} },
	)

	processResponsesSSE(
		context.Background(),
		strings.NewReader("data: {\"type\":\"error\"}\n\n"),
		stream,
		0,
		responsesStreamMeta{HTTPStatus: 200, RequestID: "req_test_123"},
	)

	for result := range stream.Iterator(context.Background()) {
		errEvent, ok := result.Value.(LLMErrorEvent)
		if !ok {
			continue
		}
		errText := errEvent.Error.Error()
		for _, want := range []string{
			"provider supplied an empty error event",
			`last_event="error"`,
			"http_status=200",
			`request_id="req_test_123"`,
			`payload="{\"type\":\"error\"}"`,
		} {
			if !strings.Contains(errText, want) {
				t.Errorf("error %q missing %q", errText, want)
			}
		}
		return
	}
	t.Fatal("expected an LLMErrorEvent")
}

func TestResponseRequestID(t *testing.T) {
	header := map[string][]string{"X-Request-Id": {" request-123 "}}
	if got := responseRequestID(header); got != "request-123" {
		t.Errorf("responseRequestID() = %q, want request-123", got)
	}
}
