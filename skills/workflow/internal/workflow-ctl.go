package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

const (
	WorkflowDir    = ".workflow"
	StateFile      = "STATE.json"
	SkillsDir      = "~/.ai/skills/workflow"
	TemplateDir    = "templates"
	RegistryFile   = "registry.json"
	ArtifactDir    = ".workflow/artifacts"
)

// State represents the workflow state
type State struct {
	Template     string   `json:"template"`
	TemplateName string   `json:"templateName"`
	Description  string   `json:"description"`
	Phases       []Phase  `json:"phases"`
	CurrentPhase int      `json:"currentPhase"`
	Status       string   `json:"status"`
	StartedAt    string   `json:"startedAt"`
	ArtifactDir  string   `json:"artifactDir"`
}

// Phase represents a workflow phase
type Phase struct {
	Name   string `json:"name"`
	Index  int    `json:"index"`
	Status string `json:"status"`
}

// Template represents a workflow template
type Template struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Phases      []string `json:"phases"`
	Category    string   `json:"category"`
}

// Registry represents the template registry
type Registry struct {
	Templates map[string]Template `json:"templates"`
}

var (
	skillsPath string
)

func init() {
	home, _ := os.UserHomeDir()
	skillsPath = filepath.Join(home, ".ai", "skills", "workflow")
}

func loadRegistry() (*Registry, error) {
	registryPath := filepath.Join(skillsPath, TemplateDir, RegistryFile)
	data, err := os.ReadFile(registryPath)
	if err != nil {
		return nil, err
	}
	var registry Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, err
	}
	return &registry, nil
}

func loadState() (*State, error) {
	statePath := filepath.Join(WorkflowDir, StateFile)
	data, err := os.ReadFile(statePath)
	if err != nil {
		return nil, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func saveState(state *State) error {
	if err := os.MkdirAll(WorkflowDir, 0755); err != nil {
		return err
	}
	statePath := filepath.Join(WorkflowDir, StateFile)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath, data, 0644)
}

var rootCmd = &cobra.Command{
	Use:   "workflow-ctl",
	Short: "Workflow state control tool",
}

var startCmd = &cobra.Command{
	Use:   "start <template> <description>",
	Short: "Start a new workflow",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		templateName := args[0]
		description := args[1]

		registry, err := loadRegistry()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load registry: %v\n", err)
			os.Exit(1)
		}

		template, ok := registry.Templates[templateName]
		if !ok {
			fmt.Fprintf(os.Stderr, "Template '%s' not found\n", templateName)
			os.Exit(1)
		}

		// Create artifact directory
		artifactDir := filepath.Join(ArtifactDir, template.Category, templateName)
		if err := os.MkdirAll(artifactDir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create artifact dir: %v\n", err)
			os.Exit(1)
		}

		// Create phases
		phases := make([]Phase, len(template.Phases))
		for i, name := range template.Phases {
			status := "pending"
			if i == 0 {
				status = "active"
			}
			phases[i] = Phase{Name: name, Index: i, Status: status}
		}

		state := &State{
			Template:     templateName,
			TemplateName: template.Name,
			Description:  description,
			Phases:       phases,
			CurrentPhase: 0,
			Status:       "in_progress",
			ArtifactDir:  artifactDir,
		}

		if err := saveState(state); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save state: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Workflow started: %s\n", template.Name)
		fmt.Printf("Phases: %v\n", template.Phases)
		fmt.Printf("Artifact dir: %s\n", artifactDir)
	},
}

var advanceCmd = &cobra.Command{
	Use:   "advance [--phase <name>]",
	Short: "Advance to next phase",
	Run: func(cmd *cobra.Command, args []string) {
		targetPhase, _ := cmd.Flags().GetString("phase")

		state, err := loadState()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load state: %v\n", err)
			os.Exit(1)
		}

		// If specific phase requested, jump to it
		if targetPhase != "" {
			found := false
			for i, phase := range state.Phases {
				if phase.Name == targetPhase {
					state.CurrentPhase = i
					state.Status = "in_progress"
					state.Phases[i].Status = "active"
					found = true
					break
				}
			}
			if !found {
				fmt.Fprintf(os.Stderr, "Phase '%s' not found\n", targetPhase)
				os.Exit(1)
			}
		} else {
			// Mark current phase as completed, move to next
			if state.CurrentPhase < len(state.Phases) {
				state.Phases[state.CurrentPhase].Status = "completed"
			}

			state.CurrentPhase++
			if state.CurrentPhase >= len(state.Phases) {
				state.Status = "completed"
				fmt.Println("All phases complete!")
			} else {
				state.Phases[state.CurrentPhase].Status = "active"
				fmt.Printf("Advanced to phase: %s\n", state.Phases[state.CurrentPhase].Name)
			}
		}

		if err := saveState(state); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save state: %v\n", err)
			os.Exit(1)
		}
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show workflow status",
	Run: func(cmd *cobra.Command, args []string) {
		state, err := loadState()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load state: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("Template: %s\n", state.TemplateName)
		fmt.Printf("Status:   %s\n", state.Status)
		fmt.Printf("Artifact: %s\n", state.ArtifactDir)
		fmt.Println("")
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
			}
			fmt.Printf("  %s [%d] %s (%s)\n", marker, i+1, phase.Name, phase.Status)
		}
	},
}

var pauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "Pause workflow",
	Run: func(cmd *cobra.Command, args []string) {
		state, err := loadState()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load state: %v\n", err)
			os.Exit(1)
		}
		state.Status = "paused"
		if err := saveState(state); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save state: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Workflow paused")
	},
}

var resumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Resume workflow",
	Run: func(cmd *cobra.Command, args []string) {
		state, err := loadState()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to load state: %v\n", err)
			os.Exit(1)
		}
		if state.Status != "paused" {
			fmt.Fprintf(os.Stderr, "Workflow is not paused\n")
			os.Exit(1)
		}
		state.Status = "in_progress"
		if err := saveState(state); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to save state: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Workflow resumed at phase: %s\n", state.Phases[state.CurrentPhase].Name)
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(advanceCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(pauseCmd)
	rootCmd.AddCommand(resumeCmd)
	advanceCmd.Flags().String("phase", "", "Jump to specific phase")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}