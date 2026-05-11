package main

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// --- YAML schema types ---

type Plan struct {
	Version    string   `yaml:"version"`
	Metadata   Metadata `yaml:"metadata"`
	Tasks      []Task   `yaml:"tasks"`
	Groups     []Group  `yaml:"groups"`
	GroupOrder []string `yaml:"group_order"`
	Risks      []Risk   `yaml:"risks"`
}

type Metadata struct {
	SpecFile string `yaml:"spec_file"`
}

type Task struct {
	ID             string   `yaml:"id"`
	Title          string   `yaml:"title"`
	Description    string   `yaml:"description"`
	EstimatedHours float64  `yaml:"estimated_hours"`
	DependsOn      []string `yaml:"depends_on"`
	Dependencies   []string `yaml:"dependencies"`
	Group          string   `yaml:"group"`
}

type Group struct {
	Name          string   `yaml:"name"`
	Title         string   `yaml:"title"`
	Description   string   `yaml:"description"`
	Tasks         []string `yaml:"tasks"`
	CommitMessage string   `yaml:"commit_message"`
}

type Risk struct {
	Area       string `yaml:"area"`
	Risk       string `yaml:"risk"`
	Mitigation string `yaml:"mitigation"`
}

// --- Diagnostic ---

type Severity int

const (
	Error   Severity = iota
	Warning
	Info
)

type Diagnostic struct {
	Severity Severity
	Message  string
}

func (d Diagnostic) String() string {
	switch d.Severity {
	case Error:
		return fmt.Sprintf("🚫 %s", d.Message)
	case Warning:
		return fmt.Sprintf("⚠️  %s", d.Message)
	case Info:
		return fmt.Sprintf("ℹ️  %s", d.Message)
	default:
		return d.Message
	}
}

// --- Linter ---

