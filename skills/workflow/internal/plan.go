package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// === Plan Lint ===

func runPlanLint(cmd *cobra.Command, args []string) {
	asJSON, _ := cmd.Flags().GetBool("json")
	planFile := args[0]

	data, err := os.ReadFile(planFile)
	if err != nil {
		issue := LintIssue{Level: "error", Message: fmt.Sprintf("read file: %v", err)}
		if asJSON {
			outputLintJSON([]LintIssue{issue})
		} else {
			fmt.Printf("❌ Plan validation failed:\n🚫 [error] %s\n", issue.Message)
		}
		os.Exit(1)
		return
	}

	var plan Plan
	if err := yaml.Unmarshal(data, &plan); err != nil {
		issue := LintIssue{Level: "error", Message: fmt.Sprintf("YAML parse: %v", err)}
		if asJSON {
			outputLintJSON([]LintIssue{issue})
		} else {
			fmt.Printf("❌ Plan validation failed:\n🚫 [error] %s\n", issue.Message)
		}
		os.Exit(1)
		return
	}

	issues := lintPlan(plan)

	strategy := assessStrategy(plan)
	if strategy != "" {
		issues = append(issues, LintIssue{Level: "info", Message: strategy})
	}

	if asJSON {
		outputLintJSON(issues)
		for _, iss := range issues {
			if iss.Level == "error" {
				os.Exit(1)
			}
		}
		return
	}

	if len(issues) == 0 {
		fmt.Println("✅ Plan is valid")
		return
	}

	var strategyIssues, otherIssues []LintIssue
	for _, iss := range issues {
		if strings.HasPrefix(iss.Message, "Strategy:") {
			strategyIssues = append(strategyIssues, iss)
		} else {
			otherIssues = append(otherIssues, iss)
		}
	}

	if len(otherIssues) > 0 {
		fmt.Println("❌ Plan validation issues:")
		for _, iss := range otherIssues {
			prefix := "ℹ️"
			switch iss.Level {
			case "error":
				prefix = "🚫"
			case "warning":
				prefix = "⚠️"
			}
			fmt.Printf("%s [%s] %s\n", prefix, iss.Level, iss.Message)
		}
	}

	for _, iss := range strategyIssues {
		fmt.Printf("💡 %s\n", iss.Message)
	}

	for _, iss := range otherIssues {
		if iss.Level == "error" {
			os.Exit(1)
		}
	}
}

func outputLintJSON(issues []LintIssue) {
	type jsonOutput struct {
		Valid    bool        `json:"valid"`
		Issues   []LintIssue `json:"issues,omitempty"`
		Strategy string      `json:"strategy,omitempty"`
	}
	out := jsonOutput{Valid: true}
	hasError := false
	for _, iss := range issues {
		if strings.HasPrefix(iss.Message, "Strategy:") {
			out.Strategy = strings.TrimPrefix(iss.Message, "Strategy: ")
			continue
		}
		out.Issues = append(out.Issues, iss)
		if iss.Level == "error" {
			hasError = true
		}
	}
	if len(out.Issues) > 0 {
		out.Valid = false
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(data))
	if hasError {
		os.Exit(1)
	}
}

func assessStrategy(plan Plan) string {
	totalTasks := len(plan.Tasks)
	if totalTasks == 0 {
		return ""
	}

	totalHours := 0
	for _, t := range plan.Tasks {
		totalHours += t.EstimatedHours
	}

	var sb strings.Builder
	sb.WriteString("Strategy: ")

	switch {
	case totalTasks <= 2:
		sb.WriteString(fmt.Sprintf("small scope (%d tasks, %dh) — agent executes directly, single commit", totalTasks, totalHours))
	case totalTasks <= 6:
		sb.WriteString(fmt.Sprintf("medium scope (%d tasks, %dh) — sub-agents per group, serial or light parallel", totalTasks, totalHours))
	default:
		sb.WriteString(fmt.Sprintf("large scope (%d tasks, %dh) — full fan-out with parallel workers (implement-team.sh --workers 3+)", totalTasks, totalHours))
	}

	maxDepth := dependencyDepth(plan)
	if maxDepth > 3 {
		sb.WriteString(fmt.Sprintf(". Deep dependency chain (depth %d) — consider flattening", maxDepth))
	}

	return sb.String()
}

