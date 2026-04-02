package testutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// ============================================================================
// RoundTripper-based VCR Transport
// ============================================================================

// VCRRoundTripper is an http.RoundTripper that records or replays HTTP interactions.
// Install it on the default http.Client or pass it to any HTTP client.
//
// This is the core mechanism for intercepting LLM API calls.
// It wraps a real transport (for recording) or returns canned responses (for replay).
type VCRRoundTripper struct {
	mu          sync.Mutex
	vcr         *VCR
	realTransport http.RoundTripper
}

// NewVCRRoundTripper creates a new VCR RoundTripper.
func NewVCRRoundTripper(vcr *VCR) *VCRRoundTripper {
	return &VCRRoundTripper{
		vcr:           vcr,
		realTransport: http.DefaultTransport,
	}
}

// RoundTrip implements http.RoundTripper.
func (rt *VCRRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	switch rt.vcr.mode {
	case ModeRecord:
		return rt.roundTripRecord(req)
	case ModeReplay:
		return rt.roundTripReplay(req)
	default:
		return nil, fmt.Errorf("VCR: unknown mode")
	}
}

func (rt *VCRRoundTripper) roundTripRecord(req *http.Request) (*http.Response, error) {
	// Capture request body
	var reqBody string
	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("VCR record: failed to read request body: %w", err)
		}
		reqBody = string(bodyBytes)
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	// Sanitize request headers
	reqHeaders := sanitizeHeaders(req.Header)

	// Make the real request
	resp, err := rt.realTransport.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Capture response body
	var respBody string
	if resp.Body != nil {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("VCR record: failed to read response body: %w", err)
		}
		respBody = string(bodyBytes)
		resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	// Record the interaction
	rt.vcr.addInteraction(Interaction{
		Request: RecordedRequest{
			Method:  req.Method,
			URL:     req.URL.String(),
			Headers: reqHeaders,
			Body:    reqBody,
		},
		Response: RecordedResponse{
			StatusCode: resp.StatusCode,
			Headers:    resp.Header.Clone(),
			Body:       respBody,
		},
	})

	return resp, nil
}

func (rt *VCRRoundTripper) roundTripReplay(req *http.Request) (*http.Response, error) {
	interaction := rt.vcr.nextInteraction()

	// Build response from recorded data
	resp := &http.Response{
		StatusCode: interaction.Response.StatusCode,
		Header:     interaction.Response.Headers,
		Body:       io.NopCloser(bytes.NewBufferString(interaction.Response.Body)),
		// Set minimal required fields
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
	}

	return resp, nil
}

// ============================================================================
// High-level test helper
// ============================================================================

// ScenarioTestEnv is the main entry point for scenario-based agent tests.
//
// It manages:
//   - HTTP transport replacement (via VCRRoundTripper) for LLM call interception
//   - Mock tool registry for deterministic tool results
//   - Temporary session storage
//   - Assertion helpers
//
// # Usage
//
//	func TestMyScenario(t *testing.T) {
//	    env := testutil.NewScenarioTestEnv(t, "basic_query")
//	    env.ReplayOrSkip()
//	    defer env.Close()
//
//	    agent := env.CreateAgent()
//	    err := agent.ExecuteTurn(ctx, "hello")
//	    require.NoError(t, err)
//
//	    env.Assert().LastAssistantContains("hello")
//	}
type ScenarioTestEnv struct {
	t       *testing.T
	vcr     *VCR
	tempDir string

	// Session management
	sessionID  string
	sessionDir string

	// Model config (for the agent)
	model  *llm.Model
	apiKey string

	// Mock tools
	mockTools *MockToolRegistry

	// Transport management
	originalTransport http.RoundTripper
	rt                *VCRRoundTripper
}

// NewScenarioTestEnv creates a new scenario test environment.
//   - cassetteName: name for the VCR cassette (e.g., "basic_query", "multi_turn")
//
// Cassettes are stored in pkg/testutil/testdata/<cassetteName>.yaml
func NewScenarioTestEnv(t *testing.T, cassetteName string) *ScenarioTestEnv {
	t.Helper()

	tempDir := t.TempDir()
	sessionID := fmt.Sprintf("test-%d", time.Now().UnixNano())
	sessionDir := tempDir + "/sessions/" + sessionID

	cassetteDir := "testdata"

	vcr := NewVCR(t, cassetteDir, cassetteName)

	model := &llm.Model{
		ID:            "test-model",
		Provider:      "test",
		BaseURL:       "http://mock-api",
		API:           "openai-completions",
		ContextWindow: 200000,
	}

	env := &ScenarioTestEnv{
		t:         t,
		vcr:       vcr,
		tempDir:   tempDir,
		sessionID: sessionID,
		sessionDir: sessionDir,
		model:     model,
		apiKey:    "test-key",
		mockTools: SetupStandardTools(t),
	}

	return env
}

