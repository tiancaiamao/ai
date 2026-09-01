//go:build e2e

package e2e

import (
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

	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/transport"
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
	fmt.Fprintf(os.Stderr, "\n=== E2E coverage (whole app via `ai acp` subprocess) ===\n%s\nprofile: %s\n", total, profile)
}

// --- Black-box subprocess driver ---

type acpServer struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	client    *rpc.ACPClient
	sessionID string
	log       *acpLog
	stderrBuf *syncBuffer
	stop      chan struct{}
	once      sync.Once
}

type acpLog struct {
	client  *rpc.ACPClient
	mu      sync.Mutex
	history []rpc.ACPUpdate
	done    chan struct{}
}

func newACPLog(client *rpc.ACPClient) *acpLog {
	l := &acpLog{client: client, done: make(chan struct{})}
	go func() {
		defer close(l.done)
		for update := range client.Updates() {
			l.mu.Lock()
			l.history = append(l.history, update)
			l.mu.Unlock()
		}
	}()
	return l
}

func (l *acpLog) waitUpdate(t *testing.T, predicate func(rpc.ACPUpdate) bool, timeout time.Duration) rpc.ACPUpdate {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		l.mu.Lock()
		for i, update := range l.history {
			if predicate(update) {
				l.history = append(l.history[:i], l.history[i+1:]...)
				l.mu.Unlock()
				return update
			}
		}
		l.mu.Unlock()
		select {
		case <-l.done:
			t.Fatalf("ACP stream closed while waiting for update")
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for ACP update")
	return rpc.ACPUpdate{}
}

func (l *acpLog) waitEvent(typ, pick, want string, timeout time.Duration) string {
	update := l.waitUpdate(nil, func(u rpc.ACPUpdate) bool {
		if typ == "agent_end" {
			return u.SessionUpdate == "_turn_end"
		}
		if typ == "compaction_end" {
			meta, _ := u.Meta.(map[string]any)
			status, _ := meta["status"].(string)
			return u.SessionUpdate == "_compaction" && status == "end"
		}
		return u.SessionUpdate == typ
	}, timeout)
	data, _ := json.Marshal(update)
	return string(data)
}

func (l *acpLog) History() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	data, _ := json.Marshal(l.history)
	return string(data)
}

func (l *acpLog) DebugState() string { return "ACP update log" }

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

// startACPServer launches `ai acp` as a subprocess with an isolated HOME.
// The subprocess speaks ACP over stdio; the driver uses the same ACP client as
// production callers.
func startACPServer(t *testing.T, m e2eModel, workDir string, flags ...string) *acpServer {
	t.Helper()
	home := t.TempDir()
	if err := writeE2EModels(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id); err != nil {
		t.Fatalf("write isolated models.json: %v", err)
	}
	return startACPServerHome(t, home, m.provider+"/"+m.id, workDir, flags...)
}

