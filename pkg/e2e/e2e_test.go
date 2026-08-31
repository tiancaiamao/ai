//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Instrumented build + coverage plumbing (TestMain) ---

var (
	binaryPath string
	covDataDir string
)

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "ai-e2e-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: cannot create temp dir: %v\n", err)
		os.Exit(1)
	}
	binaryPath = filepath.Join(tmp, "ai")
	covDataDir = filepath.Join(tmp, "covdata")

	build := exec.Command("go", "build", "-cover", "-covermode=atomic", "-o", binaryPath, "./cmd/ai")
	build.Dir = repoRoot()
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: instrumented build failed: %v\n%s\n", err, out)
		os.Exit(1)
	}
	if err := os.MkdirAll(covDataDir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: cannot create covdata dir: %v\n", err)
		os.Exit(1)
	}

	code := m.Run()

	if code == 0 {
		reportCoverage(tmp)
	}
	os.Exit(code)
}

func repoRoot() string {
	wd, _ := os.Getwd()
	root := wd
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			return root
		}
		parent := filepath.Dir(root)
		if parent == root {
			return wd
		}
		root = parent
	}
}

// reportCoverage merges all subprocess profiles and prints the whole-app total.
func reportCoverage(tmp string) {
	merged := filepath.Join(tmp, "merged")
	os.RemoveAll(merged)
	if err := os.MkdirAll(merged, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: mkdir merged: %v\n", err)
		return
	}

	merge := exec.Command("go", "tool", "covdata", "merge", "-i="+covDataDir, "-o="+merged)
	if out, err := merge.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: covdata merge failed: %v\n%s\n", err, out)
		return
	}

	profile := filepath.Join(tmp, "e2e-coverage.out")
	convert := exec.Command("go", "tool", "covdata", "textfmt", "-i="+merged, "-o="+profile)
	if out, err := convert.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "e2e: covdata textfmt failed: %v\n%s\n", err, out)
		return
	}

	funcOut, err := exec.Command("go", "tool", "cover", "-func="+profile).CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: cover -func failed: %v\n", err)
		return
	}
	lines := strings.Split(string(funcOut), "\n")
	total := "(no data)"
	if len(lines) >= 2 {
		total = lines[len(lines)-2]
	}
	fmt.Fprintf(os.Stderr, "\n=== E2E coverage (whole app via `ai rpc` subprocess) ===\n%s\nprofile: %s\n", total, profile)
}

// --- Black-box subprocess driver ---

type rpcServer struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	log       *logReader
	stderrMu  sync.Mutex
	stderrBuf *syncBuffer
	stop      chan struct{}
	once      sync.Once
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startRPCServer launches `ai rpc` as a subprocess with an isolated HOME.
// workDir is the subprocess cwd ("" → fresh temp dir).
func startRPCServer(t *testing.T, m e2eModel, workDir string, flags ...string) *rpcServer {
	t.Helper()
	home := t.TempDir()
	if err := writeE2EModels(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id); err != nil {
		t.Fatalf("write isolated models.json: %v", err)
	}
	return startRPCServerHome(t, home, m.provider+"/"+m.id, workDir, flags...)
}

// startRPCServerHome is like startRPCServer but lets the caller seed an
// isolated HOME directory first (roles, agent.yaml, sessions, ...).
func startRPCServerHome(t *testing.T, home, defaultPath, workDir string, flags ...string) *rpcServer {
	t.Helper()
	if workDir == "" {
		workDir = t.TempDir()
	}
	args := []string{"rpc"}
	args = append(args, flags...)
	args = append(args, "-model", defaultPath)

	cmd := exec.Command(binaryPath, args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"GOCOVERDIR="+covDataDir,
		"OLLAMA_API_KEY=e2e",
		"AI_MODELS_PATH="+filepath.Join(home, ".ai", "models.json"),
	)

	stderrBuf := &syncBuffer{}
	cmd.Stderr = stderrBuf

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	var stdout io.Reader
	if v := os.Getenv("E2E_WIRE_LOG"); v != "" {
		f, ferr := os.Create(v)
		if ferr != nil {
			t.Fatalf("wire log: %v", ferr)
		}
		t.Cleanup(func() { f.Close() })
		stdout = io.TeeReader(mustStdout(cmd), f)
	} else {
		stdout = mustStdout(cmd)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ai rpc: %v", err)
	}

	rs := &rpcServer{
		cmd:       cmd,
		stdin:     stdin,
		log:       newLogReader(stdout, t),
		stderrBuf: stderrBuf,
		stop:      make(chan struct{}),
	}
	go rs.run()
	t.Cleanup(rs.kill)

	if rs.log.waitEvent("server_start", "", "", 30*time.Second) == "" {
		if rs.cmd.ProcessState != nil {
			t.Fatalf("ai rpc exited early: %v\nstderr:\n%s", rs.cmd.ProcessState, stderrBuf.String())
		}
		t.Fatalf("no server_start event. stderr:\n%s", stderrBuf.String())
	}
	return rs
}

