package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// RunSubagentFunc is the function type for running a subagent in headless mode.
// It's injected by the main application to avoid circular dependencies.
type RunSubagentFunc func(ctx context.Context, task string, allowedTools []string, maxTurns int) (string, error)

// SubagentTool spawns a subagent to handle delegated tasks.
type SubagentTool struct {
	cwd         string
	parentCtx   context.Context
	registry    *Registry
	runSubagent RunSubagentFunc
}

// SubagentConfig holds configuration for subagent execution.
type SubagentConfig struct {
	// Tools restricts which tools the subagent can use (nil = all)
	Tools []string `json:"tools,omitempty"`
	// MaxTurns limits the conversation turns (default: 10)
	MaxTurns int `json:"max_turns,omitempty"`
	// Timeout limits execution time in seconds (default: 120)
	Timeout int `json:"timeout,omitempty"`
}

// TaskResult holds the result of a single task execution.
type TaskResult struct {
	Task    string `json:"task"`
	Success bool   `json:"success"`
	Text    string `json:"text,omitempty"`
	Error   string `json:"error,omitempty"`
}

// NewSubagentTool creates a new Subagent tool.
func NewSubagentTool(cwd string, parentCtx context.Context, registry *Registry, runSubagent RunSubagentFunc) *SubagentTool {
	return &SubagentTool{
		cwd:         cwd,
		parentCtx:   parentCtx,
		registry:    registry,
		runSubagent: runSubagent,
	}
}

// Name returns the tool name.
func (t *SubagentTool) Name() string {
	return "subagent"
}

// Description returns the tool description.
func (t *SubagentTool) Description() string {
	return `Spawn a subagent to handle delegated tasks. The subagent runs with isolated context and restricted tools.

Use for:
- Parallel execution of independent tasks
- Tasks requiring focused attention  
- Breaking down complex problems into sub-tasks

The subagent cannot use the subagent tool (no nesting).`
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *SubagentTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "Single task for the subagent to complete",
			},
			"tasks": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Multiple tasks to execute in parallel (mutually exclusive with 'task')",
			},
			"config": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tools": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Whitelist of tools the subagent can use",
					},
					"max_turns": map[string]any{
						"type":        "integer",
						"description": "Maximum conversation turns (default: 10)",
						"minimum":     1,
						"maximum":     25,
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Timeout in seconds (default: 120)",
						"minimum":     10,
						"maximum":     600,
					},
				},
			},
		},
	}
}

// Execute runs the subagent tool.
func (t *SubagentTool) Execute(ctx context.Context, args map[string]any) ([]agent.ContentBlock, error) {
	// Parse arguments
	task, hasTask := args["task"].(string)
	tasksRaw, hasTasks := args["tasks"].([]any)

	// Validate: must have either task or tasks, not both
	if !hasTask && !hasTasks {
		return nil, fmt.Errorf("either 'task' or 'tasks' must be provided")
	}
	if hasTask && hasTasks {
		return nil, fmt.Errorf("'task' and 'tasks' are mutually exclusive")
	}

	// Parse config
	config := SubagentConfig{
		MaxTurns: 10,
		Timeout:  120,
	}
	if configRaw, ok := args["config"].(map[string]any); ok {
		if tools, ok := configRaw["tools"].([]any); ok {
			config.Tools = make([]string, 0, len(tools))
			for _, tool := range tools {
				if name, ok := tool.(string); ok {
					config.Tools = append(config.Tools, name)
				}
			}
		}
		if maxTurns, ok := configRaw["max_turns"].(float64); ok {
			config.MaxTurns = int(maxTurns)
		}
		if timeout, ok := configRaw["timeout"].(float64); ok {
			config.Timeout = int(timeout)
		}
	}

	// Execute
	var results []TaskResult
	if hasTask {
		// Single task
		result := t.executeSingleTask(ctx, task, config)
		results = []TaskResult{result}
	} else {
		// Multiple tasks in parallel
		tasks := make([]string, 0, len(tasksRaw))
		for _, t := range tasksRaw {
			if s, ok := t.(string); ok {
				tasks = append(tasks, s)
			}
		}
		results = t.executeParallelTasks(ctx, tasks, config)
	}

	// Format output
	return t.formatResults(results), nil
}

// executeSingleTask executes a single task and returns the result.
func (t *SubagentTool) executeSingleTask(ctx context.Context, task string, config SubagentConfig) TaskResult {
	// Create timeout context
	timeout := time.Duration(config.Timeout) * time.Second
	childCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Execute in headless mode
	text, err := t.runSubagentHeadless(childCtx, task, config)

	if err != nil {
		return TaskResult{
			Task:    task,
			Success: false,
			Error:   err.Error(),
		}
	}
	return TaskResult{
		Task:    task,
		Success: true,
		Text:    text,
	}
}

// executeParallelTasks executes multiple tasks in parallel.
func (t *SubagentTool) executeParallelTasks(ctx context.Context, tasks []string, config SubagentConfig) []TaskResult {
	results := make([]TaskResult, len(tasks))
	var wg sync.WaitGroup

	// Semaphore for concurrency limit (max 3 concurrent)
	sem := make(chan struct{}, 3)

	for i, task := range tasks {
		wg.Add(1)
		go func(idx int, task string) {
			defer wg.Done()

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }()

			// Execute with parent context (cancellation propagates)
			results[idx] = t.executeSingleTask(ctx, task, config)
		}(i, task)
	}

	wg.Wait()
	return results
}

// formatResults formats the results into content blocks.
func (t *SubagentTool) formatResults(results []TaskResult) []agent.ContentBlock {
	if len(results) == 0 {
		return []agent.ContentBlock{
			agent.TextContent{Type: "text", Text: "No tasks executed"},
		}
	}

	if len(results) == 1 {
		// Single task: simple output
		r := results[0]
		if r.Success {
			return []agent.ContentBlock{
				agent.TextContent{Type: "text", Text: r.Text},
			}
		}
		return []agent.ContentBlock{
			agent.TextContent{Type: "text", Text: fmt.Sprintf("Task failed: %s", r.Error)},
		}
	}

	// Multiple tasks: structured output
	output := "## Subagent Results\n\n"
	for i, r := range results {
		output += fmt.Sprintf("### Task %d: %s\n", i+1, r.Task)
		if r.Success {
			output += fmt.Sprintf("**Status**: ✓ Success\n\n%s\n\n", r.Text)
		} else {
			output += fmt.Sprintf("**Status**: ✗ Failed\n\n**Error**: %s\n\n", r.Error)
		}
	}
	return []agent.ContentBlock{
		agent.TextContent{Type: "text", Text: output},
	}
}

// runSubagentHeadless executes a subagent in headless mode.
func (t *SubagentTool) runSubagentHeadless(ctx context.Context, task string, config SubagentConfig) (string, error) {
	if t.runSubagent == nil {
		return "", fmt.Errorf("subagent execution not configured (runSubagent func is nil)")
	}

	// Call the injected function
	return t.runSubagent(ctx, task, config.Tools, config.MaxTurns)
}
