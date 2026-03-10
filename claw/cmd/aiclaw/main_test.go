package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigApplyDefaults(t *testing.T) {
	tests := []struct {
		name           string
		configJSON     string
		expectedID     string
		expectedProvider string
		expectedBaseURL string
		expectedAPI    string
	}{
		{
			name: "empty model section",
			configJSON: `{
				"model": {}
			}`,
			expectedID:     "glm-4-flash",
			expectedProvider: "zai",
			expectedBaseURL: "https://api.z.ai/api/coding/paas/v4",
			expectedAPI:    "openai-completions",
		},
		{
			name: "no model section",
			configJSON: `{
				"channels": {
					"feishu": {
						"enabled": false
					}
				}
			}`,
			expectedID:     "glm-4-flash",
			expectedProvider: "zai",
			expectedBaseURL: "https://api.z.ai/api/coding/paas/v4",
			expectedAPI:    "openai-completions",
		},
		{
			name: "partial model section - only ID",
			configJSON: `{
				"model": {
					"id": "custom-model"
				}
			}`,
			expectedID:     "custom-model",
			expectedProvider: "zai",
			expectedBaseURL: "https://api.z.ai/api/coding/paas/v4",
			expectedAPI:    "openai-completions",
		},
		{
			name: "partial model section - ID and provider",
			configJSON: `{
				"model": {
					"id": "test-model",
					"provider": "test-provider"
				}
			}`,
			expectedID:     "test-model",
			expectedProvider: "test-provider",
			expectedBaseURL: "https://api.z.ai/api/coding/paas/v4",
			expectedAPI:    "openai-completions",
		},
		{
			name: "full model section - no defaults applied",
			configJSON: `{
				"model": {
					"id": "my-model",
					"provider": "my-provider",
					"baseUrl": "https://my.api.com",
					"api": "my-api"
				}
			}`,
			expectedID:     "my-model",
			expectedProvider: "my-provider",
			expectedBaseURL: "https://my.api.com",
			expectedAPI:    "my-api",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			if err := json.Unmarshal([]byte(tt.configJSON), &cfg); err != nil {
				t.Fatalf("Failed to parse config: %v", err)
			}

			cfg.applyDefaults()

			if cfg.Model.ID != tt.expectedID {
				t.Errorf("Expected Model.ID = %q, got %q", tt.expectedID, cfg.Model.ID)
			}
			if cfg.Model.Provider != tt.expectedProvider {
				t.Errorf("Expected Model.Provider = %q, got %q", tt.expectedProvider, cfg.Model.Provider)
			}
			if cfg.Model.BaseURL != tt.expectedBaseURL {
				t.Errorf("Expected Model.BaseURL = %q, got %q", tt.expectedBaseURL, cfg.Model.BaseURL)
			}
			if cfg.Model.API != tt.expectedAPI {
				t.Errorf("Expected Model.API = %q, got %q", tt.expectedAPI, cfg.Model.API)
			}
		})
	}
}

func TestLoadConfigWithDefaults(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Test 1: Config without model section
	configWithoutModel := `{
		"channels": {
			"feishu": {
				"enabled": false
			}
		}
	}`

	if err := os.WriteFile(configPath, []byte(configWithoutModel), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	if cfg.Model.ID != "glm-4-flash" {
		t.Errorf("Expected default Model.ID = 'glm-4-flash', got %q", cfg.Model.ID)
	}
	if cfg.Model.Provider != "zai" {
		t.Errorf("Expected default Model.Provider = 'zai', got %q", cfg.Model.Provider)
	}
	if cfg.Model.BaseURL != "https://api.z.ai/api/coding/paas/v4" {
		t.Errorf("Expected default Model.BaseURL, got %q", cfg.Model.BaseURL)
	}

	// Test 2: Config with partial model section
	configWithPartialModel := `{
		"model": {
			"id": "custom-model"
		}
	}`

	if err := os.WriteFile(configPath, []byte(configWithPartialModel), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err = loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	if cfg.Model.ID != "custom-model" {
		t.Errorf("Expected Model.ID = 'custom-model', got %q", cfg.Model.ID)
	}
	if cfg.Model.Provider != "zai" {
		t.Errorf("Expected default Model.Provider = 'zai', got %q", cfg.Model.Provider)
	}

	// Test 3: Config with full model section - defaults should not override
	configWithFullModel := `{
		"model": {
			"id": "my-model",
			"provider": "my-provider",
			"baseUrl": "https://my.api.com"
		}
	}`

	if err := os.WriteFile(configPath, []byte(configWithFullModel), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err = loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	if cfg.Model.ID != "my-model" {
		t.Errorf("Expected Model.ID = 'my-model', got %q", cfg.Model.ID)
	}
	if cfg.Model.Provider != "my-provider" {
		t.Errorf("Expected Model.Provider = 'my-provider', got %q", cfg.Model.Provider)
	}
	if cfg.Model.BaseURL != "https://my.api.com" {
		t.Errorf("Expected Model.BaseURL = 'https://my.api.com', got %q", cfg.Model.BaseURL)
	}
}