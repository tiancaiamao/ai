package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	team "orchestrate"
	"github.com/spf13/cobra"
)

var (
	cwd      string
	jsonFlag bool
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "orchestrate",
	Short: "Team orchestration CLI for multi-agent workflows",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cwd, "cwd", "", "Working directory (default: current directory)")
	rootCmd.PersistentFlags().BoolVar(&jsonFlag, "json", false, "Output as JSON")
}

// getCwd returns working directory
func getCwd() string {
	if cwd != "" {
		return cwd
	}
	wd, _ := os.Getwd()
	return wd
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start a team with a workflow",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
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

		wd := getCwd()
		runtime := team.NewRuntime(wd)

		// Load workflow
		var workflow *team.Workflow
		var err error
		
		// Try as template name first, then as path
		workflow, err = team.LoadWorkflowFromTemplate(workflowPath)
		if err != nil {
			workflow, err = team.LoadWorkflow(workflowPath)
			if err != nil {
				return fmt.Errorf("failed to load workflow: %w", err)
			}
		}

		// Default config
		if name == "" {
			name = filepath.Base(wd) + "-team"
		}
		if workerCount == 0 {
			workerCount = 3
		}
		if maxRetries == 0 {
			maxRetries = 3
		}

		config := &team.TeamConfig{
			Name:        name,
			Workflow:    workflowPath,
			WorkerCount: workerCount,
			MaxRetries:  maxRetries,
		}

		// Runtime config
		rc := team.DefaultRuntimeConfig()
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
		if rc.UseTmux && team.IsTmuxAvailable() {
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
	rootCmd.AddCommand(startCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show team status",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		wd := getCwd()
		runtime := team.NewRuntime(wd)

		state, tasks, err := runtime.Status()
		if err != nil {
			return fmt.Errorf("failed to get status: %w", err)
		}

		if jsonFlag {
			// Clean up descriptions for JSON output
			jsonTasks := make([]map[string]interface{}, len(tasks))
			for i, t := range tasks {
				// Keep only first line of description to avoid JSON parsing issues
				descLines := strings.Split(t.Description, "\n")
				cleanDesc := descLines[0]
				if len(cleanDesc) > 200 {
					cleanDesc = cleanDesc[:200] + "..."
				}

				jsonTasks[i] = map[string]interface{}{
					"id":           t.ID,
					"subject":      t.Subject,
					"description":  cleanDesc,
					"status":       t.Status,
					"claimed_by":   t.ClaimedBy,
					"blocked_by":   t.BlockedBy,
					"created_at":   t.CreatedAt,
					"started_at":   t.StartedAt,
					"completed_at": t.CompletedAt,
					"retry_count":  t.RetryCount,
				}
			}

			output := map[string]interface{}{
				"state": state,
				"tasks": jsonTasks,
			}
			data, _ := json.MarshalIndent(output, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		fmt.Printf("Team Status: %s\n", state.Phase)
		fmt.Printf("Active Workers: %d\n", state.ActiveCount)
		fmt.Println()
		fmt.Println("Tasks:")
		for _, t := range tasks {
			status := string(t.Status)
			if t.ClaimedBy != "" {
				status += fmt.Sprintf(" (%s)", t.ClaimedBy)
			}
			fmt.Printf("  [%s] %s - %s\n", t.ID, status, t.Subject)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}

var apiCmd = &cobra.Command{
	Use:   "api <operation> --input '<json>'",
	Short: "Team API for workers",
	Args:  cobra.MinimumNArgs(1),
}

func init() {
	apiCmd.PersistentFlags().String("input", "", "JSON input")
	rootCmd.AddCommand(apiCmd)
}

// parseInput parses JSON input from flag
func parseInput(cmd *cobra.Command) map[string]interface{} {
	// Try to get input from current command, then from parent
	var inputStr string
	if cmd.Flags().Lookup("input") != nil {
		inputStr, _ = cmd.Flags().GetString("input")
	}
	if inputStr == "" && cmd.Parent() != nil && cmd.Parent().Flags().Lookup("input") != nil {
		inputStr, _ = cmd.Parent().Flags().GetString("input")
	}
	
	if inputStr == "" {
		return nil
	}
	var input map[string]interface{}
	if err := json.Unmarshal([]byte(inputStr), &input); err != nil {
		fmt.Fprintf(os.Stderr, "Invalid JSON input: %v\n", err)
		os.Exit(1)
	}
	return input
}

// outputJSON outputs result as JSON
func outputJSON(result interface{}, err error) {
	if err != nil {
		output := map[string]interface{}{
			"ok":  false,
			"error": err.Error(),
		}
		data, _ := json.Marshal(output)
		fmt.Println(string(data))
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(data))
}

var apiCreateTaskCmd = &cobra.Command{
	Use:   "create-task --input '{...}'",
	Short: "Create a new task",
	Run: func(cmd *cobra.Command, args []string) {
		input := parseInput(cmd)
		wd := getCwd()
		api := team.NewAPI(team.NewStorage(wd))

		subject, _ := input["subject"].(string)
		description, _ := input["description"].(string)
		
		var blockedBy []string
		if v, ok := input["blocked_by"].([]interface{}); ok {
			for _, id := range v {
				if s, ok := id.(string); ok {
					blockedBy = append(blockedBy, s)
				}
			}
		}

		task, err := api.CreateTask(subject, description, blockedBy)
		outputJSON(map[string]interface{}{"ok": true, "task": task}, err)
	},
}

var apiUpdateTaskCmd = &cobra.Command{
	Use:   "update-task --input '{...}'",
	Short: "Update a task",
	Run: func(cmd *cobra.Command, args []string) {
		input := parseInput(cmd)
		wd := getCwd()
		api := team.NewAPI(team.NewStorage(wd))

		taskID, _ := input["task_id"].(string)
		delete(input, "task_id")
		
		err := api.UpdateTask(taskID, input)
		outputJSON(map[string]interface{}{"ok": true, "task_id": taskID}, err)
	},
}

var apiClaimTaskCmd = &cobra.Command{
	Use:   "claim-task --input '{...}'",
	Short: "Claim a task",
	Run: func(cmd *cobra.Command, args []string) {
		input := parseInput(cmd)
		wd := getCwd()
		api := team.NewAPI(team.NewStorage(wd))

		taskID, _ := input["task_id"].(string)
		worker, _ := input["worker"].(string)

		task, token, err := api.ClaimTask(taskID, worker)
		outputJSON(map[string]interface{}{
			"ok":          true,
			"task":        task,
			"claim_token": token,
		}, err)
	},
}

var apiStartTaskCmd = &cobra.Command{
	Use:   "start-task --input '{...}'",
	Short: "Start a claimed task",
	Run: func(cmd *cobra.Command, args []string) {
		input := parseInput(cmd)
		wd := getCwd()
		api := team.NewAPI(team.NewStorage(wd))

		taskID, _ := input["task_id"].(string)
		claimToken, _ := input["claim_token"].(string)

		err := api.StartTask(taskID, claimToken)
		outputJSON(map[string]interface{}{"ok": true, "task_id": taskID}, err)
	},
}

var apiCompleteTaskCmd = &cobra.Command{
	Use:   "complete-task --input '{...}'",
	Short: "Complete a task",
	Run: func(cmd *cobra.Command, args []string) {
		input := parseInput(cmd)
		wd := getCwd()
		api := team.NewAPI(team.NewStorage(wd))

		taskID, _ := input["task_id"].(string)
		claimToken, _ := input["claim_token"].(string)
		summary, _ := input["summary"].(string)

		err := api.CompleteTask(taskID, claimToken, summary)
		outputJSON(map[string]interface{}{"ok": true, "task_id": taskID}, err)
	},
}

var apiFailTaskCmd = &cobra.Command{
	Use:   "fail-task --input '{...}'",
	Short: "Fail a task",
	Run: func(cmd *cobra.Command, args []string) {
		input := parseInput(cmd)
		wd := getCwd()
		api := team.NewAPI(team.NewStorage(wd))

		taskID, _ := input["task_id"].(string)
		claimToken, _ := input["claim_token"].(string)
		errMsg, _ := input["error"].(string)

		err := api.FailTask(taskID, claimToken, errMsg)
		outputJSON(map[string]interface{}{"ok": true, "task_id": taskID}, err)
	},
}

var apiReleaseTaskCmd = &cobra.Command{
	Use:   "release-task --input '{...}'",
	Short: "Release a task claim",
	Run: func(cmd *cobra.Command, args []string) {
		input := parseInput(cmd)
		wd := getCwd()
		api := team.NewAPI(team.NewStorage(wd))

		taskID, _ := input["task_id"].(string)
		claimToken, _ := input["claim_token"].(string)

		err := api.ReleaseTask(taskID, claimToken)
		outputJSON(map[string]interface{}{"ok": true, "task_id": taskID}, err)
	},
}

var apiListTasksCmd = &cobra.Command{
	Use:   "list-tasks",
	Short: "List all tasks",
	Run: func(cmd *cobra.Command, args []string) {
		wd := getCwd()
		api := team.NewAPI(team.NewStorage(wd))

		tasks, err := api.ListTasks()
		outputJSON(map[string]interface{}{"ok": true, "tasks": tasks}, err)
	},
}

var apiReadTaskCmd = &cobra.Command{
	Use:   "read-task --input '{...}'",
	Short: "Read a task",
	Run: func(cmd *cobra.Command, args []string) {
		input := parseInput(cmd)
		wd := getCwd()
		api := team.NewAPI(team.NewStorage(wd))

		taskID, _ := input["task_id"].(string)

		task, err := api.ReadTask(taskID)
		outputJSON(map[string]interface{}{"ok": true, "task": task}, err)
	},
}

func init() {
	apiCmd.AddCommand(apiCreateTaskCmd)
	apiCmd.AddCommand(apiUpdateTaskCmd)
	apiCmd.AddCommand(apiClaimTaskCmd)
	apiCmd.AddCommand(apiStartTaskCmd)
	apiCmd.AddCommand(apiCompleteTaskCmd)
	apiCmd.AddCommand(apiFailTaskCmd)
	apiCmd.AddCommand(apiReleaseTaskCmd)
	apiCmd.AddCommand(apiListTasksCmd)
	apiCmd.AddCommand(apiReadTaskCmd)
	
	// api request-review
	var apiRequestReviewCmd = &cobra.Command{
		Use:   "request-review --input '<json>'",
		Short: "Request human review",
		Long:  "Request a review checkpoint. Worker pauses until human approves.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			input := parseInput(cmd)
			taskID, _ := input["task_id"].(string)
			phaseID, _ := input["phase_id"].(string)
			workerName, _ := input["worker_name"].(string)
			summary, _ := input["summary"].(string)
			outputFile, _ := input["output_file"].(string)
			
			wd := getCwd()
			storage := team.NewStorage(wd)
			api := team.NewAPI(storage)
			
			req, err := api.RequestReview(taskID, phaseID, workerName, summary, outputFile)
			if err != nil {
				return fmt.Errorf("failed to request review: %w", err)
			}
			
			output := map[string]interface{}{
				"status":      "review_requested",
				"task_id":     req.TaskID,
				"created_at":  req.CreatedAt,
			}
			data, _ := json.Marshal(output)
			fmt.Println(string(data))
			return nil
		},
	}
	apiRequestReviewCmd.Flags().String("input", "", "JSON input")
	apiCmd.AddCommand(apiRequestReviewCmd)
	
	// api check-review
	var apiCheckReviewCmd = &cobra.Command{
		Use:   "check-review --input '<json>'",
		Short: "Check review status",
		Long:  "Check if review has been completed. Returns approved/rejected/pending.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			input := parseInput(cmd)
			taskID, _ := input["task_id"].(string)
			
			wd := getCwd()
			storage := team.NewStorage(wd)
			
			result, err := storage.ReadReviewResult(taskID)
			if err != nil {
				// No result yet - still pending
				output := map[string]interface{}{
					"status": "pending",
				}
				data, _ := json.Marshal(output)
				fmt.Println(string(data))
				return nil
			}
			
			output := map[string]interface{}{
				"status":    "completed",
				"approved":  result.Approved,
				"comment":   result.Comment,
				"reviewer":  result.Reviewer,
				"reviewed_at": result.ReviewedAt,
			}
			data, _ := json.Marshal(output)
			fmt.Println(string(data))
			return nil
		},
	}
	apiCheckReviewCmd.Flags().String("input", "", "JSON input")
	apiCmd.AddCommand(apiCheckReviewCmd)
}

// logs command - aggregate logs from all tasks
var logsCmd = &cobra.Command{
	Use:   "logs [task-id]",
	Short: "View aggregated logs",
	Long:  "View logs from all tasks or a specific task",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wd := getCwd()
		storage := team.NewStorage(wd)
		follow, _ := cmd.Flags().GetBool("follow")
		tail, _ := cmd.Flags().GetInt("tail")

		if len(args) > 0 {
			// Specific task
			taskID := args[0]
			logPath := storage.Root() + "/logs/" + taskID + ".log"
			if follow {
				// tail -f
				execCmd := exec.Command("tail", "-f", "-n", fmt.Sprintf("%d", tail), logPath)
				execCmd.Stdout = os.Stdout
				execCmd.Stderr = os.Stderr
				return execCmd.Run()
			}
			data, err := os.ReadFile(logPath)
			if err != nil {
				return fmt.Errorf("failed to read log: %w", err)
			}
			fmt.Println(string(data))
			return nil
		}

		// All tasks
		runtime := team.NewRuntime(wd)
		logs, err := runtime.AggregateLogs()
		if err != nil {
			return fmt.Errorf("failed to aggregate logs: %w", err)
		}

		if jsonFlag {
			data, _ := json.MarshalIndent(logs, "", "  ")
			fmt.Println(string(data))
			return nil
		}

		for taskID, log := range logs {
			fmt.Printf("=== %s ===\n", taskID)
			// Tail if requested
			if tail > 0 {
				lines := strings.Split(log, "\n")
				start := len(lines) - tail
				if start < 0 {
					start = 0
				}
				for i := start; i < len(lines); i++ {
					fmt.Println(lines[i])
				}
			} else {
				fmt.Println(log)
			}
			fmt.Println()
		}
		return nil
	},
}

func init() {
	logsCmd.Flags().Bool("follow", false, "Follow log output (like tail -f)")
	logsCmd.Flags().Int("tail", 50, "Number of lines to show from end")
	rootCmd.AddCommand(logsCmd)
}

// stop command
var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the team",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		wd := getCwd()
		storage := team.NewStorage(wd)
		
		// Read config to get team name
		config, err := storage.ReadConfig()
		if err != nil {
			return fmt.Errorf("no team found in %s", wd)
		}
		
		// Kill tmux session
		if team.IsTmuxAvailable() {
			tmux := team.NewTmuxManager(config.Name, wd)
			if tmux.SessionExists() {
				fmt.Printf("Stopping tmux session '%s'...\n", config.Name)
				tmux.KillSession()
			}
		}
		
		// Update state
		state, _ := storage.ReadState()
		if state != nil {
			state.Phase = "stopped"
			state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			storage.WriteState(state)
		}
		
		fmt.Println("Team stopped")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}

// attach command - attach to tmux session
var attachCmd = &cobra.Command{
	Use:   "attach [worker-name]",
	Short: "Attach to tmux session or worker window",
	Long:  "Attach to the team's tmux session. Optionally specify a worker window.",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wd := getCwd()
		storage := team.NewStorage(wd)
		
		config, err := storage.ReadConfig()
		if err != nil {
			return fmt.Errorf("no team found in %s", wd)
		}
		
		if !team.IsTmuxAvailable() {
			return fmt.Errorf("tmux not available")
		}
		
		tmux := team.NewTmuxManager(config.Name, wd)
		if !tmux.SessionExists() {
			return fmt.Errorf("tmux session '%s' not found", config.Name)
		}
		
		// Attach to session
		target := config.Name
		if len(args) > 0 {
			target = config.Name + ":" + args[0]
		}
		
		// exec tmux attach
		execCmd := exec.Command("tmux", "attach", "-t", target)
		execCmd.Stdin = os.Stdin
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr
		return execCmd.Run()
	},
}

