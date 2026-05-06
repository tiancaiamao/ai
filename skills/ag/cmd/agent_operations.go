package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/genius/ag/internal/agent"
	"github.com/genius/ag/internal/backend"
	"github.com/genius/ag/internal/storage"
)

// Spawn creates an agent using ai's new infrastructure.
func Spawn(id, system, input, cwd, backendName string) error {
	if backendName == "ai" {
		// Start agent through ai serve adapter.
		return aiAdapter.SpawnWithAIServe(id, system, input, cwd)
	}

		return spawnWithRawBackend(id, system, input, cwd, backendName)
}

func spawnWithRawBackend(id, system, input, cwd, backendName string) error {
	backendsPath := backend.FindBackendsFile()
	backends, err := backend.LoadOrDefault(backendsPath)
	if err != nil {
		return fmt.Errorf("load backends: %w", err)
	}
	be, err := backends.Find(backendName)
	if err != nil {
		return fmt.Errorf("unknown backend %q: %w (available: %v)", backendName, err, backends.Names())
	}
	if be.Protocol != backend.ProtocolRaw {
		return fmt.Errorf("backend %q requires protocol %q but only 'ai' or raw backends are supported", backendName, be.Protocol)
	}

		agentDir := agent.AgentDir(id)
	if err := os.MkdirAll(agentDir, 0755); err != nil {
		return fmt.Errorf("create agent dir: %w", err)
	}

		cmd := exec.Command(be.Command, be.Args...)
	if cwd != "" {
		cmd.Dir = cwd
	}

	// For codex backend, set proxy environment variables
	if backendName == "codex" {
		cmd.Env = os.Environ()
		// Append proxy settings if not already set
		if os.Getenv("HTTP_PROXY") == "" && os.Getenv("http_proxy") == "" {
			cmd.Env = append(cmd.Env, "HTTP_PROXY=http://127.0.0.1:8119")
		}
		if os.Getenv("HTTPS_PROXY") == "" && os.Getenv("https_proxy") == "" {
			cmd.Env = append(cmd.Env, "HTTPS_PROXY=http://127.0.0.1:8119")
		}
	}

		// For codex backend, pass input as additional argument instead of stdin
	// to avoid "file already closed" error when codex reads from stdin.
	// Also prepend system prompt if provided (codex has no --system flag).
	if backendName == "codex" {
		prompt := input
		if system != "" {
			prompt = fmt.Sprintf("[System Instructions]\n%s\n\n[Task]\n%s", system, input)
		}
		cmd.Args = append(cmd.Args, prompt)
	}

		// Capture stdout and stderr together (equivalent to CombinedOutput).
	var outputBuf bytes.Buffer
	var outputMu sync.Mutex
	combinedWriter := io.MultiWriter(&outputBuf)
	safeWriter := syncWriter{w: combinedWriter, mu: &outputMu}
	// Pipe both stdout and stderr into the combined buffer via goroutines.
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()
	stdoutDone := pipeToWriter(stdoutPipe, safeWriter)
	stderrDone := pipeToWriter(stderrPipe, safeWriter)

	// Only set stdin for backends that need it (not codex)
	if backendName != "codex" {
		cmd.Stdin = strings.NewReader(input)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start backend %q: %w", backendName, err)
	}

	// Record PID immediately after process starts so DetectStale can check
	// liveness even if the parent `ag spawn` process is killed.
	act := agent.Activity{
		Status:    "running",
		Backend:   backendName,
		StartedAt: time.Now().Unix(),
		Pid:       cmd.Process.Pid,
	}
	if err := storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), act); err != nil {
		return fmt.Errorf("write activity.json: %w", err)
	}

	// Wait for pipes to finish, then wait for the process.
	<-stdoutDone
	<-stderrDone
	runErr := cmd.Wait()

	output := outputBuf.Bytes()

	// 使用格式化写入器写入 stream.log
	if err := WriteFormattedOutput(agentDir, output, backendName); err != nil {
		fmt.Printf("Warning: failed to write formatted stream.log: %v\n", err)
		// 降级为原始写入方式
		_ = os.WriteFile(filepath.Join(agentDir, "stream.log"), output, 0644)
	}

	// 仍然保存原始输出到 output 文件
	_ = os.WriteFile(filepath.Join(agentDir, "output"), output, 0644)

	act.FinishedAt = time.Now().Unix()
	if runErr != nil {
		act.Status = "failed"
		act.Error = runErr.Error()
	} else {
		act.Status = "done"
	}
	if err := storage.AtomicWriteJSON(filepath.Join(agentDir, "activity.json"), act); err != nil {
		return fmt.Errorf("write final activity.json: %w", err)
	}

	if runErr != nil {
		return fmt.Errorf("backend %q failed: %w", backendName, runErr)
	}
	return nil
}

// pipeToWriter drains reader into writer in a goroutine, returning a channel
// that is closed when the reader reaches EOF.
func pipeToWriter(r io.Reader, w io.Writer) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		io.Copy(w, r)
	}()
	return ch
}

// syncWriter wraps an io.Writer with a mutex for concurrent access safety.
type syncWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (sw syncWriter) Write(p []byte) (int, error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.w.Write(p)
}