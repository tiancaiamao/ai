package context

import (
	"context"
	"log/slog"

	"github.com/tiancaiamao/ai/pkg/skill"
)

// AgentContext represents the context for agent execution.
type AgentContext struct {
	SystemPrompt          string         `json:"systemPrompt,omitempty"`
	Messages              []AgentMessage `json:"messages"`
	Tools                 []Tool         `json:"tools,omitempty"`
	Skills                []skill.Skill  `json:"skills,omitempty"`                // Loaded skills
	LastCompactionSummary string         `json:"lastCompactionSummary,omitempty"` // Last compaction summary for incremental updates

	// allowedTools restricts which tools can be executed.
	// nil means all tools are allowed, non-nil is a whitelist.
	allowedTools map[string]bool `json:"-"`

	// LLMContext is the agent's llm context for context management.
	LLMContext *LLMContext `json:"-"`

	// Runtime meta snapshot state for stable system-tail injection.
	RuntimeMetaSnapshot string `json:"-"`
	RuntimeMetaBand     string `json:"-"`
	RuntimeMetaTurns    int    `json:"-"`

	// ContextMgmtState tracks LLM's context management behavior for adaptive reminders.
	ContextMgmtState *ContextMgmtState `json:"-"`
}

// ContextMgmtState tracks LLM's context management decisions for adaptive reminder frequency.
type ContextMgmtState struct {
	// Frequency adjustment (turns between reminders)
	ReminderFrequency int    // Current: 5-30, Default: 10
	SkipUntilTurn     int    // Skip reminders until this turn (set by LLM via skip_turns)

	// Statistics for adaptive adjustment
	ProactiveDecisions int // LLM made decisions without being reminded
	ReminderNeeded     int // LLM needed reminders to make decisions

	// Turn tracking
	CurrentTurn int // Current turn number (updated every loop iteration)

	// Last action state
	LastDecisionTurn  int    // Turn number of last decision
	LastActionTaken   string // "truncate", "compact", "both", "skip"
	LastReminderTurn  int    // Turn number of last reminder shown
	LastReminderUrgency string // "none", "low", "medium", "high", "critical"

	// Compliance tracking
	ReminderShownThisTurn bool // Was reminder shown in current turn?
	DecisionMadeThisTurn bool // Did LLM call llm_context_decision this turn?
}

// DefaultContextMgmtState creates a new ContextMgmtState with defaults.
func DefaultContextMgmtState() *ContextMgmtState {
	return &ContextMgmtState{
		ReminderFrequency:    10, // Default: remind every 10 turns
		SkipUntilTurn:       0,
		ProactiveDecisions:  0,
		ReminderNeeded:      0,
		CurrentTurn:         0,
		LastDecisionTurn:    0,
		LastActionTaken:     "none",
		LastReminderTurn:    0,
		LastReminderUrgency: "none",
	}
}

// MarkReminderShown marks that a reminder was shown this turn.
func (s *ContextMgmtState) MarkReminderShown() {
	if s == nil {
		return
	}
	s.ReminderShownThisTurn = true
}

// MarkDecisionMade marks that LLM called llm_context_decision this turn.
func (s *ContextMgmtState) MarkDecisionMade() {
	if s == nil {
		return
	}
	s.DecisionMadeThisTurn = true
}

// ResetTurnTracking resets per-turn tracking flags at the start of each turn.
func (s *ContextMgmtState) ResetTurnTracking() {
	if s == nil {
		return
	}
	s.ReminderShownThisTurn = false
	s.DecisionMadeThisTurn = false
}

// CheckAndApplyCompliance checks if LLM complied with the protocol when reminder was shown.
// If reminder was shown but LLM didn't call llm_context_decision, apply penalty.
func (s *ContextMgmtState) CheckAndApplyCompliance() {
	if s == nil {
		return
	}
	// If reminder was shown but LLM didn't make a decision, apply penalty
	if s.ReminderShownThisTurn && !s.DecisionMadeThisTurn {
		s.ReminderNeeded++
		s.AdjustFrequency()
		slog.Info("[ContextMgmt] LLM ignored reminder, applying penalty",
			"reminder_shown", s.ReminderShownThisTurn,
			"decision_made", s.DecisionMadeThisTurn,
			"reminder_needed", s.ReminderNeeded,
		)
	}
}

