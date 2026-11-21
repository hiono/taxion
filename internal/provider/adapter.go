// Package provider defines the trusted boundary between Taxion Controllers and external Providers.
package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

type Identity struct {
	ProviderID          string
	AccountID           string
	SDKVersion          string
	ConfigurationSHA256 string
}

func (i Identity) Validate() error {
	if strings.TrimSpace(i.ProviderID) == "" || strings.TrimSpace(i.AccountID) == "" ||
		strings.TrimSpace(i.SDKVersion) == "" || !validSHA256(i.ConfigurationSHA256) {
		return errors.New("Provider identity requires provider, account, SDK version, and configuration digest")
	}
	return nil
}

type Capability string

const (
	CapabilityStartIdempotency  Capability = "start.idempotency"
	CapabilityStartLookup       Capability = "start.lookup"
	CapabilitySendIdempotency   Capability = "send.idempotency"
	CapabilitySendLookup        Capability = "send.lookup"
	CapabilitySessionReattach   Capability = "session.cross_process_reattachment"
	CapabilityEventReplay       Capability = "event.replay"
	CapabilityStopProof         Capability = "stop.proof"
	CapabilityFinalOutput       Capability = "result.final_output_retrieval"
	CapabilityUsageIdentity     Capability = "usage.identity"
	CapabilityUsageCompleteness Capability = "usage.completeness"
)

type EvidenceLevel string

const (
	EvidenceSynthetic EvidenceLevel = "E2"
	EvidenceSDK       EvidenceLevel = "E3"
	EvidenceLive      EvidenceLevel = "E4"
	EvidenceDurable   EvidenceLevel = "E5"
)

type CapabilityEvidence struct {
	Capability     Capability
	Identity       Identity
	TestID         string
	EvidenceSHA256 string
	EvidenceLevel  EvidenceLevel
	VerifiedAt     time.Time
}

type Capabilities struct {
	Identity                    Identity
	StartIdempotency            bool
	StartLookup                 bool
	SendIdempotency             bool
	SendLookup                  bool
	CrossProcessSessionReattach bool
	EventReplay                 bool
	StopProof                   bool
	FinalOutputRetrieval        bool
	UsageIdentity               bool
	UsageCompleteness           bool
	Evidence                    []CapabilityEvidence
}

func (c Capabilities) Has(capability Capability) bool {
	switch capability {
	case CapabilityStartIdempotency:
		return c.StartIdempotency
	case CapabilityStartLookup:
		return c.StartLookup
	case CapabilitySendIdempotency:
		return c.SendIdempotency
	case CapabilitySendLookup:
		return c.SendLookup
	case CapabilitySessionReattach:
		return c.CrossProcessSessionReattach
	case CapabilityEventReplay:
		return c.EventReplay
	case CapabilityStopProof:
		return c.StopProof
	case CapabilityFinalOutput:
		return c.FinalOutputRetrieval
	case CapabilityUsageIdentity:
		return c.UsageIdentity
	case CapabilityUsageCompleteness:
		return c.UsageCompleteness
	default:
		return false
	}
}

func (c Capabilities) HasEvidenceAtLeast(capability Capability, minimum EvidenceLevel) bool {
	if !c.Has(capability) {
		return false
	}
	minimumRank := evidenceRank(minimum)
	for _, evidence := range c.Evidence {
		if evidence.Capability == capability && evidenceRank(evidence.EvidenceLevel) >= minimumRank && minimumRank > 0 {
			return true
		}
	}
	return false
}

func (c Capabilities) SupportsSafeStart() bool {
	return c.HasEvidenceAtLeast(CapabilityStartIdempotency, EvidenceLive) ||
		c.HasEvidenceAtLeast(CapabilityStartLookup, EvidenceLive)
}

func (c Capabilities) SupportsSafeSend() bool {
	return c.HasEvidenceAtLeast(CapabilitySendIdempotency, EvidenceLive) ||
		c.HasEvidenceAtLeast(CapabilitySendLookup, EvidenceLive)
}

func (c Capabilities) SupportsCrossProcessReattachment() bool {
	return c.HasEvidenceAtLeast(CapabilitySessionReattach, EvidenceDurable)
}

func (c Capabilities) SupportsEventReplay() bool {
	return c.HasEvidenceAtLeast(CapabilityEventReplay, EvidenceDurable)
}

