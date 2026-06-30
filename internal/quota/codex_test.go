package quota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCodexChecker_SpendControlReached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"spend_control": map[string]any{"reached": true},
			"email":         "user@example.com",
			"account_id":    "acct-secret",
		})
	}))
	defer srv.Close()

	c := NewCodexChecker(srv.URL, fakeCodexCreds)
	st := c.Check(context.Background())
	if !st.IsBlocked() {
		t.Fatal("expected blocked when spend_control.reached")
	}
}

func TestCodexChecker_RateLimitReachedType(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rate_limit_reached_type": "primary",
		})
	}))
	defer srv.Close()

	c := NewCodexChecker(srv.URL, fakeCodexCreds)
	st := c.Check(context.Background())
	if !st.IsBlocked() {
		t.Fatal("expected blocked when rate_limit_reached_type set")
	}
}

func TestCodexChecker_NullRateLimitBusinessPlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"rate_limit": nil,
			"plan_type":  "business",
		})
	}))
	defer srv.Close()

	c := NewCodexChecker(srv.URL, fakeCodexCreds)
	st := c.Check(context.Background())
	if st.IsBlocked() {
		t.Fatalf("rate_limit:null should not block, got %+v", st)
	}
}

func TestCodexChecker_401CheckFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := NewCodexChecker(srv.URL, fakeCodexCreds)
	st := c.Check(context.Background())
	if st.Kind != KindCheckFailed {
		t.Fatalf("expected CheckFailed on 401, got %v", st.Kind)
	}
}

func TestRedactCodexSnapshot(t *testing.T) {
	entry := RedactCodexSnapshot(map[string]any{
		"email":      "alice@example.com",
		"plan_type":  "team",
		"rate_limit": map[string]any{"primary_window": map[string]any{"used_percent": 8}},
	})
	if entry.Email == "alice@example.com" {
		t.Fatal("email should be redacted")
	}
	if entry.PlanType != "team" {
		t.Fatalf("plan_type=%q", entry.PlanType)
	}
}

func fakeCodexCreds() (any, error) {
	return &CodexCredentials{AccessToken: "tok", AccountID: "acct"}, nil
}
