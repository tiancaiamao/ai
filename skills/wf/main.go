package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// === Types ===

const (
	stateFile = "STATE.json"
	auditFile = "AUDIT.jsonl"
	wfDir     = ".wf"
)

// State is the minimal gate state file.
type State struct {
	ID           string  `json:"id"`
	Description  string  `json:"description"`
	Phases       []Phase `json:"phases"`
	CurrentPhase int     `json:"currentPhase"`
	Status       string  `json:"status"` // in_progress, completed, paused
	CreatedAt    string  `json:"createdAt"`
	UpdatedAt    string  `json:"updatedAt"`
}

// Phase represents a single gate phase (design, plan, implement).
type Phase struct {
	Name           string `json:"name"`
	Gate           bool   `json:"gate"`
	Status         string `json:"status"` // pending, active, completed, failed
	Output         string `json:"output,omitempty"`
	GateApproved   bool   `json:"gateApproved,omitempty"`
	ApprovedAt     string `json:"approvedAt,omitempty"`
	ApproveMessage string `json:"approveMessage,omitempty"`
	Notes          string `json:"notes,omitempty"`
}

// AuditEvent for the audit log.
type AuditEvent struct {
	Timestamp string `json:"ts"`
	Event     string `json:"event"`
	Phase     string `json:"phase,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// === State I/O ===

func loadState() (*State, error) {
	p := filepath.Join(wfDir, stateFile)
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read state: %w (run 'wf init' first)", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	return &s, nil
}

func saveState(s *State) error {
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		return fmt.Errorf("create .wf dir: %w", err)
	}
	s.UpdatedAt = time.Now().Format(time.RFC3339)
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	return os.WriteFile(filepath.Join(wfDir, stateFile), data, 0644)
}

func audit(event, phase, detail string) {
	if err := os.MkdirAll(wfDir, 0755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(wfDir, auditFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	evt := AuditEvent{time.Now().Format(time.RFC3339), event, phase, detail}
	data, _ := json.Marshal(evt)
	f.WriteString(string(data) + "\n")
}

func currentPhase(s *State) *Phase {
	if s.CurrentPhase >= len(s.Phases) {
		return nil
	}
	return &s.Phases[s.CurrentPhase]
}

// === Commands ===

func runInit(cmd *cobra.Command, args []string) {
	desc := strings.Join(args, " ")

	phases := []Phase{
		{Name: "design", Gate: true, Status: "active"},
		{Name: "plan", Gate: true, Status: "pending"},
		{Name: "implement", Gate: false, Status: "pending"},
	}

	s := &State{
		ID:           fmt.Sprintf("wf-%d", time.Now().Unix()),
		Description:  desc,
		Phases:       phases,
		CurrentPhase: 0,
		Status:       "in_progress",
		CreatedAt:    time.Now().Format(time.RFC3339),
	}
	if err := saveState(s); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	audit("init", "", desc)
	fmt.Printf("Initialized: %s\n", desc)
	fmt.Printf("Phases: design → plan → implement\n")
}

func runStatus(cmd *cobra.Command, args []string) {
	asJSON, _ := cmd.Flags().GetBool("json")
	s, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if asJSON {
		data, _ := json.MarshalIndent(s, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Printf("Workflow: %s\n", s.Description)
	fmt.Printf("Status:   %s\n\n", s.Status)

	for i, p := range s.Phases {
		marker := "○"
		switch p.Status {
		case "active":
			marker = "▶"
		case "completed":
			marker = "✓"
		case "failed":
			marker = "✗"
		}

		gate := ""
		if p.Gate {
			if p.GateApproved {
				gate = " [approved]"
			} else {
				gate = " [gate]"
			}
		}

		output := ""
		if p.Output != "" {
			output = fmt.Sprintf(" → %s", p.Output)
		}

		fmt.Printf("  %s %d. %s%s%s\n", marker, i+1, p.Name, gate, output)
		if p.Notes != "" {
			for _, line := range strings.Split(p.Notes, "\n") {
				fmt.Printf("     %s\n", line)
			}
		}
	}
}

func runApprove(cmd *cobra.Command, args []string) {
	message, _ := cmd.Flags().GetString("message")
	if message == "" {
		fmt.Fprintf(os.Stderr, "Error: --message is required. Pass the user's actual confirmation words.\n")
		fmt.Fprintf(os.Stderr, "Example: wf approve --message \"方案可以\"\n")
		os.Exit(1)
	}

	s, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if s.Status == "completed" {
		fmt.Fprintf(os.Stderr, "Error: workflow already completed\n")
		os.Exit(1)
	}

	cp := currentPhase(s)
	if cp == nil {
		fmt.Fprintf(os.Stderr, "Error: no active phase\n")
		os.Exit(1)
	}

	if !cp.Gate {
		fmt.Fprintf(os.Stderr, "Error: phase '%s' does not have a gate\n", cp.Name)
		os.Exit(1)
	}

	cp.GateApproved = true
	cp.ApprovedAt = time.Now().Format(time.RFC3339)
	cp.ApproveMessage = message

	if err := saveState(s); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	audit("approve", cp.Name, message)
	fmt.Printf("✅ Gate '%s' approved\n", cp.Name)
}

func runReject(cmd *cobra.Command, args []string) {
	feedback := strings.Join(args, " ")

	s, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cp := currentPhase(s)
	if cp == nil {
		fmt.Fprintf(os.Stderr, "Error: no active phase\n")
		os.Exit(1)
	}

	cp.GateApproved = false
	cp.ApprovedAt = ""
	cp.ApproveMessage = ""
	if feedback != "" {
		cp.Notes += "\n[rejected] " + feedback
	}

	if err := saveState(s); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	audit("reject", cp.Name, feedback)
	fmt.Printf("❌ Gate '%s' rejected: %s\n", cp.Name, feedback)
}

func runAdvance(cmd *cobra.Command, args []string) {
	outputFile, _ := cmd.Flags().GetString("output")

	s, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if s.Status == "completed" {
		fmt.Fprintf(os.Stderr, "Error: workflow already completed\n")
		os.Exit(1)
	}

	cp := currentPhase(s)
	if cp == nil {
		fmt.Fprintf(os.Stderr, "Error: no active phase\n")
		os.Exit(1)
	}

	// Hard gate check
	if cp.Gate && !cp.GateApproved {
		fmt.Fprintf(os.Stderr, "❌ Gate \"%s\" not approved. Run: wf approve --message \"<user's words>\"\n", cp.Name)
		os.Exit(1)
	}

	// Validate output file exists
	target := outputFile
	if target == "" {
		target = cp.Output
	}
	if target != "" {
		if _, err := os.Stat(target); err != nil {
			fmt.Fprintf(os.Stderr, "❌ Output file does not exist: %s\n", target)
			os.Exit(1)
		}
	}

	cp.Status = "completed"
	if outputFile != "" {
		cp.Output = outputFile
	}

	audit("advance", cp.Name, fmt.Sprintf("output=%s", outputFile))

	s.CurrentPhase++
	if s.CurrentPhase >= len(s.Phases) {
		s.Status = "completed"
		if err := saveState(s); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		audit("complete", "", "")
		fmt.Println("✅ All phases complete!")
		return
	}

	next := &s.Phases[s.CurrentPhase]
	next.Status = "active"
	if err := saveState(s); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Advanced to: %s\n", next.Name)
	if next.Gate {
		fmt.Println("This phase requires approval before advancing.")
	}
}

func runNote(cmd *cobra.Command, args []string) {
	text := strings.Join(args, " ")

	s, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cp := currentPhase(s)
	if cp == nil {
		fmt.Fprintf(os.Stderr, "Error: no active phase\n")
		os.Exit(1)
	}

	cp.Notes += "\n" + text
	if err := saveState(s); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	audit("note", cp.Name, text)
	fmt.Printf("📝 Note added to '%s'\n", cp.Name)
}

func runBack(cmd *cobra.Command, args []string) {
	steps := 1
	if len(args) > 0 {
		_, err := fmt.Sscanf(args[0], "%d", &steps)
		if err != nil || steps < 1 {
			fmt.Fprintf(os.Stderr, "Error: argument must be a positive integer\n")
			os.Exit(1)
		}
	}

	s, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	target := s.CurrentPhase - steps
	if target < 0 {
		fmt.Fprintf(os.Stderr, "Error: cannot go back %d phases (current: %d)\n", steps, s.CurrentPhase)
		os.Exit(1)
	}

	// Reset phases after target
	for i := target + 1; i < len(s.Phases); i++ {
		s.Phases[i].Status = "pending"
		s.Phases[i].Output = ""
		s.Phases[i].GateApproved = false
		s.Phases[i].ApprovedAt = ""
		s.Phases[i].ApproveMessage = ""
	}

	s.Phases[target].Status = "active"
	s.CurrentPhase = target
	s.Status = "in_progress"

	if err := saveState(s); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	audit("back", s.Phases[target].Name, fmt.Sprintf("steps=%d", steps))
	fmt.Printf("Rolled back to: %s\n", s.Phases[target].Name)
}

// === Main ===

func main() {
	rootCmd := &cobra.Command{
		Use:   "wf",
		Short: "Gate control for brainstorm → plan → implement workflow",
	}

	initCmd := &cobra.Command{
		Use:   "init [description]",
		Short: "Initialize workflow with design → plan → implement phases",
		Args:  cobra.MinimumNArgs(1),
		Run:   runInit,
	}
	statusCmd := &cobra.Command{
		Use:   "status [--json]",
		Short: "Show current workflow state",
		Run:   runStatus,
	}
	statusCmd.Flags().Bool("json", false, "Output as JSON")

	approveCmd := &cobra.Command{
		Use:   "approve --message <user-words>",
		Short: "Approve current gate (requires user's actual confirmation words)",
		Run:   runApprove,
	}
	approveCmd.Flags().String("message", "", "User's confirmation message (required)")

	rejectCmd := &cobra.Command{
		Use:   "reject [feedback]",
		Short: "Reject current gate with feedback",
		Args:  cobra.MinimumNArgs(1),
		Run:   runReject,
	}

	advanceCmd := &cobra.Command{
		Use:   "advance [--output file]",
		Short: "Advance to next phase (validates gate is approved)",
		Run:   runAdvance,
	}
	advanceCmd.Flags().String("output", "", "Output artifact file path")

	noteCmd := &cobra.Command{
		Use:   "note <text>",
		Short: "Add progress note to current phase",
		Args:  cobra.MinimumNArgs(1),
		Run:   runNote,
	}

	backCmd := &cobra.Command{
		Use:   "back [steps]",
		Short: "Roll back to a previous phase",
		Args:  cobra.MaximumNArgs(1),
		Run:   runBack,
	}

	rootCmd.AddCommand(initCmd, statusCmd, approveCmd, rejectCmd, advanceCmd, noteCmd, backCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}