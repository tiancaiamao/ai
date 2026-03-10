package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name           string
		configJSON     string
		expectError    bool
		expectedID     string
		expectedProvider string
		expectedBaseURL string
		expectedAPI    string
	}{
		{
			name: "empty model section - should error",
			configJSON: `{
				"model": {}
			}`,
			expectError: true,
		},
		{
			name: "no model section - should error",
			configJSON: `{
				"channels": {
					"feishu": {
						"enabled": false
					}
				}
			}`,
			expectError: true,
		},
		{
			name: "partial model section - only ID - should error",
			configJSON: `{
				"model": {
					"id": "custom-model"
				}
			}`,
			expectError: true,
		},
		{
			name: "partial model section - ID and provider - should error",
			configJSON: `{
				"model": {
					"id": "test-model",
					"provider": "test-provider"
				}
			}`,
			expectError: true,
		},
		{
			name: "full model section - no error",
			configJSON: `{
				"model": {
					"id": "my-model",
					"provider": "my-provider",
					"baseUrl": "https://my.api.com",
					"api": "my-api"
				}
			}`,
			expectError: false,
			expectedID:     "my-model",
			expectedProvider: "my-provider",
			expectedBaseURL: "https://my.api.com",
			expectedAPI:    "my-api",
		},
		{
			name: "full model section without api - should default api",
			configJSON: `{
				"model": {
					"id": "my-model",
					"provider": "my-provider",
					"baseUrl": "https://my.api.com"
				}
			}`,
			expectError: false,
			expectedID:     "my-model",
			expectedProvider: "my-provider",
			expectedBaseURL: "https://my.api.com",
			expectedAPI:    "openai-completions",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cfg Config
			if err := json.Unmarshal([]byte(tt.configJSON), &cfg); err != nil {
				t.Fatalf("Failed to parse config: %v", err)
			}

			err := cfg.validate()

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error but got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
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
			}
		})
	}
}

func TestLoadConfigWithValidation(t *testing.T) {
	// Create a temporary config file
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Test 1: Config without model section - should error
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
	if err == nil {
		t.Errorf("Expected error for config without model section, got nil")
	}

	// Test 2: Config with partial model section - should error
	configWithPartialModel := `{
		"model": {
			"id": "custom-model"
		}
	}`

	if err := os.WriteFile(configPath, []byte(configWithPartialModel), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err = loadConfig(configPath)
	if err == nil {
		t.Errorf("Expected error for partial model section, got nil")
	}

	// Test 3: Config with full model section - should succeed
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
	// API should default to openai-completions
	if cfg.Model.API != "openai-completions" {
		t.Errorf("Expected Model.API = 'openai-completions', got %q", cfg.Model.API)
	}

	// Test 4: Config with explicit API - should preserve it
	configWithAPI := `{
		"model": {
			"id": "my-model",
			"provider": "my-provider",
			"baseUrl": "https://my.api.com",
			"api": "custom-api"
		}
	}`

	if err := os.WriteFile(configPath, []byte(configWithAPI), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}

	cfg, err = loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig failed: %v", err)
	}

	if cfg.Model.API != "custom-api" {
		t.Errorf("Expected Model.API = 'custom-api', got %q", cfg.Model.API)
	}
}