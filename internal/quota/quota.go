// Package quota provides zero-token HTTP quota preflight for worker CLIs.
package quota

import "time"

// Kind classifies a quota check result.
type Kind int

const (
	KindAvailable Kind = iota
	KindBlocked
	KindNoCredentials
	KindCheckFailed
)

// Status is the outcome of a quota check for one agent type.
type Status struct {
	Kind    Kind
	Reason  string
	Summary string
	ResetAt time.Time
	Err     error
}

// Available reports the agent type can be spawned (quota not exhausted).
func Available(summary string) Status {
	return Status{Kind: KindAvailable, Summary: summary}
}

// Blocked reports an explicit quota block from the provider API.
func Blocked(reason, summary string, resetAt time.Time) Status {
	return Status{Kind: KindBlocked, Reason: reason, Summary: summary, ResetAt: resetAt}
}

// NoCredentials reports OAuth credentials are unavailable (API-key auth, missing token).
// Spawn path treats this as fail-open.
func NoCredentials() Status {
	return Status{Kind: KindNoCredentials, Summary: "no OAuth credentials"}
}

// CheckFailed reports a network or auth error during the HTTP check.
// Spawn path treats this as fail-open.
func CheckFailed(err error) Status {
	return Status{Kind: KindCheckFailed, Err: err, Summary: "check failed"}
}

// IsBlocked is true only for an explicit provider block signal.
func (s Status) IsBlocked() bool {
	return s.Kind == KindBlocked
}

// FailOpen is true when spawn should proceed despite the check outcome.
func (s Status) FailOpen() bool {
	switch s.Kind {
	case KindBlocked:
		return false
	default:
		return true
	}
}