func (c Capabilities) ValidateForLiveExecution(expected Identity) error {
	if err := c.Validate(expected); err != nil {
		return err
	}
	for _, capability := range allCapabilities {
		if !c.Has(capability) {
			continue
		}
		minimum := EvidenceLive
		if capability == CapabilitySessionReattach || capability == CapabilityEventReplay {
			minimum = EvidenceDurable
		}
		if !c.HasEvidenceAtLeast(capability, minimum) {
			return fmt.Errorf("capability %q does not meet minimum evidence level %s", capability, minimum)
		}
	}
	return nil
}

func evidenceRank(level EvidenceLevel) int {
	switch level {
	case EvidenceSynthetic:
		return 2
	case EvidenceSDK:
		return 3
	case EvidenceLive:
		return 4
	case EvidenceDurable:
		return 5
	default:
		return 0
	}
}

func (c Capabilities) Validate(expected Identity) error {
	if err := c.Identity.Validate(); err != nil {
		return fmt.Errorf("invalid capability identity: %w", err)
	}
	if err := expected.Validate(); err != nil {
		return fmt.Errorf("invalid expected Provider identity: %w", err)
	}
	if c.Identity != expected {
		return errors.New("Provider capability identity does not match the selected Provider configuration")
	}

	seen := make(map[Capability]struct{}, len(c.Evidence))
	for _, evidence := range c.Evidence {
		if !c.Has(evidence.Capability) {
			return fmt.Errorf("capability evidence exists for disabled or unknown capability %q", evidence.Capability)
		}
		if _, exists := seen[evidence.Capability]; exists {
			return fmt.Errorf("duplicate capability evidence for %q", evidence.Capability)
		}
		seen[evidence.Capability] = struct{}{}
		if evidence.Identity != c.Identity || strings.TrimSpace(evidence.TestID) == "" ||
			!validSHA256(evidence.EvidenceSHA256) || evidence.VerifiedAt.IsZero() ||
			(evidenceRank(evidence.EvidenceLevel) == 0) {
			return fmt.Errorf("capability evidence for %q is incomplete or bound to another Provider configuration", evidence.Capability)
		}
	}
	for _, capability := range allCapabilities {
		if c.Has(capability) {
			if _, exists := seen[capability]; !exists {
				return fmt.Errorf("enabled capability %q has no verification evidence", capability)
			}
		}
	}
	return nil
}

var allCapabilities = []Capability{
	CapabilityStartIdempotency, CapabilityStartLookup,
	CapabilitySendIdempotency, CapabilitySendLookup,
	CapabilitySessionReattach, CapabilityEventReplay,
	CapabilityStopProof, CapabilityFinalOutput,
	CapabilityUsageIdentity, CapabilityUsageCompleteness,
}

type ContextDocument struct {
	ID      string
	SHA256  string
	Content string
}

func (d ContextDocument) Validate() error {
	if !validIdentifier(d.ID) || !validSHA256(d.SHA256) || sha256Hex(d.Content) != d.SHA256 {
		return errors.New("Provider context document identity or content digest is invalid")
	}
	return nil
}

type StartRequest struct {
	OperationID     string
	JobID           string
	RoleExecutionID string
	RequestSHA256   string
	PromptSHA256    string
	Prompt          string
	Context         []ContextDocument
	MaxTurns        uint32
	MaxTokens       uint64
	Deadline        time.Time
}

func (r StartRequest) Validate() error {
	if !validIdentifier(r.OperationID) || !validIdentifier(r.JobID) || !validIdentifier(r.RoleExecutionID) ||
		!validSHA256(r.RequestSHA256) || !validSHA256(r.PromptSHA256) ||
		r.Prompt == "" || sha256Hex(r.Prompt) != r.PromptSHA256 ||
		r.MaxTurns == 0 || r.MaxTokens == 0 || r.Deadline.IsZero() {
		return errors.New("Provider Start request identity, prompt, budget, or deadline is invalid")
	}
	seen := make(map[string]struct{}, len(r.Context))
	for _, document := range r.Context {
		if err := document.Validate(); err != nil {
			return err
		}
		if _, exists := seen[document.ID]; exists {
			return errors.New("Provider Start request contains duplicate context document IDs")
		}
		seen[document.ID] = struct{}{}
	}
	return nil
}

type StartResult struct {
	OperationID    string
	SessionID      string
	ReceiptID      string
	EvidenceSHA256 string
}

type TurnRequest struct {
	OperationID  string
	TurnID       string
	SessionID    string
	PromptSHA256 string
	Prompt       string
	MaxTokens    uint64
}

