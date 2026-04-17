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

func runStart(cmd *cobra.Command, args []string) {
	templateName := args[0]
	description := args[1]

	id, template, err := resolveTemplate(templateName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	artifactDir := filepath.Join(WorkflowDir, ArtifactDir, id)
	if err := os.MkdirAll(artifactDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error: create artifact dir: %v\n", err)
		os.Exit(1)
	}

	phases := make([]Phase, len(template.Phases))
	for i, tp := range template.Phases {
		status := "pending"
		if i == 0 {
			status = "active"
		}
		phases[i] = Phase{
			Name:   tp.Name,
			Skill:  tp.Skill,
			Gate:   tp.Gate,
			Status: status,
		}
	}

	state := &State{
		SchemaVersion: CurrentSchemaVersion,
		ID:            fmt.Sprintf("wf-%s-%d", id, time.Now().Unix()),
		Template:      id,
		TemplateName:  template.Name,
		Description:   description,
		Phases:        phases,
		CurrentPhase:  0,
		Status:        "in_progress",
		StartedAt:     time.Now().Format(time.RFC3339),
		ArtifactDir:   artifactDir,
	}

	if err := saveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	audit("start", "", fmt.Sprintf("template=%s description=%s", id, description))

	fmt.Printf("Started: %s\n", template.Name)
	fmt.Printf("Phases:  %s\n", phaseFlow(phases))
	fmt.Printf("Artifacts: %s\n", artifactDir)
}

func runAdvance(cmd *cobra.Command, args []string) {
	outputFile, _ := cmd.Flags().GetString("output")
	force, _ := cmd.Flags().GetBool("force")

	state, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := validateWorkflowAction(state, "advance"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cp := currentPhase(state)
	if cp == nil {
		fmt.Fprintf(os.Stderr, "Error: no active phase\n")
		os.Exit(1)
	}

	// Gate check: require approval for gate phases unless --force
	if cp.Gate && !cp.GateApproved && !force {
		fmt.Fprintf(os.Stderr, "Error: phase '%s' requires gate approval before advancing.\n", cp.Name)
		fmt.Fprintf(os.Stderr, "Run 'workflow-ctl approve' first, or use --force to skip.\n")
		os.Exit(1)
	}

	// Validate output file exists (unless --force)
	if !force {
		if err := validateAdvanceOutput(cp, outputFile); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			fmt.Fprintf(os.Stderr, "Use --force to skip this check.\n")
			os.Exit(1)
		}
	}

	// Validate transition
	if err := validateTransition(cp.Status, "completed"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cp.Status = "completed"
	if outputFile != "" {
		cp.Output = outputFile
	}

	audit("advance", cp.Name, fmt.Sprintf("output=%s", outputFile))

	state.CurrentPhase++
	if state.CurrentPhase >= len(state.Phases) {
		state.Status = "completed"
		if err := saveState(state); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		audit("complete", "", "")
		fmt.Println("All phases complete!")
		return
	}

	next := &state.Phases[state.CurrentPhase]
	next.Status = "active"
	if err := saveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Advanced to: %s (skill: %s)\n", next.Name, next.Skill)
	if next.Gate {
		fmt.Println("This phase requires approval before advancing.")
	}
	fmt.Printf("Artifacts: %s\n", state.ArtifactDir)
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

	state, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := validateWorkflowAction(state, "back"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	target := state.CurrentPhase - steps
	if target < 0 {
		fmt.Fprintf(os.Stderr, "Error: cannot go back %d phases (current: %d)\n", steps, state.CurrentPhase)
		os.Exit(1)
	}

	// Reset phases after target to pending, but preserve Output as PreviousOutput
	for i := target + 1; i < len(state.Phases); i++ {
		p := &state.Phases[i]
		if p.Output != "" {
			p.PreviousOutput = p.Output
		}
		p.Status = "pending"
		p.Output = ""
		p.GateApproved = false
		p.ApprovedAt = ""
		p.Notes = ""
	}

	// Activate target phase
	targetPhase := &state.Phases[target]
	targetPhase.Status = "active"
	targetPhase.GateApproved = false
	targetPhase.ApprovedAt = ""
	// Preserve notes on the target phase — they provide context for re-doing the phase
	// Notes from subsequent phases are already cleared above
	state.CurrentPhase = target
	state.Status = "in_progress"

	if err := saveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	audit("back", targetPhase.Name, fmt.Sprintf("steps=%d", steps))

	fmt.Printf("Rolled back to: %s (phase %d/%d)\n", targetPhase.Name, target+1, len(state.Phases))
	fmt.Printf("Reset phases: %s\n", phaseNames(state.Phases[target+1:]))
}

func runApprove(cmd *cobra.Command, args []string) {
	state, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := validateWorkflowAction(state, "approve"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cp := currentPhase(state)
	if !cp.Gate {
		fmt.Fprintf(os.Stderr, "Error: phase '%s' does not have a gate\n", cp.Name)
		os.Exit(1)
	}
	if cp.Status != "active" {
		fmt.Fprintf(os.Stderr, "Error: phase '%s' is not active (status: %s)\n", cp.Name, cp.Status)
		os.Exit(1)
	}

	cp.GateApproved = true
	cp.ApprovedAt = time.Now().Format(time.RFC3339)
	if err := saveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	audit("approve", cp.Name, "")

	fmt.Printf("Approved: %s — ready to advance\n", cp.Name)
}

func runReject(cmd *cobra.Command, args []string) {
	feedback := ""
	if len(args) > 0 {
		feedback = strings.Join(args, " ")
	}

	state, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := validateWorkflowAction(state, "reject"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cp := currentPhase(state)
	if !cp.Gate {
		fmt.Fprintf(os.Stderr, "Error: phase '%s' does not have a gate\n", cp.Name)
		os.Exit(1)
	}

	cp.GateApproved = false
	// Append feedback to notes (don't overwrite existing notes)
	if feedback != "" {
		if cp.Notes != "" {
			cp.Notes += "\n"
		}
		cp.Notes += "[reject] " + feedback
	}
	if err := saveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	audit("reject", cp.Name, feedback)

	fmt.Printf("Rejected: %s — phase remains active for revision\n", cp.Name)
	if feedback != "" {
		fmt.Printf("Feedback: %s\n", feedback)
	}
}

func runSkip(cmd *cobra.Command, args []string) {
	reason := ""
	if len(args) > 0 {
		reason = strings.Join(args, " ")
	}

	state, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := validateWorkflowAction(state, "skip"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cp := currentPhase(state)

	// Validate transition
	if err := validateTransition(cp.Status, "skipped"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cp.Status = "skipped"
	// Append skip reason to notes (don't overwrite existing notes)
	if reason != "" {
		if cp.Notes != "" {
			cp.Notes += "\n"
		}
		cp.Notes += "[skip] " + reason
	}

	audit("skip", cp.Name, reason)

	state.CurrentPhase++
	if state.CurrentPhase >= len(state.Phases) {
		state.Status = "completed"
		if err := saveState(state); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		audit("complete", "", "")
		fmt.Println("All phases complete!")
		return
	}

	next := &state.Phases[state.CurrentPhase]
	next.Status = "active"
	if err := saveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Skipped: %s — advanced to: %s (skill: %s)\n", cp.Name, next.Name, next.Skill)
	if next.Gate {
		fmt.Println("This phase requires approval before advancing.")
	}
	fmt.Printf("Artifacts: %s\n", state.ArtifactDir)
}

func runNote(cmd *cobra.Command, args []string) {
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Error: note text required\n")
		os.Exit(1)
	}
	text := strings.Join(args, " ")

	state, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := validateWorkflowAction(state, "note"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cp := currentPhase(state)
	if cp.Notes != "" {
		cp.Notes += "\n"
	}
	cp.Notes += text
	if err := saveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Note added to phase '%s'\n", cp.Name)
}

func runFail(cmd *cobra.Command, args []string) {
	state, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := validateWorkflowAction(state, "fail"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	reason := "no reason given"
	if len(args) > 0 {
		reason = strings.Join(args, " ")
	}

	cp := currentPhase(state)
	cp.Status = "failed"
	state.Status = "failed"
	if err := saveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	audit("fail", cp.Name, reason)

	fmt.Printf("Phase '%s' marked as failed: %s\n", cp.Name, reason)
	fmt.Println("Fix the issue and run 'workflow-ctl retry' to re-activate this phase.")
}

func runRetry(cmd *cobra.Command, args []string) {
	state, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := validateWorkflowAction(state, "retry"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	cp := currentPhase(state)
	cp.Status = "active"
	state.Status = "in_progress"
	if err := saveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	audit("retry", cp.Name, "")

	fmt.Printf("Retrying phase: %s\n", cp.Name)
}

func runStatus(cmd *cobra.Command, args []string) {
	asJSON, _ := cmd.Flags().GetBool("json")

	state, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if asJSON {
		data, _ := json.MarshalIndent(state, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Printf("Workflow:  %s\n", state.TemplateName)
	fmt.Printf("Template:  %s\n", state.Template)
	fmt.Printf("Status:    %s\n", state.Status)
	fmt.Printf("Artifacts: %s\n", state.ArtifactDir)
	if state.SchemaVersion > 0 {
		fmt.Printf("Schema:    v%d\n", state.SchemaVersion)
	}
	fmt.Println()
	fmt.Println("Phases:")

	for i, phase := range state.Phases {
		marker := "○"
		switch phase.Status {
		case "completed":
			marker = "✓"
		case "active":
			marker = "▶"
		case "failed":
			marker = "✗"
		case "skipped":
			marker = "⏭"
		}
		gate := ""
		if phase.Gate {
			if phase.GateApproved {
				gate = " [approved]"
			} else if phase.Status == "active" {
				gate = " [gate: awaiting approval]"
			} else if phase.Status == "pending" {
				gate = " [gate]"
			}
		}
		skill := fmt.Sprintf(" (skill: %s)", phase.Skill)
		fmt.Printf("  %s [%d] %s — %s%s%s\n", marker, i+1, phase.Name, phase.Status, skill, gate)
		if phase.Output != "" {
			fmt.Printf("      output: %s\n", phase.Output)
		}
		if phase.PreviousOutput != "" {
			fmt.Printf("      prev:   %s\n", phase.PreviousOutput)
		}
		if phase.ApprovedAt != "" {
			fmt.Printf("      approved at: %s\n", phase.ApprovedAt)
		}
		if phase.Notes != "" {
			lines := strings.Split(phase.Notes, "\n")
			last := lines[len(lines)-1]
			if len(last) > 80 {
				last = last[:77] + "..."
			}
			fmt.Printf("      notes: %s\n", last)
			if len(lines) > 1 {
				fmt.Printf("      (%d more note(s))\n", len(lines)-1)
			}
		}
	}
}

func runPause(cmd *cobra.Command, args []string) {
	state, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := validateWorkflowAction(state, "pause"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	state.Status = "paused"
	if err := saveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	audit("pause", currentPhase(state).Name, "")
	fmt.Printf("Paused at phase: %s\n", currentPhase(state).Name)
}

func runResume(cmd *cobra.Command, args []string) {
	state, err := loadState()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	if err := validateWorkflowAction(state, "resume"); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	state.Status = "in_progress"
	if err := saveState(state); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	audit("resume", currentPhase(state).Name, "")
	fmt.Printf("Resumed at phase: %s\n", currentPhase(state).Name)
}

func runTemplates(cmd *cobra.Command, args []string) {
	registry, err := loadRegistry()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if len(args) > 0 {
		id, t, err := resolveTemplate(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Template: %s (%s)\n", t.Name, id)
		fmt.Printf("Description: %s\n", t.Description)
		fmt.Printf("Complexity: %s\n", t.Complexity)
		fmt.Printf("Phases:\n")
		for _, p := range t.Phases {
			gate := ""
			if p.Gate {
				gate = " [gate]"
			}
			fmt.Printf("  - %s (skill: %s)%s\n", p.Name, p.Skill, gate)
		}
		if len(t.Aliases) > 0 {
			fmt.Printf("Aliases: %s\n", strings.Join(t.Aliases, ", "))
		}
		return
	}

	fmt.Println("Available templates:")
	fmt.Println()
	for id, t := range registry.Templates {
		phases := make([]string, len(t.Phases))
		for i, p := range t.Phases {
			phases[i] = p.Name
		}
		fmt.Printf("  %-12s %s\n", id, t.Name)
		fmt.Printf("  %-12s %s\n", "", t.Description)
		fmt.Printf("  %-12s %s → %s\n", "", t.Complexity, strings.Join(phases, " → "))
		fmt.Println()
	}
}