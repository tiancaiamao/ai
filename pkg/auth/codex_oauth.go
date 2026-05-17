// Package auth provides OAuth authentication for providers that require it
// (e.g. OpenAI Codex subscription via ChatGPT).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CodexOAuth constants following pi-mono's OpenAI Codex implementation.
const (
	CodexClientID    = "app_EMoamEEZ73f0CkXaXp7hrann"
	CodexAuthorizeURL = "https://auth.openai.com/oauth/authorize"
	CodexTokenURL     = "https://auth.openai.com/oauth/token"
	CodexRedirectURI  = "http://localhost:1455/auth/callback"
	CodexScope        = "openid profile email offline_access"
	CodexJWTClaimPath = "https://api.openai.com/auth"
)

// CodexCredentials holds OAuth tokens for the Codex provider.
type CodexCredentials struct {
	Access    string `json:"access"`
	Refresh   string `json:"refresh"`
	Expires   int64  `json:"expires"`   // Unix timestamp (ms)
	AccountID string `json:"accountId"`
}

// IsExpired returns true if the access token has expired (with 60s buffer).
func (c *CodexCredentials) IsExpired() bool {
	if c.Expires == 0 {
		return true
	}
	return time.Now().UnixMilli() > c.Expires-60000
}

// pkce holds PKCE verifier and challenge.
type pkce struct {
	verifier  string
	challenge string
}

func generatePKCE() (*pkce, error) {
	verifier := make([]byte, 32)
	if _, err := rand.Read(verifier); err != nil {
		return nil, fmt.Errorf("generate PKCE verifier: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(verifier)

	h := sha256.Sum256([]byte(encoded))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])

	return &pkce{verifier: encoded, challenge: challenge}, nil
}

func generateState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}

// callbackResult holds the OAuth callback result.
type callbackResult struct {
	code  string
	state string
}

// localServer is a temporary HTTP server to receive the OAuth callback.
type localServer struct {
	listener net.Listener
	server   *http.Server
	resultCh chan callbackResult
	state    string
	cancel   context.CancelFunc
}

func startLocalServer(state string) (*localServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:1455")
	if err != nil {
		return nil, fmt.Errorf("listen on callback port: %w", err)
	}

	ls := &localServer{
		listener: listener,
		resultCh: make(chan callbackResult, 1),
		state:    state,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", ls.handleCallback)
	ls.server = &http.Server{Handler: mux}

	ctx, cancel := context.WithCancel(context.Background())
	ls.cancel = cancel

	go ls.server.Serve(listener)

	// Shutdown server when context is cancelled
	go func() {
		<-ctx.Done()
		ls.server.Close()
	}()

	return ls, nil
}

func (ls *localServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")

	if state != ls.state {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `<html><body><h2>Authentication failed: state mismatch</h2></body></html>`)
		return
	}

	// Send result
	select {
	case ls.resultCh <- callbackResult{code: code, state: state}:
	default:
	}

	w.Header().Set("Content-Type", "text/html")
	fmt.Fprint(w, `<html><body><h2>Authentication successful! You can close this tab.</h2></body></html>`)
}

func (ls *localServer) waitForCode(ctx context.Context) (string, error) {
	select {
	case result := <-ls.resultCh:
		return result.code, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (ls *localServer) close() {
	if ls.cancel != nil {
		ls.cancel()
	}
	if ls.listener != nil {
		ls.listener.Close()
	}
}

// tokenResponse is the response from the token endpoint.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
}

// tokenError is an error from the token endpoint.
type tokenError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func exchangeCode(code string, verifier string) (*CodexCredentials, error) {
	data := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {CodexClientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {CodexRedirectURI},
	}

	resp, err := http.Post(CodexTokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("token exchange request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read token response: %w", err)
	}

	if resp.StatusCode != 200 {
		var tokErr tokenError
		if json.Unmarshal(body, &tokErr) == nil && tokErr.ErrorDescription != "" {
			return nil, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, tokErr.ErrorDescription)
		}
		return nil, fmt.Errorf("token exchange failed (%d): %s", resp.StatusCode, string(body))
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse token response: %w", err)
	}

	if tokenResp.AccessToken == "" || tokenResp.RefreshToken == "" {
		return nil, fmt.Errorf("token response missing access_token or refresh_token")
	}

	accountID, err := extractAccountID(tokenResp.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("extract account ID from token: %w", err)
	}

	return &CodexCredentials{
		Access:    tokenResp.AccessToken,
		Refresh:   tokenResp.RefreshToken,
		Expires:   time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UnixMilli(),
		AccountID: accountID,
	}, nil
}

// RefreshCodexToken refreshes an expired access token using the refresh token.
func RefreshCodexToken(refreshToken string) (*CodexCredentials, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {CodexClientID},
	}

	resp, err := http.Post(CodexTokenURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("token refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read refresh response: %w", err)
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("token refresh failed (%d): %s", resp.StatusCode, string(body))
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parse refresh response: %w", err)
	}

	if tokenResp.AccessToken == "" || tokenResp.RefreshToken == "" {
		return nil, fmt.Errorf("refresh response missing access_token or refresh_token")
	}

	accountID, err := extractAccountID(tokenResp.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("extract account ID from refreshed token: %w", err)
	}

	return &CodexCredentials{
		Access:    tokenResp.AccessToken,
		Refresh:   tokenResp.RefreshToken,
		Expires:   time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).UnixMilli(),
		AccountID: accountID,
	}, nil
}