func dependencyDepth(plan Plan) int {
	taskMap := make(map[string]*PTask)
	for i := range plan.Tasks {
		taskMap[plan.Tasks[i].ID] = &plan.Tasks[i]
	}

	depth := make(map[string]int)
	var getDepth func(string) int
	getDepth = func(id string) int {
		if d, ok := depth[id]; ok {
			return d
		}
		t, exists := taskMap[id]
		if !exists || len(t.Dependencies) == 0 {
			depth[id] = 1
			return 1
		}
		maxD := 0
		for _, dep := range t.Dependencies {
			d := getDepth(dep)
			if d > maxD {
				maxD = d
			}
		}
		depth[id] = maxD + 1
		return maxD + 1
	}

	maxDepth := 0
	for _, t := range plan.Tasks {
		d := getDepth(t.ID)
		if d > maxDepth {
			maxDepth = d
		}
	}
	return maxDepth
}

func lintPlan(plan Plan) []LintIssue {
	var issues []LintIssue
	issues = append(issues, lintStructure(plan)...)
	issues = append(issues, lintTaskIDs(plan)...)
	issues = append(issues, lintDependencies(plan)...)
	issues = append(issues, lintGranularity(plan)...)
	issues = append(issues, lintGroups(plan)...)
	issues = append(issues, lintRisks(plan)...)
	return issues
}

func lintStructure(plan Plan) []LintIssue {
	var issues []LintIssue
	if plan.Version == "" {
		issues = append(issues, LintIssue{"error", "missing field: version"})
	}
	if plan.Metadata.SpecFile == "" {
		issues = append(issues, LintIssue{"warning", "missing field: metadata.spec_file"})
	}
	if len(plan.Tasks) == 0 {
		issues = append(issues, LintIssue{"error", "no tasks defined"})
	}
	return issues
}

func lintTaskIDs(plan Plan) []LintIssue {
	var issues []LintIssue
	seen := make(map[string]bool)
	for _, t := range plan.Tasks {
		if t.ID == "" {
			issues = append(issues, LintIssue{"error", "task missing id"})
		}
		if seen[t.ID] {
			issues = append(issues, LintIssue{"error", fmt.Sprintf("duplicate task id: %s", t.ID)})
		}
		seen[t.ID] = true
	}
	return issues
}

func lintDependencies(plan Plan) []LintIssue {
	var issues []LintIssue

	taskMap := make(map[string]*PTask)
	for i := range plan.Tasks {
		taskMap[plan.Tasks[i].ID] = &plan.Tasks[i]
	}

	// Check that all dependencies reference existing tasks
	for _, t := range plan.Tasks {
		for _, dep := range t.Dependencies {
			if _, ok := taskMap[dep]; !ok {
				issues = append(issues, LintIssue{
					"error", fmt.Sprintf("task %s: dependency '%s' not found", t.ID, dep),
				})
			}
		}
	}

	// Cycle detection via DFS
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int)
	var visit func(string) bool
	visit = func(id string) bool {
		if color[id] == gray {
			return true
		}
		if color[id] == black {
			return false
		}
		color[id] = gray
		if t, ok := taskMap[id]; ok {
			for _, dep := range t.Dependencies {
				if visit(dep) {
					return true
				}
			}
		}
		color[id] = black
		return false
	}
	for _, t := range plan.Tasks {
		if visit(t.ID) {
			issues = append(issues, LintIssue{"error", fmt.Sprintf("dependency cycle involving task: %s", t.ID)})
			break
		}
	}

	// Check group ordering vs dependencies
	taskToGroup := make(map[string]string)
	for _, g := range plan.Groups {
		for _, tid := range g.Tasks {
			taskToGroup[tid] = g.Name
		}
	}
	for _, t := range plan.Tasks {
		for _, dep := range t.Dependencies {
			tg, tOk := taskToGroup[t.ID]
			dg, dOk := taskToGroup[dep]
			if tOk && dOk {
				ti, di := -1, -1
				for i, g := range plan.GroupOrder {
					if g == tg {
						ti = i
					}
					if g == dg {
						di = i
					}
				}
				if ti != -1 && di != -1 && ti < di {
					issues = append(issues, LintIssue{
						"warning",
						fmt.Sprintf("task %s (group '%s') depends on %s (group '%s'), but group order may execute task first", t.ID, tg, dep, dg),
					})
				}
			}
		}
	}

	return issues
}

