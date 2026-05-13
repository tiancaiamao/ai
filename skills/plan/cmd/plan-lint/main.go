package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// --- Frontmatter schema types (YAML) ---

type Plan struct {
	Version    string   `yaml:"version"`
	Metadata   Metadata `yaml:"metadata"`
	Tasks      []Task   // Parsed from Markdown body, not YAML
	Groups     []Group  `yaml:"groups"`
	GroupOrder []string `yaml:"group_order"`
	Risks      []Risk   `yaml:"risks"`
}

type Metadata struct {
	SpecFile string `yaml:"spec_file"`
}

type Task struct {
	ID             string
	Title          string
	Description    string // Full Markdown body of the task section
	EstimatedHours float64
	DependsOn      []string
	Dependencies   []string
	Group          string
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

// --- Markdown Parser ---

// parseMarkdownPlan splits a Markdown file into frontmatter and task sections.
func parseMarkdownPlan(data []byte) (*Plan, []Diagnostic) {
	var diags []Diagnostic

	text := string(data)
	frontmatterYAML, body, err := extractFrontmatter(text)
	if err != nil {
		diags = append(diags, Diagnostic{Error, fmt.Sprintf("failed to extract frontmatter: %v", err)})
		return nil, diags
	}

	plan := &Plan{}

	// Parse frontmatter YAML
	if err := yaml.Unmarshal([]byte(frontmatterYAML), plan); err != nil {
		diags = append(diags, Diagnostic{Error, fmt.Sprintf("failed to parse frontmatter YAML: %v", err)})
		return nil, diags
	}

	// Parse task sections from Markdown body
	tasks, taskDiags := parseTaskSections(body)
	diags = append(diags, taskDiags...)
	plan.Tasks = tasks

	return plan, diags
}

// extractFrontmatter splits text into YAML frontmatter (between --- delimiters) and body.
func extractFrontmatter(text string) (frontmatter string, body string, err error) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "---") {
		return "", "", fmt.Errorf("file must start with YAML frontmatter (---)")
	}

	// Find the closing ---
	// Start after the first ---
	rest := text[3:]
	// Look for the next --- on its own line
	closingIdx := strings.Index(rest, "\n---")
	if closingIdx < 0 {
		return "", "", fmt.Errorf("frontmatter not closed: missing closing ---")
	}

	frontmatter = rest[:closingIdx]
	body = rest[closingIdx+4:] // skip \n---
	body = strings.TrimPrefix(body, "\n")

	return frontmatter, body, nil
}

// taskHeaderRe matches `## T001 — Task title (3h)` or `## T001 - Task title (3h)`
var taskHeaderRe = regexp.MustCompile(`^## (T\d+)\s*[—\-]\s*(.+?)(?:\s*\((\d+(?:\.\d+)?)h\))?\s*$`)

// depLineRe matches `**Dependencies:** T001, T003` or `**Dependencies:** none`
var depLineRe = regexp.MustCompile(`^\*\*Dependencies?:\*\*\s*(.+)$`)

// groupLineRe matches `**Group:** agent`
var groupLineRe = regexp.MustCompile(`^\*\*Group:\*\*\s*(.+)$`)

// parseTaskSections splits the Markdown body into task sections by `## Txxx` headers.
func parseTaskSections(body string) ([]Task, []Diagnostic) {
	var diags []Diagnostic
	var tasks []Task

	if strings.TrimSpace(body) == "" {
		return tasks, diags
	}

	// Split on `## T` pattern. We use a regex to find all task headers.
	// First, split by lines and find task header positions.
	lines := strings.Split(body, "\n")

	type section struct {
		headerIdx int
		lines     []string
	}
	var sections []section

	currentSection := -1
	for i, line := range lines {
		if taskHeaderRe.MatchString(line) {
			sections = append(sections, section{headerIdx: i, lines: []string{line}})
			currentSection = len(sections) - 1
		} else if currentSection >= 0 {
			sections[currentSection].lines = append(sections[currentSection].lines, line)
		}
		// Lines before the first task header are ignored (could be intro text)
	}

	for _, sec := range sections {
		task, taskDiags := parseTaskSection(sec.lines)
		diags = append(diags, taskDiags...)
		if task != nil {
			tasks = append(tasks, *task)
		}
	}

	return tasks, diags
}

