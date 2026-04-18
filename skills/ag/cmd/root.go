package cmd

import (
	"encoding/json"
	"fmt"
	"os"
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

		if err := agent.ValidateID(id); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if agent.Exists(id) {
			fmt.Fprintf(os.Stderr, "agent already exists: %s\n", id)
			os.Exit(1)
		}

		if err := Spawn(id, system, input, cwd); err != nil {
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

		if err := agent.EnsureExists(id); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		activity, err := agent.ReadActivity(id)
		if err != nil {
			fmt.Fprintf(os.Stderr, "no activity for agent %s\n", id)
			os.Exit(1)
		}

		// Check stale: verify tmux session and PID
		DetectStale(id, activity)

		if format == "json" {
			data, _ := json.MarshalIndent(activity, "", "  ")
			fmt.Println(string(data))
			return
		}

		fmt.Printf("Agent: %s\n", id)
		fmt.Printf("Status: %s\n", activity.Status)
		if activity.Pid > 0 {
			fmt.Printf("PID: %d\n", activity.Pid)
		}
		if activity.StartedAt > 0 {
			fmt.Printf("Started: %s\n", formatTime(activity.StartedAt))
			if activity.Status == "running" {
				fmt.Printf("Uptime: %s\n", formatDuration(timeNow()-activity.StartedAt))
			}
		}
		if activity.FinishedAt > 0 {
			fmt.Printf("Finished: %s\n", formatTime(activity.FinishedAt))
			fmt.Printf("Duration: %s\n", formatDuration(activity.FinishedAt-activity.StartedAt))
		}
		fmt.Printf("Turns: %d\n", activity.Turns)
		if activity.TokensTotal > 0 {
			fmt.Printf("Tokens: in=%d out=%d total=%d\n", activity.TokensIn, activity.TokensOut, activity.TokensTotal)
		}
		if activity.LastTool != "" {
			fmt.Printf("Last tool: %s\n", activity.LastTool)
		}
		if activity.LastText != "" {
			text := activity.LastText
			if len(text) > 200 {
				text = text[:200] + "..."
			}
			fmt.Printf("Last text: %s\n", text)
		}
		if activity.Error != "" {
			fmt.Printf("Error: %s\n", activity.Error)
		}
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
			data, _ := json.MarshalIndent(map[string]string{"output": output}, "", "  ")
			fmt.Println(string(data))
			return
		}

		fmt.Print(output)
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
		fmt.Fprintln(w, "ID\tSTATUS\tSTARTED")
		for _, a := range allAgents {
			started := "-"
			if a.StartedAt > 0 {
				started = formatTime(a.StartedAt)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", a.ID, a.Status, started)
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
		fmt.Fprintln(w, "ID\tSTATUS\tCLAIMANT\tDESCRIPTION")
		for _, t := range tasks {
			desc := t.Description
			if len(desc) > 50 {
				desc = desc[:50] + "..."
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Status, t.Claimant, desc)
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

func formatTime(unix int64) string {
	if unix == 0 {
		return "-"
	}
	t := time.Unix(unix, 0)
	return t.Format("2006-01-02 15:04:05")
}

func formatDuration(seconds int64) string {
	if seconds < 0 {
		return "-"
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm%ds", seconds/60, seconds%60)
	}
	return fmt.Sprintf("%dh%dm", seconds/3600, (seconds%3600)/60)
}

func timeNow() int64 {
	return time.Now().Unix()
}

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
	agentOutputCmd.Flags().Int("tail", 0, "Show last N bytes of output")
	agentRmCmd.Flags().Bool("force", false, "Kill agent before removing")
	agentWaitCmd.Flags().Int("timeout", 300, "Timeout in seconds (0 = no timeout)")

	// Status/ls format flag
	for _, c := range []*cobra.Command{agentStatusCmd, agentLsCmd, agentOutputCmd} {
		c.Flags().String("format", "", "Output format: json")
	}

	// Task flags
	taskCreateCmd.Flags().String("spec", "", "Spec file path")
	taskListCmd.Flags().String("status", "", "Filter by status")
	taskListCmd.Flags().String("format", "", "Output format: json")
	taskNextCmd.Flags().String("claimant", "", "Claimant identifier")
	taskDoneCmd.Flags().String("summary", "", "Task summary")
	taskFailCmd.Flags().String("error", "", "Error message")
	taskFailCmd.Flags().Bool("retryable", false, "Mark as retryable")
	taskShowCmd.Flags().String("format", "", "Output format: json")

	// Send/recv flags
	sendCmd.Flags().String("file", "", "Send file contents from path")
	recvCmd.Flags().Bool("wait", false, "Wait for a message if none available")
	recvCmd.Flags().Int("timeout", 60, "Timeout in seconds for --wait")
	recvCmd.Flags().Bool("all", false, "Receive all pending messages")

	// Agent subcommands
	agentCmd.AddCommand(
		agentSpawnCmd, agentStatusCmd, agentSteerCmd, agentAbortCmd,
		agentPromptCmd, agentKillCmd, agentShutdownCmd, agentRmCmd,
		agentOutputCmd, agentWaitCmd, agentLsCmd,
	)

	// Channel subcommands
	channelCmd.AddCommand(channelCreateCmd, channelLsCmd, channelRmCmd)

	// Task subcommands
	taskDepCmd.AddCommand(taskDepAddCmd, taskDepRmCmd, taskDepLsCmd)
	taskCmd.AddCommand(taskCreateCmd, taskImportPlanCmd, taskListCmd, taskClaimCmd, taskNextCmd, taskDoneCmd, taskFailCmd, taskShowCmd, taskDepCmd)

	// Root subcommands
	rootCmd.AddCommand(agentCmd, bridgeCmd, sendCmd, recvCmd, channelCmd, taskCmd)
}