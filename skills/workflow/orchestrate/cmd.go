package orchestrate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

// getCwd returns the current working directory
func getCwd() string {
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a team with a workflow",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd := getCwd()
		workflowPath, _ := cmd.Flags().GetString("workflow")
		workerCount, _ := cmd.Flags().GetInt("workers")
		maxRetries, _ := cmd.Flags().GetInt("max-retries")
		name, _ := cmd.Flags().GetString("name")
		timeout, _ := cmd.Flags().GetDuration("timeout")
		heartbeatTTL, _ := cmd.Flags().GetDuration("heartbeat-ttl")
		noTmux, _ := cmd.Flags().GetBool("no-tmux")

		if workflowPath == "" {
			return fmt.Errorf("--workflow is required")
		}

		runtime := NewRuntime(cwd)

		// Load workflow
		var workflow *Workflow
		var err error

		// Try as template name first, then as path
		workflow, err = LoadWorkflowFromTemplate(workflowPath)
		if err != nil {
			workflow, err = LoadWorkflow(workflowPath)
			if err != nil {
				return fmt.Errorf("failed to load workflow: %w", err)
			}
		}

		// Default config
		if name == "" {
			name = filepath.Base(cwd) + "-team"
		}
		if workerCount == 0 {
			workerCount = 3
		}
		if maxRetries == 0 {
			maxRetries = 3
		}

		config := &TeamConfig{
			Name:        name,
			Workflow:    workflowPath,
			WorkerCount: workerCount,
			MaxRetries:  maxRetries,
		}

		// Runtime config
		rc := DefaultRuntimeConfig()
		if timeout > 0 {
			rc.TaskTimeout = timeout
		}
		if heartbeatTTL > 0 {
			rc.HeartbeatTTL = heartbeatTTL
		}
		if noTmux {
			rc.UseTmux = false
		}

		if err := runtime.StartWithConfig(config, workflow, rc); err != nil {
			return fmt.Errorf("failed to start team: %w", err)
		}

		fmt.Printf("Team '%s' started with workflow '%s'\n", name, workflowPath)
		fmt.Printf("Workers: %d, Max retries: %d, Timeout: %v\n", workerCount, maxRetries, rc.TaskTimeout)
		if rc.UseTmux && IsTmuxAvailable() {
			fmt.Printf("Tmux session: %s\n", name)
		}
		fmt.Println("Use 'orchestrate status' to check progress")
		fmt.Println("Use 'orchestrate logs' to view logs")
		fmt.Println("Use 'orchestrate stop' to stop")

		// Block until stopped
		runtime.Wait()
		return nil
	},
}

func init() {
	startCmd.Flags().String("workflow", "", "Workflow file or template name")
	startCmd.Flags().Int("workers", 3, "Number of workers")
	startCmd.Flags().Int("max-retries", 3, "Maximum retries per task")
	startCmd.Flags().String("name", "", "Team name (default: <dir>-team)")
	startCmd.Flags().Duration("timeout", 30*time.Minute, "Task timeout (e.g., 30m, 1h)")
	startCmd.Flags().Duration("heartbeat-ttl", 5*time.Minute, "Worker heartbeat TTL")
	startCmd.Flags().Bool("no-tmux", false, "Disable tmux integration")
}

// statusCmd represents the status command
var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show team status",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd := getCwd()
		runtime := NewRuntime(cwd)

		state, tasks, err := runtime.LoadState()
		if err != nil {
			return fmt.Errorf("failed to load state: %w", err)
		}

		jsonFlag, _ := cmd.Flags().GetBool("json")
		if jsonFlag {
			output := map[string]interface{}{
				"state": state,
				"tasks": tasks,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(output)
		}

		// Human-readable output
		fmt.Printf("Phase: %s\n", state.Phase)
		fmt.Printf("Active workers: %d\n", state.ActiveCount)
		fmt.Printf("Last updated: %s\n", state.UpdatedAt)
		fmt.Println("\nTasks:")
		for _, task := range tasks {
			statusIcon := "⏳"
			if task.Status == "completed" {
				statusIcon = "✅"
			} else if task.Status == "failed" {
				statusIcon = "❌"
			} else if task.Status == "claimed" {
				statusIcon = "🔄"
			} else if task.Status == "blocked" {
				statusIcon = "🔒"
			}

			fmt.Printf("  %s [%s] %s", statusIcon, task.ID, task.Subject)
			if task.ClaimedBy != "" {
				fmt.Printf(" (Worker: %s)", task.ClaimedBy)
			}
			fmt.Println()
		}

		return nil
	},
}

func init() {
	statusCmd.Flags().Bool("json", false, "Output JSON")
}

// stopCmd represents the stop command
var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the team",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd := getCwd()
		storage := NewStorage(cwd)
		if err := storage.Init(); err != nil {
			return fmt.Errorf("failed to initialize storage: %w", err)
		}
		if err := storage.RequestStop(); err != nil {
			return fmt.Errorf("failed to request stop: %w", err)
		}

		fmt.Println("Stop requested")
		return nil
	},
}

// logsCmd represents the logs command
var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show team logs",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd := getCwd()
		runtime := NewRuntime(cwd)
		taskFilter, _ := cmd.Flags().GetString("task")

		logs, err := runtime.GetLogs()
		if err != nil {
			return fmt.Errorf("failed to get logs: %w", err)
		}

		for _, log := range logs {
			if taskFilter != "" && log.TaskID != taskFilter {
				continue
			}
			fmt.Printf("[%s] %s: %s\n", log.Timestamp, log.TaskID, log.Message)
		}

		return nil
	},
}

func init() {
	logsCmd.Flags().String("task", "", "Filter by task ID")
}

// approveCmd represents the approve command
var approveCmd = &cobra.Command{
	Use:   "approve <task-id>",
	Short: "Approve a task",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		taskID := args[0]
		cwd := getCwd()
		runtime := NewRuntime(cwd)
		comment, _ := cmd.Flags().GetString("comment")

		if err := runtime.ApproveTask(taskID, comment); err != nil {
			return fmt.Errorf("failed to approve task: %w", err)
		}

		fmt.Printf("Task '%s' approved\n", taskID)
		return nil
	},
}

func init() {
	approveCmd.Flags().String("comment", "Approved", "Approval comment")
}

// templatesCmd represents the templates command
var templatesCmd = &cobra.Command{
	Use:   "templates",
	Short: "List available workflow templates",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		jsonFlag, _ := cmd.Flags().GetBool("json")

		// Get templates from multiple sources
		templates := ListTemplates()

		if jsonFlag {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(templates)
		}

		// Human-readable output
		fmt.Println("Available templates:")
		for _, tmpl := range templates {
			fmt.Printf("  - %s: %s\n", tmpl.Name, tmpl.Description)
		}

		return nil
	},
}

func init() {
	templatesCmd.Flags().Bool("json", false, "Output JSON")
}