func mustStdout(cmd *exec.Cmd) io.Reader {
	r, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	return r
}

// run reaps the subprocess and closes the stop channel.
func (rs *rpcServer) run() {
	rs.cmd.Wait()
	close(rs.stop)
}

// kill closes stdin (graceful EOF shutdown) and, if the process lingers,
// force-kills it.
func (rs *rpcServer) kill() {
	rs.once.Do(func() {
		rs.stdin.Close()
		select {
		case <-rs.stop:
		case <-time.After(8 * time.Second):
			rs.cmd.Process.Kill()
			<-rs.stop
		}
	})
}

// logReader relays subprocess stdout lines to the test log and provides
// line-based event waiting. It decouples the subprocess from the test so a
// slow test assertion never blocks the subprocess.
type logReader struct {
	t       *testing.T
	mu      sync.Mutex
	buf     *bufio.Reader
	pending []string
	history []string
	done    chan struct{}
}

func newLogReader(r io.Reader, t *testing.T) *logReader {
	lr := &logReader{
		t:    t,
		buf:  bufio.NewReaderSize(r, 4*1024*1024),
		done: make(chan struct{}),
	}
	go lr.run()
	return lr
}

func (lr *logReader) run() {
	for {
		line, err := lr.buf.ReadString('\n')
		if line != "" {
			lr.mu.Lock()
			lr.pending = append(lr.pending, line)
			lr.history = append(lr.history, line)
			lr.mu.Unlock()
		}
		if err != nil {
			close(lr.done)
			return
		}
	}
}

// waitEvent waits until a parsed JSON line has type == typ (typ "" matches any)
// and, when pick != "", ev[pick] == want. It returns the matched line or "" on
// timeout / EOF. Unmatched lines are preserved for later calls (an event can
// share a drain batch with the line we match on, and discarding it would lose
// the event forever).
func (lr *logReader) waitEvent(typ, pick, want string, timeout time.Duration) string {
	match := func(ev map[string]any) bool {
		if typ != "" && ev["type"] != typ {
			return false
		}
		if pick == "" {
			return true
		}
		v, _ := ev[pick].(string)
		return v == want
	}
	deadline := time.Now().Add(timeout)
	for {
		found, keep := lr.scan(match)
		lr.putBack(keep)
		if found != "" {
			return found
		}
		if time.Now().After(deadline) || lr.closed() {
			found, keep := lr.scan(match)
			lr.putBack(keep)
			if found != "" {
				return found
			}
			return ""
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-lr.done:
		case <-lr.t.Context().Done():
			return ""
		}
	}
}

// scan drains all pending lines, returning the first match and every
// unmatched line (to be put back by the caller).
func (lr *logReader) scan(match func(map[string]any) bool) (found string, keep []string) {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	lines := lr.pending
	lr.pending = nil
	for _, line := range lines {
		if found == "" {
			var ev map[string]any
			if err := json.Unmarshal([]byte(line), &ev); err == nil && match(ev) {
				found = line
				continue
			}
		}
		keep = append(keep, line)
	}
	return found, keep
}

// putBack returns unmatched lines to the front of pending, preserving order
// relative to any lines that arrived while they were drained.
func (lr *logReader) putBack(lines []string) {
	if len(lines) == 0 {
		return
	}
	lr.mu.Lock()
	defer lr.mu.Unlock()
	lr.pending = append(lines, lr.pending...)
}

// History returns every line the subprocess has emitted so far.
func (lr *logReader) History() string {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	return strings.Join(lr.history, "\n")
}

// DebugState returns a diagnostic snapshot: whether the reader has seen EOF
// and how many lines are still unconsumed in pending.
func (lr *logReader) DebugState() string {
	lr.mu.Lock()
	defer lr.mu.Unlock()
	return fmt.Sprintf("closed=%v pending=%d lastNWait=%d", lr.closedLocked(), len(lr.pending), len(lr.history))
}

func (lr *logReader) closedLocked() bool {
	select {
	case <-lr.done:
		return true
	default:
		return false
	}
}

func (lr *logReader) closed() bool {
	select {
	case <-lr.done:
		return true
	default:
		return false
	}
}

// send writes a raw line to the subprocess stdin.
func (rs *rpcServer) send(t *testing.T, raw string) {
	t.Helper()
	if _, err := fmt.Fprintf(rs.stdin, "%s\n", raw); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
}

