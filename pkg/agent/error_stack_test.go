package agent

import (
	"errors"
	"strings"
	"testing"
)

func TestWithErrorStack(t *testing.T) {
	base := errors.New("boom")
	err := WithErrorStack(base)
	if err == nil {
		t.Fatal("expected wrapped error")
	}
	if err.Error() != base.Error() {
		t.Fatalf("expected error message %q, got %q", base.Error(), err.Error())
	}
	stack := ErrorStack(err)
	if strings.TrimSpace(stack) == "" {
		t.Fatal("expected non-empty stack trace")
	}
}

func TestNewErrorEventIncludesStack(t *testing.T) {
	event := NewErrorEvent(errors.New("rate limit"))
	if event.Type != EventError {
		t.Fatalf("expected %q type, got %q", EventError, event.Type)
	}
	if event.Error != "rate limit" {
		t.Fatalf("expected error message to be preserved, got %q", event.Error)
	}
	if strings.TrimSpace(event.ErrorStack) == "" {
		t.Fatal("expected error stack in event payload")
	}
}
