package quota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGeminiChecker_RemainingFractionZeroBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1internal:retrieveUserQuota" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"buckets": []any{
					map[string]any{"modelId": "flash", "remainingFraction": 0},
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"cloudaicompanionProject": "proj-1",
		})
	}))
	defer srv.Close()

	c := NewGeminiChecker(srv.URL+"/v1internal", fakeGeminiCreds)
	st := c.check(context.Background())
	if !st.IsBlocked() {
		t.Fatalf("expected blocked, got %+v", st)
	}
}

func TestGeminiChecker_NoCredentialsFailOpen(t *testing.T) {
	c := NewGeminiChecker("http://example", func() (any, error) {
		return nil, errNoCreds
	})
	st := c.Check(context.Background())
	if st.IsBlocked() || st.Kind == KindCheckFailed {
		t.Fatalf("gemini must fail-open, got %+v", st)
	}
}

func TestGeminiChecker_ErrorFailOpen(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewGeminiChecker(srv.URL+"/v1internal", fakeGeminiCreds)
	st := c.Check(context.Background())
	if st.IsBlocked() {
		t.Fatal("any gemini error must fail-open")
	}
}

func fakeGeminiCreds() (any, error) {
	return &GeminiCredentials{AccessToken: "gem-tok"}, nil
}
