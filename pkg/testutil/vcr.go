// Package testutil provides recording and replaying infrastructure for tests.
//
// # Design
//
// The VCR (Video Cassette Recorder) pattern allows tests to:
//   - Record: Make real LLM API calls and save HTTP interactions to YAML cassette files
//   - Replay: Load saved interactions and replay them without hitting real APIs
//
// This enables deterministic, fast, offline tests that don't require API keys.
//
// # Cassette Format
//
// Each cassette is a YAML file containing a sequence of HTTP interactions:
//
//	interactions:
//	  - request:
//	      method: POST
//	      url: https://api.openai.com/v1/chat/completions
//	      headers:
//	        Content-Type: ["application/json"]
//	      body: '{"model":"gpt-4","messages":[...]}'
//	    response:
//	      status_code: 200
//	      headers:
//	        Content-Type: ["text/event-stream"]
//	      body: "data: {\"choices\":[...]}\n\ndata: [DONE]\n\n"
//
// # Usage
//
//	// In your test:
//	vcr := testutil.NewVCR(t, "testdata/TestMyFeature")
//	vcr.Record()  // or vcr.Replay()
//
//	client := vcr.HTTPClient()
//	// Use client as if it were a real HTTP client
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Mode represents the VCR operation mode.
type Mode int

const (
	// ModeRecord makes real HTTP calls and saves interactions.
	ModeRecord Mode = iota
	// ModeReplay loads saved interactions and replays them without network.
	ModeReplay
)

func (m Mode) String() string {
	switch m {
	case ModeRecord:
		return "record"
	case ModeReplay:
		return "replay"
	default:
		return "unknown"
	}
}

// VCR manages HTTP interaction recording and replaying.
type VCR struct {
	t          *testing.T
	cassetteDir  string
	cassetteName string
	mode        Mode
	interactions []Interaction
	current     int
}

// NewVCR creates a new VCR instance.
//   - cassetteDir: directory to store cassette files (e.g., "testdata/TestMyFeature")
//   - cassetteName: name for the cassette file (e.g., "simple_query")
//
// The cassette file will be stored at <cassetteDir>/<cassetteName>.yaml
func NewVCR(t *testing.T, cassetteDir, cassetteName string) *VCR {
	return &VCR{
		t:            t,
		cassetteDir:  cassetteDir,
		cassetteName: cassetteName,
	}
}

// Record sets the VCR to record mode.
// Real HTTP calls will be made and the responses saved.
// Requires ZAI_API_KEY (or appropriate API key) to be set.
func (v *VCR) Record() *VCR {
	v.t.Helper()
	v.mode = ModeRecord
	v.interactions = nil
	v.current = 0
	return v
}

// Replay sets the VCR to replay mode.
// Saved interactions will be replayed without making real HTTP calls.
// If no cassette file exists, the test fails with a helpful message.
func (v *VCR) Replay() *VCR {
	v.t.Helper()
	v.mode = ModeReplay

	cassettePath := v.cassettePath()
	data, err := os.ReadFile(cassettePath)
	if err != nil {
		v.t.Fatalf("VCR cassette not found at %s. Run with -record flag to create it: %v", cassettePath, err)
	}

	cassette, err := LoadCassette(data)
	if err != nil {
		v.t.Fatalf("Failed to load cassette %s: %v", cassettePath, err)
	}

	v.interactions = cassette.Interactions
	v.current = 0
	v.t.Logf("VCR: Loaded %d interactions from %s", len(v.interactions), cassettePath)
	return v
}

// ReplayOrSkip sets the VCR to replay mode, but skips the test
// if the cassette file doesn't exist (useful for CI).
func (v *VCR) ReplayOrSkip() *VCR {
	v.t.Helper()
	v.mode = ModeReplay

	cassettePath := v.cassettePath()
	data, err := os.ReadFile(cassettePath)
	if err != nil {
		v.t.Skipf("VCR cassette not found at %s (skipping in CI)", cassettePath)
	}

	cassette, err := LoadCassette(data)
	if err != nil {
		v.t.Fatalf("Failed to load cassette %s: %v", cassettePath, err)
	}

	v.interactions = cassette.Interactions
	v.current = 0
	v.t.Logf("VCR: Loaded %d interactions from %s", len(v.interactions), cassettePath)
	return v
}

// HTTPClient returns an *http.Client that either records or replays HTTP interactions.
//
// In record mode: uses a real http.Client with a transport that records responses.
// In replay mode: uses a mock transport that returns saved responses.
func (v *VCR) HTTPClient() *httpClient {
	return &httpClient{
		vcr: v,
	}
}

// cassettePath returns the full path to the cassette file.
func (v *VCR) cassettePath() string {
	return filepath.Join(v.cassetteDir, v.cassetteName+".yaml")
}

// save saves recorded interactions to the cassette file.
func (v *VCR) save() error {
	if v.mode != ModeRecord {
		return nil
	}

	cassette := &Cassette{
		Version:      CassetteVersion,
		Interactions: v.interactions,
	}

	data, err := MarshalCassette(cassette)
	if err != nil {
		return fmt.Errorf("failed to marshal cassette: %w", err)
	}

	// Ensure directory exists
	if err := os.MkdirAll(v.cassetteDir, 0755); err != nil {
		return fmt.Errorf("failed to create cassette directory: %w", err)
	}

	if err := os.WriteFile(v.cassettePath(), data, 0644); err != nil {
		return fmt.Errorf("failed to write cassette: %w", err)
	}

	v.t.Logf("VCR: Saved %d interactions to %s", len(v.interactions), v.cassettePath())
	return nil
}

// nextInteraction returns the next interaction for replay.
func (v *VCR) nextInteraction() *Interaction {
	if v.current >= len(v.interactions) {
		v.t.Fatalf("VCR: No more interactions to replay (used %d, have %d). "+
			"The test is making more HTTP requests than were recorded.",
			v.current, len(v.interactions))
	}
	i := v.interactions[v.current]
	v.current++
	return &i
}

// addInteraction records a new interaction.
func (v *VCR) addInteraction(interaction Interaction) {
	v.interactions = append(v.interactions, interaction)
}

// Cleanup should be called at the end of the test (via t.Cleanup).
func (v *VCR) Cleanup() {
	if v.mode == ModeRecord {
		if err := v.save(); err != nil {
			v.t.Errorf("VCR: Failed to save cassette: %v", err)
		}
	}
}

// Mode returns the current VCR mode.
func (v *VCR) Mode() Mode {
	return v.mode
}

// InteractionCount returns the number of recorded/loaded interactions.
func (v *VCR) InteractionCount() int {
	return len(v.interactions)
}
