//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/rpc"
	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

// TestE2E_ACPSocketLifecycle verifies the real serve/socket lifecycle without
// using the legacy flat NDJSON RPC protocol: one ACP client drives a prompt,
// a second client observes live updates, and a fresh client replays history.
func TestE2E_ACPSocketLifecycle(t *testing.T) {
	m := requireEndpoint(t)
	home, err := os.MkdirTemp("", "ai-e2e-h")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	workDir, err := os.MkdirTemp("", "ai-e2e-w")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(workDir) })
	if err := writeE2EModels(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id); err != nil {
		t.Fatal(err)
	}

	idFile := filepath.Join(workDir, "run-id.txt")
	cmd := exec.Command(binaryPath,
		"serve",
		"--model", m.provider+"/"+m.id,
		"--session", filepath.Join(workDir, "session.jsonl"),
		"--id-file", idFile,
	)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"GOCOVERDIR="+covDataDir,
		"OLLAMA_API_KEY=e2e",
		"AI_MODELS_PATH="+filepath.Join(home, ".ai", "models.json"),
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ai serve: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Signal(os.Interrupt)
			_ = cmd.Wait()
		}
	})

	var id string
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(idFile); err == nil && strings.TrimSpace(string(data)) != "" {
			id = strings.TrimSpace(string(data))
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if id == "" {
		t.Fatalf("serve did not publish run id: %s", stderr.String())
	}

	sockPath := tui.SocketPath(filepath.Join(home, ".ai"), id)
	driver, sid, err := rpc.DialACP(sockPath)
	if err != nil {
		t.Fatalf("dial driver ACP socket: %v\nstderr: %s", err, stderr.String())
	}
	defer driver.Close()

	watcher, watcherSID, err := rpc.DialACP(sockPath)
	if err != nil {
		t.Fatalf("dial watcher ACP socket: %v", err)
	}
	defer watcher.Close()
	if watcherSID != sid {
		t.Fatalf("watcher session id = %q, driver session id = %q", watcherSID, sid)
	}

	if err := driver.PromptAsync(sid, "Reply with exactly: live-ok"); err != nil {
		t.Fatalf("send ACP prompt: %v", err)
	}

	var sawUser, sawAssistant, sawUsage, sawTurnEnd bool
	var assistantText strings.Builder
	deadline = time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) && !sawTurnEnd {
		select {
		case update, ok := <-watcher.Updates():
			if !ok {
				t.Fatal("watcher ACP stream closed before _turn_end")
			}
			switch update.SessionUpdate {
			case "user_message_chunk":
				sawUser = strings.Contains(acpUpdateTextForTest(update.Content), "live-ok")
			case "agent_message_chunk":
				assistantText.WriteString(acpUpdateTextForTest(update.Content))
				sawAssistant = strings.Contains(assistantText.String(), "live-ok")
			case "usage_update":
				sawUsage = update.Used != nil && update.Size != nil && *update.Size > 0
			case "_turn_end":
				sawTurnEnd = true
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !sawTurnEnd {
		t.Fatalf("live ACP stream did not reach _turn_end")
	}
	if !sawUser || !sawAssistant {
		t.Fatalf("live ACP updates missing user=%v assistant=%v", sawUser, sawAssistant)
	}
	if !sawUsage {
		t.Fatal("live ACP updates missing usage_update")
	}

	// Prompt's response is independently routed to its driving client.
	resultCh := make(chan struct {
		stopReason string
		err        error
	}, 1)
	go func() {
		stopReason, err := driver.Prompt(sid, "Reply with exactly: response-ok")
		resultCh <- struct {
			stopReason string
			err        error
		}{stopReason, err}
	}()
	select {
	case result := <-resultCh:
		if result.err != nil {
			t.Fatalf("ACP prompt response: %v", result.err)
		}
		if result.stopReason != "end_turn" {
			t.Fatalf("ACP stop reason = %q, want end_turn", result.stopReason)
		}
	case <-time.After(3 * time.Minute):
		t.Fatal("timed out waiting for ACP prompt response")
	}

	replay, replaySID, err := rpc.DialACP(sockPath)
	if err != nil {
		t.Fatalf("dial replay ACP socket: %v", err)
	}
	defer replay.Close()
	if replaySID != sid {
		t.Fatalf("replay session id = %q, want %q", replaySID, sid)
	}
	if err := replay.LoadSession(sid); err != nil {
		t.Fatalf("ACP session/load: %v", err)
	}

	var replayUser, replayAssistant, replayDone bool
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && !replayDone {
		select {
		case update, ok := <-replay.Updates():
			if !ok {
				t.Fatal("replay ACP stream closed before load completion")
			}
			switch update.SessionUpdate {
			case "user_message_chunk":
				text := acpUpdateTextForTest(update.Content)
				replayUser = replayUser || strings.Contains(text, "live-ok") || strings.Contains(text, "response-ok")
			case "agent_message_chunk":
				replayAssistant = replayAssistant || strings.Contains(acpUpdateTextForTest(update.Content), "live-ok") || strings.Contains(acpUpdateTextForTest(update.Content), "response-ok")
			case rpc.ACPUpdateSessionLoadEnd:
				replayDone = true
			}
		case <-time.After(100 * time.Millisecond):
		}
	}
	if !replayDone || !replayUser || !replayAssistant {
		t.Fatalf("session/load replay incomplete: done=%v user=%v assistant=%v", replayDone, replayUser, replayAssistant)
	}
}

func acpUpdateTextForTest(content any) string {
	if m, ok := content.(map[string]any); ok {
		if text, ok := m["text"].(string); ok {
			return text
		}
	}
	return fmt.Sprint(content)
}