func (r TurnRequest) Validate() error {
	if !validIdentifier(r.OperationID) || !validIdentifier(r.TurnID) || !validIdentifier(r.SessionID) ||
		!validSHA256(r.PromptSHA256) || r.Prompt == "" || sha256Hex(r.Prompt) != r.PromptSHA256 ||
		r.MaxTokens == 0 {
		return errors.New("Provider Turn request identity, prompt digest, or token budget is invalid")
	}
	return nil
}

type TurnResult struct {
	OperationID    string
	TurnID         string
	SessionID      string
	ReceiptID      string
	Status         string
	TokensUsed     uint64
	Output         string
	EvidenceSHA256 string
}

type StopRequest struct {
	OperationID string
	SessionID   string
}

func (r StopRequest) Validate() error {
	if !validIdentifier(r.OperationID) || !validIdentifier(r.SessionID) {
		return errors.New("Provider Stop request identity is invalid")
	}
	return nil
}

type StopResult struct {
	OperationID    string
	SessionID      string
	Stopped        bool
	EvidenceSHA256 string
}

type ResumeRequest struct {
	OperationID string
	SessionID   string
}

func (r ResumeRequest) Validate() error {
	if !validIdentifier(r.OperationID) || !validIdentifier(r.SessionID) {
		return errors.New("Provider Resume request identity is invalid")
	}
	return nil
}

type ResumeResult struct {
	OperationID    string
	SessionID      string
	Attached       bool
	EvidenceSHA256 string
}

type LookupRequest struct {
	OperationID string
	SessionID   string
	TurnID      string
}

func (r LookupRequest) Validate() error {
	if !validIdentifier(r.OperationID) ||
		(r.SessionID != "" && !validIdentifier(r.SessionID)) ||
		(r.TurnID != "" && (!validIdentifier(r.TurnID) || !validIdentifier(r.SessionID))) {
		return errors.New("Provider Lookup request identity is invalid")
	}
	return nil
}

type LookupResult struct {
	Found          bool
	OperationID    string
	Status         string
	ReceiptID      string
	SessionID      string
	TurnID         string
	ResultSHA256   string
	UsageID        string
	EvidenceSHA256 string
}

type ResultRequest struct {
	OperationID string
	SessionID   string
	TurnID      string
}

func (r ResultRequest) Validate() error {
	if !validIdentifier(r.OperationID) || !validIdentifier(r.SessionID) || !validIdentifier(r.TurnID) {
		return errors.New("Provider Result request identity is invalid")
	}
	return nil
}

type Result struct {
	OperationID    string
	SessionID      string
	TurnID         string
	Content        string
	ResultSHA256   string
	EvidenceSHA256 string
}

type UsageRequest struct {
	OperationID string
	SessionID   string
	TurnID      string
}

func (r UsageRequest) Validate() error {
	if !validIdentifier(r.OperationID) || !validIdentifier(r.SessionID) || !validIdentifier(r.TurnID) {
		return errors.New("Provider Usage request identity is invalid")
	}
	return nil
}

type Usage struct {
	OperationID    string
	SessionID      string
	TurnID         string
	UsageID        string
	Tokens         uint64
	EvidenceSHA256 string
}

type ReplayEventsRequest struct {
	SessionID   string
	AfterCursor string
}

func (r ReplayEventsRequest) Validate() error {
	if !validIdentifier(r.SessionID) || (r.AfterCursor != "" && !validIdentifier(r.AfterCursor)) {
		return errors.New("Provider event replay request identity or cursor is invalid")
	}
	return nil
}

type EventKind string

const (
	EventOperationAccepted EventKind = "operation.accepted"
	EventTurnStarted       EventKind = "turn.started"
	EventTurnCompleted     EventKind = "turn.completed"
	EventTurnFailed        EventKind = "turn.failed"
	EventTurnUnknown       EventKind = "turn.unknown"
	EventSessionAttached   EventKind = "session.attached"
	EventSessionDetached   EventKind = "session.detached"
	EventSessionStopped    EventKind = "session.stopped"
	EventUsageReported     EventKind = "usage.reported"
)

type ProviderEvent struct {
	EventID        string
	SessionID      string
	OperationID    string
	TurnID         string
	Kind           EventKind
	PreviousCursor string
	Cursor         string
	ObservedAt     time.Time
	EvidenceSHA256 string
}

