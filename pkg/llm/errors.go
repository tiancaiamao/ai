package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// APIError represents a generic non-200 API response.
type APIError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = "unknown API error"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("API error (%d): %s", e.StatusCode, msg)
	}
	return "API error: " + msg
}

// ContextLengthExceededError indicates request context exceeded model limits.
type ContextLengthExceededError struct {
	StatusCode int
	Message    string
	Body       string
}

func (e *ContextLengthExceededError) Error() string {
	msg := strings.TrimSpace(e.Message)
	if msg == "" {
		msg = "context length exceeded"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("context length exceeded (%d): %s", e.StatusCode, msg)
	}
	return "context length exceeded: " + msg
}

// ClassifyAPIError converts an API response payload into a typed error.
func ClassifyAPIError(statusCode int, payload string) error {
	payload = strings.TrimSpace(payload)
	message := extractAPIErrorMessage(payload)
	if message == "" {
		message = payload
	}
	if message == "" {
		message = "unknown API error"
	}

	if looksLikeContextLengthExceeded(message) || looksLikeContextLengthExceeded(payload) {
		return &ContextLengthExceededError{
			StatusCode: statusCode,
			Message:    message,
			Body:       payload,
		}
	}

	return &APIError{
		StatusCode: statusCode,
		Message:    message,
		Body:       payload,
	}
}

// IsContextLengthExceeded reports whether an error is due to context/token limits.
func IsContextLengthExceeded(err error) bool {
	if err == nil {
		return false
	}
	var cle *ContextLengthExceededError
	if errors.As(err, &cle) {
		return true
	}
	return looksLikeContextLengthExceeded(err.Error())
}

func extractAPIErrorMessage(payload string) string {
	if payload == "" {
		return ""
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return ""
	}

	// Common OpenAI-compatible format:
	// {"error":{"message":"...", ...}}
	if rawErr, ok := decoded["error"]; ok {
		switch v := rawErr.(type) {
		case string:
			return strings.TrimSpace(v)
		case map[string]any:
			if message, ok := v["message"].(string); ok {
				return strings.TrimSpace(message)
			}
			if detail, ok := v["detail"].(string); ok {
				return strings.TrimSpace(detail)
			}
			if typ, ok := v["type"].(string); ok {
				return strings.TrimSpace(typ)
			}
		}
	}

	// Other common patterns.
	if message, ok := decoded["message"].(string); ok {
		return strings.TrimSpace(message)
	}
	if detail, ok := decoded["detail"].(string); ok {
		return strings.TrimSpace(detail)
	}

	return ""
}

func looksLikeContextLengthExceeded(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}

	needles := []string{
		"context length",
		"context window",
		"contextwindow",
		"maximum context",
		"max context",
		"context limit",
		"too many tokens",
		"maximum number of tokens",
		"prompt is too long",
		"token limit exceeded",
		"contextlength",
		"context_window_exceeded",
		"contextwindowexceeded",
		"finishreasonlength",
	}
	for _, needle := range needles {
		if strings.Contains(s, needle) {
			return true
		}
	}
	return false
}
