package models

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	done := make(chan string, 1)
	go func() {
		buf := make([]byte, 8192)
		n, _ := r.Read(buf)
		done <- string(buf[:n])
	}()

	fn()
	_ = w.Close()
	return <-done
}

// writeModelsFile creates an isolated models.json and points the config at it.
// The "zai" provider entry has no API key env var set in tests, so filtering
// by keys keeps only the models we register keys for.
func writeModelsFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	modelsJSON := `{
  "providers": {
    "zai": {
      "baseUrl": "https://api.z.ai/api/coding/paas/v4",
      "api": "openai-completions",
      "models": [
        {"id": "glm-4.5-air", "contextWindow": 128000, "maxTokens": 4096},
        {"id": "glm-4.6", "contextWindow": 200000, "maxTokens": 131072, "reasoning": true, "input": ["image"]}
      ]
    },
    "openai": {
      "baseUrl": "https://api.openai.com/v1",
      "models": [
        {"id": "gpt-4o-mini", "contextWindow": 128000, "maxTokens": 16384}
      ]
    }
  }
}`
	path := filepath.Join(dir, "models.json")
	if err := os.WriteFile(path, []byte(modelsJSON), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AI_MODELS_PATH", path)
	return path
}

func withArgs(t *testing.T, args ...string) {
	t.Helper()
	old := os.Args
	// In production the dispatcher trims the subcommand name before
	// ModelsSubcommand parses os.Args[1:].
	os.Args = append([]string{"ai"}, args...)
	t.Cleanup(func() { os.Args = old })
}

func TestModelsSubcommandLists(t *testing.T) {
	writeModelsFile(t)
	t.Setenv("ZAI_API_KEY", "k1")
	t.Setenv("OPENAI_API_KEY", "k2")

	out := captureStdout(t, func() {
		withArgs(t)
		ModelsSubcommand()
	})

	for _, want := range []string{"glm-4.5-air", "glm-4.6", "gpt-4o-mini", "provider", "model"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	// Sorted: glm-4.5-air before glm-4.6.
	if strings.Index(out, "glm-4.5-air") > strings.Index(out, "glm-4.6") {
		t.Error("models not sorted by id")
	}
}

func TestModelsSubcommandFilterAndProvider(t *testing.T) {
	writeModelsFile(t)
	t.Setenv("ZAI_API_KEY", "k1")

	out := captureStdout(t, func() {
		withArgs(t, "glm-4.6")
		ModelsSubcommand()
	})
	if !strings.Contains(out, "glm-4.6") || strings.Contains(out, "glm-4.5-air") {
		t.Errorf("positional filter failed:\n%s", out)
	}

	out = captureStdout(t, func() {
		withArgs(t, "--provider", "zai")
		ModelsSubcommand()
	})
	if !strings.Contains(out, "glm-4.5-air") || strings.Contains(out, "gpt-4o-mini") {
		t.Errorf("provider filter failed:\n%s", out)
	}
}

func TestModelsSubcommandNoMatch(t *testing.T) {
	writeModelsFile(t)
	t.Setenv("ZAI_API_KEY", "k1")

	out := captureStdout(t, func() {
		withArgs(t, "nonexistent-model")
		ModelsSubcommand()
	})
	if !strings.Contains(out, `no models matching "nonexistent-model"`) {
		t.Errorf("unexpected output: %s", out)
	}

	out = captureStdout(t, func() {
		withArgs(t, "--provider", "anthropic")
		ModelsSubcommand()
	})
	if !strings.Contains(out, `no models for provider "anthropic"`) {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestFormatTokenCount(t *testing.T) {
	cases := map[int]string{
		0:       "-",
		-5:      "-",
		500:     "500",
		128000:  "128K",
		131072:  "131.1K",
		1000000: "1M",
		1500000: "1.5M",
	}
	for in, want := range cases {
		if got := formatTokenCount(in); got != want {
			t.Errorf("formatTokenCount(%d) = %q; want %q", in, got, want)
		}
	}
}

func TestRowHelpers(t *testing.T) {
	if boolStr(true) != "yes" || boolStr(false) != "no" {
		t.Error("boolStr broken")
	}
	if inputHas([]string{"image", "text"}, "image") != "yes" {
		t.Error("inputHas should find image")
	}
	if inputHas([]string{"text"}, "image") != "no" {
		t.Error("inputHas should report no")
	}
	if pad("ab", 5) != "ab   " {
		t.Errorf("pad = %q", pad("ab", 5))
	}
	if pad("abcdef", 3) != "abcdef" {
		t.Errorf("pad should not truncate, got %q", pad("abcdef", 3))
	}
}
