package context

import (
	"context"
	"log/slog"
	"sync"

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

	// LLMContext is the agent's llm context file manager.
	LLMContext *LLMContext `json:"-"`

	// TaskTrackingState tracks task_tracking behavior for reminders.
	TaskTrackingState *TaskTrackingState `json:"-"`

	// Runtime meta snapshot state for stable system-tail injection.
	RuntimeMetaSnapshot string `json:"-"`
	RuntimeMetaBand     string `json:"-"`
	RuntimeMetaTurns    int    `json:"-"`

	// ContextMgmtState tracks LLM's context management behavior for adaptive reminders.
	// Persisted across compacts/restarts so proactive/reminded counters survive.
	ContextMgmtState *ContextMgmtState `json:"-"`

	// PostCompactRecovery indicates that overview.md should be injected for recovery after compact.
	// Set after compact completes, reset after injection.
	PostCompactRecovery bool `json:"-"`

	// AllowReminders controls whether reminders should be injected in this turn.
	// Set to true only when:
	// 1. First turn (user initiated)
	// 2. Previous turn had tool calls (loop will continue)
	// This prevents reminders from triggering unwanted LLM responses when
	// the assistant is about to end the conversation.
	AllowReminders bool `json:"-"`

	// OnMessagesChanged is called when messages are modified (e.g., after compact).
	// This allows persistence to session storage.
	OnMessagesChanged func() error `json:"-"`

	// contextMgmtMu serializes context_management mutations to shared turn state
	// and assistant message tool-call arguments.
	contextMgmtMu sync.Mutex `json:"-"`
}

// ContextMgmtState tracks LLM's context management decisions for adaptive reminder frequency.
// Runtime state only — resets on agent restart, which is expected behavior.
type ContextMgmtState struct {
	mu sync.RWMutex `json:"-"`

	// Frequency adjustment (turns between reminders)
	ReminderFrequency int `json:"-"`
	SkipUntilTurn     int `json:"-"` // Skip reminders until this turn (set by LLM via skip_turns)

	// Statistics for adaptive adjustment
	ProactiveDecisions int `json:"-"` // LLM made decisions without being reminded
	ReminderNeeded     int `json:"-"` // LLM needed reminders to make decisions

	// Turn tracking
	CurrentTurn int `json:"-"` // Current turn number (updated every loop iteration)

	// Last action state
	LastDecisionTurn    int    `json:"-"` // Turn number of last decision
	LastActionTaken     string `json:"-"` // "truncate", "compact", "both", "skip"
	LastReminderTurn    int    `json:"-"` // Turn number of last reminder shown
	LastReminderUrgency string `json:"-"` // "none", "low", "medium", "high", "critical"

	// Decision reminder state (independent from task_tracking reminder)
	DecisionPressureTurns int `json:"-"` // Consecutive turns under decision pressure
	LastPressureTurn      int `json:"-"` // Last turn when pressure was observed

	// Compliance tracking (ephemeral per-turn, not persisted)
	ReminderShownThisTurn bool `json:"-"` // Was reminder shown in current turn?
	DecisionMadeThisTurn  bool `json:"-"` // Did LLM call context_management this turn?
}

// ContextMgmtSnapshot is an immutable runtime view of ContextMgmtState.
type ContextMgmtSnapshot struct {
	ReminderFrequency  int
	ProactiveDecisions int
	ReminderNeeded     int
	CurrentTurn        int
	LastReminderTurn   int
	Score              string
}

const (
	decisionPressureTokensWithStale = 30.0
	decisionPressureTokensOnly      = 40.0 // More aggressive: trigger at 40% instead of 50%
	decisionPressureStaleOnly       = 10
	decisionReminderWarmupTurns     = 2
)

// DefaultContextMgmtState creates a new ContextMgmtState with defaults.
func DefaultContextMgmtState() *ContextMgmtState {
	return &ContextMgmtState{
		ReminderFrequency:     10, // Default: remind every 10 turns
		SkipUntilTurn:         0,
		ProactiveDecisions:    0,
		ReminderNeeded:        0,
		CurrentTurn:           0,
		LastDecisionTurn:      0,
		LastActionTaken:       "none",
		LastReminderTurn:      0,
		LastReminderUrgency:   "none",
		DecisionPressureTurns: 0,
		LastPressureTurn:      0,
	}
}

// MarkReminderShown marks that a reminder was shown this turn.
func (s *ContextMgmtState) MarkReminderShown() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ReminderShownThisTurn = true
}

// RecordDecisionForCurrentTurn records a decision using the state's current turn
// and reminder flags. Returns whether the decision was reminded.
func (s *ContextMgmtState) RecordDecisionForCurrentTurn(action string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	wasReminded := s.ReminderShownThisTurn
	s.recordDecisionLocked(s.CurrentTurn, action, wasReminded)
	return wasReminded
}

// ResetTurnTracking resets per-turn tracking flags at the start of each turn.
func (s *ContextMgmtState) ResetTurnTracking() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ReminderShownThisTurn = false
	s.DecisionMadeThisTurn = false
}

// CheckAndApplyCompliance checks if LLM complied with the protocol when reminder was shown.
// If reminder was shown but LLM didn't call context_management, apply penalty.
func (s *ContextMgmtState) CheckAndApplyCompliance() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// If reminder was shown but LLM didn't make a decision, apply penalty
	if s.ReminderShownThisTurn && !s.DecisionMadeThisTurn {
		s.ReminderNeeded++
		s.adjustFrequencyLocked()
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
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adjustFrequencyLocked()
}

