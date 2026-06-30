package quota

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClaudeChecker_SpendCriticalBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"spend": map[string]any{"enabled": true, "severity": "critical", "percent": 100},
		})
	}))
	defer srv.Close()

	c := NewClaudeChecker(srv.URL, fakeClaudeCreds)
	st := c.Check(context.Background())
	if !st.IsBlocked() {
		t.Fatalf("expected blocked, got %+v", st)
	}
}

func TestClaudeChecker_SpendDisabledStalePercentAvailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"spend": map[string]any{"enabled": false, "percent": 100},
		})
	}))
	defer srv.Close()

	c := NewClaudeChecker(srv.URL, fakeClaudeCreds)
	st := c.Check(context.Background())
	if st.IsBlocked() {
		t.Fatalf("expected available when spend disabled, got %+v", st)
	}
}

func TestClaudeChecker_ExtraUsageGatedOnEnabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"extra_usage": map[string]any{"is_enabled": true, "utilization": 100},
		})
	}))
	defer srv.Close()

	c := NewClaudeChecker(srv.URL, fakeClaudeCreds)
	st := c.Check(context.Background())
	if !st.IsBlocked() {
		t.Fatal("expected blocked when extra_usage enabled at 100%")
	}
}

func TestClaudeChecker_NoCredentials(t *testing.T) {
	c := NewClaudeChecker("http://example", func() (any, error) {
		return nil, errNoCreds
	})
	st := c.Check(context.Background())
	if st.Kind != KindNoCredentials {
		t.Fatalf("expected NoCredentials, got %v", st.Kind)
	}
}

var errNoCreds = errors.New("no creds")

func fakeClaudeCreds() (any, error) {
	return &ClaudeCredentials{AccessToken: "test-token"}, nil
}
