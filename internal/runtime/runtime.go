// Package runtime defines the SDK-neutral execution contract.
//
// Adapter events use neutral types so provider-specific SDK contracts do
// not become governance-core dependencies (ADR-0003).
package runtime

import (
	"context"
	"encoding/json"
)

// Event is a plain, SDK-neutral runtime event.
type Event struct {
	Type     string
	Payload  string
	Sequence uint64 `json:"sequence,omitempty"`
}

const (
	EventInput        = "input"
	EventOutput       = "output"
	EventOutputDelta  = "output_delta"
	EventTurnStarted  = "turn_started"
	EventTurnEnded    = "turn_ended"
	EventTurnFinished = "turn_finished"
	EventFailed       = "failed"
	EventCancelled    = "cancelled"
	EventUsage        = "usage"
	EventContextUsage = "context_usage"
	EventCompaction   = "compaction"
	EventPermission   = "permission"
)

// Runtime is the minimal control-plane contract:
//
//	Start   — begin isolated execution for a task
//	Send    — send another instruction to the same session
//	Resume  — validate a prior session handle (adapters need not restart it)
//	Stop    — halt execution
//	Events  — stream neutral events
//
// Implementations must isolate Coder and Reviewer contexts (ROLE-001).
type Runtime interface {
	Start(ctx context.Context, taskID string) (sessionID string, err error)
	Send(ctx context.Context, sessionID, prompt string) error
	Resume(ctx context.Context, sessionID string) error
	Stop(ctx context.Context, sessionID string) error
	Events(ctx context.Context, sessionID string) (<-chan Event, error)
}

// Identity exposes the role and configuration boundary used for durable
// Dispatch-to-session binding.
type Identity interface {
	Runtime
	Role() string
	ConfigHash() string
	Worktree() string
}

// ConfigurationIdentity exposes the selected tuple without provider SDK types.
// Runtime authentication and repository access do not establish AI contract eligibility.
type ConfigurationIdentity interface {
	Identity
	ExecutionConfiguration() json.RawMessage
	CheckAuthentication(ctx context.Context, expectedAccount string) error
	CheckRepositoryAccess(ctx context.Context) error
}

type EventCursor interface {
	EventCursor(sessionID string) (uint64, error)
	EventsAfter(ctx context.Context, sessionID string, cursor uint64) (<-chan Event, error)
}

// ManagedCommit binds the host operation, not an agent-selected repository or scanner.
type ManagedCommit struct {
	DispatchID string
	Scanner    string
	Commit     func(ctx context.Context, sessionID, message string) (string, error)        `json:"-"`
	Stage      func(ctx context.Context, sessionID string, paths []string) (string, error) `json:"-"`
}
