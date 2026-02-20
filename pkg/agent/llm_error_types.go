package agent

import "strings"

const (
	llmErrorTypeRateLimit    = "rate_limit"
	llmErrorTypeTimeout      = "timeout"
	llmErrorTypeContextLimit = "context_limit"
	llmErrorTypeNetwork      = "network"
	llmErrorTypeServer       = "server"
	llmErrorTypeClient       = "client"
	llmErrorTypeCanceled     = "canceled"
	llmErrorTypeUnknown      = "unknown"
)

func normalizeLLMErrorType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case llmErrorTypeRateLimit:
		return llmErrorTypeRateLimit
	case llmErrorTypeTimeout:
		return llmErrorTypeTimeout
	case llmErrorTypeContextLimit:
		return llmErrorTypeContextLimit
	case llmErrorTypeNetwork:
		return llmErrorTypeNetwork
	case llmErrorTypeServer:
		return llmErrorTypeServer
	case llmErrorTypeClient:
		return llmErrorTypeClient
	case llmErrorTypeCanceled:
		return llmErrorTypeCanceled
	default:
		return llmErrorTypeUnknown
	}
}

func inferLLMErrorTypeFromMessage(message string) string {
	lower := strings.ToLower(strings.TrimSpace(message))
	switch {
	case lower == "":
		return llmErrorTypeUnknown
	case strings.Contains(lower, "rate limit"), strings.Contains(lower, "429"), strings.Contains(lower, "quota"):
		return llmErrorTypeRateLimit
	case strings.Contains(lower, "context deadline exceeded"), strings.Contains(lower, "timeout"), strings.Contains(lower, "timed out"):
		return llmErrorTypeTimeout
	case strings.Contains(lower, "context length"), strings.Contains(lower, "context window"), strings.Contains(lower, "token limit"):
		return llmErrorTypeContextLimit
	case strings.Contains(lower, "connection"), strings.Contains(lower, "dns"), strings.Contains(lower, "dial tcp"), strings.Contains(lower, "no such host"), strings.Contains(lower, "eof"):
		return llmErrorTypeNetwork
	case strings.Contains(lower, "api error (5"), strings.Contains(lower, "service unavailable"), strings.Contains(lower, "bad gateway"), strings.Contains(lower, "gateway timeout"):
		return llmErrorTypeServer
	case strings.Contains(lower, "api error (4"), strings.Contains(lower, "unauthorized"), strings.Contains(lower, "forbidden"):
		return llmErrorTypeClient
	case strings.Contains(lower, "context canceled"), strings.Contains(lower, "cancelled"):
		return llmErrorTypeCanceled
	default:
		return llmErrorTypeUnknown
	}
}
