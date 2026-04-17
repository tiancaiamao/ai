package main

import (
	"fmt"
	"os"
)

// === State Transition Guards ===

// validTransitions defines allowed phase status transitions.
var validTransitions = map[string]map[string]bool{
	"pending":   {"active": true},
	"active":    {"completed": true, "failed": true, "skipped": true},
	"completed": {}, // terminal (unless back)
	"failed":    {"active": true},
	"skipped":   {}, // terminal (unless back)
}

// validateTransition checks if moving from->to is allowed.
func validateTransition(from, to string) error {
	allowed, ok := validTransitions[from]
	if !ok {
		return fmt.Errorf("unknown phase status: %s", from)
	}
	if !allowed[to] {
		return fmt.Errorf("invalid transition: %s → %s", from, to)
	}
	return nil
}

// validateWorkflowAction checks if an action is valid for the current workflow state.
func validateWorkflowAction(state *State, action string) error {
	switch action {
	case "advance", "approve", "reject", "note", "fail", "skip":
		if state.Status == "completed" {
			return fmt.Errorf("workflow already completed")
		}
		if state.Status == "paused" {
			return fmt.Errorf("workflow is paused. Run 'resume' first")
		}
		if state.Status == "failed" && action != "fail" {
			return fmt.Errorf("workflow is in failed state. Run 'retry' first")
		}
	case "back":
		if state.CurrentPhase == 0 {
			return fmt.Errorf("already at first phase, cannot go back")
		}
	case "pause":
		if state.Status != "in_progress" {
			return fmt.Errorf("cannot pause (current status: %s)", state.Status)
		}
	case "resume":
		if state.Status != "paused" {
			return fmt.Errorf("workflow is not paused (current: %s)", state.Status)
		}
	case "retry":
		if state.Status != "failed" {
			return fmt.Errorf("workflow is not in failed state (current: %s)", state.Status)
		}
	}
	return nil
}

// currentPhase returns a pointer to the currently active phase.
func currentPhase(state *State) *Phase {
	if state.CurrentPhase >= len(state.Phases) {
		return nil
	}
	return &state.Phases[state.CurrentPhase]
}

// validateAdvanceOutput checks that the output file exists for the current phase.
func validateAdvanceOutput(phase *Phase, outputFile string) error {
	target := outputFile
	if target == "" {
		target = phase.Output
	}
	if target == "" {
		return nil // no output declared, skip check
	}
	if _, err := os.Stat(target); err != nil {
		return fmt.Errorf("output file does not exist: %s", target)
	}
	return nil
}