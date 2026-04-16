package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	// Ensure .ag/ structure exists on every command
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		if err := storage.Init(); err != nil {
			fmt.Fprintf(os.Stderr, "Error initializing storage: %v\n", err)
			os.Exit(1)
		}
	}

	rootCmd.AddCommand(spawnCmd, waitCmd, killCmd, outputCmd, statusCmd, lsCmd, rmCmd)
	rootCmd.AddCommand(sendCmd, recvCmd)
	rootCmd.AddCommand(readCmd, stopCmd)
	rootCmd.AddCommand(channelCmd)
	rootCmd.AddCommand(taskCmd)
}

// ========== Agent Commands ==========

var (
	spawnSystem     string
	spawnInput      string
	spawnMode       string
	spawnCwd        string
	spawnTimeout    string
	spawnMock       bool
	spawnMockScript string
)

var spawnCmd = &cobra.Command{
	Use:   "spawn --id <name> [--system <prompt>] [--input <file|text>] [--mode <headless|rpc>] [--timeout <duration>]",
	Short: "Spawn a new agent",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		id, _ := cmd.Flags().GetString("id")
		if id == "" {
			fmt.Fprintln(os.Stderr, "Error: --id is required")
			os.Exit(1)
		}

		meta, err := agent.Spawn(agent.SpawnConfig{
			ID:         id,
			System:     spawnSystem,
			Input:      spawnInput,
			Mode:       spawnMode,
			Cwd:        spawnCwd,
			Timeout:    spawnTimeout,
			Mock:       spawnMock,
			MockScript: spawnMockScript,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Agent spawned: %s (pid: %d)\n", meta.ID, meta.Pid)
	},
}

var waitTimeout int

var waitCmd = &cobra.Command{
	Use:   "wait <agent-id...> [--timeout <seconds>]",
	Short: "Wait for one or more agents to complete",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		failed := 0
		for _, id := range args {
			if err := agent.Wait(id, waitTimeout); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				failed++
			} else {
				fmt.Printf("Agent %s done\n", id)
			}
		}
		if failed > 0 {
			os.Exit(1)
		}
	},
}

var killCmd = &cobra.Command{
	Use:   "kill <agent-id>",
	Short: "Kill an agent",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := agent.Kill(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Agent %s killed\n", args[0])
	},
}

var outputCmd = &cobra.Command{
	Use:   "output <agent-id>",
	Short: "Get agent's final output",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		data, err := agent.Output(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Stdout.Write(data)
	},
}

var statusCmd = &cobra.Command{
	Use:   "status <agent-id>",
	Short: "Show agent status",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		status, meta, err := agent.Status(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Agent: %s\n", args[0])
		fmt.Printf("Status: %s\n", status)
		if meta.Pid > 0 {
			fmt.Printf("PID: %d\n", meta.Pid)
		}
		if meta.StartedAt > 0 {
			fmt.Printf("Started: %s\n", formatTime(meta.StartedAt))
		}
		if meta.FinishedAt > 0 {
			fmt.Printf("Finished: %s\n", formatTime(meta.FinishedAt))
			fmt.Printf("Duration: %s\n", formatDuration(meta.FinishedAt-meta.StartedAt))
		}
		if meta.ExitCode != 0 {
			fmt.Printf("Exit code: %d\n", meta.ExitCode)
		}
	},
}

var lsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all agents",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		agents, err := agent.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(agents) == 0 {
			fmt.Println("No agents")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSTATUS\tPID\tUPTIME")
		for _, a := range agents {
			pid := "-"
			if a.Meta.Pid > 0 {
				pid = fmt.Sprintf("%d", a.Meta.Pid)
			}
			uptime := "-"
			if a.Status == "running" && a.Meta.StartedAt > 0 {
				uptime = formatDuration(timeNow() - a.Meta.StartedAt)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.ID, a.Status, pid, uptime)
		}
		w.Flush()
	},
}

// ========== Message Commands ==========

var (
	sendFile string
)

