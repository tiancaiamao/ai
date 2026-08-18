//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestE2E_LLMDecideAsk exercises the LLM-decides compaction path
// (shouldCompactLLMDecide → askLLM). With a declared context window of 12000,
// LLMDecide thresholds are soft=3000 / medium=4200 / high=6000 / hard=9000,
// and ask intervals are 15/10/7 tool calls per tier. The baseline (system
// prompt + tool schemas) is ~1600-3000 estimated tokens. One 8000-char bash
// output (~2000 tokens) plus sixteen tool-call rounds push the session into
// the soft..hard band with the tool-call counter past every tier interval,
// so the pre-LLM ShouldCompact check must consult the model via askLLM.
//
// askLLM opens a compact_llm_decide_ask span unconditionally, so the span's
// presence in the server's Perfetto trace files proves the ask executed
// (the follow-up compaction decision — yes/no — depends on the model).
func TestE2E_LLMDecideAsk(t *testing.T) {
	m := requireEndpoint(t)
	home := t.TempDir()
	if err := writeE2EModelsWindow(filepath.Join(home, ".ai", "models.json"), m.provider, m.baseURL, m.id, 12000); err != nil {
		t.Fatalf("write isolated models.json: %v", err)
	}

	rs := startRPCServerHome(t, home, m.provider+"/"+m.id, t.TempDir())
	rs.promptAndWait(t, `Use the bash tool once to run: head -c 8000 /dev/zero | tr '\0' 'x'. Then use the bash tool 15 more times, one command per call, in order: echo ok1, echo ok2, echo ok3, echo ok4, echo ok5, echo ok6, echo ok7, echo ok8, echo ok9, echo ok10, echo ok11, echo ok12, echo ok13, echo ok14, echo ok15. After all sixteen calls finish, reply with the single word ok.`)

	// Traces flush on shutdown; close stdin and wait for exit before reading.
	rs.closeStdin()
	if err := rs.waitExit(30 * time.Second); err != nil {
		t.Fatalf("server did not exit: %v", err)
	}

	if !traceContainsEvent(t, filepath.Join(home, ".ai", "traces"), "compact_llm_decide_ask") {
		t.Fatalf("no compact_llm_decide_ask span in traces under %s — askLLM never fired", filepath.Join(home, ".ai", "traces"))
	}
}

// traceContainsEvent reports whether any trace JSON file under dir contains an
// event or span with the given name.
func traceContainsEvent(t *testing.T, dir, eventName string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		if strings.Contains(string(data), `"name":"`+eventName+`"`) {
			t.Logf("found %s in %s", eventName, e.Name())
			return true
		}
	}
	return false
}
