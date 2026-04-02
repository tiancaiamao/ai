package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// MockTool is a controllable mock implementation of the Tool interface.
// It returns predefined results for specific tool calls.
type MockTool struct {
	name        string
	description string
	parameters  map[string]any
	handler     func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error)
	callCount   int
	lastArgs    map[string]any
}

// NewMockTool creates a new mock tool with the given name.
func NewMockTool(name string) *MockTool {
	return &MockTool{
		name:        name,
		description: fmt.Sprintf("Mock %s tool for testing", name),
		parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"input": map[string]any{
					"type":        "string",
					"description": "Input for the mock tool",
				},
			},
		},
		handler: func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
			return []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: fmt.Sprintf("mock %s result", name)},
			}, nil
		},
	}
}

// WithDescription sets the tool description.
func (m *MockTool) WithDescription(desc string) *MockTool {
	m.description = desc
	return m
}

// WithParameters sets the tool parameters schema.
func (m *MockTool) WithParameters(params map[string]any) *MockTool {
	m.parameters = params
	return m
}

// WithHandler sets a custom handler function for the tool.
func (m *MockTool) WithHandler(fn func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error)) *MockTool {
	m.handler = fn
	return m
}

// WithStaticResult sets the tool to always return a static text result.
func (m *MockTool) WithStaticResult(text string) *MockTool {
	m.handler = func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
		return []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: text},
		}, nil
	}
	return m
}

// WithJSONResult sets the tool to return a JSON-encoded result.
func (m *MockTool) WithJSONResult(v any) *MockTool {
	jsonBytes, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("failed to marshal JSON result: %v", err))
	}
	m.handler = func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
		return []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: string(jsonBytes)},
		}, nil
	}
	return m
}

// WithError sets the tool to always return an error.
func (m *MockTool) WithError(errMsg string) *MockTool {
	m.handler = func(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
		return nil, fmt.Errorf("%s", errMsg)
	}
	return m
}

// Name implements the Tool interface.
func (m *MockTool) Name() string { return m.name }

// Description implements the Tool interface.
func (m *MockTool) Description() string { return m.description }

// Parameters implements the Tool interface.
func (m *MockTool) Parameters() map[string]any { return m.parameters }

// Execute implements the Tool interface.
func (m *MockTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	m.callCount++
	m.lastArgs = args
	return m.handler(ctx, args)
}

// CallCount returns the number of times the tool was called.
func (m *MockTool) CallCount() int { return m.callCount }

// LastArgs returns the arguments from the most recent call.
func (m *MockTool) LastArgs() map[string]any { return m.lastArgs }

// WasCalled returns true if the tool was called at least once.
func (m *MockTool) WasCalled() bool { return m.callCount > 0 }

// MockToolRegistry manages a collection of mock tools for testing.
type MockToolRegistry struct {
	tools    map[string]*MockTool
	recorder *ToolJournal
	t        *testing.T
	mode     Mode
}

// NewMockToolRegistry creates a new mock tool registry.
func NewMockToolRegistry(t *testing.T) *MockToolRegistry {
	return &MockToolRegistry{
		tools: make(map[string]*MockTool),
		t:     t,
	}
}

// WithRecordMode sets the registry to record real tool calls.
func (r *MockToolRegistry) WithRecordMode(journal *ToolJournal) *MockToolRegistry {
	r.mode = ModeRecord
	r.recorder = journal
	return r
}

// WithReplayMode sets the registry to replay recorded tool calls.
func (r *MockToolRegistry) WithReplayMode(records map[string][]ToolCallRecord) *MockToolRegistry {
	r.mode = ModeReplay
	for id, recs := range records {
		for _, rec := range recs {
			tool := NewMockTool(rec.ToolName).WithStaticResult(rec.Result)
			r.tools[rec.ToolCallID] = tool
			_ = id
		}
	}
	return r
}

// Register adds a mock tool to the registry.
func (r *MockToolRegistry) Register(tool *MockTool) {
	r.tools[tool.name] = tool
}

// Get returns a mock tool by name.
func (r *MockToolRegistry) Get(name string) (*MockTool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// All returns all mock tools as agentctx.Tool slice.
func (r *MockToolRegistry) All() []agentctx.Tool {
	result := make([]agentctx.Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		result = append(result, tool)
	}
	return result
}

// SetupStandardTools creates the standard set of mock tools.
// Each tool returns a sensible default response.
func SetupStandardTools(t *testing.T) *MockToolRegistry {
	t.Helper()
	registry := NewMockToolRegistry(t)

	// bash tool - returns empty output
	registry.Register(NewMockTool("bash").WithStaticResult(""))

	// read tool - returns file content
	registry.Register(NewMockTool("read").WithStaticResult(""))

	// write tool - returns success
	registry.Register(NewMockTool("write").WithStaticResult("File written successfully"))

	// edit tool - returns success
	registry.Register(NewMockTool("edit").WithStaticResult("File edited successfully"))

	// grep tool - returns empty results
	registry.Register(NewMockTool("grep").WithStaticResult(""))

	// change_workspace tool - returns success
	registry.Register(NewMockTool("change_workspace").WithStaticResult("Workspace changed"))

	return registry
}
