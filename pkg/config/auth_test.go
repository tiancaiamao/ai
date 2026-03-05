package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveAPIKey_PrefersAuthByDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZAI_API_KEY", "env-key")
	t.Setenv("AI_API_KEY_SOURCE", "")

	authPath := filepath.Join(home, ".ai", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"zai":{"key":"auth-key"}}`), 0644); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	key, err := ResolveAPIKey("zai")
	if err != nil {
		t.Fatalf("ResolveAPIKey returned error: %v", err)
	}
	if key != "auth-key" {
		t.Fatalf("expected auth key, got %q", key)
	}
}

func TestResolveAPIKey_PreferEnvWhenConfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZAI_API_KEY", "env-key")
	t.Setenv("AI_API_KEY_SOURCE", "env")

	authPath := filepath.Join(home, ".ai", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"zai":{"key":"auth-key"}}`), 0644); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	key, err := ResolveAPIKey("zai")
	if err != nil {
		t.Fatalf("ResolveAPIKey returned error: %v", err)
	}
	if key != "env-key" {
		t.Fatalf("expected env key, got %q", key)
	}
}

func TestResolveAPIKey_FallsBackToEnvWhenAuthMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MINIMAX_API_KEY", "minimax-env-key")
	t.Setenv("AI_API_KEY_SOURCE", "")

	key, err := ResolveAPIKey("minimax")
	if err != nil {
		t.Fatalf("ResolveAPIKey returned error: %v", err)
	}
	if key != "minimax-env-key" {
		t.Fatalf("expected env fallback key, got %q", key)
	}
}

func TestResolveAPIKey_FallsBackToAuthWhenEnvMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("AI_API_KEY_SOURCE", "")

	authPath := filepath.Join(home, ".ai", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authPath), 0755); err != nil {
		t.Fatalf("mkdir auth dir: %v", err)
	}
	if err := os.WriteFile(authPath, []byte(`{"openrouter":{"apiKey":"or-auth-key"}}`), 0644); err != nil {
		t.Fatalf("write auth file: %v", err)
	}

	key, err := ResolveAPIKey("openrouter")
	if err != nil {
		t.Fatalf("ResolveAPIKey returned error: %v", err)
	}
	if key != "or-auth-key" {
		t.Fatalf("expected auth fallback key, got %q", key)
	}
}