func Lint(plan *Plan) []Diagnostic {
	var diags []Diagnostic

	// Collect task IDs
	taskIDs := make(map[string]bool)
	for _, t := range plan.Tasks {
		taskIDs[t.ID] = true
	}

	// Collect group names
	groupNames := make(map[string]bool)
	for _, g := range plan.Groups {
		groupNames[g.Name] = true
	}

	// --- Errors ---

	// 1. Missing version
	if plan.Version == "" {
		diags = append(diags, Diagnostic{Error, "missing top-level 'version' field"})
	}

	// 2. Missing metadata.spec_file
	if plan.Metadata.SpecFile == "" {
		diags = append(diags, Diagnostic{Error, "missing 'metadata.spec_file' field"})
	}

	// 3. No tasks
	if len(plan.Tasks) == 0 {
		diags = append(diags, Diagnostic{Error, "no tasks defined"})
	}

	// 4. No groups
	if len(plan.Groups) == 0 {
		diags = append(diags, Diagnostic{Error, "no groups defined"})
	}

	// Per-task validation
	descDoneWhenRe := regexp.MustCompile(`(?i)(done\s*when|done-when|acceptance)`)
	designRefRe := regexp.MustCompile(`(?i)(design\.md|see\s+design)`)

	for _, t := range plan.Tasks {
		// 7. estimated_hours must be > 0
		if t.EstimatedHours <= 0 {
			diags = append(diags, Diagnostic{Error, fmt.Sprintf("task %s: estimated_hours must be > 0", t.ID)})
		}

		// 5. depends_on references non-existent task
		deps := effectiveDeps(t)
		for _, dep := range deps {
			if !taskIDs[dep] {
				diags = append(diags, Diagnostic{Error, fmt.Sprintf("task %s: depends_on references non-existent task %s", t.ID, dep)})
			}
		}

		// New: task description empty or shorter than 50 chars
		trimmedDesc := strings.TrimSpace(t.Description)
		if len(trimmedDesc) < 50 {
			diags = append(diags, Diagnostic{Error, fmt.Sprintf("task %s: description is too short (%d chars, minimum 50)", t.ID, len(trimmedDesc))})
		}

		// New: task description must contain "Done when" or "done-when" or "Acceptance"
		if len(trimmedDesc) >= 50 && !descDoneWhenRe.MatchString(trimmedDesc) {
			diags = append(diags, Diagnostic{Error, fmt.Sprintf("task %s: description must contain a 'Done when' or 'Acceptance' section", t.ID)})
		}

		// New: task description references design.md / see design
		if designRefRe.MatchString(trimmedDesc) {
			diags = append(diags, Diagnostic{Warning, fmt.Sprintf("task %s: description references design.md — tasks should be self-contained", t.ID)})
		}

		// Warning: task > 6 hours
		if t.EstimatedHours > 6 {
			diags = append(diags, Diagnostic{Warning, fmt.Sprintf("task %s: estimated %.1f hours — consider splitting into smaller tasks", t.ID, t.EstimatedHours)})
		}

		// Info: task < 1 hour
		if t.EstimatedHours > 0 && t.EstimatedHours < 1 {
			diags = append(diags, Diagnostic{Info, fmt.Sprintf("task %s: estimated %.1f hours — very small, consider merging with related work", t.ID, t.EstimatedHours)})
		}
	}

	// 6 & New: Circular dependency detection (build graph, detect cycles)
	diags = append(diags, detectCycles(plan.Tasks, taskIDs)...)

	// 8. Group references non-existent task
	for _, g := range plan.Groups {
		for _, tid := range g.Tasks {
			if !taskIDs[tid] {
				diags = append(diags, Diagnostic{Error, fmt.Sprintf("group %s: references non-existent task %s", g.Name, tid)})
			}
		}
	}

	// 9. group_order references non-existent group
	for _, gn := range plan.GroupOrder {
		if !groupNames[gn] {
			diags = append(diags, Diagnostic{Error, fmt.Sprintf("group_order: references non-existent group %q", gn)})
		}
	}

	// New: Group contains tasks with no dependency between them
	for _, g := range plan.Groups {
		taskIDSet := make(map[string]bool)
		for _, tid := range g.Tasks {
			taskIDSet[tid] = true
		}
		// Check if there exists at least one pair of tasks in the group
		// where neither depends on the other (directly or transitively).
		// We just check direct dependencies for simplicity.
		if len(g.Tasks) > 1 {
			anyLinked := false
			for _, tid := range g.Tasks {
				task := findTask(plan.Tasks, tid)
				if task != nil {
					for _, dep := range effectiveDeps(*task) {
						if taskIDSet[dep] {
							anyLinked = true
							break
						}
					}
				}
				if anyLinked {
					break
				}
			}
			if !anyLinked {
				diags = append(diags, Diagnostic{Warning, fmt.Sprintf("group %s: tasks have no dependency links between them — may indicate missing dependency or wrong grouping", g.Name)})
			}
		}
	}

	// --- Warnings ---

	// 1. Missing group_order
	if len(plan.GroupOrder) == 0 {
		diags = append(diags, Diagnostic{Warning, "missing 'group_order' field — recommended to define execution order"})
	}

	// 2. No risks
	if len(plan.Risks) == 0 {
		diags = append(diags, Diagnostic{Warning, "no risks defined — recommend adding 2-5 risks with mitigations"})
	}

	return diags
}

// effectiveDeps returns the dependency list, supporting both `depends_on` and `dependencies` fields.
func effectiveDeps(t Task) []string {
	if len(t.DependsOn) > 0 {
		return t.DependsOn
	}
	return t.Dependencies
}

func findTask(tasks []Task, id string) *Task {
	for i := range tasks {
		if tasks[i].ID == id {
			return &tasks[i]
		}
	}
	return nil
}

