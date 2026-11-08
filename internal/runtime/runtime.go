// Package runtime defines the SDK-neutral execution contract.
//
// Adapter events use neutral types so provider-specific SDK contracts do
// not become governance-core dependencies (ADR-0003).
package runtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/tools"
)

// Event is a plain, SDK-neutral runtime event.
type Event struct {
	Type      string
	Payload   string
	Sequence  uint64 `json:"sequence,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	TurnID    string `json:"turnId,omitempty"`
	AgentID   string `json:"agentId,omitempty"`
}

const (
	EventInput           = "input"
	EventOutput          = "output"
	EventOutputDelta     = "output_delta"
	EventTurnStarted     = "turn_started"
	EventTurnEnded       = "turn_ended"
	EventTurnFinished    = "turn_finished"
	EventFailed          = "failed"
	EventCancelled       = "cancelled"
	EventUsage           = "usage"
	EventUsageCheckpoint = "usage_checkpoint"
	EventContextUsage    = "context_usage"
	EventCompaction      = "compaction"
	EventPermission      = "permission"
	EventExecution       = "execution_observation"
	EventUnclassified    = "unclassified_native_event"
)

// QuestionEventAllowed is shared by live and saved advice-only turns.
func QuestionEventAllowed(eventType string) bool {
	switch eventType {
	case EventInput, EventOutput, EventOutputDelta, EventTurnStarted, EventTurnEnded,
		EventTurnFinished, EventUsage, EventUsageCheckpoint, EventContextUsage, "acp_connection", "acp_capabilities":
		return true
	default:
		return false
	}
}

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
	// Send may return before completion; ctx must remain live for the turn.
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
// Authentication, repository access and provider service state do not establish
// AI contract eligibility or the contractual payer.
type ConfigurationIdentity interface {
	Identity
	ExecutionConfiguration() json.RawMessage
	CheckAuthentication(ctx context.Context, expectedAccount string) error
	CheckRepositoryAccess(ctx context.Context) error
	CheckServiceAccess(ctx context.Context, sessionID string) error
}

// ProviderName defaults legacy adapters to their existing Copilot data gate.
// Alternative runtimes must identify their own outgoing-data permission.
func ProviderName(worker Identity) string {
	if provider, ok := worker.(interface{ Provider() string }); ok {
		return provider.Provider()
	}
	return "copilot"
}

type EventCursor interface {
	EventCursor(sessionID string) (uint64, error)
	EventsAfter(ctx context.Context, sessionID string, cursor uint64) (<-chan Event, error)
}

type ToolPolicyIdentity interface {
	Identity
	ToolPolicy() *tools.Policy
}

// StopAttachment lets providers attach without loading executable extensions.
type StopAttachment interface {
	ResumeForStop(context.Context, string) error
}

// SavedTurn is observed from the bound provider session, never supplied as a
// caller-authored result. Reading it must not resume pending execution.
type SavedTurn struct {
	SessionID   string               `json:"sessionId"`
	InputHash   string               `json:"inputHash"`
	Input       string               `json:"-"`
	MessageID   string               `json:"messageId"`
	TurnID      string               `json:"turnId"`
	Payload     string               `json:"payload"`
	SubmittedAt time.Time            `json:"submittedAt"`
	FinishedAt  time.Time            `json:"finishedAt"`
	ShutdownAt  time.Time            `json:"shutdownAt"`
	HistoryHash string               `json:"historyHash"`
	Events      []Event              `json:"events"`
	UsageTotals []core.DispatchUsage `json:"usageTotals,omitempty"`
}

// ManagedCommit binds the host operation, not an agent-selected repository or scanner.
type ManagedCommit struct {
	DispatchID string
	Scanner    string
	Commit     func(ctx context.Context, sessionID, message string) (string, error)        `json:"-"`
	Stage      func(ctx context.Context, sessionID string, paths []string) (string, error) `json:"-"`
}

type ManagedSearch struct {
	TaskID    string                                                 `json:"taskId"`
	ScopeHash string                                                 `json:"scopeHash"`
	Query     string                                                 `json:"query"`
	Search    func(context.Context, string) (json.RawMessage, error) `json:"-"`
}

type ManagedReviewProgress struct {
	Basis core.ReviewProgress                                                `json:"basis"`
	Save  func(context.Context, string, core.ReviewProgress) (string, error) `json:"-"`
}

type QuotaSnapshot struct {
	Provider            string     `json:"provider"`
	Account             string     `json:"account"`
	Resource            string     `json:"resource"`
	Scope               string     `json:"scope"`
	Unit                string     `json:"unit"`
	Source              string     `json:"source"`
	State               string     `json:"state"`
	Reason              string     `json:"reason,omitempty"`
	ObservedAt          time.Time  `json:"observedAt"`
	ExpiresAt           *time.Time `json:"expiresAt,omitempty"`
	ResetAt             *time.Time `json:"resetAt,omitempty"`
	Entitlement         *int64     `json:"entitlement,omitempty"`
	Used                *int64     `json:"used,omitempty"`
	Remaining           *int64     `json:"remaining,omitempty"`
	RemainingPercentage *float64   `json:"remainingPercentage,omitempty"`
	Unlimited           bool       `json:"unlimited,omitempty"`
	ExecutionDenied     bool       `json:"executionDenied,omitempty"`
	DenialReason        string     `json:"denialReason,omitempty"`
}

// AccountQuota reads only the selected account, without a model turn.
type AccountQuota interface {
	ReadAccountQuota(context.Context, string) (QuotaSnapshot, error)
}

type BudgetGuidanceIdentity interface {
	BudgetGuidancePolicy() *core.BudgetGuidancePolicy
}
