package quota

import "context"

// Checker performs a zero-token HTTP quota check for one worker agent type.
type Checker interface {
	AgentType() string
	Check(ctx context.Context) Status
}