// rpcAck sends {"type":typ,"message":msg} and asserts the synchronous success
// response. Returns the response data map when present.
func (rs *rpcServer) rpcAck(t *testing.T, typ, msg string) map[string]any {
	t.Helper()
	rs.send(t, typedJSON(typ, msg))
	resp := rs.log.waitEvent("response", "command", typ, 60*time.Second)
	if resp == "" {
		t.Fatalf("no response for type %q. stderr:\n%s", typ, rs.logTail())
	}
	var r struct {
		Success bool            `json:"success"`
		Error   string          `json:"error"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp), &r); err != nil {
		t.Fatalf("parse response for %q: %v\n%s", typ, err, resp)
	}
	if !r.Success {
		t.Fatalf("command %q failed: %s", typ, r.Error)
	}
	if len(r.Data) > 0 {
		var m map[string]any
		if err := json.Unmarshal(r.Data, &m); err == nil {
			return m
		}
	}
	return nil
}

// rpcAckWithData sends a command and validates the response data using the provided validator.
// Unlike rpcAck, this ensures the data field is actually checked.
func (rs *rpcServer) rpcAckWithData(t *testing.T, typ, msg string, validate func(map[string]any)) {
	t.Helper()
	rs.send(t, typedJSON(typ, msg))
	resp := rs.log.waitEvent("response", "command", typ, 60*time.Second)
	if resp == "" {
		t.Fatalf("no response for type %q. stderr:\n%s", typ, rs.logTail())
	}
	var r struct {
		Success bool            `json:"success"`
		Error   string          `json:"error"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp), &r); err != nil {
		t.Fatalf("parse response for %q: %v\n%s", typ, err, resp)
	}
	if !r.Success {
		t.Fatalf("command %q failed: %s", typ, r.Error)
	}
	if len(r.Data) == 0 {
		t.Fatalf("command %q returned empty data field", typ)
	}
	var m map[string]any
	if err := json.Unmarshal(r.Data, &m); err != nil {
		t.Fatalf("unmarshal data for %q: %v\n%s", typ, err, string(r.Data))
	}
	validate(m)
}

// rpcErr sends {"type":typ,"message":msg} and asserts it fails with an error
// containing wantErr.
func (rs *rpcServer) rpcErr(t *testing.T, typ, msg, wantErr string) {
	t.Helper()
	rs.send(t, typedJSON(typ, msg))
	resp := rs.log.waitEvent("response", "command", typ, 60*time.Second)
	if resp == "" {
		t.Fatalf("no response for type %q (expected error %q). stderr:\n%s", typ, wantErr, rs.logTail())
	}
	var r struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(resp), &r); err != nil {
		t.Fatalf("parse response for %q: %v", typ, err)
	}
	if r.Success {
		t.Fatalf("command %q unexpectedly succeeded (wanted error %q)", typ, wantErr)
	}
	if !strings.Contains(r.Error, wantErr) {
		t.Fatalf("command %q error %q does not contain %q", typ, r.Error, wantErr)
	}
}

// promptAndWait sends a user prompt and blocks until agent_end, returning the
// assistant's final text.
func (rs *rpcServer) promptAndWait(t *testing.T, msg string) string {
	t.Helper()
	rs.send(t, typedJSON("prompt", msg))
	if ack := rs.log.waitEvent("response", "command", "prompt", 30*time.Second); ack == "" {
		t.Fatalf("no prompt ack. stderr:\n%s", rs.logTail())
	}
	if ev := rs.log.waitEvent("agent_end", "", "", 10*time.Minute); ev == "" {
		t.Fatalf("no agent_end after prompt %q. stderr:\n%s", msg, rs.logTail())
	} else {
		t.Logf("agent_end: %s", ev)
	}
	return rs.lastAssistantText(t)
}

func (rs *rpcServer) lastAssistantText(t *testing.T) string {
	t.Helper()
	rs.send(t, `{"type":"get_last_assistant_text"}`)
	resp := rs.log.waitEvent("response", "command", "get_last_assistant_text", 30*time.Second)
	if resp == "" {
		return ""
	}
	var r struct {
		Data struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(resp), &r); err != nil {
		return ""
	}
	return r.Data.Text
}

// logTail returns the accumulated subprocess stderr.
func (rs *rpcServer) logTail() string {
	return rs.stderrBuf.String()
}

func typedJSON(typ, msg string) string {
	if msg == "" {
		return `{"type":"` + typ + `"}`
	}
	b, _ := json.Marshal(msg)
	return `{"type":"` + typ + `","message":` + string(b) + `}`
}
