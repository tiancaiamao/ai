package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	return spawnWithRawBackend(id, input, cwd, backendName)
}

func spawnWithRawBackend(id, input, cwd, backendName string) error {
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

	// Capture stdout and stderr together (equivalent to CombinedOutput).
	var outputBuf bytes.Buffer
	stdoutPipe, _ := cmd.StdoutPipe()
	stderrPipe, _ := cmd.StderrPipe()
	combinedWriter := io.MultiWriter(&outputBuf)
	// Pipe both stdout and stderr into the combined buffer via goroutines.
	stdoutDone := pipeToWriter(stdoutPipe, combinedWriter)
	stderrDone := pipeToWriter(stderrPipe, combinedWriter)

	cmd.Stdin = strings.NewReader(input)

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