// AdjustFrequency adjusts the reminder frequency based on LLM's behavior.
// More proactive decisions = lower frequency (fewer reminders).
// More reminders needed = higher frequency (more reminders).
func (s *ContextMgmtState) AdjustFrequency() {
	if s == nil {
		return
	}

	// Calculate ratio: positive means proactive, negative means needs reminders
	ratio := s.ProactiveDecisions - s.ReminderNeeded

	switch {
	case ratio >= 5: // Very proactive
		s.ReminderFrequency = min(30, s.ReminderFrequency+2)
	case ratio >= 2: // Somewhat proactive
		s.ReminderFrequency = min(30, s.ReminderFrequency+1)
	case ratio <= -5: // Needs many reminders
		s.ReminderFrequency = max(5, s.ReminderFrequency-2)
	case ratio <= -2: // Needs some reminders
		s.ReminderFrequency = max(5, s.ReminderFrequency-1)
	}
	// If ratio is between -1 and 1, keep current frequency
}

// RecordDecision records a context management decision made by LLM.
func (s *ContextMgmtState) RecordDecision(turn int, action string, wasReminded bool) {
	if s == nil {
		return
	}

	s.LastDecisionTurn = turn
	s.LastActionTaken = action

	if wasReminded {
		s.ReminderNeeded++
	} else {
		s.ProactiveDecisions++
	}

	s.AdjustFrequency()
}

// SetSkipUntil sets the skip period and counts it as proactive if LLM promises to check back later.
func (s *ContextMgmtState) SetSkipUntil(turn, skipTurns int, wasReminded bool) {
	if s == nil {
		return
	}

	s.SkipUntilTurn = turn + skipTurns

	// Setting a skip is considered proactive behavior
	if !wasReminded {
		s.ProactiveDecisions++
		s.AdjustFrequency()
	}
}

// ShouldShowReminder determines if a reminder should be shown this turn.
// tokensPercent is the current context usage percentage (0-100). If < 10, reminders are suppressed
// unless urgency is critical.
func (s *ContextMgmtState) ShouldShowReminder(turn int, actionRequired string, urgency string, tokensPercent int) bool {
	if s == nil {
		return false
	}

	// Always show for critical urgency
	if urgency == "critical" {
		return true
	}

	// Don't show if no action is required
	if actionRequired == "none" {
		return false
	}

	// Don't show if we're in a skip period
	// Use < (not <=) because the skip was set in a previous turn, and we want to suppress
	// reminders in the turns AFTER the skip was set, not including the current turn
	if turn < s.SkipUntilTurn {
		return false
	}

	// Don't show reminders if context usage is below 10% (unless critical)
	// This reduces noise when there's plenty of context space available
	if tokensPercent < 10 {
		return false
	}

	// Check if enough turns have passed since last reminder
	turnsSinceReminder := turn - s.LastReminderTurn
	return turnsSinceReminder >= s.ReminderFrequency
}

// RecordReminder records that a reminder was shown.
func (s *ContextMgmtState) RecordReminder(turn int, urgency string) {
	if s == nil {
		return
	}

	s.LastReminderTurn = turn
	s.LastReminderUrgency = urgency
}

// GetScore returns a human-readable score of LLM's proactiveness.
func (s *ContextMgmtState) GetScore() string {
	if s == nil {
		return "unknown"
	}

	ratio := s.ProactiveDecisions - s.ReminderNeeded
	total := s.ProactiveDecisions + s.ReminderNeeded
	if total == 0 {
		return "no_data"
	}

	switch {
	case ratio >= 5:
		return "excellent"
	case ratio >= 2:
		return "good"
	case ratio >= -1:
		return "fair"
	default:
		return "needs_improvement"
	}
}

