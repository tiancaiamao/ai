package cmd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/channel"
	"github.com/genius/ag/internal/storage"
	"github.com/genius/ag/internal/task"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "ag",
	Short: "Agent orchestration CLI",
	Long:  "ag provides primitives for spawning, communicating, and coordinating AI agents.",
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// checkBinaryStaleness warns if the ag binary is older than its source code,
// which means the user needs to rebuild.
func checkBinaryStaleness() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exeInfo, err := os.Stat(exe)
	if err != nil {
		return
	}

	// Walk source files in the executable's directory tree
	// (assuming the source is nearby, e.g. ~/.ai/skills/ag/)
	sourceDir := filepath.Dir(exe)
	newerFiles := 0
	filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if strings.HasSuffix(path, ".go") && info.ModTime().After(exeInfo.ModTime()) {
			newerFiles++
		}
		return nil
	})

	if newerFiles > 0 {
		log.Printf("⚠️  Binary is stale (%d source files newer). Run: cd %s && go build -o ag .", newerFiles, sourceDir)
	}
}

var taskRunCmd = &cobra.Command{
	Use:   "run",
	Short: "Run scheduler to execute tasks automatically",
	Run: func(cmd *cobra.Command, args []string) {
		checkBinaryStaleness()

				maxConcurrent, _ := cmd.Flags().GetInt("max-concurrent")
		maxRetries, _ := cmd.Flags().GetInt("max-retries")
		timeoutSec, _ := cmd.Flags().GetInt("timeout")
		pollMs, _ := cmd.Flags().GetInt("poll")
		design, _ := cmd.Flags().GetString("design")
		skipReview, _ := cmd.Flags().GetBool("skip-review")
		detach, _ := cmd.Flags().GetBool("detach")
		callback, _ := cmd.Flags().GetString("callback")

		cfg := task.DefaultSchedulerConfig()
		cfg.MaxConcurrent = maxConcurrent
		cfg.MaxRetries = maxRetries
		cfg.Timeout = time.Duration(timeoutSec) * time.Second
		cfg.PollInterval = time.Duration(pollMs) * time.Millisecond
		cfg.DesignFile = design
		cfg.SkipReview = skipReview
		cfg.Callback = callback

		cwd, _ := os.Getwd()
		cfg.WorkDir = cwd

		if detach {
			runDetached(os.Args)
			return
		}

		// Setup logging to file + stderr when running in foreground
		logFile, err := os.OpenFile(filepath.Join(storage.BaseDir, "scheduler.log"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err == nil {
			log.SetOutput(io.MultiWriter(os.Stderr, logFile))
			defer logFile.Close()
		}

				// Write PID file and ensure cleanup on exit
		pidPath := filepath.Join(storage.BaseDir, "scheduler.pid")
		os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)
		defer os.Remove(pidPath)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Handle Ctrl+C gracefully
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt)
		go func() {
			<-sigChan
			fmt.Println("\nStopping scheduler...")
			cancel()
		}()

				if err := task.RunScheduler(ctx, cfg); err != nil {
			os.Remove(pidPath)
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}

// runDetached re-executes the same command without --detach in the background,
// redirecting output to scheduler.log.
func runDetached(originalArgs []string) {
	// Build args without any --detach / --detach=true / --detach=false form
	var cleanArgs []string
	for _, a := range originalArgs {
		if a == "--detach" || a == "--detach=true" || a == "--detach=false" {
			continue
		}
		if strings.HasPrefix(a, "--detach=") {
			continue
		}
		cleanArgs = append(cleanArgs, a)
	}

	logPath := filepath.Join(storage.BaseDir, "scheduler.log")
	pidPath := filepath.Join(storage.BaseDir, "scheduler.pid")

	// Open log file
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open log file: %v\n", err)
		os.Exit(1)
	}

	cmd := exec.Command(cleanArgs[0], cleanArgs[1:]...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		fmt.Fprintf(os.Stderr, "Failed to start scheduler: %v\n", err)
		os.Exit(1)
	}

	// Write PID
	os.WriteFile(pidPath, []byte(fmt.Sprintf("%d", cmd.Process.Pid)), 0644)

	fmt.Printf("Scheduler started in background (PID %d)\n", cmd.Process.Pid)
	fmt.Printf("  ag task log     — follow output\n")
	fmt.Printf("  ag task stop    — stop scheduler\n")
	fmt.Printf("  ag task ls      — check progress\n")
	cmd.Process.Release()
}

var taskTransitionCmd = &cobra.Command{
	Use:   "transition <id> <state>",
	Short: "Transition task to a new state (state machine validated)",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		t, err := task.Transition(args[0], args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%s → %s\n", t.ID, t.Status)
	},
}

var taskRetryCmd = &cobra.Command{
	Use:   "retry <id>",
	Short: "Retry a failed task",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		maxRetries, _ := cmd.Flags().GetInt("max-retries")
		t, err := task.Retry(args[0], maxRetries)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("retried %s (attempt %d)\n", t.ID, t.RetryCount)
	},
}

