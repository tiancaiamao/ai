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
	if value := strings.TrimSpace(os.Getenv(envVar)); value != "" {
		return value, nil
	}

	authPath, err := GetDefaultAuthPath()
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("set %s or add %s", envVar, authPath)
		}
		return "", fmt.Errorf("failed to read auth file: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", fmt.Errorf("failed to parse auth file: %w", err)
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
		return "", fmt.Errorf("no credentials for %q in %s", providerKey, authPath)
	}

	var key string
	if err := json.Unmarshal(entryRaw, &key); err == nil {
		key = strings.TrimSpace(key)
		if key != "" {
			return key, nil
		}
	}

	var entry AuthEntry
	if err := json.Unmarshal(entryRaw, &entry); err != nil {
		return "", fmt.Errorf("invalid auth entry for %q in %s", providerKey, authPath)
	}
	if entry.APIKey != "" {
		return entry.APIKey, nil
	}
	if entry.Key != "" {
		return entry.Key, nil
	}
	if entry.Token != "" {
		return entry.Token, nil
	}

	return "", fmt.Errorf("empty credentials for %q in %s", providerKey, authPath)
}