func (s *ContextMgmtState) adjustFrequencyLocked() {
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

func (s *ContextMgmtState) recordDecisionLocked(turn int, action string, wasReminded bool) {
	s.DecisionMadeThisTurn = true
	s.LastDecisionTurn = turn
	if action == "" {
		action = "none"
	}
	s.LastActionTaken = action

	if wasReminded {
		s.ReminderNeeded++
	} else {
		s.ProactiveDecisions++
	}
	s.DecisionPressureTurns = 0
	s.LastPressureTurn = 0

	s.adjustFrequencyLocked()
}

// SetSkipUntil sets the skip period.
// Decision statistics are recorded by RecordDecisionForCurrentTurn.
func (s *ContextMgmtState) SetSkipUntil(turn, skipTurns int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	s.SkipUntilTurn = turn + skipTurns
}

// ShouldShowReminder determines if a reminder should be shown this turn.
// tokensPercent is the current context usage percentage (0-100). If < 10, reminders are suppressed
// unless urgency is critical.
func (s *ContextMgmtState) ShouldShowReminder(turn int, actionRequired string, urgency string, tokensPercent int) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

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
	s.mu.Lock()
	defer s.mu.Unlock()

	s.LastReminderTurn = turn
	s.LastReminderUrgency = urgency
	s.ReminderShownThisTurn = true
}

// DecisionPressureRequired reports whether context_management should be considered.
func DecisionPressureRequired(tokensPercent float64, staleToolOutputs int) bool {
	return (tokensPercent >= decisionPressureTokensWithStale && staleToolOutputs > 0) ||
		tokensPercent >= decisionPressureTokensOnly ||
		staleToolOutputs >= decisionPressureStaleOnly
}

// DecisionReminderUrgency classifies pressure severity for reminder telemetry.
func DecisionReminderUrgency(tokensPercent float64, staleToolOutputs int) string {
	switch {
	case tokensPercent >= 75 || staleToolOutputs >= 40:
		return "critical"
	case tokensPercent >= 60 || staleToolOutputs >= 25:
		return "high"
	case tokensPercent >= 45 || staleToolOutputs >= 15:
		return "medium"
	default:
		return "low"
	}
}

// ShouldShowDecisionReminder evaluates decision pressure and decides whether to remind this turn.
// This logic is independent from task_tracking reminder state.
func (s *ContextMgmtState) ShouldShowDecisionReminder(turn int, tokensPercent float64, staleToolOutputs int) (bool, string) {
	if s == nil {
		return false, "none"
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	pressure := DecisionPressureRequired(tokensPercent, staleToolOutputs)
	urgency := DecisionReminderUrgency(tokensPercent, staleToolOutputs)
	if !pressure {
		s.DecisionPressureTurns = 0
		s.LastPressureTurn = 0
		return false, "none"
	}

	// Count pressured turns only once per turn.
	if s.LastPressureTurn != turn {
		s.DecisionPressureTurns++
		s.LastPressureTurn = turn
	}

	if turn < s.SkipUntilTurn {
		return false, urgency
	}
	if s.DecisionPressureTurns < decisionReminderWarmupTurns {
		return false, urgency
	}

	// First reminder for a pressure wave should be immediate after warmup.
	if s.LastReminderTurn == 0 {
		return true, urgency
	}

	turnsSinceReminder := turn - s.LastReminderTurn
	return turnsSinceReminder >= s.ReminderFrequency, urgency
}

// GetScore returns a human-readable score of LLM's proactiveness.
func (s *ContextMgmtState) GetScore() string {
	if s == nil {
		return "unknown"
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return scoreFromCounts(s.ProactiveDecisions, s.ReminderNeeded)
}

func scoreFromCounts(proactiveDecisions, reminderNeeded int) string {
	ratio := proactiveDecisions - reminderNeeded
	total := proactiveDecisions + reminderNeeded
	if total == 0 {
		return "no_data"
	}

	switch {
	case ratio >= 5:
		return "excellent"
	case ratio >= 2:
		return "good"
	case ratio >= 1:
		return "fair"
	default:
		// proactive <= reminded: needs improvement
		return "needs_improvement"
	}
}

// SetCurrentTurn updates the state's current turn counter.
func (s *ContextMgmtState) SetCurrentTurn(turn int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.CurrentTurn = turn
}

// GetCurrentTurn returns the current turn number.
func (s *ContextMgmtState) GetCurrentTurn() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.CurrentTurn
}

// Snapshot returns a consistent view for read-heavy callers.
func (s *ContextMgmtState) Snapshot() ContextMgmtSnapshot {
	if s == nil {
		return ContextMgmtSnapshot{Score: "unknown"}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ContextMgmtSnapshot{
		ReminderFrequency:  s.ReminderFrequency,
		ProactiveDecisions: s.ProactiveDecisions,
		ReminderNeeded:     s.ReminderNeeded,
		CurrentTurn:        s.CurrentTurn,
		LastReminderTurn:   s.LastReminderTurn,
		Score:              scoreFromCounts(s.ProactiveDecisions, s.ReminderNeeded),
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
type toolExecutionCallIDKey struct{}

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

// WithToolExecutionCallID stores the current tool call ID in ctx so tools can
// access their own call metadata without sharing mutable state across goroutines.
func WithToolExecutionCallID(ctx context.Context, toolCallID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, toolExecutionCallIDKey{}, toolCallID)
}

// ToolExecutionCallID returns the current tool call ID for the running tool
// execution, when available.
func ToolExecutionCallID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	toolCallID, _ := ctx.Value(toolExecutionCallIDKey{}).(string)
	return toolCallID
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

// LockContextManagement serializes context_management updates on this context.
func (c *AgentContext) LockContextManagement() {
	if c == nil {
		return
	}
	c.contextMgmtMu.Lock()
}

// UnlockContextManagement releases the context_management serialization lock.
func (c *AgentContext) UnlockContextManagement() {
	if c == nil {
		return
	}
	c.contextMgmtMu.Unlock()
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
