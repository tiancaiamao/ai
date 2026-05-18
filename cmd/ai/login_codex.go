package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/tiancaiamao/ai/pkg/auth"
)

// loginCodexSubcommand implements the 'ai login-codex' subcommand.
// It initiates the OAuth PKCE flow for OpenAI Codex (ChatGPT subscription).
func loginCodexSubcommand() {
	fs := flag.NewFlagSet("login-codex", flag.ExitOnError)
	fs.Parse(os.Args[1:])

	fmt.Println("Starting OpenAI Codex (ChatGPT) authentication...")
	fmt.Println()
	fmt.Println("This will authenticate via your ChatGPT Plus/Pro subscription.")
	fmt.Println("A browser window will open for you to log in.")
	fmt.Println()

	ctx := context.Background()
	creds, err := auth.TryLoginWithPortRetry(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Authentication failed: %v\n", err)
		os.Exit(1)
	}

	// Save credentials
	if err := auth.SaveCodexCredentials(creds); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save credentials: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("Credentials saved to ~/.ai/auth.json")
	fmt.Println("You can now use the openai-codex provider with models like:")
	fmt.Println("  - gpt-5.1")
	fmt.Println("  - gpt-5.1-codex-max")
	fmt.Println("  - gpt-5.5")
	fmt.Println()
	fmt.Println("Example config:")
	fmt.Println(`  {
    "model": {
      "id": "gpt-5.1",
      "provider": "openai-codex",
      "baseUrl": "https://chatgpt.com/backend-api",
      "api": "openai-codex-responses"
    }
  }`)
}