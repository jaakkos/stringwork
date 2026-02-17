package app

import (
	"sync"
	"time"
)

// SessionRegistry tracks connected MCP client sessions and their associated
// agent names. Multiple sessions can be active (SSE and Streamable HTTP).
type SessionRegistry struct {
	mu           sync.RWMutex
	sessions     map[string]string    // sessionID → agentName
	agents       map[string]string    // agentName → sessionID (reverse lookup)
	lastActivity map[string]time.Time // sessionID → last activity timestamp
}

// NewSessionRegistry creates an empty registry.
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{
		sessions:     make(map[string]string),
		agents:       make(map[string]string),
		lastActivity: make(map[string]time.Time),
	}
}

// SetAgent associates a session with an agent name.
// If the agent was previously bound to a different session, the old mapping is removed.
func (r *SessionRegistry) SetAgent(sessionID, agent string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Remove old session for this agent (if any).
	if oldSID, ok := r.agents[agent]; ok && oldSID != sessionID {
		delete(r.sessions, oldSID)
		delete(r.lastActivity, oldSID)
	}
	r.sessions[sessionID] = agent
	r.agents[agent] = sessionID
	r.lastActivity[sessionID] = time.Now()
}

// GetAgent returns the agent name for a session, or "" if unknown.
func (r *SessionRegistry) GetAgent(sessionID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[sessionID]
}

// GetSessionForAgent returns the session ID bound to an agent, or "" if none.
func (r *SessionRegistry) GetSessionForAgent(agent string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.agents[agent]
}

// HasActiveSession returns true if the agent has a connected session.
func (r *SessionRegistry) HasActiveSession(agent string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.agents[agent]
	return ok
}

// ConnectedAgents returns the list of currently connected agent names.
func (r *SessionRegistry) ConnectedAgents() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agents := make([]string, 0, len(r.agents))
	for a := range r.agents {
		agents = append(agents, a)
	}
	return agents
}

// TouchSession records activity for a session (call on each tool invocation).
func (r *SessionRegistry) TouchSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[sessionID]; ok {
		r.lastActivity[sessionID] = time.Now()
	}
}

// LastActivityForAgent returns the last activity time for an agent's session.
// Returns zero time if the agent has no session.
func (r *SessionRegistry) LastActivityForAgent(agent string) time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sid, ok := r.agents[agent]
	if !ok {
		return time.Time{}
	}
	return r.lastActivity[sid]
}

// RemoveSession unregisters a session (e.g. on disconnect).
func (r *SessionRegistry) RemoveSession(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	agent, ok := r.sessions[sessionID]
	if ok {
		delete(r.agents, agent)
	}
	delete(r.sessions, sessionID)
	delete(r.lastActivity, sessionID)
}

// AgentCount returns the number of connected agents.
func (r *SessionRegistry) AgentCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}

// BackdateActivity sets the last activity time for a session to a specific time.
// This is primarily for testing watchdog stale-session detection.
func (r *SessionRegistry) BackdateActivity(sessionID string, t time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sessions[sessionID]; ok {
		r.lastActivity[sessionID] = t
	}
}
