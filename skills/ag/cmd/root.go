package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/channel"
	"github.com/genius/ag/internal/storage"
	"github.com/genius/ag/internal/task"
	"github.com/genius/ag/internal/team"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
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
	// Ensure runtime storage exists for the currently selected team context.
	rootCmd.PersistentPreRun = func(cmd *cobra.Command, args []string) {
		baseDir, _, err := team.ResolveBaseDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error resolving team context: %v\n", err)
			os.Exit(1)
		}
		storage.SetBaseDir(baseDir)
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
	rootCmd.AddCommand(teamCmd)
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

// ========== Team Commands ==========

var teamCmd = &cobra.Command{
	Use:   "team",
	Short: "Manage team runtime context",
}

var teamDescription string

var teamInitCmd = &cobra.Command{
	Use:   "init <team-id>",
	Short: "Initialize a team workspace and make it current",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		meta, err := team.Init(args[0], teamDescription)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Team initialized: %s\n", meta.ID)
	},
}

var teamUseCmd = &cobra.Command{
	Use:   "use <team-id>",
	Short: "Switch current team context",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if err := team.Use(args[0]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Current team: %s\n", args[0])
	},
}

var teamCurrentCmd = &cobra.Command{
	Use:   "current",
	Short: "Show the current team context",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		current, err := team.Current()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if current == "" {
			fmt.Println("(none)")
			return
		}
		fmt.Println(current)
	},
}

var teamListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List teams",
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		teams, err := team.List()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(teams) == 0 {
			fmt.Println("No teams")
			return
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "TEAM\tCURRENT\tSTATUS\tRUNNING\tTASKS\tPENDING")
		for _, item := range teams {
			current := "-"
			if item.Current {
				current = "*"
			}
			fmt.Fprintf(
				w,
				"%s\t%s\t%s\t%d\t%d\t%d\n",
				item.ID,
				current,
				item.Status,
				item.RunningAgents,
				item.TasksTotal,
				item.TasksPending,
			)
		}
		w.Flush()
	},
}

var teamDoneName string

var teamDoneCmd = &cobra.Command{
	Use:   "done",
	Short: "Mark a team as done (defaults to current team)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		meta, err := team.Done(teamDoneName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Team marked done: %s\n", meta.ID)
	},
}

var (
	teamCleanupName  string
	teamCleanupForce bool
)

var teamCleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Delete a team workspace (defaults to current team)",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		if err := team.Cleanup(teamCleanupName, teamCleanupForce); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if teamCleanupName == "" {
			fmt.Println("Current team cleaned up")
		} else {
			fmt.Printf("Team cleaned up: %s\n", teamCleanupName)
		}
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
		fmt.Fprintln(w, "ID\tSTATUS\tCLAIMANT\tBLOCKED_BY\tDESCRIPTION")
		for _, t := range tasks {
			desc := t.Description
			if len(desc) > 60 {
				desc = desc[:57] + "..."
			}
			claimant := "-"
			if t.Claimant != "" {
				claimant = t.Claimant
			}
			blockedBy := "-"
			unmet, err := task.UnmetDependencies(t.ID)
			if err == nil && len(unmet) > 0 {
				blockedBy = strings.Join(unmet, ",")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Status, claimant, blockedBy, desc)
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

var taskNextAs string

var taskNextCmd = &cobra.Command{
	Use:   "next [--as <agent-id>]",
	Short: "Claim the next pending, unblocked task",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		agentID := taskNextAs
		if agentID == "" {
			agentID = os.Getenv("AG_AGENT_ID")
		}
		if agentID == "" {
			agentID = "manual"
		}
		t, err := task.Next(agentID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("%s\t%s\n", t.ID, t.Description)
	},
}

var taskDepCmd = &cobra.Command{
	Use:   "dep",
	Short: "Manage task dependencies",
}

var taskDepAddCmd = &cobra.Command{
	Use:   "add <task-id> <dep-id>",
	Short: "Add dependency: <task-id> depends on <dep-id>",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		t, err := task.AddDependency(args[0], args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(t.Dependencies) == 0 {
			fmt.Printf("Task %s has no dependencies\n", t.ID)
			return
		}
		fmt.Printf("Task %s dependencies: %s\n", t.ID, strings.Join(t.Dependencies, ", "))
	},
}

var taskDepRmCmd = &cobra.Command{
	Use:   "rm <task-id> <dep-id>",
	Short: "Remove dependency from a task",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		t, err := task.RemoveDependency(args[0], args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(t.Dependencies) == 0 {
			fmt.Printf("Task %s has no dependencies\n", t.ID)
			return
		}
		fmt.Printf("Task %s dependencies: %s\n", t.ID, strings.Join(t.Dependencies, ", "))
	},
}

var taskDepLsCmd = &cobra.Command{
	Use:     "ls <task-id>",
	Aliases: []string{"list"},
	Short:   "List dependencies and their status for a task",
	Args:    cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		deps, err := task.Dependencies(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if len(deps) == 0 {
			fmt.Println("No dependencies")
			return
		}
		unmet, _ := task.UnmetDependencies(args[0])
		unmetSet := map[string]bool{}
		for _, dep := range unmet {
			unmetSet[dep] = true
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "DEP_ID\tSTATUS")
		for _, depID := range deps {
			status := "done"
			if unmetSet[depID] {
				status = "blocked"
			}
			fmt.Fprintf(w, "%s\t%s\n", depID, status)
		}
		w.Flush()
	},
}

type planImportDoc struct {
	Metadata struct {
		SpecFile string `yaml:"spec_file"`
	} `yaml:"metadata"`
	Tasks []planImportTask `yaml:"tasks"`
}

type planImportTask struct {
	ID           string   `yaml:"id"`
	Title        string   `yaml:"title"`
	Description  string   `yaml:"description"`
	Dependencies []string `yaml:"dependencies"`
}

var taskImportSpec string

var taskImportPlanCmd = &cobra.Command{
	Use:   "import-plan <plan.yml>",
	Short: "Import tasks and dependencies from PLAN.yml",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		planPath := args[0]
		data, err := os.ReadFile(planPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading plan file: %v\n", err)
			os.Exit(1)
		}

		var doc planImportDoc
		if err := yaml.Unmarshal(data, &doc); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing plan yaml: %v\n", err)
			os.Exit(1)
		}
		if len(doc.Tasks) == 0 {
			fmt.Fprintln(os.Stderr, "Error: no tasks found in plan")
			os.Exit(1)
		}

		specFile := strings.TrimSpace(taskImportSpec)
		if specFile == "" {
			specFile = strings.TrimSpace(doc.Metadata.SpecFile)
		}

		created := 0
		for _, pt := range doc.Tasks {
			taskID := strings.TrimSpace(pt.ID)
			if taskID == "" {
				fmt.Fprintln(os.Stderr, "Error: plan task missing id")
				os.Exit(1)
			}
			desc := strings.TrimSpace(pt.Title)
			if strings.TrimSpace(pt.Description) != "" {
				if desc != "" {
					desc = desc + " - " + strings.TrimSpace(pt.Description)
				} else {
					desc = strings.TrimSpace(pt.Description)
				}
			}
			if desc == "" {
				desc = fmt.Sprintf("Task %s", taskID)
			}

			if _, err := task.CreateWithID(taskID, desc, specFile); err != nil {
				fmt.Fprintf(os.Stderr, "Error importing task %s: %v\n", taskID, err)
				os.Exit(1)
			}
			created++
		}

		for _, pt := range doc.Tasks {
			taskID := strings.TrimSpace(pt.ID)
			for _, depID := range pt.Dependencies {
				depID = strings.TrimSpace(depID)
				if depID == "" {
					continue
				}
				if _, err := task.AddDependency(taskID, depID); err != nil {
					fmt.Fprintf(os.Stderr, "Error adding dependency %s -> %s: %v\n", taskID, depID, err)
					os.Exit(1)
				}
			}
		}

		fmt.Printf("Imported %d task(s) from %s\n", created, planPath)
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
		if len(t.Dependencies) > 0 {
			fmt.Printf("dependencies: %s\n", strings.Join(t.Dependencies, ", "))
			if unmet, err := task.UnmetDependencies(t.ID); err == nil && len(unmet) > 0 {
				fmt.Printf("blocked_by: %s\n", strings.Join(unmet, ", "))
			}
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

	// team flags
	teamInitCmd.Flags().StringVar(&teamDescription, "description", "", "Team description")
	teamDoneCmd.Flags().StringVar(&teamDoneName, "team", "", "Team ID (defaults to current)")
	teamCleanupCmd.Flags().StringVar(&teamCleanupName, "team", "", "Team ID (defaults to current)")
	teamCleanupCmd.Flags().BoolVar(&teamCleanupForce, "force", false, "Force cleanup even with running agents")

	// team subcommands
	teamCmd.AddCommand(teamInitCmd, teamUseCmd, teamCurrentCmd, teamListCmd, teamDoneCmd, teamCleanupCmd)

	// task flags
	taskCreateCmd.Flags().StringVar(&taskSpecFile, "file", "", "Spec file path")
	taskImportPlanCmd.Flags().StringVar(&taskImportSpec, "spec", "", "Optional SPEC.md path override")
	taskListCmd.Flags().StringVar(&taskListStatus, "status", "", "Filter by status (pending|claimed|done|failed)")
	taskClaimCmd.Flags().StringVar(&taskClaimAs, "as", "", "Agent ID claiming the task")
	taskNextCmd.Flags().StringVar(&taskNextAs, "as", "", "Agent ID claiming the task")
	taskDoneCmd.Flags().StringVar(&taskDoneOutput, "output", "", "Output file path")
	taskFailCmd.Flags().StringVar(&taskFailError, "error", "unknown error", "Error message")

	// task dependency subcommands
	taskDepCmd.AddCommand(taskDepAddCmd, taskDepRmCmd, taskDepLsCmd)

	// task subcommands
	taskCmd.AddCommand(taskCreateCmd, taskImportPlanCmd, taskListCmd, taskClaimCmd, taskNextCmd, taskDoneCmd, taskFailCmd, taskShowCmd, taskDepCmd)
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