var sendCmd = &cobra.Command{
	Use:   "send <target> [--file <file> | <message>]",
	Short: "Send a message to an agent or channel",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target := args[0]
		var data []byte
		var isFile bool

		if sendFile != "" {
			// Read file contents here, not just the path
			fileData, err := os.ReadFile(sendFile)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
				os.Exit(1)
			}
			data = fileData
			isFile = false // contents already read, no need for channel.Send to re-read
		} else if len(args) > 1 {
			data = []byte(args[1])
		} else {
			// Read from stdin
			stat, _ := os.Stdin.Stat()
			if (stat.Mode() & os.ModeCharDevice) != 0 {
				fmt.Fprintln(os.Stderr, "Error: provide message via argument, --file, or stdin")
				os.Exit(1)
			}
			data = readStdin()
		}

		if err := channel.Send(target, data, isFile); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Message sent to %s\n", target)
	},
}

var (
	recvWait    bool
	recvTimeout int
	recvAll     bool
)

var recvCmd = &cobra.Command{
	Use:   "recv <source> [--wait] [--timeout <seconds>] [--all]",
	Short: "Receive a message from an agent or channel",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		data, err := channel.Recv(args[0], recvWait, recvTimeout, recvAll)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Stdout.Write(data)
	},
}

// ========== RPC Agent Commands ==========

var (
	readFollow bool
	readRaw    bool
)

var readCmd = &cobra.Command{
	Use:   "read <agent-id> [--follow] [--raw]",
	Short: "Read events from an RPC-mode agent",
	Long: `Read events from a running or completed RPC-mode agent.

By default, shows new events since the last read. Use --follow to stream
events in real-time. Use --raw to output raw JSON lines.

For headless-mode agents, this falls back to showing the output file.`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		agentDir := storage.AgentDir(id)
		if !storage.Exists(agentDir) {
			fmt.Fprintf(os.Stderr, "Error: agent not found: %s\n", id)
			os.Exit(1)
		}

		meta := &agent.Meta{}
		storage.ReadJSON(filepath.Join(agentDir, "meta.json"), meta)

		// For non-RPC agents, fall back to output file
		if meta.Mode != "rpc" {
			outputPath := filepath.Join(agentDir, "output")
			data, err := os.ReadFile(outputPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: agent is not in RPC mode and has no output\n")
				os.Exit(1)
			}
			os.Stdout.Write(data)
			return
		}

		// Read offset tracking
		offsetFile := filepath.Join(agentDir, "read_offset")
		offset := 0
		if data, err := os.ReadFile(offsetFile); err == nil {
			fmt.Sscanf(string(data), "%d", &offset)
		}

		if readFollow {
			// Stream events in real-time
			for {
				events, err := agent.ReadEvents(id, offset)
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error reading events: %v\n", err)
					os.Exit(1)
				}
				for _, event := range events {
					if readRaw {
						fmt.Println(string(event))
					} else {
						printEvent(event)
					}
					offset++
				}
				// Save offset
				os.WriteFile(offsetFile, []byte(fmt.Sprintf("%d", offset)), 0644)

				status := storage.ReadStatus(agentDir)
				if status != agent.StatusRunning && status != agent.StatusSpawning {
					// Agent finished, drain remaining events
					events, _ = agent.ReadEvents(id, offset)
					for _, event := range events {
						if readRaw {
							fmt.Println(string(event))
						} else {
							printEvent(event)
						}
						offset++
					}
					os.WriteFile(offsetFile, []byte(fmt.Sprintf("%d", offset)), 0644)
					return
				}
				time.Sleep(500 * time.Millisecond)
			}
		}

		// One-shot: read new events
		events, err := agent.ReadEvents(id, offset)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading events: %v\n", err)
			os.Exit(1)
		}
		for _, event := range events {
			if readRaw {
				fmt.Println(string(event))
			} else {
				printEvent(event)
			}
			offset++
		}
		os.WriteFile(offsetFile, []byte(fmt.Sprintf("%d", offset)), 0644)
	},
}

