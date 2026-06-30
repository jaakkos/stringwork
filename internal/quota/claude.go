package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const defaultClaudeUsageURL = "https://api.anthropic.com/api/oauth/usage"

// ClaudeChecker calls Anthropic's OAuth usage endpoint (0 LLM tokens).
type ClaudeChecker struct {
	baseURL    string
	loadCreds  CredentialLoader
	httpClient *http.Client
}

// NewClaudeChecker creates a checker. baseURL defaults to the real API host;
// loadCreds defaults to LoadClaudeCredentials.
func NewClaudeChecker(baseURL string, loadCreds CredentialLoader) *ClaudeChecker {
	if baseURL == "" {
		baseURL = defaultClaudeUsageURL
	}
	if loadCreds == nil {
		loadCreds = func() (any, error) { return LoadClaudeCredentials() }
	}
	return &ClaudeChecker{
		baseURL:    baseURL,
		loadCreds:  loadCreds,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *ClaudeChecker) AgentType() string { return "claude-code" }

func (c *ClaudeChecker) Check(ctx context.Context) Status {
	raw, err := c.loadCreds()
	if err != nil {
		return NoCredentials()
	}
	creds, ok := raw.(*ClaudeCredentials)
	if !ok || creds == nil || creds.AccessToken == "" {
		return NoCredentials()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL, nil)
	if err != nil {
		return CheckFailed(err)
	}
	req.Header.Set("Authorization", "Bearer "+creds.AccessToken)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
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
		return CheckFailed(fmt.Errorf("anthropic usage API: HTTP %d", resp.StatusCode))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CheckFailed(fmt.Errorf("anthropic usage API: HTTP %d", resp.StatusCode))
	}

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return CheckFailed(err)
	}
	return classifyClaudeUsage(data)
}

func classifyClaudeUsage(data map[string]any) Status {
	if blocked, reason, summary, resetAt := claudeBlocked(data); blocked {
		return Blocked(reason, summary, resetAt)
	}
	summary := claudeSummary(data)
	return Available(summary)
}

func claudeBlocked(data map[string]any) (bool, string, string, time.Time) {
	if spend, _ := data["spend"].(map[string]any); spend != nil {
		enabled, _ := spend["enabled"].(bool)
		if enabled {
			severity, _ := spend["severity"].(string)
			percent := floatFromAny(spend["percent"])
			if severity == "critical" || percent >= 100 {
				return true, "org-spend-cap",
					fmt.Sprintf("org spend %.0f%%", percent),
					parseResetTime(spend["reset_at"])
			}
		}
	}
	if extra, _ := data["extra_usage"].(map[string]any); extra != nil {
		isEnabled, _ := extra["is_enabled"].(bool)
		if isEnabled {
			util := floatFromAny(extra["utilization"])
			if util >= 100 {
				return true, "extra-usage-exhausted",
					fmt.Sprintf("extra usage %.0f%%", util),
					parseResetTime(extra["reset_at"])
			}
		}
	}
	for _, key := range []string{"five_hour", "seven_day"} {
		if win, _ := data[key].(map[string]any); win != nil {
			util := floatFromAny(win["utilization"])
			if util >= 100 {
				label := strings.ReplaceAll(key, "_", " ")
				return true, key + "-window",
					fmt.Sprintf("%s window %.0f%%", label, util),
					parseResetTime(win["reset_at"])
			}
		}
	}
	return false, "", "", time.Time{}
}

func claudeSummary(data map[string]any) string {
	var parts []string
	if spend, _ := data["spend"].(map[string]any); spend != nil {
		if enabled, _ := spend["enabled"].(bool); enabled {
			parts = append(parts, fmt.Sprintf("spend %.0f%%", floatFromAny(spend["percent"])))
		}
	}
	if extra, _ := data["extra_usage"].(map[string]any); extra != nil {
		if isEnabled, _ := extra["is_enabled"].(bool); isEnabled {
			parts = append(parts, fmt.Sprintf("extra %.0f%%", floatFromAny(extra["utilization"])))
		}
	}
	for _, key := range []string{"five_hour", "seven_day"} {
		if win, _ := data[key].(map[string]any); win != nil {
			if util := floatFromAny(win["utilization"]); util > 0 {
				parts = append(parts, fmt.Sprintf("%s %.0f%%", key, util))
			}
		}
	}
	if len(parts) == 0 {
		return "OK"
	}
	return strings.Join(parts, ", ")
}

func floatFromAny(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	default:
		return 0
	}
}

func parseResetTime(v any) time.Time {
	s, _ := v.(string)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t, err = time.Parse(time.RFC3339Nano, s)
	}
	if err != nil {
		return time.Time{}
	}
	return t
}
