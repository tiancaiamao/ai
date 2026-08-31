package llm

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tiancaiamao/ai/pkg/netutil"
)

type responsesRequestOptions struct {
	Endpoint            string
	Body                []byte
	Headers             http.Header
	Proxy               string
	UseEnvironmentProxy bool
	MaxRetries          int
}

// doResponsesRequest sends a Responses API request. Authentication and body
// construction stay provider-specific, while transport, retries, and status
// handling are shared by OpenAI and Codex.
func doResponsesRequest(ctx context.Context, opts responsesRequestOptions) (*http.Response, error) {
	const baseDelay = 500 * time.Millisecond

	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, opts.Endpoint, bytes.NewReader(opts.Body))
		if err != nil {
			return nil, err
		}
		req.Header = opts.Headers.Clone()

		client, err := newResponsesHTTPClient(opts.Proxy, opts.UseEnvironmentProxy)
		if err != nil {
			return nil, fmt.Errorf("configure model proxy: %w", err)
		}
		if deadline, ok := ctx.Deadline(); ok {
			if remaining := time.Until(deadline); remaining > 0 {
				client.Timeout = remaining
			}
		}

		resp, err := client.Do(req)
		if err != nil {
			if attempt < opts.MaxRetries {
				if err := waitResponsesRetry(ctx, baseDelay*time.Duration(1<<uint(attempt))); err != nil {
					return nil, err
				}
				continue
			}
			return nil, fmt.Errorf("request failed after retries: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if isRetryableStatus(resp.StatusCode) && attempt < opts.MaxRetries {
			delay := baseDelay * time.Duration(1<<uint(attempt))
			if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
				if parsed := parseRetryAfterHeader(retryAfter); parsed > 0 {
					delay = parsed
				}
			}
			if retryAfterMS := resp.Header.Get("Retry-After-Ms"); retryAfterMS != "" {
				if parsed, err := time.ParseDuration(retryAfterMS + "ms"); err == nil && parsed > 0 {
					delay = parsed
				}
			}
			if err := waitResponsesRetry(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		return nil, ClassifyAPIError(resp.StatusCode, string(body))
	}

	return nil, fmt.Errorf("no response after retries")
}

func newResponsesHTTPClient(proxyURL string, useEnvironmentProxy bool) (*http.Client, error) {
	if useEnvironmentProxy {
		return netutil.NewEnvironmentHTTPClient()
	}
	return netutil.NewHTTPClient(proxyURL)
}

func isRetryableStatus(status int) bool {
	return status == 429 || status == 500 || status == 502 || status == 503 || status == 504
}

func waitResponsesRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