func printEvent(event json.RawMessage) {
	var e struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(event, &e); err != nil {
		return
	}
	switch e.Type {
	case "message_update":
		// Streaming text delta — extract and print incrementally
		var full struct {
			AssistantMessageEvent struct {
				Type  string `json:"type"`
				Delta string `json:"delta"`
			} `json:"assistantMessageEvent"`
		}
		if json.Unmarshal(event, &full) == nil {
			switch full.AssistantMessageEvent.Type {
			case "text_delta":
				fmt.Print(full.AssistantMessageEvent.Delta)
			case "thinking_delta":
				// Suppress thinking output by default (use --raw to see)
			}
		}
	case "message_end":
		// Full message available, but we already printed deltas.
		// Only print if we haven't seen any deltas (e.g., reading completed agent).
		// For now, just print a newline after assistant messages.
		var full struct {
			Message struct {
				Role string `json:"role"`
			} `json:"message"`
		}
		if json.Unmarshal(event, &full) == nil && full.Message.Role == "assistant" {
			fmt.Println()
		}
	case "agent_start", "agent_end", "turn_start", "turn_end",
		"message_start", "tool_execution_start", "tool_execution_end":
		// Structured events — show summary on stderr
		fmt.Fprintf(os.Stderr, "[%s]\n", e.Type)
	default:
		// Other events: suppress by default (use --raw to see)
	}
}

var stopCmd = &cobra.Command{
	Use:   "stop <agent-id>",
	Short: "Gracefully stop an RPC-mode agent",
	Long: `Gracefully stop an RPC-mode agent by sending an abort command.
For headless-mode agents, falls back to kill (SIGTERM).`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		id := args[0]
		agentDir := storage.AgentDir(id)
		if !storage.Exists(agentDir) {
			fmt.Fprintf(os.Stderr, "Error: agent not found: %s\n", id)
			os.Exit(1)
		}

		meta := &agent.Meta{}
		storage.ReadJSON(filepath.Join(agentDir, "meta.json"), meta)

		status := storage.ReadStatus(agentDir)
		if status != agent.StatusRunning {
			fmt.Fprintf(os.Stderr, "Error: agent %s is %s (not running)\n", id, status)
			os.Exit(1)
		}

		if meta.Mode == "rpc" {
			// RPC mode: the ai process reads from stdin (managed by python bridge).
			// We can't inject an abort command via the same stdin.
			// Kill the process group to stop the agent.
			if err := agent.Kill(id); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Agent %s stopped\n", id)
		} else {
			// Headless: just kill
			if err := agent.Kill(id); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Agent %s killed\n", id)
		}
	},
}

// ========== Channel Commands ==========

var channelCmd = &cobra.Command{
	Use:   "channel",
	Short: "Manage channels",
}

var channelCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a channel",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := channel.Create(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Channel created: %s\n", args[0])
	},
}

var channelLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List channels",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		channels, err := channel.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(channels) == 0 {
			fmt.Println("No channels")
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
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Channel removed: %s\n", args[0])
	},
}

// ========== Task Commands ==========

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "Manage tasks",
}

var (
	taskSpecFile string
)

var taskCreateCmd = &cobra.Command{
	Use:   "create <description>",
	Short: "Create a new task",
	Args:  cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		desc := args[0]
		if len(args) > 1 {
			// Join all args as description
			for _, a := range args[1:] {
				desc += " " + a
			}
		}
		t, err := task.Create(desc, taskSpecFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(t.ID)
	},
}

var (
	taskListStatus string
)

var taskListCmd = &cobra.Command{
	Use:     "list [--status <status>]",
	Short:   "List tasks",
	Aliases: []string{"ls"},
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		tasks, err := task.List(taskListStatus)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(tasks) == 0 {
			fmt.Println("No tasks")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tSTATUS\tCLAIMANT\tDESCRIPTION")
		for _, t := range tasks {
			desc := t.Description
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}
			claimant := "-"
			if t.Claimant != "" {
				claimant = t.Claimant
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.ID, t.Status, claimant, desc)
		}
		w.Flush()
	},
}

var taskClaimAs string

var taskClaimCmd = &cobra.Command{
	Use:   "claim <task-id> [--as <agent-id>]",
	Short: "Claim a pending task",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		agentID := taskClaimAs
		if agentID == "" {
			agentID = os.Getenv("AG_AGENT_ID")
		}
		if agentID == "" {
			agentID = "manual"
		}
		t, err := task.Claim(args[0], agentID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Task %s claimed by %s\n", t.ID, t.Claimant)
	},
}

var taskDoneOutput string

