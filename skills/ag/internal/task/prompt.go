package task

import (
	"fmt"
	"strings"
)

// BuildWorkerPrompt constructs the prompt for a worker agent.
func BuildWorkerPrompt(t *Task, designFile string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are implementing task %s: %s\n\n", t.ID, t.Title))
	sb.WriteString("## Task Description\n")
	sb.WriteString(t.Description)
	sb.WriteString("\n\n")

	if designFile != "" {
		sb.WriteString(fmt.Sprintf("## Design Reference\n"))
		sb.WriteString(fmt.Sprintf("Read %s for broader context if needed.\n\n", designFile))
	}

	sb.WriteString("## Rules\n")
	sb.WriteString("- Only implement this specific task. Do not touch unrelated code.\n")
	sb.WriteString("- Run affected tests before finishing.\n")
	sb.WriteString("- Write a brief summary of what you changed when done.\n")

	return sb.String()
}

// BuildReviewerPrompt constructs the prompt for a reviewer agent.
func BuildReviewerPrompt(tasks []*Task, diff string) string {
	var sb strings.Builder

	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.ID
	}
	sb.WriteString(fmt.Sprintf("You are reviewing the implementation of tasks: %s\n\n", strings.Join(ids, ", ")))

	sb.WriteString("## Changes\n")
	sb.WriteString(diff)
	sb.WriteString("\n\n")

	sb.WriteString("## Original task descriptions\n")
	for _, t := range tasks {
		sb.WriteString(fmt.Sprintf("### %s: %s\n%s\n\n", t.ID, t.Title, t.Description))
	}

	sb.WriteString("## Review criteria\n")
	sb.WriteString("- Correctness: Does the implementation match the task description?\n")
	sb.WriteString("- Edge cases: Are edge cases from the description handled?\n")
	sb.WriteString("- Tests: Are affected tests passing?\n")
	sb.WriteString("- Style: Does it follow project conventions?\n\n")

	sb.WriteString("Output your verdict and comments.\n")
	sb.WriteString("If everything looks good, say: REVIEW_PASS\n")
	sb.WriteString("If changes are needed, list specific issues to fix.\n")

	return sb.String()
}