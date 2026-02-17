package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
)

// SubagentRunner manages subagent execution.
type SubagentRunner struct {
	config   *config.Config
	model    llm.Model
	apiKey   string
	cwd      string
	registry *tools.Registry

	// Cache for loaded skills
	skills []skill.Skill
	once   sync.Once
}

// NewSubagentRunner creates a new SubagentRunner.
func NewSubagentRunner(cfg *config.Config, model llm.Model, apiKey, cwd string, registry *tools.Registry) *SubagentRunner {
	return &SubagentRunner{
		config:   cfg,
		model:    model,
		apiKey:   apiKey,
		cwd:      cwd,
		registry: registry,
	}
}

// Run executes a subagent task in headless mode.
func (r *SubagentRunner) Run(ctx context.Context, task string, allowedTools []string, maxTurns int) (string, error) {
	// 1. Build system prompt
	basePrompt := `You are a focused subagent executing a specific task.
Complete the task efficiently and report your findings.
Do not include chain-of-thought or thinking tags in your output.
Be concise and focused on the task at hand.`

	promptBuilder := prompt.NewBuilder(basePrompt, r.cwd)

	// Get available tools (filtered by whitelist)
	availableTools := r.getFilteredTools(allowedTools)
	promptBuilder.SetTools(availableTools)

	// Load skills (cached)
	skills := r.loadSkills()
	promptBuilder.SetSkills(skills)

	systemPrompt := promptBuilder.Build()

	// 2. Create isolated agent context
	agentCtx := agent.NewAgentContext(systemPrompt)
	for _, tool := range availableTools {
		agentCtx.AddTool(tool)
	}

	// Apply tool whitelist
	if allowedTools != nil {
		agentCtx.SetAllowedTools(allowedTools)
	}

	// 3. Create agent
	ag := agent.NewAgentWithContext(r.model, r.apiKey, agentCtx)
	defer ag.Shutdown()

	// 4. Configure compactor (lighter config for subagents)
	compactorConfig := r.config.Compactor
	if compactorConfig == nil {
		compactorConfig = compact.DefaultConfig()
	}
	compactor := compact.NewCompactor(
		compactorConfig,
		r.model,
		r.apiKey,
		basePrompt,
		64000, // smaller context window for subagents
	)
	ag.SetCompactor(&noopCompactor{compactor})
	ag.SetToolCallCutoff(compactorConfig.ToolCallCutoff)
	ag.SetToolSummaryStrategy(compactorConfig.ToolSummaryStrategy)

	// 5. Configure executor
	concurrencyConfig := r.config.Concurrency
	if concurrencyConfig == nil {
		concurrencyConfig = config.DefaultConcurrencyConfig()
	}
	executor := agent.NewExecutorPool(map[string]int{
		"maxConcurrentTools": concurrencyConfig.MaxConcurrentTools,
		"toolTimeout":        concurrencyConfig.ToolTimeout,
		"queueTimeout":       concurrencyConfig.QueueTimeout,
	})
	ag.SetExecutor(executor)

	// 6. Configure tool output limits
	toolOutputConfig := r.config.ToolOutput
	if toolOutputConfig == nil {
		toolOutputConfig = config.DefaultToolOutputConfig()
	}
	ag.SetToolOutputLimits(agent.ToolOutputLimits{
		MaxLines:             toolOutputConfig.MaxLines,
		MaxBytes:             toolOutputConfig.MaxBytes,
		MaxChars:             toolOutputConfig.MaxChars,
		LargeOutputThreshold: toolOutputConfig.LargeOutputThreshold,
		TruncateMode:         toolOutputConfig.TruncateMode,
	})

	// 7. Run the agent (non-streaming, collect result)
	err := ag.Prompt(task)
	if err != nil {
		return "", fmt.Errorf("subagent prompt failed: %w", err)
	}

	// Wait for completion
	ag.Wait()

	// 8. Extract final text from messages
	messages := agentCtx.Messages
	finalText := agent.GetFinalAssistantText(messages)

	return finalText, nil
}

// getFilteredTools returns tools filtered by the whitelist.
// If whitelist is nil, returns all tools except "subagent" (no nesting).
func (r *SubagentRunner) getFilteredTools(whitelist []string) []agent.Tool {
	allTools := r.registry.All()
	if whitelist == nil {
		// Return all tools except subagent
		result := make([]agent.Tool, 0, len(allTools))
		for _, t := range allTools {
			if t.Name() != "subagent" {
				result = append(result, t)
			}
		}
		return result
	}

	// Filter by whitelist
	whitelistSet := make(map[string]bool)
	for _, name := range whitelist {
		whitelistSet[name] = true
	}
	// Always exclude subagent (no nesting)
	delete(whitelistSet, "subagent")

	result := make([]agent.Tool, 0, len(whitelist))
	for _, t := range allTools {
		if whitelistSet[t.Name()] {
			result = append(result, t)
		}
	}
	return result
}

// loadSkills loads skills (cached after first load)
func (r *SubagentRunner) loadSkills() []skill.Skill {
	r.once.Do(func() {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			r.skills = []skill.Skill{}
			return
		}

		agentDir := filepath.Join(homeDir, ".ai")
		skillLoader := skill.NewLoader(agentDir)
		skillResult := skillLoader.Load(&skill.LoadOptions{
			CWD:             r.cwd,
			AgentDir:        agentDir,
			SkillPaths:      nil,
			IncludeDefaults: true,
		})
		r.skills = skillResult.Skills
	})

	return r.skills
}

// noopCompactor wraps a compactor but does no session writing
type noopCompactor struct {
	*compact.Compactor
}

func (n *noopCompactor) CompactIfNeeded(messages []agent.AgentMessage) ([]agent.AgentMessage, error) {
	return n.Compactor.CompactIfNeeded(messages)
}

// noopSessionWriter for subagents (no persistence)
type noopSessionWriter struct{}

func (n *noopSessionWriter) Append(sess *session.Session, msg agent.AgentMessage) {}
func (n *noopSessionWriter) Close() error                                         { return nil }
