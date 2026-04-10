package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/internal/evolvemini"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--help" {
		fmt.Fprintln(os.Stderr, "Worker: reads WorkerInput JSON from stdin, executes mini compact, writes WorkerOutput to stdout")
		os.Exit(0)
	}

	var input evolvemini.WorkerInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		fmt.Fprintf(os.Stderr, "parse stdin: %v\n", err)
		os.Exit(1)
	}

	result := executeCompact(input)
	output, _ := json.Marshal(result)
	os.Stdout.Write(output)
}

func executeCompact(input evolvemini.WorkerInput) evolvemini.WorkerOutput {
	snap := input.Snapshot

	agentCtx := &agentctx.AgentContext{
		RecentMessages: snap.RecentMessages,
		LLMContext:     snap.LLMContext,
		AgentState:     snap.AgentState,
		SystemPrompt:   prompt.CompactorBasePrompt(),
	}
	if agentCtx.AgentState == nil {
		agentCtx.AgentState = agentctx.NewAgentState("", "")
		agentCtx.AgentState.TokensLimit = 200_000
		agentCtx.AgentState.TotalTurns = len(snap.RecentMessages) / 2
	}

	tokensBefore := agentCtx.EstimateTokens()
	contextBefore := serializeMessages(agentCtx.RecentMessages)
	llmContext := snap.LLMContext

	apiKey := os.Getenv("ZAI_API_KEY")
	if apiKey == "" {
		return errorOutput("ZAI_API_KEY not set")
	}

	modelID := os.Getenv("ZAI_MODEL_ID")
	if modelID == "" {
		modelID = "gpt-4o-mini"
	}

	envBaseURL := os.Getenv("ZAI_BASE_URL")
	envAPI := os.Getenv("ZAI_MODEL_API")

	model := llm.Model{
		ID:            modelID,
		Provider:      "zai",
		BaseURL:       envBaseURL,
		API:           envAPI,
		ContextWindow: snap.ContextWindow,
	}
	if model.API == "" {
		model.API = "openai-completions"
	}

	compactor := compact.NewLLMMiniCompactor(
		compact.DefaultLLMMiniCompactorConfig(),
		model,
		apiKey,
		snap.ContextWindow,
		prompt.LLMMiniCompactSystemPrompt(),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := compactor.CompactWithCtx(ctx, agentCtx)
	if err != nil {
		return errorOutput(fmt.Sprintf("compact failed: %v", err))
	}

	tokensAfter := agentCtx.EstimateTokens()
	contextAfter := serializeMessages(agentCtx.RecentMessages)

	return evolvemini.WorkerOutput{
		Success:         result.Summary != "",
		TokensBefore:    tokensBefore,
		TokensAfter:     tokensAfter,
		InferredToolAction: inferToolAction(contextBefore, contextAfter, llmContext, agentCtx.LLMContext),
		ContextBefore:   contextBefore,
		ContextAfter:    contextAfter,
		LLMContextBefore: llmContext,
		LLMContextAfter: agentCtx.LLMContext,
	}
}

func serializeMessages(msgs []agentctx.AgentMessage) string {
	var b strings.Builder
	for _, msg := range msgs {
		fmt.Fprintf(&b, "[%s]\n", msg.Role)

		for _, bl := range msg.Content {
			switch c := bl.(type) {
			case agentctx.TextContent:
				b.WriteString(c.Text)
			case agentctx.ToolCallContent:
				argsJSON, _ := json.Marshal(c.Arguments)
				fmt.Fprintf(&b, "toolCall(%s, %s)\n", c.Name, string(argsJSON))
			case agentctx.ThinkingContent:
				fmt.Fprintf(&b, "<thinking>%s</thinking>\n", c.Thinking)
			}
		}

		if msg.Role == "toolResult" {
			fmt.Fprintf(&b, "[toolResult:%s] %s\n", msg.ToolName, msg.ExtractText())
		}

		b.WriteString("\n---\n")
	}
	return b.String()
}

func errorOutput(errMsg string) evolvemini.WorkerOutput {
	return evolvemini.WorkerOutput{
		Success: false,
		Error:   errMsg,
	}
}

func inferToolAction(beforeCtx, afterCtx, beforeLLMContext, afterLLMContext string) string {
	// Check for LLMContext update
	if beforeLLMContext != afterLLMContext && len(afterLLMContext) > 0 {
		// If LLMContext changed significantly, it was update_llm_context
		if len(afterLLMContext) > len(beforeLLMContext)+100 || len(beforeLLMContext) == 0 {
			return "update_llm_context"
		}
	}

	// Check for truncation
	// Truncation adds "[truncated] markers or shortens messages
	if strings.Contains(afterCtx, "[truncated]") ||
	   strings.Contains(afterCtx, "...truncated") ||
	   len(afterCtx) < len(beforeCtx)*9/10 {
		return "truncate_messages"
	}

	// If tokens barely changed but no truncation, it's no_action
	if len(beforeCtx) == len(afterCtx) && beforeLLMContext == afterLLMContext {
		return "no_action"
	}

	return "unknown"
}