// parseTaskSection parses a single task section into a Task.
func parseTaskSection(lines []string) (*Task, []Diagnostic) {
	var diags []Diagnostic

	if len(lines) == 0 {
		return nil, diags
	}

	task := &Task{}

	// Parse header line: `## T001 — Task title (3h)`
	headerMatch := taskHeaderRe.FindStringSubmatch(lines[0])
	if headerMatch == nil {
		diags = append(diags, Diagnostic{Error, fmt.Sprintf("invalid task header: %s", lines[0])})
		return nil, diags
	}

	task.ID = headerMatch[1]
	task.Title = strings.TrimSpace(headerMatch[2])
	if headerMatch[3] != "" {
		hours, err := strconv.ParseFloat(headerMatch[3], 64)
		if err == nil {
			task.EstimatedHours = hours
		}
	}

	// Parse metadata lines and collect body
	var bodyLines []string
	for _, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)

		if depMatch := depLineRe.FindStringSubmatch(trimmed); depMatch != nil {
			depStr := strings.TrimSpace(depMatch[1])
			if strings.EqualFold(depStr, "none") || depStr == "" {
				task.DependsOn = []string{}
				task.Dependencies = []string{}
			} else {
				deps := parseDepList(depStr)
				task.DependsOn = deps
				task.Dependencies = deps
			}
			continue
		}

		if groupMatch := groupLineRe.FindStringSubmatch(trimmed); groupMatch != nil {
			task.Group = strings.TrimSpace(groupMatch[1])
			continue
		}

		bodyLines = append(bodyLines, line)
	}

	task.Description = strings.Join(bodyLines, "\n")
	// Trim leading/trailing blank lines from description
	task.Description = strings.TrimSpace(task.Description)

	return task, diags
}