func init() {
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if err := storage.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing storage: %v\n", err)
			os.Exit(1)
		}
	}
}

// ========== Agent Commands ==========

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage agents (spawn, status, steer, abort, kill, etc.)",
}

var agentSpawnCmd = &cobra.Command{
	Use:   "spawn <id>",
	Short: "Spawn a new agent",
	Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		system, _ := cmd.Flags().GetString("system")
		input, _ := cmd.Flags().GetString("input")
		cwd, _ := cmd.Flags().GetString("cwd")
		backend, _ := cmd.Flags().GetString("backend")

		// Resolve @file prefix for --system flag.
		if strings.HasPrefix(system, "@") {
			data, err := os.ReadFile(system[1:])
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to read system prompt file: %v\n", err)
				os.Exit(1)
			}
			system = string(data)
		}

		if err := agent.ValidateID(id); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if agent.Exists(id) {
			fmt.Fprintf(os.Stderr, "agent already exists: %s\n", id)
			os.Exit(1)
		}

		if err := Spawn(id, system, input, cwd, backend); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(id)
	},
}

var agentStatusCmd = &cobra.Command{
	Use:   "status <id>",
	Short: "Show agent activity status",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		format, _ := cmd.Flags().GetString("format")

		// 使用新的 GetAgentStatus 函数
		activity, err := GetAgentStatus(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "no activity for agent %s: %v\n", id, err)
			os.Exit(1)
		}

		// Check stale: verify tmux session and PID (仅对传统 bridge)
		if activity.Backend != "ai" {
			DetectStale(id, activity)
		}

		FormatAgentStatus(activity, format, id)
	},
}
var agentSteerCmd = &cobra.Command{
	Use:   "steer <id> <message>",
	Short: "Send a steering message to a running agent",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		resp, err := BridgeCommand(args[0], "steer", args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !resp.OK {
			fmt.Fprintf(os.Stderr, "steer failed: %s\n", resp.Error)
			os.Exit(1)
		}
		fmt.Println("ok")
	},
}

var agentAbortCmd = &cobra.Command{
	Use:   "abort <id>",
	Short: "Abort the current task (agent stays alive for follow-up)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		resp, err := BridgeCommand(args[0], "abort", "")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !resp.OK {
			fmt.Fprintf(os.Stderr, "abort failed: %s\n", resp.Error)
			os.Exit(1)
		}
		fmt.Println("ok")
	},
}

var agentPromptCmd = &cobra.Command{
	Use:   "prompt <id> <message>",
	Short: "Send a follow-up prompt to an idle or running agent",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		resp, err := BridgeCommand(args[0], "prompt", args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if !resp.OK {
			fmt.Fprintf(os.Stderr, "prompt failed: %s\n", resp.Error)
			os.Exit(1)
		}
		fmt.Println("ok")
	},
}