// extractAccountID extracts the chatgpt_account_id from a JWT access token.
func extractAccountID(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("invalid JWT: expected 3 parts, got %d", len(parts))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode JWT payload: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("parse JWT claims: %w", err)
	}

	auth, ok := claims[CodexJWTClaimPath].(map[string]any)
	if !ok {
		return "", fmt.Errorf("JWT missing %s claim", CodexJWTClaimPath)
	}

	accountID, ok := auth["chatgpt_account_id"].(string)
	if !ok || accountID == "" {
		return "", fmt.Errorf("JWT missing chatgpt_account_id")
	}

	return accountID, nil
}

// LoginCodex initiates the OpenAI Codex OAuth login flow.
// It opens a browser for the user to authenticate, then waits for the callback.
// Returns credentials on success.
func LoginCodex(ctx context.Context) (*CodexCredentials, error) {
	p, err := generatePKCE()
	if err != nil {
		return nil, err
	}

	state, err := generateState()
	if err != nil {
		return nil, err
	}

	// Build authorization URL
	authURL, err := url.Parse(CodexAuthorizeURL)
	if err != nil {
		return nil, err
	}
	authURL.RawQuery = url.Values{
		"response_type":         {"code"},
		"client_id":             {CodexClientID},
		"redirect_uri":          {CodexRedirectURI},
		"scope":                 {CodexScope},
		"state":                 {state},
		"code_challenge":        {p.challenge},
		"code_challenge_method": {"S256"},
		"originator":            {"ai"},
	}.Encode()

	// Start local callback server
	srv, err := startLocalServer(state)
	if err != nil {
		return nil, fmt.Errorf("start callback server: %w", err)
	}
	defer srv.close()

	// Print instructions
	fmt.Println("Open the following URL in your browser to authenticate:")
	fmt.Println()
	fmt.Println("  " + authURL.String())
	fmt.Println()
	fmt.Println("Waiting for authentication callback...")

	// Wait for callback with timeout
	callbackCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	code, err := srv.waitForCode(callbackCtx)
	if err != nil {
		return nil, fmt.Errorf("waiting for callback: %w", err)
	}

	if code == "" {
		return nil, errors.New("no authorization code received")
	}

	// Exchange code for tokens
	creds, err := exchangeCode(code, p.verifier)
	if err != nil {
		return nil, fmt.Errorf("exchange authorization code: %w", err)
	}

	fmt.Println("Authentication successful!")
	return creds, nil
}

// SaveCodexCredentials saves Codex OAuth credentials to ~/.ai/auth.json.
func SaveCodexCredentials(creds *CodexCredentials) error {
	authPath, err := getAuthFilePath()
	if err != nil {
		return err
	}

	// Read existing auth file
	data := make(map[string]any)
	if existing, err := os.ReadFile(authPath); err == nil {
		json.Unmarshal(existing, &data)
	}

	// Set codex credentials
	data["openai-codex"] = map[string]any{
		"type":     "oauth",
		"access":   creds.Access,
		"refresh":  creds.Refresh,
		"expires":  creds.Expires,
		"accountId": creds.AccountID,
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(authPath), 0700); err != nil {
		return err
	}

	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(authPath, out, 0600)
}

// LoadCodexCredentials loads Codex OAuth credentials from ~/.ai/auth.json.
// If the access token is expired, it attempts to refresh automatically.
func LoadCodexCredentials() (*CodexCredentials, error) {
	authPath, err := getAuthFilePath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(authPath)
	if err != nil {
		return nil, fmt.Errorf("read auth file: %w", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse auth file: %w", err)
	}

	entryRaw, ok := raw["openai-codex"]
	if !ok {
		return nil, fmt.Errorf("no openai-codex entry in %s", authPath)
	}

	var entry struct {
		Type      string `json:"type"`
		Access    string `json:"access"`
		Refresh   string `json:"refresh"`
		Expires   int64  `json:"expires"`
		AccountID string `json:"accountId"`
	}
	if err := json.Unmarshal(entryRaw, &entry); err != nil {
		return nil, fmt.Errorf("parse openai-codex entry: %w", err)
	}

	creds := &CodexCredentials{
		Access:    entry.Access,
		Refresh:   entry.Refresh,
		Expires:   entry.Expires,
		AccountID: entry.AccountID,
	}

	// Auto-refresh if expired
	if creds.IsExpired() {
		slog.Info("Codex access token expired, refreshing...")
		refreshed, err := RefreshCodexToken(creds.Refresh)
		if err != nil {
			return nil, fmt.Errorf("refresh expired token: %w", err)
		}
		// Save refreshed credentials
		if err := SaveCodexCredentials(refreshed); err != nil {
			slog.Warn("Failed to save refreshed Codex credentials", "error", err)
		}
		return refreshed, nil
	}

	return creds, nil
}

func getAuthFilePath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".ai", "auth.json"), nil
}

// ResolveCodexAPIKey resolves the API key (access token) for the Codex provider.
// It loads credentials from auth.json, refreshing if necessary.
// Returns the access token and account ID.
func ResolveCodexAPIKey() (accessToken string, accountID string, err error) {
	creds, err := LoadCodexCredentials()
	if err != nil {
		return "", "", fmt.Errorf("load Codex credentials: %w", err)
	}
	return creds.Access, creds.AccountID, nil
}

// ExtractAccountID is the public wrapper for extracting account ID from a JWT token.
func ExtractAccountID(token string) (string, error) {
	return extractAccountID(token)
}

// ensure port is available by checking if nothing is listening
var portMutex sync.Mutex

// TryLoginWithPortRetry attempts login, retrying with different ports if 1455 is busy.
func TryLoginWithPortRetry(ctx context.Context) (*CodexCredentials, error) {
	portMutex.Lock()
	defer portMutex.Unlock()

	return LoginCodex(ctx)
}

// parseRetryAfterHeader parses a Retry-After header value into a Duration.
func parseRetryAfterHeader(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		d := time.Until(at)
		if d > 0 {
			return d
		}
	}
	return 0
}