func init() {
	rootCmd.AddCommand(attachCmd)
}

// capture command - capture worker output
var captureCmd = &cobra.Command{
	Use:   "capture [worker-name]",
	Short: "Capture worker output from tmux",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wd := getCwd()
		storage := team.NewStorage(wd)
		
		config, err := storage.ReadConfig()
		if err != nil {
			return fmt.Errorf("no team found in %s", wd)
		}
		
		if !team.IsTmuxAvailable() {
			return fmt.Errorf("tmux not available")
		}
		
		tmux := team.NewTmuxManager(config.Name, wd)
		
		if len(args) > 0 {
			// Capture specific worker
			output, err := tmux.CapturePane(args[0])
			if err != nil {
				return fmt.Errorf("failed to capture: %w", err)
			}
			fmt.Println(output)
			return nil
		}
		
		// Capture all workers
		windows, err := tmux.ListWindows()
		if err != nil {
			return fmt.Errorf("failed to list windows: %w", err)
		}
		
		for _, win := range windows {
			if win == config.Name {
				continue // Skip main window
			}
			output, err := tmux.CapturePane(win)
			if err != nil {
				fmt.Printf("=== %s (error: %v) ===\n", win, err)
				continue
			}
			fmt.Printf("=== %s ===\n", win)
			fmt.Println(output)
			fmt.Println()
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(captureCmd)
}

// review command - human-in-the-loop review
var reviewCmd = &cobra.Command{
	Use:   "review",
	Short: "Review pending checkpoints",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		wd := getCwd()
		storage := team.NewStorage(wd)
		api := team.NewAPI(storage)
		
		requests, err := api.ListPendingReviews()
		if err != nil {
			return fmt.Errorf("failed to list reviews: %w", err)
		}
		
		if len(requests) == 0 {
			fmt.Println("No pending reviews")
			return nil
		}
		
		if jsonFlag {
			data, _ := json.MarshalIndent(requests, "", "  ")
			fmt.Println(string(data))
			return nil
		}
		
		fmt.Printf("Pending Reviews (%d):\n\n", len(requests))
		for i, req := range requests {
			fmt.Printf("%d. Task: %s (Phase: %s)\n", i+1, req.TaskID, req.PhaseID)
			fmt.Printf("   Worker: %s\n", req.WorkerName)
			fmt.Printf("   Summary: %s\n", req.Summary)
			if req.OutputFile != "" {
				fmt.Printf("   Output: %s\n", req.OutputFile)
			}
			fmt.Printf("   Created: %s\n\n", req.CreatedAt.Format(time.RFC3339))
		}
		
		fmt.Println("To approve: orchestrate review approve <task-id> --comment '...'")
		fmt.Println("To reject:  orchestrate review reject <task-id> --comment '...'")
		return nil
	},
}

