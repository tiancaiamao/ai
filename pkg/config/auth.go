package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AuthEntry holds API key credentials for a provider.
type AuthEntry struct {
	Type   string `json:"type,omitempty"`
	Key    string `json:"key,omitempty"`
	APIKey string `json:"apiKey,omitempty"`
	Token  string `json:"token,omitempty"`
}

// GetDefaultAuthPath returns the default auth file path.
func GetDefaultAuthPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(homeDir, ".ai", "auth.json"), nil
}

// ResolveAPIKey resolves API key from env or auth.json for the provider.
func ResolveAPIKey(provider string) (string, error) {
	providerKey := strings.ToLower(strings.TrimSpace(provider))
	if providerKey == "" {
		providerKey = "zai"
	}

	envVar := strings.ToUpper(providerKey) + "_API_KEY"
	envValue := strings.TrimSpace(os.Getenv(envVar))

	authValue, authPath, authErr := resolveAPIKeyFromAuth(providerKey)
	preferEnv := strings.EqualFold(strings.TrimSpace(os.Getenv("AI_API_KEY_SOURCE")), "env")

	// Default to auth-first to avoid stale shell env overriding managed auth.json.
	if preferEnv {
		if envValue != "" {
			return envValue, nil
		}
		if authValue != "" {
			return authValue, nil
		}
	} else {
		if authValue != "" {
			return authValue, nil
		}
		if envValue != "" {
			return envValue, nil
		}
	}

	if authErr != nil {
		return "", authErr
	}
	if authPath == "" {
		var err error
		authPath, err = GetDefaultAuthPath()
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("set %s or add %s", envVar, authPath)
}

func resolveAPIKeyFromAuth(providerKey string) (string, string, error) {
	authPath, err := GetDefaultAuthPath()
	if err != nil {
		return "", "", err
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", authPath, nil
		}
		return "", authPath, fmt.Errorf("failed to read auth file: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", authPath, fmt.Errorf("failed to parse auth file: %w", err)
	}

	entryRaw, ok := raw[providerKey]
	if !ok {
		for key, value := range raw {
			if strings.EqualFold(key, providerKey) {
				entryRaw = value
				ok = true
				break
			}
		}
	}
	if !ok {
		return "", authPath, nil
	}

	var key string
	if err := json.Unmarshal(entryRaw, &key); err == nil {
		key = strings.TrimSpace(key)
		if key != "" {
			return key, authPath, nil
		}
	}

	var entry AuthEntry
	if err := json.Unmarshal(entryRaw, &entry); err != nil {
		return "", authPath, fmt.Errorf("invalid auth entry for %q in %s", providerKey, authPath)
	}
	if entry.APIKey != "" {
		return strings.TrimSpace(entry.APIKey), authPath, nil
	}
	if entry.Key != "" {
		return strings.TrimSpace(entry.Key), authPath, nil
	}
	if entry.Token != "" {
		return strings.TrimSpace(entry.Token), authPath, nil
	}

	return "", authPath, fmt.Errorf("empty credentials for %q in %s", providerKey, authPath)
}