func lintGranularity(plan Plan) []LintIssue {
	var issues []LintIssue
	for _, t := range plan.Tasks {
		if t.EstimatedHours > 4 && len(t.Subtasks) == 0 {
			issues = append(issues, LintIssue{
				"warning", fmt.Sprintf("task %s (%s) is large (%dh) with no subtasks — consider breaking down", t.ID, t.Title, t.EstimatedHours),
			})
		}
	}
	return issues
}

func lintGroups(plan Plan) []LintIssue {
	var issues []LintIssue
	groupTasks := make(map[string]bool)

	for _, g := range plan.Groups {
		if g.Name == "" {
			issues = append(issues, LintIssue{"error", "group missing name"})
			continue
		}
		if len(g.Tasks) == 0 {
			issues = append(issues, LintIssue{"warning", fmt.Sprintf("group '%s' has no tasks", g.Name)})
		}
		if len(g.Tasks) > 6 {
			issues = append(issues, LintIssue{"info", fmt.Sprintf("group '%s' has %d tasks — consider splitting", g.Name, len(g.Tasks))})
		}
		for _, tid := range g.Tasks {
			key := g.Name + ":" + tid
			if groupTasks[key] {
				issues = append(issues, LintIssue{"warning", fmt.Sprintf("task %s appears in multiple groups", tid)})
			}
			groupTasks[key] = true
		}
	}

	groupNames := make(map[string]bool)
	for _, g := range plan.Groups {
		groupNames[g.Name] = true
	}
	for _, name := range plan.GroupOrder {
		if !groupNames[name] {
			issues = append(issues, LintIssue{"error", fmt.Sprintf("group_order references non-existent group: %s", name)})
		}
	}
	return issues
}

func lintRisks(plan Plan) []LintIssue {
	var issues []LintIssue
	if len(plan.Risks) == 0 {
		issues = append(issues, LintIssue{"warning", "no risks defined (recommend 2-5 key risks)"})
		return issues
	}
	for i, r := range plan.Risks {
		if r.Area == "" {
			issues = append(issues, LintIssue{"warning", fmt.Sprintf("risk #%d: missing area", i+1)})
		}
		if r.Risk == "" {
			issues = append(issues, LintIssue{"warning", fmt.Sprintf("risk #%d: missing risk description", i+1)})
		}
		if r.Mitigation == "" {
			issues = append(issues, LintIssue{"info", fmt.Sprintf("risk #%d: missing mitigation strategy", i+1)})
		}
	}
	return issues
}

// === Plan Render ===