var reviewApproveCmd = &cobra.Command{
	Use:   "approve <task-id>",
	Short: "Approve a review",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wd := getCwd()
		storage := team.NewStorage(wd)
		api := team.NewAPI(storage)
		
		taskID := args[0]
		comment, _ := cmd.Flags().GetString("comment")
		reviewer, _ := cmd.Flags().GetString("reviewer")
		
		if err := api.SubmitReview(taskID, true, comment, reviewer); err != nil {
			return fmt.Errorf("failed to approve: %w", err)
		}
		
		// Remove review request
		storage.DeleteReviewRequest(taskID)
		
		fmt.Printf("Task %s approved\n", taskID)
		return nil
	},
}

var reviewRejectCmd = &cobra.Command{
	Use:   "reject <task-id>",
	Short: "Reject a review",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wd := getCwd()
		storage := team.NewStorage(wd)
		api := team.NewAPI(storage)
		
		taskID := args[0]
		comment, _ := cmd.Flags().GetString("comment")
		reviewer, _ := cmd.Flags().GetString("reviewer")
		
		if err := api.SubmitReview(taskID, false, comment, reviewer); err != nil {
			return fmt.Errorf("failed to reject: %w", err)
		}
		
		// Remove review request
		storage.DeleteReviewRequest(taskID)
		
		fmt.Printf("Task %s rejected\n", taskID)
		return nil
	},
}