var agentKillCmd = &cobra.Command{
	Use:   "kill <id>",
	Short: "Kill agent (tmux session), preserves files for diagnostics",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		if err := agent.EnsureExists(id); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := Kill(id); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("killed")
	},
}

var agentShutdownCmd = &cobra.Command{
	Use:   "shutdown <id>",
	Short: "Gracefully shut down agent via RPC",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		if err := agent.EnsureExists(id); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := Shutdown(id); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("shutdown")
	},
}

var agentRmCmd = &cobra.Command{
	Use:   "rm <id>",
	Short: "Remove agent files (must be in terminal state, use --force to kill first)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		force, _ := cmd.Flags().GetBool("force")

		if err := agent.EnsureExists(id); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		if force {
			_ = Kill(id) // kill first, ignore errors (may already be dead)
		}

		if err := Rm(id); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("removed %s\n", id)
	},
}

var agentOutputCmd = &cobra.Command{
	Use:   "output <id>",
	Short: "Show agent output (only when terminal)",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		tailN, _ := cmd.Flags().GetInt("tail")
		format, _ := cmd.Flags().GetString("format")

		if err := agent.EnsureExists(id); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		output, err := Output(id, tailN)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		if format == "json" {
			// Structured output with metadata
			act, _ := agent.ReadActivity(id)
			result := map[string]any{
				"output": output,
			}
			if act != nil {
				result["status"] = act.Status
				result["backend"] = act.Backend
				result["duration"] = formatDuration(act.FinishedAt - act.StartedAt)
				result["turns"] = act.Turns
				if act.TokensTotal > 0 {
					result["tokensTotal"] = act.TokensTotal
				}
			}
			data, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(data))
			return
		}

		fmt.Print(output)
	},
}

var agentConversationCmd = &cobra.Command{
	Use:   "conversation <id>",
	Short: "Show agent conversation in a cleaner format",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		format, _ := cmd.Flags().GetString("format")
		nth, _ := cmd.Flags().GetInt("nth")

		// 获取对话
		conversation, err := GetConversation(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		// 根据格式输出
		switch format {
		case "json":
			data, _ := json.MarshalIndent(conversation, "", "  ")
			fmt.Println(string(data))
		case "markdown":
			fmt.Println(conversation.FormatAsMarkdown())
		case "text":
			fmt.Println(conversation.FormatAsText())
		case "last-assistant":
			if nth > 0 {
				fmt.Println(conversation.GetNthLastAssistantResponse(nth))
			} else {
				fmt.Println(conversation.GetLastAssistantResponse())
			}
		case "last-user":
			fmt.Println(conversation.GetLastUserMessage())
		default:
			fmt.Println(conversation.FormatAsText())
		}
	},
}

var agentWaitCmd = &cobra.Command{
	Use:   "wait <id> [<id2>...]",
	Short: "Wait for agents to reach terminal state",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		timeoutSec, _ := cmd.Flags().GetInt("timeout")
		if err := Wait(cmd.Context(), args, timeoutSec); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("done")
	},
}

var agentLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all agents",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		format, _ := cmd.Flags().GetString("format")
		allAgents, err := agent.List()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		if format == "json" {
			data, _ := json.MarshalIndent(allAgents, "", "  ")
			fmt.Println(string(data))
			return
		}

		if len(allAgents) == 0 {
			fmt.Println("No agents.")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSTATUS\tBACKEND\tSTARTED")
		for _, a := range allAgents {
			started := "-"
			if a.StartedAt > 0 {
				started = formatTime(a.StartedAt)
			}
			be := a.Backend
			if be == "" {
				be = "ai"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.ID, a.Status, be, started)
		}
		w.Flush()
	},
}

// ========== Bridge Command (hidden, internal) ==========

var bridgeCmd = &cobra.Command{
	Use:    "bridge <id>",
	Short:  "Internal: run bridge process for an agent (inside tmux)",
	Args:   cobra.ExactArgs(1),
	Hidden: true,
	Run: func(cmd *cobra.Command, args []string) {
		if err := RunBridge(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "bridge error: %v\n", err)
			os.Exit(1)
		}
	},
}