// detectCycles uses DFS to find cycles in the dependency graph.
func detectCycles(tasks []Task, taskIDs map[string]bool) []Diagnostic {
	var diags []Diagnostic

	// Build adjacency list
	adj := make(map[string][]string)
	for _, t := range tasks {
		adj[t.ID] = effectiveDeps(t)
	}

	// State: 0=unvisited, 1=in-progress, 2=done
	state := make(map[string]int)
	inStack := make(map[string]bool)

	var cycleEdges []string // "A → B → C → A" formatted cycles

	var dfs func(id string, path []string)
	dfs = func(id string, path []string) {
		if state[id] == 2 {
			return
		}
		if inStack[id] {
			// Found cycle — find where it starts in path
			cycleStart := -1
			for i, p := range path {
				if p == id {
					cycleStart = i
					break
				}
			}
			if cycleStart >= 0 {
				cycle := append(path[cycleStart:], id)
				cycleEdges = append(cycleEdges, strings.Join(cycle, " → "))
			}
			return
		}

		state[id] = 1
		inStack[id] = true
		path = append(path, id)

		for _, dep := range adj[id] {
			if taskIDs[dep] {
				dfs(dep, path)
			}
		}

		path = path[:len(path)-1]
		inStack[id] = false
		state[id] = 2
	}

	for _, t := range tasks {
		if state[t.ID] == 0 {
			dfs(t.ID, nil)
		}
	}

	seen := make(map[string]bool)
	for _, c := range cycleEdges {
		if !seen[c] {
			seen[c] = true
			diags = append(diags, Diagnostic{Error, fmt.Sprintf("circular dependency detected: %s", c)})
		}
	}

	return diags
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: plan-lint <tasks.yml>\n")
		os.Exit(2)
	}

	path := os.Args[1]
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to read %s: %v\n", path, err)
		os.Exit(2)
	}

	// Detect control characters (e.g., \x01 from Python regex backreference corruption)
	controlChars := detectControlChars(data)
	if len(controlChars) > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  File contains %d control characters:\n", len(controlChars))
		for _, cc := range controlChars {
			fmt.Fprintf(os.Stderr, "   Line %d byte %d: \\x%02x\n", cc.Line, cc.ByteOffset, cc.Byte)
		}
		fmt.Fprintf(os.Stderr, "   This may be caused by regex backreference corruption (e.g., Python re.sub using \\1 → \\x01).\n")
		fmt.Fprintf(os.Stderr, "   Fix: sed -i 's/\\x01//g' %s\n", path)
		fmt.Println()
	}

	var plan Plan
	if err := yaml.Unmarshal(data, &plan); err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to parse YAML: %v\n", err)
		os.Exit(2)
	}

	diags := Lint(&plan)

	hasErrors := false
	hasWarnings := false
	for _, d := range diags {
		fmt.Println(d.String())
		if d.Severity == Error {
			hasErrors = true
		} else if d.Severity == Warning {
			hasWarnings = true
		}
	}

	if hasErrors {
		fmt.Println()
		fmt.Println("❌ Plan validation failed (has errors)")
		os.Exit(1)
	} else if hasWarnings {
		fmt.Println()
		fmt.Println("✅ Plan is valid (with warnings — fix optional)")
		os.Exit(0)
	} else {
		fmt.Println()
		fmt.Println("✅ Plan is valid")
		os.Exit(0)
	}
}

// ControlCharLoc records where a control character was found.
type ControlCharLoc struct {
	Line       int
	ByteOffset int
	Byte       byte
}

// detectControlChars scans for non-printable control characters (0x00-0x1F except \n \r \t).
func detectControlChars(data []byte) []ControlCharLoc {
	var results []ControlCharLoc
	line := 1
	offset := 0
	for i, b := range data {
		if b == '\n' {
			line++
			offset = 0
			continue
		}
			_ = i
		offset++
		// Control chars except \t (0x09), \n (0x0A), \r (0x0D)
		if b < 0x20 && b != 0x09 && b != 0x0A && b != 0x0D {
			results = append(results, ControlCharLoc{Line: line, ByteOffset: offset, Byte: b})
			if len(results) >= 20 { // Cap at 20 to avoid flooding output
				break
			}
		}
	}
	return results
}