func runPlanRender(cmd *cobra.Command, args []string) {
	planFile := args[0]
	outputFile := planFile + ".md"
	if len(args) >= 2 {
		outputFile = args[1]
	}

	data, err := os.ReadFile(planFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	var plan Plan
	if err := yaml.Unmarshal(data, &plan); err != nil {
		fmt.Fprintf(os.Stderr, "Error: parse YAML: %v\n", err)
		os.Exit(1)
	}

	md := renderPlan(plan)
	if err := os.WriteFile(outputFile, []byte(md), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Error: write output: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Rendered: %s\n", outputFile)
}

func renderPlan(plan Plan) string {
	var sb strings.Builder

	sb.WriteString("# Plan\n\n")
	if plan.Metadata.SpecFile != "" {
		sb.WriteString(fmt.Sprintf("**Source:** %s\n\n", plan.Metadata.SpecFile))
	}
	if plan.Metadata.CreatedAt != "" {
		sb.WriteString(fmt.Sprintf("**Created:** %s\n\n", plan.Metadata.CreatedAt))
	}

	doneCount := 0
	totalHours := 0
	for _, t := range plan.Tasks {
		if t.Done {
			doneCount++
		}
		totalHours += t.EstimatedHours
	}
	if len(plan.Tasks) > 0 {
		sb.WriteString(fmt.Sprintf("**Progress:** %d/%d tasks (%.0f%%)\n", doneCount, len(plan.Tasks), float64(doneCount)/float64(len(plan.Tasks))*100))
	}
	sb.WriteString(fmt.Sprintf("**Total Effort:** %d hours\n\n", totalHours))
	sb.WriteString("---\n\n")

	sb.WriteString("## Groups\n\n")
	order := plan.GroupOrder
	if len(order) == 0 {
		for _, g := range plan.Groups {
			order = append(order, g.Name)
		}
	}

	for _, groupName := range order {
		group := findGroup(plan.Groups, groupName)
		if group == nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("### %s\n\n", group.Title))
		if group.Description != "" {
			sb.WriteString(fmt.Sprintf("%s\n\n", group.Description))
		}
		sb.WriteString(fmt.Sprintf("**Commit:** `%s`\n\n", group.CommitMessage))
		for _, tid := range group.Tasks {
			if t := findTask(plan.Tasks, tid); t != nil {
				sb.WriteString(formatTask(*t))
			}
		}
		sb.WriteString("\n")
	}

	ungrouped := findUngroupedTasks(plan)
	if len(ungrouped) > 0 {
		sb.WriteString("## Other Tasks\n\n")
		for _, t := range ungrouped {
			sb.WriteString(formatTask(t))
		}
		sb.WriteString("\n")
	}

	if len(plan.Risks) > 0 {
		sb.WriteString("## Risks\n\n")
		for i, r := range plan.Risks {
			sb.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, r.Area))
			sb.WriteString(fmt.Sprintf("**Risk:** %s\n\n", r.Risk))
			sb.WriteString(fmt.Sprintf("**Mitigation:** %s\n\n", r.Mitigation))
		}
	}

	return sb.String()
}

func formatTask(t PTask) string {
	var sb strings.Builder
	done := " "
	if t.Done {
		done = "x"
	}
	sb.WriteString(fmt.Sprintf("- [%s] **%s**: %s (%dh)", done, t.ID, t.Title, t.EstimatedHours))
	if t.File != "" {
		sb.WriteString(fmt.Sprintf(" `%s`", t.File))
	}
	if t.Priority != "" {
		sb.WriteString(fmt.Sprintf(" [%s]", strings.ToUpper(t.Priority)))
	}
	sb.WriteString("\n")
	if len(t.Dependencies) > 0 {
		sb.WriteString(fmt.Sprintf("  - Depends on: %s\n", strings.Join(t.Dependencies, ", ")))
	}
	if t.Description != "" {
		sb.WriteString(fmt.Sprintf("  - %s\n", t.Description))
	}
	for _, s := range t.Subtasks {
		d := " "
		if s.Done {
			d = "x"
		}
		sb.WriteString(fmt.Sprintf("  - [%s] %s: %s\n", d, s.ID, s.Description))
	}
	return sb.String()
}

func findTask(tasks []PTask, id string) *PTask {
	for i := range tasks {
		if tasks[i].ID == id {
			return &tasks[i]
		}
	}
	return nil
}

func findGroup(groups []PGroup, name string) *PGroup {
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i]
		}
	}
	return nil
}

func findUngroupedTasks(plan Plan) []PTask {
	grouped := make(map[string]bool)
	for _, g := range plan.Groups {
		for _, tid := range g.Tasks {
			grouped[tid] = true
		}
	}
	var ungrouped []PTask
	for _, t := range plan.Tasks {
		if !grouped[t.ID] {
			ungrouped = append(ungrouped, t)
		}
	}
	return ungrouped
}