package testutil

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// httpClient wraps http.Client with VCR recording/replaying capabilities.
type httpClient struct {
	vcr *VCR
	mu  sync.Mutex
}

// Do performs an HTTP request, either recording or replaying the interaction.
func (c *httpClient) Do(req *http.Request) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch c.vcr.mode {
	case ModeRecord:
		return c.doRecord(req)
	case ModeReplay:
		return c.doReplay(req)
	default:
		return nil, fmt.Errorf("VCR: unknown mode %v", c.vcr.mode)
	}
}

// Get performs a GET request.
func (c *httpClient) Get(url string) (*http.Response, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// Post performs a POST request.
func (c *httpClient) Post(url, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	return c.Do(req)
}

// doRecord makes a real HTTP request and records the interaction.
func (c *httpClient) doRecord(req *http.Request) (*http.Response, error) {
	// Record the request
	recordedReq, err := recordRequest(req)
	if err != nil {
		return nil, fmt.Errorf("VCR record: failed to capture request: %w", err)
	}

	// Make the real HTTP call
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	// Record the response
	recordedResp, err := recordResponse(resp)
	if err != nil {
		return nil, fmt.Errorf("VCR record: failed to capture response: %w", err)
	}

	// Save the interaction
	c.vcr.addInteraction(Interaction{
		Request:  recordedReq,
		Response: recordedResp,
	})

	// Return a new response with the same data (since we consumed the body)
	return &http.Response{
		StatusCode: recordedResp.StatusCode,
		Header:     recordedResp.Headers,
		Body:       io.NopCloser(strings.NewReader(recordedResp.Body)),
	}, nil
}

// doReplay returns a saved response without making a real HTTP call.
func (c *httpClient) doReplay(req *http.Request) (*http.Response, error) {
	interaction := c.vcr.nextInteraction()

	// Verify request matches (best-effort)
	if interaction.Request.Method != req.Method {
		c.vcr.t.Logf("VCR warning: method mismatch (expected %s, got %s)",
			interaction.Request.Method, req.Method)
	}

	// Return the recorded response
	resp := &http.Response{
		StatusCode: interaction.Response.StatusCode,
		Header:     interaction.Response.Headers,
		Body:       io.NopCloser(bytes.NewBufferString(interaction.Response.Body)),
	}

	return resp, nil
}