// ========== Send/Recv (data-plane, top-level) ==========

var sendCmd = &cobra.Command{
	Use:   "send <target> [message]",
	Short: "Send a message to a channel or agent",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target := args[0]
		filePath, _ := cmd.Flags().GetString("file")

		var data []byte
		var isFile bool

		if filePath != "" {
			data = []byte(filePath)
			isFile = true
		} else if len(args) > 1 {
			data = []byte(args[1])
		} else {
			data = readStdin()
		}

		if len(data) == 0 {
			fmt.Fprintln(os.Stderr, "no message data provided")
			os.Exit(1)
		}

		if err := channel.Send(target, data, isFile); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}

var recvCmd = &cobra.Command{
	Use:   "recv <source>",
	Short: "Receive a message from a channel or agent",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		wait, _ := cmd.Flags().GetBool("wait")
		timeoutSec, _ := cmd.Flags().GetInt("timeout")
		all, _ := cmd.Flags().GetBool("all")

		data, err := channel.Recv(args[0], wait, timeoutSec, all)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Stdout.Write(data)
	},
}

// ========== Channel Commands ==========

var channelCmd = &cobra.Command{
	Use:   "channel",
	Short: "Manage message channels",
}

var channelCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a named channel",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := channel.Create(args[0]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("channel created: %s\n", args[0])
	},
}

var channelLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all channels",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		channels, err := channel.List()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if len(channels) == 0 {
			fmt.Println("No channels.")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tMESSAGES")
		for _, ch := range channels {
			fmt.Fprintf(w, "%s\t%d\n", ch.Name, ch.Messages)
		}
		w.Flush()
	},
}

var channelRmCmd = &cobra.Command{
	Use:   "rm <name>",
	Short: "Remove a channel",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := channel.Remove(args[0]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("channel removed: %s\n", args[0])
	},
}

// ========== Task Commands ==========

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage tasks",
}

var taskCreateCmd = &cobra.Command{
	Use:   "create <description>",
	Short: "Create a new task",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		specFile, _ := cmd.Flags().GetString("spec")
		t, err := task.Create(args[0], specFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(t.ID)
	},
}

var taskImportPlanCmd = &cobra.Command{
	Use:   "import-plan <file>",
	Short: "Import tasks from a PLAN.yml file",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		count, err := task.ImportPlan(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("imported %d tasks\n", count)
	},
}

var taskListCmd = &cobra.Command{
	Use:     "ls",
	Short:   "List tasks",
	Aliases: []string{"list"},
	Run: func(cmd *cobra.Command, args []string) {
		statusFilter, _ := cmd.Flags().GetString("status")
		format, _ := cmd.Flags().GetString("format")

		tasks, err := task.List(statusFilter)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		if format == "json" {
			data, _ := json.MarshalIndent(tasks, "", "  ")
			fmt.Println(string(data))
			return
		}

				if len(tasks) == 0 {
			fmt.Println("No tasks.")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSTATUS\tCLAIMANT\tELAPSED\tDESCRIPTION")
		for _, t := range tasks {
			desc := t.Description
			if t.Title != "" {
				desc = t.Title
			}
			if len(desc) > 50 {
				desc = desc[:50] + "..."
			}
			elapsed := ""
			if t.ClaimedAt > 0 {
				dur := time.Since(time.Unix(t.ClaimedAt, 0)).Round(time.Second)
				elapsed = dur.String()
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Status, t.Claimant, elapsed, desc)
		}
		w.Flush()
	},
}

var taskClaimCmd = &cobra.Command{
	Use:   "claim <id> [claimant]",
	Short: "Claim a task",
	Args:  cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		claimant := ""
		if len(args) > 1 {
			claimant = args[1]
		}
		_, err := task.Claim(args[0], claimant)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("claimed")
	},
}