// startACPServerHome is like startACPServer but lets the caller seed an
// isolated HOME directory first (roles, agent.yaml, sessions, ...).
func startACPServerHome(t *testing.T, home, defaultPath, workDir string, flags ...string) *acpServer {
	t.Helper()
	if workDir == "" {
		workDir = t.TempDir()
	}
	args := []string{"acp"}
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
	stdout := mustStdout(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ai acp: %v", err)
	}

	client := rpc.NewACPClient(transport.NewStdio(stdout, stdin))
	if err := client.Initialize(); err != nil {
		t.Fatalf("ACP initialize: %v\nstderr:\n%s", err, stderrBuf.String())
	}
	sessionID, err := client.NewSession()
	if err != nil {
		client.Close()
		t.Fatalf("ACP session/new: %v\nstderr:\n%s", err, stderrBuf.String())
	}

	rs := &acpServer{
		cmd: cmd, stdin: stdin, client: client, sessionID: sessionID,
		log: newACPLog(client), stderrBuf: stderrBuf, stop: make(chan struct{}),
	}
	go rs.run()
	t.Cleanup(rs.kill)
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
func (rs *acpServer) run() {
	rs.cmd.Wait()
	close(rs.stop)
}

// kill closes the ACP client (graceful EOF shutdown) and, if the process
// lingers, force-kills it.
func (rs *acpServer) kill() {
	rs.once.Do(func() {
		_ = rs.client.Close()
		select {
		case <-rs.stop:
		case <-time.After(8 * time.Second):
			_ = rs.cmd.Process.Kill()
			<-rs.stop
		}
	})
}

func (rs *acpServer) promptAsync(t *testing.T, msg string) {
	t.Helper()
	if err := rs.client.PromptAsync(rs.sessionID, msg); err != nil {
		t.Fatalf("prompt %q failed: %v", msg, err)
	}
}

func (rs *acpServer) waitUpdate(t *testing.T, kind string, timeout time.Duration) rpc.ACPUpdate {
	t.Helper()
	return rs.log.waitUpdate(t, func(update rpc.ACPUpdate) bool {
		return kind == "" || update.SessionUpdate == kind
	}, timeout)
}

func (rs *acpServer) waitRequestError(t *testing.T, method string, timeout time.Duration) rpc.ACPUpdateError {
	t.Helper()
	update := rs.log.waitUpdate(t, func(update rpc.ACPUpdate) bool {
		return update.SessionUpdate == rpc.ACPUpdateRequestError
	}, timeout)
	errInfo, ok := update.Meta.(rpc.ACPUpdateError)
	if !ok {
		t.Fatalf("ACP request error has unexpected meta: %#v", update.Meta)
	}
	if method != "" && errInfo.Method != method {
		t.Fatalf("ACP request error method = %q, want %q", errInfo.Method, method)
	}
	return errInfo
}

func updateMetaMap(t *testing.T, update rpc.ACPUpdate) map[string]any {
	t.Helper()
	meta, ok := update.Meta.(map[string]any)
	if !ok {
		t.Fatalf("ACP update %q has unexpected meta: %#v", update.SessionUpdate, update.Meta)
	}
	return meta
}

// send writes a raw ACP message to the subprocess stdin. It is retained for
// protocol-level tests; normal requests should use the typed ACP client.
func (rs *acpServer) send(t *testing.T, raw string) {
	t.Helper()
	if _, err := fmt.Fprintf(rs.stdin, "%s\n", raw); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
}

func (rs *acpServer) command(t *testing.T, name, args string) map[string]any {
	t.Helper()
	text := "/" + name
	if args != "" {
		text += " " + args
	}
	raw, err := rs.client.PromptResult(rs.sessionID, text)
	if err != nil {
		t.Fatalf("command %q failed: %v", name, err)
	}
	return commandResult(t, name, raw)
}

func commandResult(t *testing.T, name string, raw json.RawMessage) map[string]any {
	t.Helper()
	var result struct {
		Meta struct {
			CommandResult json.RawMessage `json:"commandResult"`
		} `json:"_meta"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("parse command %q result: %v", name, err)
	}
	if len(result.Meta.CommandResult) == 0 || string(result.Meta.CommandResult) == "null" {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal(result.Meta.CommandResult, &data); err != nil {
		t.Fatalf("parse command %q data: %v", name, err)
	}
	return data
}

// commandAck sends a slash command through ACP and returns its result.
func (rs *acpServer) commandAck(t *testing.T, typ, msg string) map[string]any {
	t.Helper()
	return rs.command(t, typ, msg)
}

func (rs *acpServer) commandWithData(t *testing.T, typ, msg string, validate func(map[string]any)) {
	t.Helper()
	data := rs.command(t, typ, msg)
	if data == nil {
		t.Fatalf("command %q returned empty data", typ)
	}
	validate(data)
}

func (rs *acpServer) commandErr(t *testing.T, typ, msg, wantErr string) {
	t.Helper()
	text := "/" + typ
	if msg != "" {
		text += " " + msg
	}
	if _, err := rs.client.PromptResult(rs.sessionID, text); err == nil || (wantErr != "" && !strings.Contains(err.Error(), wantErr)) {
		t.Fatalf("command %q error %v does not contain %q", typ, err, wantErr)
	}
}

// promptAndWait sends a user prompt and blocks until the turn completes,
// returning the assistant's persisted final text. The ACP log is the sole
// consumer of session/update notifications.
func (rs *acpServer) promptAndWait(t *testing.T, msg string) string {
	t.Helper()
	if err := rs.client.PromptAsync(rs.sessionID, msg); err != nil {
		t.Fatalf("prompt %q failed: %v", msg, err)
	}
	rs.waitUpdate(t, "_turn_end", 10*time.Minute)
	return rs.lastAssistantText(t)
}

func (rs *acpServer) lastAssistantText(t *testing.T) string {
	t.Helper()
	data := rs.command(t, "get_last_assistant_text", "")
	if data == nil {
		return ""
	}
	text, _ := data["text"].(string)
	return text
}

// logTail returns the accumulated subprocess stderr.
func (rs *acpServer) logTail() string {
	return rs.stderrBuf.String()
}

func typedJSON(typ, msg string) string {
	if msg == "" {
		return `{"type":"` + typ + `"}`
	}
	b, _ := json.Marshal(msg)
	return `{"type":"` + typ + `","message":` + string(b) + `}`
}
