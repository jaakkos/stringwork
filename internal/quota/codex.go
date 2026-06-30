package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultCodexUsagePath = "/backend-api/wham/usage"

// CodexChecker calls ChatGPT's wham/usage endpoint (0 LLM tokens).
type CodexChecker struct {
	baseURL    string // scheme+host, e.g. https://chatgpt.com
	loadCreds  CredentialLoader
	httpClient *http.Client
}

// NewCodexChecker creates a checker. baseURL is the scheme+host (default https://chatgpt.com).
func NewCodexChecker(baseURL string, loadCreds CredentialLoader) *CodexChecker {
	if baseURL == "" {
		baseURL = "https://chatgpt.com"
	}
	if loadCreds == nil {
		loadCreds = func() (any, error) { return LoadCodexCredentials() }
	}
	return &CodexChecker{
		baseURL:    stringsTrimRightSlash(baseURL),
		loadCreds:  loadCreds,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func stringsTrimRightSlash(s string) string {
	for len(s) > 0 && s[len(s)-1] == '/' {
		s = s[:len(s)-1]
	}
	return s
}

func (c *CodexChecker) AgentType() string { return "codex" }

func (c *CodexChecker) Check(ctx context.Context) Status {
	raw, err := c.loadCreds()
	if err != nil {
		return NoCredentials()
	}
	creds, ok := raw.(*CodexCredentials)
	if !ok || creds == nil || creds.AccessToken == "" {
		return NoCredentials()
	}

	url := c.baseURL + defaultCodexUsagePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return CheckFailed(err)
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	if creds.AccountID != "" {
		req.Header.Set("ChatGPT-Account-Id", creds.AccountID)
	}
	req.Header.Set("User-Agent", "codex-cli")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CheckFailed(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return CheckFailed(err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return CheckFailed(fmt.Errorf("codex usage API: HTTP %d", resp.StatusCode))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CheckFailed(fmt.Errorf("codex usage API: HTTP %d", resp.StatusCode))
	}

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return CheckFailed(err)
	}
	return classifyCodexUsage(data)
}

func classifyCodexUsage(data map[string]any) Status {
	if blocked, reason, summary, resetAt := codexBlocked(data); blocked {
		return Blocked(reason, summary, resetAt)
	}
	return Available(codexSummary(data))
}

func codexBlocked(data map[string]any) (bool, string, string, time.Time) {
	if sc, _ := data["spend_control"].(map[string]any); sc != nil {
		if reached, _ := sc["reached"].(bool); reached {
			return true, "spend-control", "org spend cap reached", time.Time{}
		}
	}
	if rlt := data["rate_limit_reached_type"]; rlt != nil {
		return true, "rate-limit-reached",
			fmt.Sprintf("rate limit reached (%v)", rlt), time.Time{}
	}
	if rl, ok := data["rate_limit"].(map[string]any); ok && rl != nil {
		for _, key := range []string{"primary_window", "secondary_window"} {
			if win, _ := rl[key].(map[string]any); win != nil {
				used := floatFromAny(win["used_percent"])
				if used >= 100 {
					return true, key,
						fmt.Sprintf("%s %.0f%%", key, used),
						parseResetTime(win["reset_at"])
				}
			}
		}
	}
	// rate_limit: null on business plans is NOT blocked.
	return false, "", "", time.Time{}
}

func codexSummary(data map[string]any) string {
	if rl, ok := data["rate_limit"].(map[string]any); ok && rl != nil {
		if win, _ := rl["primary_window"].(map[string]any); win != nil {
			return fmt.Sprintf("primary %.0f%%", floatFromAny(win["used_percent"]))
		}
	}
	if sc, _ := data["spend_control"].(map[string]any); sc != nil {
		if reached, _ := sc["reached"].(bool); reached {
			return "spend cap reached"
		}
	}
	plan, _ := data["plan_type"].(string)
	if plan != "" {
		return "plan " + plan
	}
	return "OK"
}

// CodexSnapshotEntry holds redacted fields for worker_status / CLI JSON.
type CodexSnapshotEntry struct {
	PlanType string `json:"plan_type,omitempty"`
	Primary  string `json:"primary_window,omitempty"`
	Email    string `json:"email,omitempty"`
}

// RedactCodexSnapshot removes sensitive fields from a raw usage response.
func RedactCodexSnapshot(data map[string]any) CodexSnapshotEntry {
	entry := CodexSnapshotEntry{}
	if plan, _ := data["plan_type"].(string); plan != "" {
		entry.PlanType = plan
	}
	if rl, ok := data["rate_limit"].(map[string]any); ok && rl != nil {
		if win, _ := rl["primary_window"].(map[string]any); win != nil {
			entry.Primary = fmt.Sprintf("%.0f%%", floatFromAny(win["used_percent"]))
		}
	}
	if email, _ := data["email"].(string); email != "" {
		entry.Email = RedactEmail(email)
	}
	return entry
}