var taskNextCmd = &cobra.Command{
	Use:   "next",
	Short: "Claim the next available task (dependency-aware)",
	Run: func(cmd *cobra.Command, args []string) {
		claimant, _ := cmd.Flags().GetString("claimant")
		id, err := task.ClaimNext(claimant)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println(id)
	},
}

var taskDoneCmd = &cobra.Command{
	Use:   "done <id>",
	Short: "Mark a task as done",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		summary, _ := cmd.Flags().GetString("summary")
		_, err := task.Done(args[0], summary)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("done")
	},
}

var taskFailCmd = &cobra.Command{
	Use:   "fail <id>",
	Short: "Mark a task as failed",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		errMsg, _ := cmd.Flags().GetString("error")
		retryable, _ := cmd.Flags().GetBool("retryable")
		_, err := task.Fail(args[0], errMsg, retryable)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("failed")
	},
}

var taskCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Remove done/failed tasks and fix dangling dependencies",
	Run: func(cmd *cobra.Command, args []string) {
		cleaned, depsFixed, err := task.Cleanup()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("cleaned up %d tasks", cleaned)
		if depsFixed > 0 {
			fmt.Printf(", fixed %d dangling dependencies", depsFixed)
		}
		fmt.Println()
	},
}

var taskLogCmd = &cobra.Command{
	Use:   "log",
	Short: "Follow scheduler log output (like kubectl logs -f)",
	Run: func(cmd *cobra.Command, args []string) {
		logPath := filepath.Join(storage.BaseDir, "scheduler.log")

		tailN, _ := cmd.Flags().GetInt("tail")
		if tailN > 0 {
			// Print last N lines then exit
			data, err := os.ReadFile(logPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "No scheduler log found at %s\n", logPath)
				os.Exit(1)
			}
			lines := strings.Split(string(data), "\n")
			start := len(lines) - tailN
			if start < 0 {
				start = 0
			}
			for _, l := range lines[start:] {
				fmt.Println(l)
			}
			return
		}

		// Tail -f mode: print existing, then follow
		f, err := os.Open(logPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "No scheduler log found at %s\n", logPath)
			os.Exit(1)
		}
		defer f.Close()

		// Print existing content
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
		}

		// Follow for new content
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt)
		go func() {
			<-sigChan
			cancel()
		}()

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				scanner := bufio.NewScanner(f)
				for scanner.Scan() {
					fmt.Println(scanner.Text())
				}
			}
		}
	},
}

var taskStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop a running background scheduler",
	Run: func(cmd *cobra.Command, args []string) {
		pidPath := filepath.Join(storage.BaseDir, "scheduler.pid")
		heartbeatPath := filepath.Join(storage.BaseDir, "scheduler.heartbeat")

		data, err := os.ReadFile(pidPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "No running scheduler found (missing PID file)")
			os.Exit(1)
		}

		var pid int
		if _, err := fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid); err != nil {
			fmt.Fprintf(os.Stderr, "Invalid PID file: %s\n", string(data))
			os.Exit(1)
		}

		// Check heartbeat to detect stale vs active
		if hbData, err := os.ReadFile(heartbeatPath); err == nil {
			var hbTs int64
			fmt.Sscanf(strings.TrimSpace(string(hbData)), "%d", &hbTs)
			if hbTs > 0 {
				age := time.Since(time.Unix(hbTs, 0))
				if age > 30*time.Second {
					fmt.Printf("⚠️  Scheduler heartbeat is %s old (may already be dead)\n", age.Round(time.Second))
				} else {
					fmt.Printf("Scheduler heartbeat: %s ago (alive)\n", age.Round(time.Second))
				}
			}
		}

		// Check if process is running
		proc, err := os.FindProcess(pid)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Process %d not found\n", pid)
			os.Exit(1)
		}

		if err := proc.Signal(syscall.SIGTERM); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to stop process %d: %v\n", pid, err)
			os.Exit(1)
		}

		os.Remove(pidPath)
		fmt.Printf("Scheduler (PID %d) stopped\n", pid)
	},
}

var taskShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "Show task details",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		format, _ := cmd.Flags().GetString("format")
		t, err := task.Load(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		if format == "json" {
			data, _ := json.MarshalIndent(t, "", "  ")
			fmt.Println(string(data))
			return
		}

		fmt.Printf("ID: %s\n", t.ID)
		fmt.Printf("Status: %s\n", t.Status)
		if t.Claimant != "" {
			fmt.Printf("Claimant: %s\n", t.Claimant)
		}
		fmt.Printf("Description: %s\n", t.Description)
		if t.SpecFile != "" {
			fmt.Printf("Spec: %s\n", t.SpecFile)
		}
		if t.OutputFile != "" {
			fmt.Printf("Output: %s\n", t.OutputFile)
		}
		if len(t.Dependencies) > 0 {
			fmt.Printf("Dependencies: %v\n", t.Dependencies)
		}
		if t.Summary != "" {
			fmt.Printf("Summary: %s\n", t.Summary)
		}
		if t.Error != "" {
			fmt.Printf("Error: %s\n", t.Error)
		}
		if t.Retryable {
			fmt.Printf("Retryable: true\n")
		}
		fmt.Printf("Created: %s\n", formatTime(t.CreatedAt))
		if t.ClaimedAt > 0 {
			fmt.Printf("Claimed: %s\n", formatTime(t.ClaimedAt))
		}
		if t.FinishedAt > 0 {
			fmt.Printf("Finished: %s\n", formatTime(t.FinishedAt))
			fmt.Printf("Duration: %s\n", formatDuration(t.FinishedAt-t.ClaimedAt))
		}

		// Aggregate turns/tokens from claimant agent's activity.json (FR-023)
		if t.Claimant != "" {
			claimantAct, actErr := agent.ReadActivity(t.Claimant)
			if actErr == nil && claimantAct != nil {
				if claimantAct.Turns > 0 {
					fmt.Printf("Agent Turns: %d\n", claimantAct.Turns)
				}
				if claimantAct.TokensTotal > 0 {
					fmt.Printf("Agent Tokens: in=%d out=%d total=%d\n",
						claimantAct.TokensIn, claimantAct.TokensOut, claimantAct.TokensTotal)
				}
			}
		}
	},
}

var taskDepCmd = &cobra.Command{
	Use:   "dep",
	Short: "Manage task dependencies",
}

var taskDepAddCmd = &cobra.Command{
	Use:   "add <task-id> <depends-on-id>",
	Short: "Add a dependency",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		_, err := task.AddDependency(args[0], args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("dependency added")
	},
}

var taskDepRmCmd = &cobra.Command{
	Use:   "rm <task-id> <depends-on-id>",
	Short: "Remove a dependency",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		_, err := task.RemoveDependency(args[0], args[1])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("dependency removed")
	},
}

var taskDepLsCmd = &cobra.Command{
	Use:   "ls <task-id>",
	Short: "List task dependencies",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		deps, err := task.Dependencies(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if len(deps) == 0 {
			fmt.Println("No dependencies.")
			return
		}
		for _, dep := range deps {
			fmt.Println(dep)
		}
	},
}

// ========== Helpers ==========

