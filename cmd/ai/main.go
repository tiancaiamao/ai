package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/rpc"
)

func parseToolsFlag(toolsFlag string) []string {
	if toolsFlag == "" {
		return nil
	}
	tools := strings.Split(toolsFlag, ",")
	result := make([]string, 0, len(tools))
	for _, t := range tools {
		t = strings.TrimSpace(t)
		if t != "" {
			result = append(result, t)
		}
	}
	return result
}

// parseSystemPrompt parses the --system-prompt flag.
// If the value starts with '@', it reads the file content.
// Otherwise, it returns the value as-is.
func parseSystemPrompt(systemPromptFlag string) string {
	if systemPromptFlag == "" {
		return ""
	}
	// If starts with '@', read file
	if strings.HasPrefix(systemPromptFlag, "@") {
		filePath := strings.TrimPrefix(systemPromptFlag, "@")
		filePath = strings.TrimSpace(filePath)
		if filePath == "" {
			slog.Warn("empty file path after '@' in --system-prompt flag")
			return ""
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			slog.Error("failed to read system-prompt file", "path", filePath, "error", err)
			return ""
		}
		// Limit file size to 64KB
		if len(content) > 64*1024 {
			slog.Warn("system-prompt file too large, truncating to 64KB", "size", len(content))
			content = content[:64*1024]
		}
		return string(content)
	}
	// Otherwise, use the value as-is
	return systemPromptFlag
}

func main() {
	mode := flag.String("mode", "", "Run mode (rpc|win|headless). Default: win")
	sessionPathFlag := flag.String("session", "", "Session file path")
	debugAddr := flag.String("http", "", "Enable HTTP debug server on specified address (e.g., ':6060')")
	windowName := flag.String("name", "", "window name (default +ai)")
	maxTurns := flag.Int("max-turns", 0, "Maximum agent turns in headless mode (0 = unlimited)")
	timeoutFlag := flag.Duration("timeout", 0, "Total execution timeout in headless mode (0 = unlimited, e.g., 10m)")
	systemPromptFlag := flag.String("system-prompt", "", "System prompt (use @file to load from file)")
	toolsFlag := flag.String("tools", "", "Comma-separated list of tools to enable")
	keepToolsFlag := flag.Bool("keep-tools", false, "Keep existing tools when adding new ones")
	contextManagementPromptFlag := flag.String("context-management-prompt", "", "Custom context management prompt")
	flag.Parse()

	systemPrompt := parseSystemPrompt(*systemPromptFlag)
	tools := parseToolsFlag(*toolsFlag)

	switch *mode {
	case "rpc":
		if err := runRPC(*sessionPathFlag, *debugAddr, *maxTurns, os.Stdin, os.Stdout); err != nil {
			slog.Error("rpc error", "error", err)
			os.Exit(1)
		}
	case "headless":
		if err := runHeadless(*sessionPathFlag, *debugAddr, *maxTurns, *timeoutFlag, systemPrompt, tools, *keepToolsFlag, *contextManagementPromptFlag, flag.Args(), os.Stdout); err != nil {
			slog.Error("headless error", "error", err)
			os.Exit(1)
		}
	case "win", "":
		if err := runWinAI(*windowName, *sessionPathFlag, *debugAddr); err != nil {
			slog.Error("win-ai error", "error", err)
			os.Exit(1)
		}
	default:
		slog.Error("invalid mode", "mode", *mode, "valid_modes", "rpc|win|headless")
		slog.Info("Note: json mode is temporarily disabled during architecture migration")
		os.Exit(1)
	}
}

