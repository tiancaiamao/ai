package testutil

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

// CassetteVersion is the current version of the cassette format.
const CassetteVersion = "v1"

// Interaction represents a single HTTP request-response pair.
type Interaction struct {
	Request  RecordedRequest  `yaml:"request"`
	Response RecordedResponse `yaml:"response"`
}

// RecordedRequest represents a recorded HTTP request.
type RecordedRequest struct {
	Method  string      `yaml:"method"`
	URL     string      `yaml:"url"`
	Headers http.Header `yaml:"headers,omitempty"`
	Body    string      `yaml:"body,omitempty"`
}

// RecordedResponse represents a recorded HTTP response.
type RecordedResponse struct {
	StatusCode int         `yaml:"status_code"`
	Headers    http.Header `yaml:"headers,omitempty"`
	Body       string      `yaml:"body"`
}

// Cassette represents a collection of recorded interactions.
type Cassette struct {
	Version      string        `yaml:"version"`
	Interactions []Interaction `yaml:"interactions"`
}

// LoadCassette loads a cassette from YAML bytes.
func LoadCassette(data []byte) (*Cassette, error) {
	var cassette Cassette
	if err := yaml.Unmarshal(data, &cassette); err != nil {
		return nil, fmt.Errorf("failed to parse cassette YAML: %w", err)
	}

	if cassette.Version != CassetteVersion {
		return nil, fmt.Errorf("unsupported cassette version %q (expected %q)", cassette.Version, CassetteVersion)
	}

	return &cassette, nil
}

// MarshalCassette serializes a cassette to YAML bytes.
func MarshalCassette(cassette *Cassette) ([]byte, error) {
	data, err := yaml.Marshal(cassette)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cassette: %w", err)
	}
	return data, nil
}

// recordRequest captures an HTTP request for recording.
func recordRequest(req *http.Request) (RecordedRequest, error) {
	body := ""
	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return RecordedRequest{}, fmt.Errorf("failed to read request body: %w", err)
		}
		body = string(bodyBytes)
		// Restore the body so it can be read again
		req.Body = io.NopCloser(strings.NewReader(body))
	}

	// Sanitize headers - remove sensitive data
	headers := sanitizeHeaders(req.Header)

	return RecordedRequest{
		Method:  req.Method,
		URL:     req.URL.String(),
		Headers: headers,
		Body:    body,
	}, nil
}

// recordResponse captures an HTTP response for recording.
func recordResponse(resp *http.Response) (RecordedResponse, error) {
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return RecordedResponse{}, fmt.Errorf("failed to read response body: %w", err)
	}

	headers := sanitizeHeaders(resp.Header)

	return RecordedResponse{
		StatusCode: resp.StatusCode,
		Headers:    headers,
		Body:       string(bodyBytes),
	}, nil
}

// sanitizeHeaders removes sensitive headers (like Authorization).
func sanitizeHeaders(headers http.Header) http.Header {
	sanitized := make(http.Header)
	for k, v := range headers {
		lower := strings.ToLower(k)
		switch lower {
		case "authorization", "api-key", "x-api-key":
			sanitized.Set(k, "[REDACTED]")
		default:
			sanitized[k] = v
		}
	}
	return sanitized
}

// matchRequest checks if a recorded request matches the given request.
// This is used during replay to find the right interaction.
func matchRequest(recorded RecordedRequest, req *http.Request) bool {
	if recorded.Method != req.Method {
		return false
	}

	// Parse URLs and compare without query params (API keys often in query)
	recordedURL, err1 := url.Parse(recorded.URL)
	reqURL, err2 := url.Parse(req.URL.String())
	if err1 != nil || err2 != nil {
		return false
	}

	// Compare scheme + host + path
	if recordedURL.Scheme != reqURL.Scheme || recordedURL.Host != reqURL.Host || recordedURL.Path != reqURL.Path {
		return false
	}

	return true
}