// Record sets the environment to record mode.
// The agent will make real LLM calls (requires API key).
// All HTTP interactions are saved to the cassette file.
func (e *ScenarioTestEnv) Record(apiKey string, model llm.Model) *ScenarioTestEnv {
	e.t.Helper()
	e.apiKey = apiKey
	e.model = &model

	e.vcr.Record()

	// Install VCR transport that records
	e.rt = NewVCRRoundTripper(e.vcr)
	e.originalTransport = http.DefaultTransport
	http.DefaultTransport = e.rt

	e.t.Cleanup(func() {
		http.DefaultTransport = e.originalTransport
		e.vcr.Cleanup()
	})

	return e
}

// Replay sets the environment to replay mode.
// The agent will use saved LLM responses.
func (e *ScenarioTestEnv) Replay() *ScenarioTestEnv {
	e.t.Helper()
	e.vcr.Replay()

	e.rt = NewVCRRoundTripper(e.vcr)
	e.originalTransport = http.DefaultTransport
	http.DefaultTransport = e.rt

	e.t.Cleanup(func() {
		http.DefaultTransport = e.originalTransport
	})

	return e
}

// ReplayOrSkip sets replay mode, skipping if cassette doesn't exist.
func (e *ScenarioTestEnv) ReplayOrSkip() *ScenarioTestEnv {
	e.t.Helper()
	e.vcr.ReplayOrSkip()

	e.rt = NewVCRRoundTripper(e.vcr)
	e.originalTransport = http.DefaultTransport
	http.DefaultTransport = e.rt

	e.t.Cleanup(func() {
		http.DefaultTransport = e.originalTransport
	})

	return e
}

// WithMockTools replaces the standard mock tools with custom ones.
func (e *ScenarioTestEnv) WithMockTools(fn func(registry *MockToolRegistry)) *ScenarioTestEnv {
	fn(e.mockTools)
	return e
}

// Close cleans up the test environment.
func (e *ScenarioTestEnv) Close() {
	// Cleanup handled via t.Cleanup in Record/Replay
}

// Assert returns assertion helpers.
func (e *ScenarioTestEnv) Assert() *SnapshotHelper {
	return &SnapshotHelper{t: e.t}
}

// Model returns the model configuration.
func (e *ScenarioTestEnv) Model() *llm.Model {
	return e.model
}

// APIKey returns the API key.
func (e *ScenarioTestEnv) APIKey() string {
	return e.apiKey
}

// SessionDir returns the session directory path.
func (e *ScenarioTestEnv) SessionDir() string {
	return e.sessionDir
}

// SessionID returns the session ID.
func (e *ScenarioTestEnv) SessionID() string {
	return e.sessionID
}

// TempDir returns the temporary directory.
func (e *ScenarioTestEnv) TempDir() string {
	return e.tempDir
}

// MockTools returns the mock tool registry for customization.
func (e *ScenarioTestEnv) MockTools() *MockToolRegistry {
	return e.mockTools
}

// VCR returns the underlying VCR for advanced usage.
func (e *ScenarioTestEnv) VCR() *VCR {
	return e.vcr
}

// NewContext creates a context suitable for testing.
func NewContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}

// ============================================================================
// Scenario test functions
// ============================================================================

// ScenarioTest defines a single test scenario.
type ScenarioTest struct {
	Name        string
	Description string
	// UserMessages is the sequence of user messages to send.
	// If the LLM responds with tool calls, those are handled automatically.
	UserMessages []string
	// Validate is called after all messages are processed.
	Validate func(t *testing.T, snapshot *agentctx.ContextSnapshot)
}

// RunScenarioTests runs a table of scenario tests.
// Each scenario gets its own cassette file named after the test.
//
// Usage:
//
//	func TestScenarios(t *testing.T) {
//	    testutil.RunScenarioTests(t, []testutil.ScenarioTest{
//	        {
//	            Name: "simple_greeting",
//	            UserMessages: []string{"Say hello"},
//	            Validate: func(t *testing.T, snap *agentctx.ContextSnapshot) {
//	                // assertions...
//	            },
//	        },
//	    })
//	}
func RunScenarioTests(t *testing.T, scenarios []ScenarioTest) {
	for _, sc := range scenarios {
		t.Run(sc.Name, func(t *testing.T) {
			env := NewScenarioTestEnv(t, sc.Name)
			env.ReplayOrSkip()
			defer env.Close()

			ctx, cancel := NewContext()
			defer cancel()

			// Note: To actually run the agent, we need to create it.
			// This requires the agent constructor to accept mock tools.
			// For now, this is a placeholder that demonstrates the intended API.
			t.Skip("Scenario runner requires agent integration (see CreateMockAgent)")
			_ = ctx
			_ = sc
		})
	}
}