func readStdin() []byte {
	data := make([]byte, 0, 4096)
	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if n > 0 {
			data = append(data, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return data
}

func init() {
	// Agent flags
	agentSpawnCmd.Flags().String("system", "", "System prompt (inline or @file)")
	agentSpawnCmd.Flags().String("input", "", "Initial input message")
	agentSpawnCmd.Flags().String("cwd", "", "Working directory for the agent")
	agentSpawnCmd.Flags().String("backend", "ai", "Backend name (from backends.yaml)")
	agentOutputCmd.Flags().Int("tail", 0, "Show last N bytes of output")
	agentRmCmd.Flags().Bool("force", false, "Kill agent before removing")
	agentWaitCmd.Flags().Int("timeout", 300, "Timeout in seconds (0 = no timeout)")

		// Status/ls format flag
	for _, c := range []*cobra.Command{agentStatusCmd, agentLsCmd, agentOutputCmd, agentConversationCmd} {
		c.Flags().String("format", "", "Output format: json")
	}
	agentConversationCmd.Flags().Int("nth", 0, "For last-assistant: get Nth-from-last assistant message (2=second to last)")

		// Task flags
	taskCreateCmd.Flags().String("spec", "", "Spec file path")
	taskListCmd.Flags().String("status", "", "Filter by status")
	taskListCmd.Flags().String("format", "", "Output format: json")
	taskNextCmd.Flags().String("claimant", "", "Claimant identifier")
	taskDoneCmd.Flags().String("summary", "", "Task summary")
	taskFailCmd.Flags().String("error", "", "Error message")
	taskFailCmd.Flags().Bool("retryable", false, "Mark as retryable")
	taskShowCmd.Flags().String("format", "", "Output format: json")

	// Task run flags
	taskRunCmd.Flags().Int("max-concurrent", 2, "Max concurrent workers")
	taskRunCmd.Flags().Int("max-retries", 3, "Max retries per failed task")
	taskRunCmd.Flags().Int("timeout", 600, "Timeout per task in seconds")
	taskRunCmd.Flags().Int("poll", 5000, "Poll interval in milliseconds")
	taskRunCmd.Flags().String("design", "", "Path to design.md for worker context")
	taskRunCmd.Flags().Bool("skip-review", false, "Skip review phase")
	taskRunCmd.Flags().Bool("detach", false, "Run scheduler in background (log to .ag/scheduler.log)")
	taskRunCmd.Flags().String("callback", "", "Shell command to execute when all tasks complete (e.g. 'ag agent prompt main \"done\"')")
	taskLogCmd.Flags().Int("tail", 0, "Show last N lines (default: follow mode)")
	taskRetryCmd.Flags().Int("max-retries", 3, "Max retries allowed")

	// Send/recv flags
	sendCmd.Flags().String("file", "", "Send file contents from path")
	recvCmd.Flags().Bool("wait", false, "Wait for a message if none available")
	recvCmd.Flags().Int("timeout", 60, "Timeout in seconds for --wait")
	recvCmd.Flags().Bool("all", false, "Receive all pending messages")

	// Agent subcommands
		agentCmd.AddCommand(
		agentSpawnCmd, agentStatusCmd, agentSteerCmd, agentAbortCmd,
				agentPromptCmd, agentKillCmd, agentShutdownCmd, agentRmCmd,
		agentOutputCmd, agentConversationCmd, agentWaitCmd, agentLsCmd, agentTailCmd,
	)

	// Channel subcommands
	channelCmd.AddCommand(channelCreateCmd, channelLsCmd, channelRmCmd)

		// Task subcommands
	taskDepCmd.AddCommand(taskDepAddCmd, taskDepRmCmd, taskDepLsCmd)
				taskCmd.AddCommand(taskCreateCmd, taskImportPlanCmd, taskListCmd, taskClaimCmd, taskNextCmd, taskDoneCmd, taskFailCmd, taskShowCmd, taskDepCmd, taskRunCmd, taskTransitionCmd, taskRetryCmd, taskCleanupCmd, taskLogCmd, taskStopCmd)

		// Root subcommands
	rootCmd.AddCommand(agentCmd, bridgeCmd, sendCmd, recvCmd, channelCmd, taskCmd, convCmd, doctorCmd)
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Pre-flight check: verify ag toolchain is healthy",
	Run: func(cmd *cobra.Command, args []string) {
		pass, fail, warn := 0, 0, 0
		check := func(name string, err error) {
			if err != nil {
				fmt.Printf("  ❌ %s: %v\n", name, err)
				fail++
			} else {
				fmt.Printf("  ✅ %s\n", name)
				pass++
			}
		}
		warnCheck := func(name string, err error) {
			if err != nil {
				fmt.Printf("  ⚠️  %s: %v\n", name, err)
				warn++
			} else {
				fmt.Printf("  ✅ %s\n", name)
				pass++
			}
		}

				fmt.Println("🔍 ag toolchain health check")
		fmt.Println()

		// 1. Storage directory writable
		agentsDir, channelsDir, tasksDir := storage.Paths()
		check("storage dirs exist", os.MkdirAll(agentsDir, 0755))
		check("storage dirs exist", os.MkdirAll(channelsDir, 0755))
		check("storage dirs exist", os.MkdirAll(tasksDir, 0755))

		// Write test
		testFile := filepath.Join(tasksDir, ".health-check")
		check("storage writable", os.WriteFile(testFile, []byte("ok"), 0644))
		os.Remove(testFile)

		// 2. Task state machine smoke test
		t1, err := task.Create("doctor smoke test", "")
		check("task create", err)
		if err == nil {
			check("task claim → claimed", func() error {
				_, e := task.Claim(t1.ID, "doctor")
				return e
			}())
			check("task transition claimed→done", func() error {
				_, e := task.Transition(t1.ID, task.StatusDone)
				return e
			}())
			check("task show", func() error {
				_, e := task.Load(t1.ID)
				return e
			}())
		}

		// 3. Invalid transition rejected
		t2, _ := task.Create("doctor invalid test", "")
		if t2 != nil {
			check("invalid transition rejected", func() error {
				_, e := task.Transition(t2.ID, task.StatusDone)
				if e != nil {
					return nil // expected
				}
				return fmt.Errorf("pending→done should be invalid")
			}())

			// Clean up immediately to prevent the scheduler from claiming
			// this transient test task while it sits in pending state.
			os.RemoveAll(storage.TaskDir(t2.ID))
		}

		// 4. Dependency + cycle detection
		if t1 != nil {
			t3, _ := task.Create("doctor dep test", "")
			if t3 != nil {
				check("dependency add", func() error {
					_, e := task.AddDependency(t3.ID, t1.ID)
					return e
				}())
				check("cycle detection", func() error {
					_, e := task.AddDependency(t1.ID, t3.ID)
					if e != nil {
						return nil // expected: cycle detected
					}
					return fmt.Errorf("cycle should be detected")
				}())

				// t3 is in pending state (never transitioned), clean up directly.
				os.RemoveAll(storage.TaskDir(t3.ID))
			}
		}

		// 5. Cleanup
		cleaned, deps, err := task.Cleanup()
		check("cleanup", err)
		if err == nil {
			fmt.Printf("     (cleaned %d tasks, %d dangling deps)\n", cleaned, deps)
		}

		// 6. `ai` binary available
		aiPath, err := exec.LookPath("ai")
		warnCheck("ai binary in PATH", err)
		if err == nil {
			// Try running it
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			aiCmd := exec.CommandContext(ctx, aiPath, "--help")
			aiCmd.Stdout = nil
			aiCmd.Stderr = nil
			warnCheck("ai --help runs", aiCmd.Run())
		}

		// 7. git repo
		warnCheck("git repo present", func() error {
			_, e := exec.LookPath("git")
			return e
		}())

		// 8. ag binary not stale
		exe, _ := os.Executable()
		if exe != "" {
			exeInfo, _ := os.Stat(exe)
			modTime := exeInfo.ModTime()
			if time.Since(modTime) > 24*time.Hour {
				fmt.Printf("  ⚠️  ag binary is %s old (may need rebuild)\n", time.Since(modTime).Round(time.Hour))
				warn++
			} else {
				fmt.Printf("  ✅ ag binary built %s ago\n", time.Since(modTime).Round(time.Minute))
				pass++
			}
		}

		// Summary
		fmt.Printf("\n📊 Results: %d passed, %d failed, %d warnings\n", pass, fail, warn)
		if fail > 0 {
			fmt.Println("\n⛔ Fix errors above before running scheduler.")
			os.Exit(1)
		}
		if warn > 0 {
			fmt.Println("\n⚠️  Warnings are non-blocking but should be addressed.")
		}
	},
}
