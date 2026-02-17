package main

// Mock OAuth 2.1 endpoints for MCP HTTP transport.
//
// Claude Code's MCP client always attempts OAuth discovery when connecting
// to HTTP-based MCP servers. If the server has no auth endpoints, the client
// gets a 404 and fails to parse it as JSON, producing an error like:
//
//   Error: HTTP 404: Invalid OAuth error response: SyntaxError: ...
//
// These mock endpoints implement the minimum OAuth 2.1 surface required by the
// MCP specification so that clients complete the auth handshake without any
// real credentials. Every token request is approved, every client registration
// is accepted. This is appropriate for a local-only development tool.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// mockAuthServer holds state for the mock OAuth flow.
type mockAuthServer struct {
	baseURL string
	logger  *log.Logger

	mu    sync.Mutex
	codes map[string]mockAuthCode // authorization codes awaiting exchange
}

type mockAuthCode struct {
	clientID    string
	redirectURI string
	createdAt   time.Time
}

func newMockAuthServer(baseURL string, logger *log.Logger) *mockAuthServer {
	return &mockAuthServer{
		baseURL: baseURL,
		logger:  logger,
		codes:   make(map[string]mockAuthCode),
	}
}

// registerRoutes adds all mock OAuth routes to the given mux.
func (m *mockAuthServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/.well-known/oauth-authorization-server", m.handleMetadata)
	mux.HandleFunc("/register", m.handleRegister)
	mux.HandleFunc("/authorize", m.handleAuthorize)
	mux.HandleFunc("/token", m.handleToken)
}

// handleMetadata returns OAuth 2.0 Authorization Server Metadata (RFC 8414).
func (m *mockAuthServer) handleMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	m.logger.Println("Mock auth: metadata discovery")

	meta := map[string]any{
		"issuer":                                m.baseURL,
		"authorization_endpoint":                m.baseURL + "/authorize",
		"token_endpoint":                        m.baseURL + "/token",
		"registration_endpoint":                 m.baseURL + "/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":       []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meta)
}

// handleRegister implements RFC 7591 Dynamic Client Registration.
// Accepts any registration and returns a client_id.
func (m *mockAuthServer) handleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// Parse the registration request (we don't validate anything).
	var req map[string]any
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request"})
		return
	}

	clientID := "mcp-mock-" + randomHex(8)

	m.logger.Printf("Mock auth: client registered: %s", clientID)

	resp := map[string]any{
		"client_id":                clientID,
		"client_id_issued_at":     time.Now().Unix(),
		"token_endpoint_auth_method": "none",
	}

	// Echo back redirect_uris if provided.
	if uris, ok := req["redirect_uris"]; ok {
		resp["redirect_uris"] = uris
	}
	if name, ok := req["client_name"]; ok {
		resp["client_name"] = name
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(resp)
}

// handleAuthorize implements the authorization endpoint.
// Auto-approves every request and redirects back with an authorization code.
func (m *mockAuthServer) handleAuthorize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	redirectURI := q.Get("redirect_uri")
	state := q.Get("state")
	clientID := q.Get("client_id")

	if redirectURI == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request", "error_description": "redirect_uri required"})
		return
	}

	// Generate authorization code.
	code := randomHex(16)

	m.mu.Lock()
	m.codes[code] = mockAuthCode{
		clientID:    clientID,
		redirectURI: redirectURI,
		createdAt:   time.Now(),
	}
	m.mu.Unlock()

	m.logger.Printf("Mock auth: authorize -> code=%s...%s for client=%s", code[:4], code[len(code)-4:], clientID)

	// Build redirect URL.
	u, err := url.Parse(redirectURI)
	if err != nil {
		http.Error(w, `{"error":"invalid_redirect_uri"}`, http.StatusBadRequest)
		return
	}
	params := u.Query()
	params.Set("code", code)
	if state != "" {
		params.Set("state", state)
	}
	u.RawQuery = params.Encode()

	http.Redirect(w, r, u.String(), http.StatusFound)
}

// handleToken implements the token endpoint.
// Accepts authorization_code and refresh_token grants, always returns a valid token.
func (m *mockAuthServer) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method_not_allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if err := r.ParseForm(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "invalid_request"})
		return
	}

	grantType := r.FormValue("grant_type")

	switch grantType {
	case "authorization_code":
		code := r.FormValue("code")
		if code == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "code required"})
			return
		}

		// Consume the code (one-time use).
		m.mu.Lock()
		_, ok := m.codes[code]
		if ok {
			delete(m.codes, code)
		}
		m.mu.Unlock()

		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "unknown or expired code"})
			return
		}

		m.logger.Println("Mock auth: token issued (authorization_code)")

	case "refresh_token":
		m.logger.Println("Mock auth: token refreshed")

	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error":             "unsupported_grant_type",
			"error_description": fmt.Sprintf("grant_type %q not supported", grantType),
		})
		return
	}

	// Issue a mock token.
	token := map[string]any{
		"access_token":  "mcp-mock-token-" + randomHex(16),
		"token_type":    "Bearer",
		"expires_in":    86400, // 24 hours
		"refresh_token": "mcp-mock-refresh-" + randomHex(16),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(token)
}

// randomHex returns n random bytes as a hex string.
func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
