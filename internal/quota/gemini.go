package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const geminiCodeAssistBase = "https://cloudcode-pa.googleapis.com/v1internal"

// GeminiChecker calls Code Assist retrieveUserQuota (0 LLM tokens).
// Hard fail-open: any error returns Available.
type GeminiChecker struct {
	baseURL    string
	loadCreds  CredentialLoader
	httpClient *http.Client
}

// NewGeminiChecker creates a checker. baseURL defaults to the Code Assist host.
func NewGeminiChecker(baseURL string, loadCreds CredentialLoader) *GeminiChecker {
	if baseURL == "" {
		baseURL = geminiCodeAssistBase
	}
	if loadCreds == nil {
		loadCreds = func() (any, error) { return LoadGeminiCredentials() }
	}
	return &GeminiChecker{
		baseURL:    stringsTrimRightSlash(baseURL),
		loadCreds:  loadCreds,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (g *GeminiChecker) AgentType() string { return "gemini" }

func (g *GeminiChecker) Check(ctx context.Context) Status {
	status := g.check(ctx)
	if status.Kind == KindNoCredentials || status.Kind == KindCheckFailed {
		// ponytail: Gemini auth is fragile; never stall the worker pool on quota errors.
		return Available("gemini quota check skipped (fail-open)")
	}
	return status
}

func (g *GeminiChecker) check(ctx context.Context) Status {
	raw, err := g.loadCreds()
	if err != nil {
		return NoCredentials()
	}
	creds, ok := raw.(*GeminiCredentials)
	if !ok || creds == nil || creds.AccessToken == "" {
		return NoCredentials()
	}

	loadBody := map[string]any{
		"metadata": map[string]any{
			"ideType":    "IDE_UNSPECIFIED",
			"platform":   "PLATFORM_UNSPECIFIED",
			"pluginType": "GEMINI",
		},
	}
	if project := os.Getenv("GOOGLE_CLOUD_PROJECT"); project != "" {
		loadBody["cloudaicompanionProject"] = project
		loadBody["metadata"].(map[string]any)["duetProject"] = project
	} else if project := os.Getenv("GOOGLE_CLOUD_PROJECT_ID"); project != "" {
		loadBody["cloudaicompanionProject"] = project
		loadBody["metadata"].(map[string]any)["duetProject"] = project
	}

	loadRes, err := g.post(ctx, "loadCodeAssist", loadBody, creds.AccessToken)
	if err != nil {
		return CheckFailed(err)
	}
	projectID, _ := loadRes["cloudaicompanionProject"].(string)
	if projectID == "" {
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if projectID == "" {
		projectID = os.Getenv("GOOGLE_CLOUD_PROJECT_ID")
	}
	if projectID == "" {
		return CheckFailed(fmt.Errorf("no Code Assist project ID"))
	}

	quotaRes, err := g.post(ctx, "retrieveUserQuota", map[string]any{"project": projectID}, creds.AccessToken)
	if err != nil {
		return CheckFailed(err)
	}
	return classifyGeminiQuota(quotaRes)
}

func (g *GeminiChecker) post(ctx context.Context, method string, payload map[string]any, token string) (map[string]any, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	url := g.baseURL + ":" + method
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(data)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("gemini %s: HTTP %d", method, resp.StatusCode)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func classifyGeminiQuota(data map[string]any) Status {
	buckets, _ := data["buckets"].([]any)
	for _, b := range buckets {
		bucket, _ := b.(map[string]any)
		if bucket == nil {
			continue
		}
		model, _ := bucket["modelId"].(string)
		frac := floatFromAny(bucket["remainingFraction"])
		if frac <= 0 {
			label := model
			if label == "" {
				label = "model"
			}
			resetAt := parseResetTime(bucket["resetTime"])
			return Blocked("quota-exhausted",
				fmt.Sprintf("%s exhausted", label), resetAt)
		}
		remaining, hasRemaining := intFromAny(bucket["remainingAmount"])
		if hasRemaining && remaining <= 0 {
			label := model
			if label == "" {
				label = "daily"
			}
			return Blocked("daily-limit",
				fmt.Sprintf("%s daily limit exhausted", label),
				parseResetTime(bucket["resetTime"]))
		}
	}
	return Available(geminiSummary(buckets))
}

func geminiSummary(buckets []any) string {
	var best string
	var bestUsed float64 = -1
	for _, b := range buckets {
		bucket, _ := b.(map[string]any)
		if bucket == nil {
			continue
		}
		model, _ := bucket["modelId"].(string)
		frac := floatFromAny(bucket["remainingFraction"])
		used := (1 - frac) * 100
		if used > bestUsed {
			bestUsed = used
			best = fmt.Sprintf("%s %.0f%% used", model, used)
		}
	}
	if best == "" {
		return "OK"
	}
	return best
}

func intFromAny(v any) (int, bool) {
	switch n := v.(type) {
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}
