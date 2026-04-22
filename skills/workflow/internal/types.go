package main

// === State Types ===

const CurrentSchemaVersion = 2

type Phase struct {
	Name               string   `json:"name"`
	Skill              string   `json:"skill"`
	Gate               bool     `json:"gate"`
	Status             string   `json:"status"` // pending, active, completed, failed, skipped
	Output             string   `json:"output,omitempty"`
	PreviousOutput     string   `json:"previousOutput,omitempty"` // preserved across back
	GateApproved       bool     `json:"gateApproved,omitempty"`
	ApprovedAt         string   `json:"approvedAt,omitempty"`
	ApproveMessage     string   `json:"approveMessage,omitempty"`
	Notes              string   `json:"notes,omitempty"`
	RequiredArtifacts  []string `json:"requiredArtifacts,omitempty"`
}

type State struct {
	SchemaVersion int     `json:"schemaVersion"`
	ID            string  `json:"id"`
	Template      string  `json:"template"`
	TemplateName  string  `json:"templateName"`
	Description   string  `json:"description"`
	Phases        []Phase `json:"phases"`
	CurrentPhase  int     `json:"currentPhase"` // 0-based index into Phases
	Status        string  `json:"status"`       // in_progress, paused, completed, failed
	StartedAt     string  `json:"startedAt"`
	UpdatedAt     string  `json:"updatedAt,omitempty"`
	ArtifactDir   string  `json:"artifactDir"`
}

// === Template Registry Types ===

type TemplatePhase struct {
	Name              string   `json:"name"`
	Skill             string   `json:"skill"`
	Gate              bool     `json:"gate"`
	RequiredArtifacts []string `json:"required_artifacts,omitempty"`
}

type Template struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Phases      []TemplatePhase `json:"phases"`
	Category    string          `json:"category"`
	Complexity  string          `json:"complexity"`
	Aliases     []string        `json:"aliases,omitempty"`
}

type Registry struct {
	Version   string              `json:"version"`
	Templates map[string]Template `json:"templates"`
}

// === Plan Types ===

type Plan struct {
	Version    string    `yaml:"version"`
	Metadata   PMetadata `yaml:"metadata"`
	Tasks      []PTask   `yaml:"tasks"`
	Groups     []PGroup  `yaml:"groups"`
	GroupOrder []string  `yaml:"group_order"`
	Risks      []PRisk   `yaml:"risks"`
}

type PMetadata struct {
	SpecFile  string `yaml:"spec_file"`
	Author    string `yaml:"author"`
	CreatedAt string `yaml:"created_at"`
}

type PTask struct {
	ID             string     `yaml:"id"`
	Title          string     `yaml:"title"`
	Description    string     `yaml:"description"`
	Priority       string     `yaml:"priority"`
	EstimatedHours int        `yaml:"estimated_hours"`
	Dependencies   []string   `yaml:"dependencies"`
	File           string     `yaml:"file"`
	Done           bool       `yaml:"done"`
	Subtasks       []PSubtask `yaml:"subtasks"`
}

type PSubtask struct {
	ID          string `yaml:"id"`
	Description string `yaml:"description"`
	Done        bool   `yaml:"done"`
}

type PGroup struct {
	Name          string   `yaml:"name"`
	Title         string   `yaml:"title"`
	Description   string   `yaml:"description"`
	Tasks         []string `yaml:"tasks"`
	CommitMessage string   `yaml:"commit_message"`
}

type PRisk struct {
	Area       string `yaml:"area"`
	Risk       string `yaml:"risk"`
	Mitigation string `yaml:"mitigation"`
}

type LintIssue struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// === Audit Event Types ===

type AuditEvent struct {
	Timestamp string `json:"ts"`
	Event     string `json:"event"`
	Phase     string `json:"phase,omitempty"`
	Detail    string `json:"detail,omitempty"`
}