package rpc

// Tests for the ACP model catalog: session/new advertises a category=model
// select config option built from the shared model registry, and the
// set-config method aliases switch the active model.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupACPEnv writes a two-model zai registry (the API-key provider used by
// the ACP smoke harness) plus a config pinning the initial active model into
// dir/runACPSmoke's config path. Returns (workspace, initialValue, otherValue).
func setupACPEnv(t *testing.T) (string, string, string) {
	t.Helper()
	initial, other := "zai/glm-a", "zai/glm-b"

	modelsPath := filepath.Join(t.TempDir(), "models.json")
	data := `{"providers":{"zai":{"baseUrl":"https://api.z.ai/api/coding/paas/v4","api":"openai-completions","models":[{"id":"glm-a","name":"GLM A"},{"id":"glm-b","name":"GLM B"}]}}}`
	if err := os.WriteFile(modelsPath, []byte(data), 0o644); err != nil {
		t.Fatalf("write models.json: %v", err)
	}
	t.Setenv("AI_MODELS_PATH", modelsPath)

	// runACPSmoke pins AI_CONFIG_PATH under this dir.
	dir := t.TempDir()
	cfg := fmt.Sprintf(
		`{"model":{"id":%q,"provider":"zai","baseUrl":"https://api.z.ai/api/coding/paas/v4","api":"openai-completions"}}`,
		strings.TrimPrefix(initial, "zai/"))
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	return dir, initial, other
}

// findModelOption extracts the category=model select option from a payload.
func findModelOption(t *testing.T, options []any) map[string]any {
	t.Helper()
	for _, raw := range options {
		opt, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if cat, _ := opt["category"].(string); cat == "model" {
			return opt
		}
	}
	t.Fatalf("no category=model config option in %v", options)
	return nil
}

func TestACPSessionNewAdvertisesModelCatalog(t *testing.T) {
	dir, initial, other := setupACPEnv(t)
	msgs := runACPSmoke(t, dir, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"session/new","params":{"cwd":"/tmp"}}`,
	})
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d: %v", len(msgs), msgs)
	}
	result, _ := msgs[1]["result"].(map[string]any)

	// Spec spelling...
	specOpts, ok := result["configOptions"].([]any)
	if !ok || len(specOpts) == 0 {
		t.Fatalf("expected configOptions array in session/new result, got %v", result)
	}
	// ...and host-friendly snake_case twin, same content.
	snakeOpts, ok := result["config_options"].([]any)
	if !ok || len(snakeOpts) != len(specOpts) {
		t.Fatalf("expected parallel config_options array, spec=%v snake=%v", specOpts, snakeOpts)
	}
	rawSpec, _ := json.Marshal(result["configOptions"])
	rawSnake, _ := json.Marshal(result["config_options"])
	if string(rawSpec) != string(rawSnake) {
		t.Errorf("configOptions and config_options diverge:\n%s\n%s", rawSpec, rawSnake)
	}

	opt := findModelOption(t, specOpts)
	if opt["type"] != "select" {
		t.Errorf("expected type select, got %v", opt["type"])
	}
	if cv, _ := opt["current_value"].(string); cv != initial {
		t.Errorf("expected current_value %q, got %q", initial, cv)
	}
	if cv, _ := opt["currentValue"].(string); cv != initial {
		t.Errorf("expected currentValue %q, got %q", initial, cv)
	}
	values := map[string]bool{}
	names := map[string]string{}
	for _, raw := range opt["options"].([]any) {
		entry := raw.(map[string]any)
		v, _ := entry["value"].(string)
		n, _ := entry["name"].(string)
		values[v] = true
		names[v] = n
	}
	if !values[initial] || !values[other] {
		t.Errorf("missing registry options: have %v", values)
	}
	if names[other] != "GLM B" {
		t.Errorf("expected display name GLM B for %s, got %q", other, names[other])
	}
}

// TestACPSetConfigAliases verifies that every accepted method alias switches
// the active model (current_value reflects the switch) and that unknown
// option ids / values yield invalid params errors.
func TestACPSetConfigAliases(t *testing.T) {
	dir, initial, other := setupACPEnv(t)

	lines := []string{
		`{"jsonrpc":"2.0","id":10,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":11,"method":"session/new","params":{"cwd":"/tmp"}}`,
		// Official v1 method, official param names (flat).
		fmt.Sprintf(`{"jsonrpc":"2.0","id":12,"method":"session/set_config_option","params":{"sessionId":"unused","configId":"model","value":%q}}`, other),
		// Alias + alternate field names, bare-id value (no provider prefix).
		fmt.Sprintf(`{"jsonrpc":"2.0","id":13,"method":"session/set_model","params":{"option_id":"model","model_id":%q}}`, strings.TrimPrefix(initial, "zai/")),
		// Nested params under a wrapper object.
		fmt.Sprintf(`{"jsonrpc":"2.0","id":14,"method":"config/set_option","params":{"configuration":{"id":"model","value":%q}}}`, other),
		// Unknown option id -> invalid params.
		`{"jsonrpc":"2.0","id":15,"method":"session/set_config_option","params":{"configId":"thinking","value":"high"}}`,
		// Unknown value -> invalid params.
		fmt.Sprintf(`{"jsonrpc":"2.0","id":16,"method":"%s","params":{"configId":"model","value":"zai/nope"}}`, "session/set_config_options"),
	}

	msgs := runACPSmoke(t, dir, lines)
	// init + new results, one adv notification, then one response per set request.
	if len(msgs) != 8 {
		t.Fatalf("expected 8 messages, got %d: %v", len(msgs), msgs)
	}

	checkSwitch := func(idx int, msgID float64, wantValue string) {
		m := msgs[idx]
		if id, _ := m["id"].(float64); id != msgID {
			t.Fatalf("message %d: expected id %v, got %v", idx, msgID, m["id"])
		}
		result, ok := m["result"].(map[string]any)
		if !ok {
			t.Fatalf("switch to %q: expected result, got %v", wantValue, m)
		}
		opts, ok := result["config_options"].([]any)
		if !ok || len(opts) == 0 {
			t.Fatalf("switch to %q: expected config_options in result, got %v", wantValue, result)
		}
		if _, ok := result["_meta"].(map[string]any); !ok {
			t.Errorf("switch to %q: expected _meta mirror, got %v", wantValue, result["_meta"])
		}
		opt := findModelOption(t, opts)
		if cv, _ := opt["current_value"].(string); cv != wantValue {
			t.Errorf("switch to %q: current_value = %q", wantValue, cv)
		}
	}

	// Switch to glm-b via official method...
	checkSwitch(3, 12, other)
	// ...back to glm-a via bare id...
	checkSwitch(4, 13, initial)
	// ...and to glm-b again via nested params.
	checkSwitch(5, 14, other)

	for _, tc := range []struct {
		id     float64
		msgIdx int
	}{{15, 6}, {16, 7}} {
		errObj, ok := msgs[tc.msgIdx]["error"].(map[string]any)
		if !ok {
			t.Errorf("request %v: expected error, got %v", tc.id, msgs[tc.msgIdx])
			continue
		}
		if code, _ := errObj["code"].(float64); code != acpErrInvalidParams {
			t.Errorf("request %v: expected error code %d, got %v", tc.id, acpErrInvalidParams, errObj["code"])
		}
	}
}
