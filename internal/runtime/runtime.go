// Package runtime defines the SDK-neutral execution contract.
//
// The governance core (internal/core) only depends on this interface,
// never on concrete SDK types. Concrete adapters (e.g. Copilot SDK for Go)
// live outside core and are evaluated as plugins (Adapter phase).
package runtime

import "context"

// Event is a plain, SDK-neutral runtime event.
type Event struct {
	Type    string
	Payload string
}

// Runtime is the minimal control-plane contract:
//
//	Start   — begin isolated execution for a task
//	Resume  — resume a prior session
//	Stop    — halt execution
//	Events  — stream neutral events
//
// Implementations must isolate Coder and Reviewer contexts (ROLE-001).
type Runtime interface {
	Start(ctx context.Context, taskID string) (sessionID string, err error)
	Resume(ctx context.Context, sessionID string) error
	Stop(ctx context.Context, sessionID string) error
	Events(ctx context.Context, sessionID string) (<-chan Event, error)
}