type EventPage struct {
	SessionID      string
	FromCursor     string
	NextCursor     string
	Events         []ProviderEvent
	HasMore        bool
	EvidenceSHA256 string
}

func (p EventPage) Validate(request ReplayEventsRequest) error {
	if !validIdentifier(p.SessionID) || p.SessionID != request.SessionID ||
		p.FromCursor != request.AfterCursor || !validSHA256(p.EvidenceSHA256) {
		return errors.New("Provider event page identity, cursor origin, or evidence is invalid")
	}
	expectedCursor := request.AfterCursor
	seen := make(map[string]struct{}, len(p.Events))
	for _, event := range p.Events {
		if !validIdentifier(event.EventID) || !validIdentifier(event.SessionID) || event.SessionID != request.SessionID ||
			!validIdentifier(event.OperationID) || (event.TurnID != "" && !validIdentifier(event.TurnID)) ||
			!validEventKind(event.Kind) || event.PreviousCursor != expectedCursor ||
			!validIdentifier(event.Cursor) || event.Cursor == event.PreviousCursor ||
			event.ObservedAt.IsZero() || !validSHA256(event.EvidenceSHA256) {
			return errors.New("Provider event page contains an invalid or discontinuous event")
		}
		if _, exists := seen[event.EventID]; exists {
			return errors.New("Provider event page contains duplicate event identities")
		}
		seen[event.EventID] = struct{}{}
		expectedCursor = event.Cursor
	}
	if p.NextCursor != expectedCursor {
		return errors.New("Provider event page next cursor does not match its event chain")
	}
	return nil
}

func validEventKind(kind EventKind) bool {
	switch kind {
	case EventOperationAccepted, EventTurnStarted, EventTurnCompleted, EventTurnFailed,
		EventTurnUnknown, EventSessionAttached, EventSessionDetached, EventSessionStopped,
		EventUsageReported:
		return true
	default:
		return false
	}
}

type Adapter interface {
	Identity() Identity
	Capabilities(context.Context) (Capabilities, error)
	Start(context.Context, StartRequest, CredentialLease) (StartResult, error)
	Send(context.Context, TurnRequest, CredentialLease) (TurnResult, error)
	Stop(context.Context, StopRequest, CredentialLease) (StopResult, error)
	Resume(context.Context, ResumeRequest, CredentialLease) (ResumeResult, error)
	Lookup(context.Context, LookupRequest, CredentialLease) (LookupResult, error)
	Result(context.Context, ResultRequest, CredentialLease) (Result, error)
	Usage(context.Context, UsageRequest, CredentialLease) (Usage, error)
	ReplayEvents(context.Context, ReplayEventsRequest, CredentialLease) (EventPage, error)
}

type FailureClass string

const (
	FailureDefinitive FailureClass = "definitive"
	FailureAmbiguous  FailureClass = "ambiguous"
)

// OperationError carries only a constrained code. Raw Provider errors and response bodies
// must remain inside the Adapter and must never be wrapped or copied into this value.
type OperationError struct {
	Class FailureClass
	Code  string
}

func NewOperationError(class FailureClass, code string) (*OperationError, error) {
	if (class != FailureDefinitive && class != FailureAmbiguous) || !validOperationCode(code) {
		return nil, errors.New("Provider operation error requires a valid class and safe code")
	}
	return &OperationError{Class: class, Code: code}, nil
}

func (e *OperationError) Error() string {
	if e == nil || !validOperationCode(e.Code) {
		return "Provider operation failed"
	}
	return "Provider operation failed (" + e.Code + ")"
}

const (
	OperationCodeUnauthorized   = "unauthorized"
	OperationCodeRateLimited    = "rate_limited"
	OperationCodeInvalidRequest = "invalid_request"
	OperationCodeNotFound       = "not_found"
	OperationCodeUnsupported    = "unsupported"
	OperationCodeTimeout        = "timeout"
	OperationCodeTransport      = "transport"
	OperationCodeUnavailable    = "unavailable"
	OperationCodeUnknown        = "unknown"
)

func validOperationCode(code string) bool {
	switch code {
	case OperationCodeUnauthorized, OperationCodeRateLimited, OperationCodeInvalidRequest,
		OperationCodeNotFound, OperationCodeUnsupported, OperationCodeTimeout,
		OperationCodeTransport, OperationCodeUnavailable, OperationCodeUnknown:
		return true
	default:
		return false
	}
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}
