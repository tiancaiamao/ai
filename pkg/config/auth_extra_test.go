package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeAuthFile writes an auth.json into a temp HOME and returns its path.
func writeAuthFile(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	authPath := filepath.Join(home, ".ai", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(content), 0644); err != nil {
		t.Fatalf("write auth file: %v", err)
	}
	return authPath
}

func TestResolveAPIKey_EmptyProviderDefaultsToZai(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("AI_API_KEY_SOURCE", "")

	// No auth file, no env → error message mentions ZAI_API_KEY.
	_, err := ResolveAPIKey("  ")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestResolveAPIKey_AuthEntryFormats(t *testing.T) {
	tests := []struct {
		name    string
		entry   string
		wantKey string
	}{
		{"plain string entry", `{"zai":"plain-key"}`, "plain-key"},
		{"apiKey field", `{"zai":{"apiKey":"ak"}}`, "ak"},
		{"key field", `{"zai":{"key":"kf"}}`, "kf"},
		{"token field", `{"zai":{"token":"tk"}}`, "tk"},
		{"case-insensitive provider match", `{"ZAI":{"key":"ci"}}`, "ci"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeAuthFile(t, tt.entry)
			t.Setenv("ZAI_API_KEY", "")
			t.Setenv("AI_API_KEY_SOURCE", "")
			key, err := ResolveAPIKey("zai")
			if err != nil {
				t.Fatalf("ResolveAPIKey error: %v", err)
			}
			if key != tt.wantKey {
				t.Errorf("key = %q, want %q", key, tt.wantKey)
			}
		})
	}
}

func TestResolveAPIKey_AuthFileErrors(t *testing.T) {
	// Invalid JSON → parse error surfaced.
	writeAuthFile(t, `{not-json`)
	t.Setenv("ZAI_API_KEY", "")
	t.Setenv("AI_API_KEY_SOURCE", "")
	_, err := ResolveAPIKey("zai")
	if err == nil {
		t.Error("expected error for invalid JSON auth file")
	}

	// Entry with all-empty credentials → error.
	writeAuthFile(t, `{"zai":{"key":""}}`)
	t.Setenv("ZAI_API_KEY", "")
	_, err = ResolveAPIKey("zai")
	if err == nil {
		t.Error("expected error for empty credentials")
	}

	// Non-string non-object entry → invalid entry error.
	writeAuthFile(t, `{"zai":42}`)
	t.Setenv("ZAI_API_KEY", "")
	_, err = ResolveAPIKey("zai")
	if err == nil {
		t.Error("expected error for invalid entry type")
	}
}

func TestResolveAPIKey_ProviderNotInAuthFallsToEnv(t *testing.T) {
	writeAuthFile(t, `{"other":{"key":"x"}}`)
	t.Setenv("OTHERPROVIDER_API_KEY", "env-value")
	t.Setenv("AI_API_KEY_SOURCE", "")
	key, err := ResolveAPIKey("otherprovider")
	if err != nil {
		t.Fatalf("ResolveAPIKey error: %v", err)
	}
	if key != "env-value" {
		t.Errorf("key = %q, want env-value", key)
	}
}

func TestResolveAPIKey_NoHomeDir(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("AI_API_KEY_SOURCE", "")
	// os.UserHomeDir fails; error should propagate when nothing else resolves.
	t.Setenv("ZAI_API_KEY", "")
	_, err := ResolveAPIKey("zai")
	if err == nil {
		t.Error("expected error when HOME is unset")
	}
}

func TestGetDefaultAuthPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := GetDefaultAuthPath()
	if err != nil {
		t.Fatalf("GetDefaultAuthPath error: %v", err)
	}
	if want := filepath.Join(home, ".ai", "auth.json"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}
