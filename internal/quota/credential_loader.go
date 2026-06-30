package quota

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// ClaudeCredentials holds Anthropic OAuth tokens for usage checks.
type ClaudeCredentials struct {
	AccessToken string
}

// CodexCredentials holds ChatGPT/Codex OAuth tokens.
type CodexCredentials struct {
	AccessToken string
	AccountID   string
}

// GeminiCredentials holds Gemini CLI OAuth tokens.
type GeminiCredentials struct {
	AccessToken string
}

// CredentialLoader loads OAuth credentials for an agent type.
type CredentialLoader func() (any, error)

// LoadClaudeCredentials reads macOS Keychain then ~/.claude/.credentials.json.
func LoadClaudeCredentials() (*ClaudeCredentials, error) {
	if runtime.GOOS == "darwin" {
		if creds, err := loadClaudeFromKeychain(); err == nil && creds != nil {
			return creds, nil
		}
	}
	return loadClaudeFromFile()
}

func loadClaudeFromKeychain() (*ClaudeCredentials, error) {
	cmd := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var data map[string]any
	if err := json.Unmarshal(out, &data); err != nil {
		return nil, err
	}
	oauth, _ := data["claudeAiOauth"].(map[string]any)
	if oauth == nil {
		return nil, fmt.Errorf("missing claudeAiOauth in keychain entry")
	}
	token, _ := oauth["accessToken"].(string)
	if token == "" {
		return nil, fmt.Errorf("empty access token in keychain entry")
	}
	return &ClaudeCredentials{AccessToken: token}, nil
}

func loadClaudeFromFile() (*ClaudeCredentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".claude", ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	oauth, _ := root["claudeAiOauth"].(map[string]any)
	if oauth == nil {
		return nil, fmt.Errorf("missing claudeAiOauth in credentials file")
	}
	token, _ := oauth["accessToken"].(string)
	if token == "" {
		return nil, fmt.Errorf("empty access token in credentials file")
	}
	return &ClaudeCredentials{AccessToken: token}, nil
}

// LoadCodexCredentials reads ~/.codex/auth.json (or $CODEX_HOME/auth.json).
func LoadCodexCredentials() (*CodexCredentials, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	path := filepath.Join(codexHome, "auth.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	tokens, _ := root["tokens"].(map[string]any)
	if tokens == nil {
		tokens = root
	}
	token, _ := tokens["access_token"].(string)
	if token == "" {
		return nil, fmt.Errorf("empty access_token in auth.json")
	}
	accountID, _ := tokens["account_id"].(string)
	return &CodexCredentials{AccessToken: token, AccountID: accountID}, nil
}

// LoadGeminiCredentials reads ~/.gemini/oauth_creds.json when auth is oauth-personal.
func LoadGeminiCredentials() (*GeminiCredentials, error) {
	authType := geminiAuthType()
	if authType != "" && authType != "oauth-personal" {
		return nil, fmt.Errorf("unsupported gemini auth type %q", authType)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".gemini", "oauth_creds.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, err
	}
	token, _ := root["access_token"].(string)
	if token == "" {
		return nil, fmt.Errorf("empty access_token in oauth_creds.json")
	}
	return &GeminiCredentials{AccessToken: token}, nil
}

func geminiAuthType() string {
	if os.Getenv("GOOGLE_GENAI_USE_GCA") == "true" {
		return "oauth-personal"
	}
	if os.Getenv("GOOGLE_GENAI_USE_VERTEXAI") == "true" {
		return "vertex-ai"
	}
	if os.Getenv("GEMINI_API_KEY") != "" {
		return "gemini-api-key"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	settingsPath := filepath.Join(home, ".gemini", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return ""
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return ""
	}
	security, _ := root["security"].(map[string]any)
	auth, _ := security["auth"].(map[string]any)
	selected, _ := auth["selectedType"].(string)
	return selected
}

// RedactEmail replaces an email-like string with a redacted placeholder.
func RedactEmail(email string) string {
	if email == "" {
		return ""
	}
	at := strings.Index(email, "@")
	if at <= 0 {
		return "[redacted]"
	}
	return email[:1] + "***" + email[at:]
}

// RedactID redacts account identifiers for logs and snapshots.
func RedactID(id string) string {
	if id == "" {
		return ""
	}
	if len(id) <= 4 {
		return "[redacted]"
	}
	return id[:2] + "…" + id[len(id)-2:]
}