// parseDepList parses a comma-separated dependency list like "T001, T003"
func parseDepList(s string) []string {
	parts := strings.Split(s, ",")
	var deps []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			deps = append(deps, p)
		}
	}
	return deps
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
		diags = append(diags, Diagnostic{Error, "missing top-level 'version' field in frontmatter"})
	}

	// 2. Missing metadata.spec_file
	if plan.Metadata.SpecFile == "" {
		diags = append(diags, Diagnostic{Error, "missing 'metadata.spec_file' field in frontmatter"})
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
		// estimated_hours must be > 0
		if t.EstimatedHours <= 0 {
			diags = append(diags, Diagnostic{Error, fmt.Sprintf("task %s: estimated_hours must be > 0", t.ID)})
		}

		// depends_on references non-existent task
		deps := effectiveDeps(t)
		for _, dep := range deps {
			if !taskIDs[dep] {
				diags = append(diags, Diagnostic{Error, fmt.Sprintf("task %s: depends_on references non-existent task %s", t.ID, dep)})
			}
		}

		// task description empty or shorter than 50 chars
		trimmedDesc := strings.TrimSpace(t.Description)
		if len(trimmedDesc) < 50 {
			diags = append(diags, Diagnostic{Error, fmt.Sprintf("task %s: description is too short (%d chars, minimum 50)", t.ID, len(trimmedDesc))})
		}

		// task description must contain "Done when" or "done-when" or "Acceptance"
		if len(trimmedDesc) >= 50 && !descDoneWhenRe.MatchString(trimmedDesc) {
			diags = append(diags, Diagnostic{Error, fmt.Sprintf("task %s: description must contain a 'Done when' or 'Acceptance' section", t.ID)})
		}

		// task description references design.md / see design
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

	// Circular dependency detection (build graph, detect cycles)
	diags = append(diags, detectCycles(plan.Tasks, taskIDs)...)

	// Group references non-existent task
	for _, g := range plan.Groups {
		for _, tid := range g.Tasks {
			if !taskIDs[tid] {
				diags = append(diags, Diagnostic{Error, fmt.Sprintf("group %s: references non-existent task %s", g.Name, tid)})
			}
		}
	}

	// group_order references non-existent group
	for _, gn := range plan.GroupOrder {
		if !groupNames[gn] {
			diags = append(diags, Diagnostic{Error, fmt.Sprintf("group_order: references non-existent group %q", gn)})
		}
	}

	// Group contains tasks with no dependency links between them (suggests missing dependency or wrong grouping)
	for _, g := range plan.Groups {
		taskIDSet := make(map[string]bool)
		for _, tid := range g.Tasks {
			taskIDSet[tid] = true
		}
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

	// Cross-validation: tasks in frontmatter groups must match task sections
	diags = append(diags, crossValidateGroups(plan)...)

	// --- Warnings ---

	// Missing group_order
	if len(plan.GroupOrder) == 0 {
		diags = append(diags, Diagnostic{Warning, "missing 'group_order' field — recommended to define execution order"})
	}

	// No risks
	if len(plan.Risks) == 0 {
		diags = append(diags, Diagnostic{Warning, "no risks defined — recommend adding 2-5 risks with mitigations"})
	}

	return diags
}

// crossValidateGroups checks that frontmatter groups[].tasks and task sections are consistent.
func crossValidateGroups(plan *Plan) []Diagnostic {
	var diags []Diagnostic

	// Collect all tasks referenced in groups
	groupTaskIDs := make(map[string]bool)
	for _, g := range plan.Groups {
		for _, tid := range g.Tasks {
			groupTaskIDs[tid] = true
		}
	}

	// Collect all task section IDs
	sectionIDs := make(map[string]bool)
	for _, t := range plan.Tasks {
		sectionIDs[t.ID] = true
	}

	// Tasks in groups but not in sections
	for tid := range groupTaskIDs {
		if !sectionIDs[tid] {
			diags = append(diags, Diagnostic{Error, fmt.Sprintf("groups[].tasks references %s but no matching task section found", tid)})
		}
	}

	// Task sections not referenced in any group
	for tid := range sectionIDs {
		if !groupTaskIDs[tid] {
			diags = append(diags, Diagnostic{Warning, fmt.Sprintf("task %s has a section but is not referenced in any group", tid)})
		}
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

	var cycleEdges []string

	var dfs func(id string, path []string)
	dfs = func(id string, path []string) {
		if state[id] == 2 {
			return
		}
		if inStack[id] {
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
		fmt.Fprintf(os.Stderr, "Usage: plan-lint <tasks.md>\n")
		os.Exit(2)
	}

	path := os.Args[1]
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Failed to read %s: %v\n", path, err)
		os.Exit(2)
	}

	// Detect control characters
	controlChars := detectControlChars(data)
	if len(controlChars) > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  File contains %d control characters:\n", len(controlChars))
		for _, cc := range controlChars {
			fmt.Fprintf(os.Stderr, "   Line %d byte %d: \\x%02x\n", cc.Line, cc.ByteOffset, cc.Byte)
		}
		fmt.Fprintf(os.Stderr, "   This may be caused by regex backreference corruption.\n")
		fmt.Fprintf(os.Stderr, "   Fix: sed -i 's/\\x01//g' %s\n", path)
		fmt.Println()
	}

	plan, parseDiags := parseMarkdownPlan(data)
	if plan == nil {
		for _, d := range parseDiags {
			fmt.Println(d.String())
		}
		fmt.Println()
		fmt.Println("❌ Plan parsing failed")
		os.Exit(2)
	}

	diags := append(parseDiags, Lint(plan)...)

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
		if b < 0x20 && b != 0x09 && b != 0x0A && b != 0x0D {
			results = append(results, ControlCharLoc{Line: line, ByteOffset: offset, Byte: b})
			if len(results) >= 20 {
				break
			}
		}
	}
	return results
}