// Tool represents a tool that can be called by the agent.
type Tool interface {
	// Name returns the tool name.
	Name() string

	// Description returns a description of what the tool does.
	Description() string

	// Parameters returns the JSON Schema for the tool parameters.
	Parameters() map[string]any

	// Execute executes the tool with the given arguments.
	Execute(ctx context.Context, args map[string]any) ([]ContentBlock, error)
}

type toolExecutionAgentContextKey struct{}

// WithToolExecutionAgentContext stores the current loop AgentContext in ctx so
// tools can mutate the active turn state instead of stale outer pointers.
func WithToolExecutionAgentContext(ctx context.Context, agentCtx *AgentContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolExecutionAgentContextKey{}, agentCtx)
}

// ToolExecutionAgentContext returns the active loop AgentContext for the
// current tool execution when available.
func ToolExecutionAgentContext(ctx context.Context) *AgentContext {
	if ctx == nil {
		return nil
	}
	agentCtx, _ := ctx.Value(toolExecutionAgentContextKey{}).(*AgentContext)
	return agentCtx
}

// NewAgentContext creates a new AgentContext with the given system prompt.
func NewAgentContext(systemPrompt string) *AgentContext {
	return &AgentContext{
		SystemPrompt: systemPrompt,
		Messages:     make([]AgentMessage, 0),
		Tools:        make([]Tool, 0),
		Skills:       make([]skill.Skill, 0),
	}
}

// NewAgentContextWithSkills creates a new AgentContext with skills.
func NewAgentContextWithSkills(systemPrompt string, skills []skill.Skill) *AgentContext {
	ctx := NewAgentContext(systemPrompt)
	ctx.Skills = skills

	// Append skills to system prompt
	if len(skills) > 0 {
		skillsText := skill.FormatForPrompt(skills)
		if skillsText != "" {
			ctx.SystemPrompt = systemPrompt + skillsText
		}
	}

	return ctx
}

// AddMessage adds a message to the context.
func (c *AgentContext) AddMessage(message AgentMessage) {
	c.Messages = append(c.Messages, message)
}

// AddTool adds a tool to the context.
func (c *AgentContext) AddTool(tool Tool) {
	if tool == nil {
		return
	}
	name := tool.Name()
	for _, existing := range c.Tools {
		if existing == nil {
			continue
		}
		if existing.Name() == name {
			return
		}
	}
	c.Tools = append(c.Tools, tool)
}

// GetTool returns a tool by name, or nil if not found.
func (c *AgentContext) GetTool(name string) Tool {
	for _, tool := range c.Tools {
		if tool.Name() == name {
			return tool
		}
	}
	return nil
}

// SetAllowedTools sets the whitelist of allowed tools.
// Pass nil to allow all tools (default behavior).
func (c *AgentContext) SetAllowedTools(tools []string) {
	if tools == nil {
		c.allowedTools = nil
		return
	}
	c.allowedTools = make(map[string]bool)
	for _, name := range tools {
		c.allowedTools[name] = true
	}
}

// IsToolAllowed checks if a tool is allowed to be executed.
// Returns true if allowedTools is nil (all allowed) or if the tool is in the whitelist.
func (c *AgentContext) IsToolAllowed(name string) bool {
	if c.allowedTools == nil {
		return true
	}
	return c.allowedTools[name]
}

// GetAllowedTools returns the list of allowed tool names.
// Returns nil if all tools are allowed.
func (c *AgentContext) GetAllowedTools() []string {
	if c.allowedTools == nil {
		return nil
	}
	result := make([]string, 0, len(c.allowedTools))
	for name := range c.allowedTools {
		result = append(result, name)
	}
	return result
}

// GetAllowedToolsMap returns the internal allowed tools map for efficient lookup.
// Returns nil if all tools are allowed.
func (c *AgentContext) GetAllowedToolsMap() map[string]bool {
	return c.allowedTools
}
