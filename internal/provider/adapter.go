// Package provider defines the trusted boundary between Taxion Controllers and external Providers.
package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
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

type StartRequest struct {
	OperationID     string
	JobID           string
	RoleExecutionID string
	RequestSHA256   string
	Prompt          string
	Context         []ContextDocument
	MaxTurns        uint32
	MaxTokens       uint64
	Deadline        time.Time
}

type StartResult struct {
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

type TurnResult struct {
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

type StopResult struct {
	Stopped        bool
	EvidenceSHA256 string
}

type ResumeRequest struct {
	OperationID string
	SessionID   string
}

type ResumeResult struct {
	Attached       bool
	EvidenceSHA256 string
}

type LookupRequest struct {
	OperationID string
	SessionID   string
	TurnID      string
}

type LookupResult struct {
	Found          bool
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

type Result struct {
	Content        string
	ResultSHA256   string
	EvidenceSHA256 string
}

type UsageRequest struct {
	OperationID string
	SessionID   string
	TurnID      string
}

type Usage struct {
	UsageID        string
	Tokens         uint64
	EvidenceSHA256 string
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
