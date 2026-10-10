// Package runtime defines the SDK-neutral execution contract.
//
// Adapter events use neutral types so provider-specific SDK contracts do
// not become governance-core dependencies (ADR-0003).
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
//	Resume  — validate a prior session handle (adapters need not restart it)
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