var reviewShowCmd = &cobra.Command{
	Use:   "show <task-id>",
	Short: "Show review output",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		wd := getCwd()
		storage := team.NewStorage(wd)
		
		taskID := args[0]
		req, err := storage.ReadReviewRequest(taskID)
		if err != nil {
			return fmt.Errorf("review request not found: %w", err)
		}
		
		fmt.Printf("Task: %s\n", req.TaskID)
		fmt.Printf("Phase: %s\n", req.PhaseID)
		fmt.Printf("Worker: %s\n", req.WorkerName)
		fmt.Printf("Summary: %s\n\n", req.Summary)
		
		// Show output file if exists
		if req.OutputFile != "" {
			data, err := os.ReadFile(req.OutputFile)
			if err != nil {
				fmt.Printf("Output file: %s (error reading)\n", req.OutputFile)
			} else {
				fmt.Println("=== Output ===")
				fmt.Println(string(data))
			}
		}
		return nil
	},
}

func init() {
	reviewApproveCmd.Flags().String("comment", "", "Approval comment")
	reviewApproveCmd.Flags().String("reviewer", "human", "Reviewer name")
	reviewRejectCmd.Flags().String("comment", "", "Rejection reason")
	reviewRejectCmd.Flags().String("reviewer", "human", "Reviewer name")
	
	reviewCmd.AddCommand(reviewApproveCmd)
	reviewCmd.AddCommand(reviewRejectCmd)
	reviewCmd.AddCommand(reviewShowCmd)
	rootCmd.AddCommand(reviewCmd)
}