type headlessRPCResponse struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Command string          `json:"command"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type headlessStreamState struct {
	lastAssistantText string
}

func runHeadless(sessionPath, debugAddr string, maxTurns int, timeout time.Duration, systemPrompt string, toolList []string, keepTools bool, cmPrompt string, prompts []string, output io.Writer) error {
	if len(prompts) == 0 {
		return fmt.Errorf("headless mode requires at least one prompt argument")
	}

	// Create overall timeout context
	var overallCtx context.Context
	var overallCancel context.CancelFunc
	if timeout > 0 {
		overallCtx, overallCancel = context.WithTimeout(context.Background(), timeout)
		defer overallCancel()
		slog.Info("Headless mode timeout set", "timeout", timeout)
	} else {
		overallCtx, overallCancel = context.WithCancel(context.Background())
		defer overallCancel()
	}

	rpcInReader, rpcInWriter := io.Pipe()
	rpcOutReader, rpcOutWriter := io.Pipe()

	rpcErrCh := make(chan error, 1)
	go func() {
		defer rpcOutWriter.Close()
		rpcErrCh <- runRPC(sessionPath, debugAddr, maxTurns, rpcInReader, rpcOutWriter, RPCOption{
			SystemPrompt: systemPrompt,
			Tools:        toolList,
			KeepTools:    keepTools,
			CMPrompt:     cmPrompt,
		})
	}()

	lineCh := make(chan []byte, 256)
	lineErrCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(rpcOutReader)
		buf := make([]byte, 0, 1024*1024)
		scanner.Buffer(buf, 64*1024*1024)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			lineCh <- line
		}
		close(lineCh)
		lineErrCh <- scanner.Err()
		close(lineErrCh)
	}()

	var state headlessStreamState
	printed := false

	// Process prompts with timeout check
	for i, promptText := range prompts {
		// Check timeout before processing each prompt
		select {
		case <-overallCtx.Done():
			return fmt.Errorf("timeout after %s", timeout)
		default:
		}

		state.lastAssistantText = ""

		promptID := fmt.Sprintf("headless-prompt-%d", i+1)
		if err := writeHeadlessCommand(rpcInWriter, rpc.RPCCommand{
			ID:      promptID,
			Type:    rpc.CommandPrompt,
			Message: promptText,
		}); err != nil {
			return err
		}

		resp, err := waitHeadlessResponseWithContext(lineCh, lineErrCh, overallCtx, promptID, &state)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return fmt.Errorf("timeout after %s", timeout)
			}
			return err
		}
		if !resp.Success {
			return fmt.Errorf("prompt failed: %s", resp.Error)
		}

		text := strings.TrimSpace(state.lastAssistantText)
		if text == "" {
			msgID := fmt.Sprintf("headless-messages-%d", i+1)
			if err := writeHeadlessCommand(rpcInWriter, rpc.RPCCommand{
				ID:   msgID,
				Type: rpc.CommandGetMessages,
			}); err != nil {
				return err
			}

			msgResp, err := waitHeadlessResponseWithContext(lineCh, lineErrCh, overallCtx, msgID, &state)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					return fmt.Errorf("timeout after %s", timeout)
				}
				return err
			}
			if !msgResp.Success {
				return fmt.Errorf("get_messages failed: %s", msgResp.Error)
			}

			text = strings.TrimSpace(extractLastAssistantText(msgResp.Data))
		}

		if text != "" {
			if printed {
				fmt.Fprintln(output)
				fmt.Fprintln(output)
			}
			fmt.Fprint(output, text)
			printed = true
		}
	}

	_ = rpcInWriter.Close()

	select {
	case err := <-rpcErrCh:
		if err != nil {
			return err
		}
	case <-time.After(2 * time.Second):
	case <-overallCtx.Done():
		return fmt.Errorf("timeout after %s", timeout)
	}

	if !printed {
		fmt.Fprintln(output)
	}

	return nil
}

func writeHeadlessCommand(w io.Writer, cmd rpc.RPCCommand) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	data = append(data, '\n')
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write command: %w", err)
	}
	return nil
}

func waitHeadlessResponseWithContext(lines <-chan []byte, lineErr <-chan error, ctx context.Context, targetID string, state *headlessStreamState) (headlessRPCResponse, error) {
	for {
		select {
		case <-ctx.Done():
			return headlessRPCResponse{}, ctx.Err()
		case line, ok := <-lines:
			if !ok {
				// lines channel closed
				err, ok := <-lineErr
				if ok && err != nil {
					return headlessRPCResponse{}, fmt.Errorf("read rpc output: %w", err)
				}
				return headlessRPCResponse{}, io.EOF
			}

			var envelope struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(line, &envelope); err != nil {
				continue
			}

			if envelope.Type == "response" {
				var resp headlessRPCResponse
				if err := json.Unmarshal(line, &resp); err != nil {
					return headlessRPCResponse{}, fmt.Errorf("decode response: %w", err)
				}
				if resp.ID == targetID {
					return resp, nil
				}
				continue
			}

			updateHeadlessStreamStateFromEvent(line, state)
		}
	}
}

func waitHeadlessResponse(lines <-chan []byte, lineErr <-chan error, targetID string, state *headlessStreamState) (headlessRPCResponse, error) {
	return waitHeadlessResponseWithContext(lines, lineErr, context.Background(), targetID, state)
}

func updateHeadlessStreamStateFromEvent(line []byte, state *headlessStreamState) {
	var evt struct {
		Type                  string                       `json:"type"`
		Message               *agentctx.AgentMessage       `json:"message,omitempty"`
		AssistantMessageEvent *agent.AssistantMessageEvent `json:"assistantMessageEvent,omitempty"`
	}
	if err := json.Unmarshal(line, &evt); err != nil {
		return
	}

	if evt.Message != nil && evt.Message.Role == "assistant" {
		if text := strings.TrimSpace(evt.Message.ExtractText()); text != "" {
			state.lastAssistantText = text
		}
	}
	if evt.AssistantMessageEvent != nil && evt.AssistantMessageEvent.Type == "text_delta" {
		if delta := strings.TrimSpace(evt.AssistantMessageEvent.Delta); delta != "" {
			state.lastAssistantText = delta
		}
	}
}

func extractLastAssistantText(data json.RawMessage) string {
	var payload struct {
		Messages []agentctx.AgentMessage `json:"messages"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return ""
	}

	for i := len(payload.Messages) - 1; i >= 0; i-- {
		msg := payload.Messages[i]
		if msg.Role != "assistant" {
			continue
		}
		if text := strings.TrimSpace(msg.ExtractText()); text != "" {
			return text
		}
	}

	return ""
}
