package collab

import (
	"github.com/jaakkos/stringwork/internal/app"
	"github.com/jaakkos/stringwork/internal/constitution"
	"github.com/jaakkos/stringwork/internal/domain"
)

// constitutionPreambleForScope resolves the configured constitution
// sources and returns the rendered pointer block to surface to the
// worker. Returns an empty string when no sources are configured or
// no files resolve — callers should treat the empty string as a
// no-op (don't add a separator, don't add a trailing newline).
//
// Partial resolution is preserved: when constitution.Resolve reports
// that some sources failed but others succeeded (the documented
// "earlier files win" semantics include "earlier sources still win
// when later ones break"), the surviving files are still rendered
// into the preamble and the error is logged via svc.Logger so
// `mcp-stringwork constitution doctor` and operator logs both surface
// the broken source. Throwing the good files away on any non-nil
// error — which is what the original implementation did — is the
// concrete regression that nullified the partial-resolve fix in
// Resolve(); we explicitly do not do that here.
//
// IMPORTANT: this performs unbounded filesystem I/O via Resolve.
// Callers that hold the CollabService global lock (i.e. inside a
// Run/Query callback) MUST first capture the Scope from state
// (cheap), return from the callback, and only then call this
// helper. claim_next + get_work_context follow that pattern; the
// API is deliberately scope-only so the foot-gun "build a preamble
// from a *Task pointer while holding the state lock" is no longer
// expressible.
func constitutionPreambleForScope(svc *app.CollabService, scope constitution.Scope) string {
	files, err := constitution.Resolve(svc.Policy().ConstitutionSources(), scope)
	if err != nil {
		if logger := svc.Logger(); logger != nil {
			logger.Printf("constitution: partial resolve failure (using %d surviving file(s)): %v", len(files), err)
		}
	}
	if len(files) == 0 {
		return ""
	}
	return constitution.BuildPreamble(files)
}

// scopeForTask derives a Scope from a task and the agent that will
// see the resolved preamble. agentRole is the parent agent type
// (e.g. "claude-code", "codex"); pass "" when the caller does not
// know it (e.g. an unauthenticated probe). The classifier stays in
// one place across the spawn-time inline path (worker_manager) and
// the MCP-response preamble path (this package) so role-scoped
// sources resolve identically in both places — fixing the original
// drift where worker_manager passed baseCfg.AgentType but the
// claim_next / get_work_context render path silently dropped the
// role.
//
// Cheap and lock-friendly: the only fields read are task.Title (a
// string) and the caller-supplied agentRole, so callers can invoke
// this from inside CollabService Run/Query callbacks without any
// I/O concern.
func scopeForTask(task *domain.Task, agentRole string) constitution.Scope {
	scope := constitution.Scope{AgentRole: agentRole}
	if task != nil {
		scope.TaskKind = constitution.TaskKindFromTitle(task.Title)
	}
	return scope
}
