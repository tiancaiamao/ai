package orchestrate

import "time"

// TaskState represents the state of a task
type TaskState string

const (
	StatePending    TaskState = "pending"
	StateClaimed    TaskState = "claimed"
	StateInProgress TaskState = "in_progress"
	StateCompleted  TaskState = "completed"
	StateFailed     TaskState = "failed"
	StateBlocked    TaskState = "blocked"
	StateApproved   TaskState = "approved"
)

// Task represents a task in the team
type Task struct {
	ID          string     `json:"id"`
	Subject     string     `json:"subject"`
	Description string     `json:"description"`
	Status      TaskState  `json:"status"`
	Owner       string     `json:"owner,omitempty"`
	BlockedBy   []string   `json:"blocked_by,omitempty"`
	ClaimedBy   string     `json:"claimed_by,omitempty"`
	ClaimedAt   *time.Time `json:"claimed_at,omitempty"`
	ClaimToken  string     `json:"claim_token,omitempty"`
	Result      string     `json:"result,omitempty"`
	Error       string     `json:"error,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	RetryCount  int        `json:"retry_count"`
}

// TeamConfig represents team configuration
type TeamConfig struct {
	Name         string `json:"name"`
	Workflow     string `json:"workflow"`
	WorkerCount  int    `json:"worker_count"`
	MaxRetries   int    `json:"max_retries"`
	Timeout      string `json:"timeout,omitempty"`
	CreatedAt    string `json:"created_at"`
}

// TeamState represents current team state
type TeamState struct {
	Phase       string `json:"phase"`
	ActiveCount int    `json:"active_count"`
	UpdatedAt   string `json:"updated_at"`
}

// WorkerStatus represents worker status
type WorkerStatus struct {
	Name      string     `json:"name"`
	TaskID    string     `json:"task_id,omitempty"`
	Status    string     `json:"status"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// Phase represents a workflow phase
type Phase struct {
	ID          string `yaml:"id"`
	Subject     string `yaml:"subject"`
	Description string `yaml:"description"`
	BlockedBy   []string `yaml:"blocked_by,omitempty"`
}

// Workflow represents a workflow template
type Workflow struct {
	Name        string  `yaml:"name"`
	Description string  `yaml:"description"`
	Phases      []Phase `yaml:"phases"`
	// HumanLoop configuration for workflow
	HumanLoop *HumanLoopConfig `yaml:"human_loop,omitempty"`
}

// HumanLoopConfig defines human-in-the-loop configuration
type HumanLoopConfig struct {
	// ReviewPhases phases that require human review before continuing
	ReviewPhases []string `yaml:"review_phases" json:"review_phases"`
	// Checkpoints task IDs that act as checkpoints requiring approval
	Checkpoints []string `yaml:"checkpoints" json:"checkpoints"`
	// AutoApproveTimeout auto-approve after this duration (0 = never)
	AutoApproveTimeout time.Duration `yaml:"auto_approve_timeout" json:"auto_approve_timeout"`
	// NotifyCommand command to run when review needed
	NotifyCommand string `yaml:"notify_command" json:"notify_command"`
}

// ReviewRequest represents a pending review
type ReviewRequest struct {
	TaskID     string    `json:"task_id"`
	PhaseID    string    `json:"phase_id"`
	WorkerName string    `json:"worker_name"`
	Summary    string    `json:"summary"`
	OutputFile string    `json:"output_file"`
	CreatedAt  time.Time `json:"created_at"`
}

// ReviewResult represents a completed review
type ReviewResult struct {
	TaskID     string    `json:"task_id"`
	Approved   bool      `json:"approved"`
	Comment    string    `json:"comment,omitempty"`
	Reviewer   string    `json:"reviewer,omitempty"`
	ReviewedAt time.Time `json:"reviewed_at"`
}

// AIAgentIntegration config for main ai agent
type AIAgentIntegration struct {
	// Enabled whether to expose team API to ai agent
	Enabled bool `json:"enabled" yaml:"enabled"`
	// ToolName name of the tool exposed to ai agent
	ToolName string `json:"tool_name" yaml:"tool_name"`
	// AutoStart automatically start team when ai agent starts in project
	AutoStart bool `json:"auto_start" yaml:"auto_start"`
}
// TemplateInfo describes a workflow template
type TemplateInfo struct {
	Name        string
	Description string
	Path        string
}

// ListTemplates returns available templates from all sources
func ListTemplates() []*TemplateInfo {
	var templates []*TemplateInfo

	// Built-in templates
	templates = append(templates, &TemplateInfo{
		Name:        "feature",
		Description: "New feature development",
		Path:        "builtin",
	})
	templates = append(templates, &TemplateInfo{
		Name:        "bugfix",
		Description: "Bug fix with root cause analysis",
		Path:        "builtin",
	})
	templates = append(templates, &TemplateInfo{
		Name:        "hotfix",
		Description: "Urgent production fix",
		Path:        "builtin",
	})
	templates = append(templates, &TemplateInfo{
		Name:        "refactor",
		Description: "Code restructuring",
		Path:        "builtin",
	})
	templates = append(templates, &TemplateInfo{
		Name:        "spike",
		Description: "Research/exploration",
		Path:        "builtin",
	})

	return templates
}

// LogEntry represents a log entry
type LogEntry struct {
	Timestamp string
	TaskID    string
	Message   string
}

