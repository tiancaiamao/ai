//go:build e2e

package e2e

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/protocol"
	"github.com/tiancaiamao/ai/pkg/transport"
	tui "github.com/tiancaiamao/ai/subcommand/run/tui"
)

// TestE2E_ACPRunRegistration verifies that `ai acp` registers itself as a
// run: run.json appears under ~/.ai/runs/<id>/ with the process PID and the
// attached session, `ai ls` shows the run (running while the prompt is in
// flight, idle after a completed turn), and closing the stdio peer shuts the
// agent down with a terminal status.
func TestE2E_ACPRunRegistration(t *testing.T) {
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

	stdinPipe, stdoutPipe, stderrBuf, cmd := startACPBinary(t, m, home, workDir)
	// The client reads the agent's stdout and writes its stdin.
	conn := transport.NewStdio(stdoutPipe, stdinPipe)
	client, sid, err := protocol.ConnectACP(conn)
	if err != nil {
		t.Fatalf("ACP handshake over stdio: %v\nstderr: %s", err, stderrBuf.String())
	}
	defer client.Close()

	baseDir := filepath.Join(home, ".ai")
	meta := waitForACPRunMeta(t, baseDir, workDir, cmd.Process.Pid)
	if meta.Session == "" {
		t.Fatalf("run.json has no Session field; run id %s", meta.ID)
	}
	if meta.Session != sid {
		t.Fatalf("run.json session = %q, want ACP session %q", meta.Session, sid)
	}

	// While no prompt has run yet, `ai ls` shows the process as running.
	if status := lsStatusFor(t, home, meta.ID); status != "running" {
		t.Fatalf("ai ls status = %q before first prompt, want running", status)
	}

	// Drive one prompt to completion over stdio; `ai ls` should flip to idle.
	if _, err := client.Prompt(sid, "Reply with exactly: acp-run-ok"); err != nil {
		t.Fatalf("ACP prompt: %v", err)
	}
	waitFor(t, 30*time.Second, "ai ls idle after turn", func() bool {
		return lsStatusFor(t, home, meta.ID) == "idle"
	})

	// Closing the stdio peer must shut the agent down and finalize run.json.
	stdinPipe.Close()
	waitFor(t, 15*time.Second, "acp process exit after stdio close", func() bool {
		select {
		case <-cmdDone(cmd):
			return true
		default:
			return false
		}
	})
	final, err := tui.LoadRunMeta(tui.RunMetaPath(baseDir, meta.ID))
	if err != nil {
		t.Fatalf("load final run meta: %v", err)
	}
	if final.Status != tui.StatusDone {
		t.Fatalf("final run status = %q, want %q", final.Status, tui.StatusDone)
	}
	if final.FinishedAt == 0 {
		t.Fatal("final run status has no FinishedAt")
	}
}

// startACPBinary spawns `ai acp` with stdin/stdout pipes for the ACP channel.
func startACPBinary(t *testing.T, m e2eModel, home, workDir string) (io.WriteCloser, io.ReadCloser, *bytes.Buffer, *exec.Cmd) {
	t.Helper()
	cmd := exec.Command(binaryPath,
		"acp",
		"--model", m.provider+"/"+m.id,
		"--session", filepath.Join(workDir, "session.jsonl"),
	)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"GOCOVERDIR="+covDataDir,
		"OLLAMA_API_KEY=e2e",
		"AI_MODELS_PATH="+filepath.Join(home, ".ai", "models.json"),
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ai acp: %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Signal(os.Interrupt)
			_ = cmd.Wait()
		}
	})
	return stdin, stdout, &stderr, cmd
}

// waitForACPRunMeta polls until the acp process publishes its run.json, then
// verifies the recorded PID.
func waitForACPRunMeta(t *testing.T, baseDir, workDir string, pid int) *tui.RunMeta {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		runs, err := tui.FindRunningByCwd(baseDir, workDir)
		if err == nil {
			for _, r := range runs {
				if r.PID == pid {
					return &r
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("acp run meta with pid %d not found under %s", pid, baseDir)
	return nil
}

// lsStatusFor runs `ai ls` and returns the STATUS column for the given run id
// prefix.
func lsStatusFor(t *testing.T, home, idPrefix string) string {
	t.Helper()
	cmd := exec.Command(binaryPath, "ls")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"AI_MODELS_PATH="+filepath.Join(home, ".ai", "models.json"),
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("ai ls: %v\n%s", err, out.String())
	}
	for _, line := range strings.Split(out.String(), "\n") {
		fields := strings.Fields(ansiReplacer.Replace(line))
		if len(fields) >= 2 && strings.HasPrefix(fields[0], idPrefix) {
			return fields[1]
		}
	}
	t.Fatalf("run %s not listed by ai ls:\n%s", idPrefix, out.String())
	return ""
}

// ansiReplacer strips the color codes emitted by `ai ls` colorizeStatus.
var ansiReplacer = strings.NewReplacer(
	"\x1b[32m", "", "\x1b[36m", "", "\x1b[90m", "", "\x1b[31m", "", "\x1b[33m", "", "\x1b[0m", "",
)

func cmdDone(cmd *exec.Cmd) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(ch)
	}()
	return ch
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
