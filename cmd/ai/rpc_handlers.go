package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tiancaiamao/ai/pkg/rpc"
)

func runRPC(sessionPath string, debugAddr string, input io.Reader, output io.Writer, customSystemPrompt string, maxTurns int, timeout time.Duration) error {
	core, err := NewRPCCore(rpcCoreConfig{
		SessionPath:        sessionPath,
		DebugAddr:          debugAddr,
		Input:              input,
		Output:             output,
		CustomSystemPrompt: customSystemPrompt,
		MaxTurns:           maxTurns,
		Timeout:            timeout,
	})
	if err != nil {
		return err
	}

	core.Server.Register(rpc.CommandPrompt, func(cmd rpc.RPCCommand) (any, error) {
		return core.handlePrompt(cmd)
	})

	core.Server.Register(rpc.CommandSteer, func(cmd rpc.RPCCommand) (any, error) {
		return core.handleSteer(cmd)
	})

	core.Server.Register(rpc.CommandFollowUp, func(cmd rpc.RPCCommand) (any, error) {
		return core.handleFollowUp(cmd)
	})

	core.Server.Register(rpc.CommandAbort, func(cmd rpc.RPCCommand) (any, error) {
		return core.handleAbort(cmd)
	})

	// Register all slash command handlers
	core.RegisterSlashCommands()

	return core.Run()
}

func newBashRunner() *bashRunner {
	return &bashRunner{}
}

type bashRunner struct {
	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
}

func (b *bashRunner) Run(cwd, command string, timeout time.Duration) (*rpc.BashResult, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("command is required")
	}

	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return nil, fmt.Errorf("bash already running")
	}

	ctx := context.Background()
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		ctx, cancel = context.WithCancel(ctx)
	}
	b.running = true
	b.cancel = cancel
	b.mu.Unlock()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	ctxErr := ctx.Err()

	b.mu.Lock()
	b.running = false
	b.cancel = nil
	b.mu.Unlock()
	cancel()

	result := &rpc.BashResult{
		Output: string(output),
	}
	if ctxErr == context.DeadlineExceeded {
		result.ExitCode = -1
		result.Error = "command timed out"
		return result, nil
	}
	if ctxErr == context.Canceled {
		result.ExitCode = -1
		result.Error = "command cancelled"
		return result, nil
	}
	if err != nil {
		result.Error = err.Error()
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = -1
		}
		return result, nil
	}
	result.ExitCode = 0
	return result, nil
}

func (b *bashRunner) Abort() error {
	b.mu.Lock()
	cancel := b.cancel
	running := b.running
	b.mu.Unlock()
	if !running || cancel == nil {
		return fmt.Errorf("no bash command running")
	}
	cancel()
	return nil
}

func getWorkflowStatus(cwd string) (*rpc.WorkflowState, error) {
	state := &rpc.WorkflowState{
		Phase:      "not_started",
		LastUpdate: time.Now().UTC().Format(time.RFC3339),
	}

	workflowDir := filepath.Join(cwd, ".workflow")
	stateFile := filepath.Join(workflowDir, "state.json")

	// Read state.json if it exists
	if data, err := os.ReadFile(stateFile); err == nil {
		var stateData struct {
			Phase     string `json:"phase"`
			StartedAt string `json:"started_at"`
			TasksFile string `json:"tasks_file"`
		}
		if err := json.Unmarshal(data, &stateData); err == nil {
			state.Phase = stateData.Phase
			state.StartedAt = stateData.StartedAt
			if stateData.TasksFile != "" {
				// Handle relative or absolute path
				if filepath.IsAbs(stateData.TasksFile) {
					state.TasksFile = stateData.TasksFile
				} else {
					state.TasksFile = filepath.Join(cwd, stateData.TasksFile)
				}
			}
		}
	}

	// Read tasks.md if specified
	if state.TasksFile != "" {
		tasksData, err := os.ReadFile(state.TasksFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read tasks file %s: %w", state.TasksFile, err)
		}

		// Parse task statuses
		lines := strings.Split(string(tasksData), "\n")
		var inProgressTask *rpc.WorkflowTask

		for _, line := range lines {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "- [") {
				continue
			}

			status := "pending"
			if strings.HasPrefix(line, "- [x]") || strings.HasPrefix(line, "- [X]") {
				status = "done"
				state.DoneTasks++
			} else if strings.HasPrefix(line, "- [-]") {
				status = "in_progress"
				state.PendingTasks++ // In-progress also counts toward pending
			} else if strings.HasPrefix(line, "- [!]") {
				status = "failed"
				state.FailedTasks++
			} else {
				state.PendingTasks++
			}

			state.TotalTasks++

			// Extract task ID and description for in-progress task
			if status == "in_progress" && inProgressTask == nil {
				// Extract task ID (e.g., TASK001, T01, etc.)
				var id string
				idMatch := regexp.MustCompile(`[A-Z]{3,}\d+|[A-Z]\d+`).FindString(line)
				if idMatch != "" {
					id = idMatch
				}

				// Extract description: remove checkbox first, then task ID
				desc := line
				// Remove checkbox
				desc = regexp.MustCompile(`^-\s*\[[xX\-\!]\]\s*`).ReplaceAllString(desc, "")
				desc = regexp.MustCompile(`^-\s*\[\s*\]\s*`).ReplaceAllString(desc, "")
				// Remove task ID (e.g., TASK002: or TASK002 )
				desc = regexp.MustCompile(`^[A-Z]{3,}\d+:?\s*`).ReplaceAllString(desc, "")
				desc = strings.TrimSpace(desc)

				inProgressTask = &rpc.WorkflowTask{
					ID:          id,
					Description: desc,
					Status:      status,
				}
			}
		}

		state.InProgressTask = inProgressTask
	}

	return state, nil
}
