package config

import (
	"fmt"

	"github.com/tiancaiamao/ai/pkg/auth"
)

// resolveCodexAPIKey resolves the API key for the openai-codex provider.
// It loads OAuth credentials from auth.json and auto-refreshes if expired.
func resolveCodexAPIKey(proxyURL string) (string, error) {
	creds, err := auth.LoadCodexCredentialsWithProxy(proxyURL)
	if err != nil {
		return "", fmt.Errorf("no Codex credentials: %w (configure via auth.json with OAuth tokens)", err)
	}
	return creds.Access, nil
}