var taskDoneCmd = &cobra.Command{
	Use:   "done <task-id> [--output <file>]",
	Short: "Mark a task as done",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		t, err := task.Done(args[0], taskDoneOutput)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Task %s done\n", t.ID)
	},
}

var taskFailError string

var taskFailCmd = &cobra.Command{
	Use:   "fail <task-id> [--error <message>]",
	Short: "Mark a task as failed",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		t, err := task.Fail(args[0], taskFailError)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Task %s failed: %s\n", t.ID, taskFailError)
	},
}

var taskShowCmd = &cobra.Command{
	Use:   "show <task-id>",
	Short: "Show task details",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		t, err := task.Show(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("id: %s\n", t.ID)
		fmt.Printf("status: %s\n", t.Status)
		if t.Claimant != "" {
			fmt.Printf("claimant: %s\n", t.Claimant)
		}
		fmt.Printf("description: %s\n", t.Description)
		if t.SpecFile != "" {
			fmt.Printf("spec: %s\n", t.SpecFile)
		}
		if t.OutputFile != "" {
			fmt.Printf("output: %s\n", t.OutputFile)
		}
		if t.Error != "" {
			fmt.Printf("error: %s\n", t.Error)
		}
		fmt.Printf("created: %s\n", formatTime(t.CreatedAt))
		if t.ClaimedAt > 0 {
			fmt.Printf("claimed: %s\n", formatTime(t.ClaimedAt))
		}
		if t.FinishedAt > 0 {
			fmt.Printf("finished: %s\n", formatTime(t.FinishedAt))
		}
	},
}

func init() {
	// spawn flags
	spawnCmd.Flags().StringVar(&spawnSystem, "system", "", "System prompt file or inline text")
	spawnCmd.Flags().StringVar(&spawnInput, "input", "", "Input file path or inline text")
	spawnCmd.Flags().StringVar(&spawnMode, "mode", "headless", "Agent mode: headless (fire-and-forget) or rpc (bidirectional with events)")
	spawnCmd.Flags().StringVar(&spawnCwd, "cwd", "", "Working directory")
	spawnCmd.Flags().StringVar(&spawnTimeout, "timeout", "10m", "Timeout (e.g. 5m, 30s)")
	spawnCmd.Flags().BoolVar(&spawnMock, "mock", false, "Use mock agent (no LLM, for testing)")
	spawnCmd.Flags().StringVar(&spawnMockScript, "mock-script", "", "Mock script path (default: echo input back)")
	spawnCmd.Flags().String("id", "", "Agent ID (required)")
	_ = cobra.MarkFlagRequired(spawnCmd.Flags(), "id")

	// wait flags
	waitCmd.Flags().IntVar(&waitTimeout, "timeout", 600, "Timeout in seconds")

	// send flags
	sendCmd.Flags().StringVar(&sendFile, "file", "", "Send file contents")

	// recv flags
	recvCmd.Flags().BoolVar(&recvWait, "wait", false, "Block until a message arrives")
	recvCmd.Flags().IntVar(&recvTimeout, "timeout", 60, "Timeout in seconds (with --wait)")
	recvCmd.Flags().BoolVar(&recvAll, "all", false, "Receive all messages")

	// read flags
	readCmd.Flags().BoolVar(&readFollow, "follow", false, "Stream events in real-time")
	readCmd.Flags().BoolVar(&readRaw, "raw", false, "Output raw JSON lines")

	// channel subcommands
	channelCmd.AddCommand(channelCreateCmd, channelLsCmd, channelRmCmd)

	// task flags
	taskCreateCmd.Flags().StringVar(&taskSpecFile, "file", "", "Spec file path")
	taskListCmd.Flags().StringVar(&taskListStatus, "status", "", "Filter by status (pending|claimed|done|failed)")
	taskClaimCmd.Flags().StringVar(&taskClaimAs, "as", "", "Agent ID claiming the task")
	taskDoneCmd.Flags().StringVar(&taskDoneOutput, "output", "", "Output file path")
	taskFailCmd.Flags().StringVar(&taskFailError, "error", "unknown error", "Error message")

	// task subcommands
	taskCmd.AddCommand(taskCreateCmd, taskListCmd, taskClaimCmd, taskDoneCmd, taskFailCmd, taskShowCmd)
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
