package orchestrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WorkerPrompt generates a prompt for a worker to execute a task
func WorkerPrompt(task *Task, workflow *Workflow, cwd string) string {
	var sb strings.Builder

	sb.WriteString("# Task Assignment\n\n")
	sb.WriteString(fmt.Sprintf("You are assigned to complete the following task:\n\n"))
	sb.WriteString(fmt.Sprintf("**Task ID:** %s\n", task.ID))
	sb.WriteString(fmt.Sprintf("**Subject:** %s\n", task.Subject))
	sb.WriteString(fmt.Sprintf("**Status:** %s\n", task.Status))
	sb.WriteString(fmt.Sprintf("**Worker:** %s\n\n", task.ClaimedBy))

	if task.Description != "" {
		sb.WriteString("## Description\n\n")
		sb.WriteString(task.Description)
		sb.WriteString("\n\n")
	}

	// Dependencies
	if len(task.BlockedBy) > 0 {
		sb.WriteString("## Dependencies\n\n")
		sb.WriteString("This task depends on the following tasks being completed:\n")
		for _, depID := range task.BlockedBy {
			sb.WriteString(fmt.Sprintf("- %s\n", depID))
		}
		sb.WriteString("\n")
	}

	// Workflow context
	if workflow != nil {
		sb.WriteString("## Workflow Context\n\n")
		sb.WriteString(fmt.Sprintf("Workflow: %s\n", workflow.Name))
		if workflow.Description != "" {
			sb.WriteString(fmt.Sprintf("Description: %s\n", workflow.Description))
		}
		sb.WriteString("\n")
	}

	// Instructions
	sb.WriteString("## Instructions\n\n")
	sb.WriteString("1. Read the task description carefully\n")
	sb.WriteString("2. Check the dependencies (read outputs in `.ai/team/outbox/`)\n")
	sb.WriteString("3. Complete the task\n")
	sb.WriteString("4. Write your output to `.ai/team/outbox/<task-id>.md`\n")
	sb.WriteString("5. Call `orchestrate api complete-task` with your summary\n")
	sb.WriteString("\n")

	// API commands
	sb.WriteString("## API Commands\n\n")
	sb.WriteString("```bash\n")
	sb.WriteString("# Update task (add notes, change description)\n")
	sb.WriteString(fmt.Sprintf("orchestrate api update-task --input '{\"task_id\":\"%s\",\"description\":\"updated description\"}'\n", task.ID))
	sb.WriteString("\n")
	sb.WriteString("# Complete task\n")
	sb.WriteString(fmt.Sprintf("orchestrate api complete-task --input '{\"task_id\":\"%s\",\"claim_token\":\"%s\",\"summary\":\"Task completed successfully\"}'\n", task.ID, task.ClaimToken))
	sb.WriteString("\n")
	sb.WriteString("# Fail task (if blocked)\n")
	sb.WriteString(fmt.Sprintf("orchestrate api fail-task --input '{\"task_id\":\"%s\",\"claim_token\":\"%s\",\"error\":\"Error message\"}'\n", task.ID, task.ClaimToken))
	sb.WriteString("```\n")
	sb.WriteString("\n")

	// Output location
	sb.WriteString("## Output\n\n")
	outboxPath := filepath.Join(cwd, ".ai", "team", "outbox", task.ID+".md")
	sb.WriteString(fmt.Sprintf("Write your output to: `%s`\n", outboxPath))
	sb.WriteString("\n")
	sb.WriteString("Format:\n")
	sb.WriteString("```markdown\n")
	sb.WriteString("# <Task Subject>\n\n")
	sb.WriteString("## Summary\n<Brief summary of what was done>\n\n")
	sb.WriteString("## Changes\n<List of files changed or actions taken>\n\n")
	sb.WriteString("## Notes\n<Any additional notes for downstream tasks>\n")
	sb.WriteString("```\n")

	return sb.String()
}

// WriteWorkerPrompt writes the prompt to a file
func WriteWorkerPrompt(task *Task, workflow *Workflow, cwd string) (string, error) {
	prompt := WorkerPrompt(task, workflow, cwd)
	
	outboxDir := filepath.Join(cwd, ".ai", "team", "outbox")
	if err := os.MkdirAll(outboxDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create outbox: %w", err)
	}

	promptPath := filepath.Join(outboxDir, task.ID+".prompt.md")
	if err := os.WriteFile(promptPath, []byte(prompt), 0644); err != nil {
		return "", fmt.Errorf("failed to write prompt: %w", err)
	}

	return promptPath, nil
}

// ReadDependencyOutputs reads outputs from tasks that this task depends on
func ReadDependencyOutputs(task *Task, cwd string) (map[string]string, error) {
	outputs := make(map[string]string)
	outboxDir := filepath.Join(cwd, ".ai", "team", "outbox")

	for _, depID := range task.BlockedBy {
		outputPath := filepath.Join(outboxDir, depID+".md")
		data, err := os.ReadFile(outputPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue // Output doesn't exist yet
			}
			return nil, fmt.Errorf("failed to read output for %s: %w", depID, err)
		}
		outputs[depID] = string(data)
	}

	return outputs, nil
}