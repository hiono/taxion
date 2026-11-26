package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrUnsupportedCapability        = errors.New("Provider capability is not verified for this configuration")
	ErrCredentialUnavailable        = errors.New("Provider credential is unavailable")
	ErrCredentialLeaseCleanupFailed = errors.New("Provider credential lease cleanup failed")
	ErrProviderOperationFailed      = errors.New("Provider operation failed")
	ErrProviderInvalidResult        = errors.New("Provider returned an invalid result")
	ErrProviderInvalidRequest       = errors.New("Provider request is invalid")
	ErrStopUnconfirmed              = errors.New("Provider stop is not confirmed")
	ErrResumeUnconfirmed            = errors.New("Provider Session reattachment is not confirmed")
)

type Runtime struct {
	adapter         Adapter
	broker          CredentialBroker
	verifier        CapabilityVerifier
	identity        Identity
	minimumEvidence EvidenceLevel
	durableEvidence EvidenceLevel
}

func NewRuntime(adapter Adapter, broker CredentialBroker, verifier CapabilityVerifier) (*Runtime, error) {
	return newRuntimeWithEvidence(adapter, broker, verifier, EvidenceLive, EvidenceDurable)
}

func (r *Runtime) Identity() Identity {
	if r == nil {
		return Identity{}
	}
	return r.identity
}

// newRuntimeWithEvidence is kept package-private so synthetic evidence policies
// cannot be selected by production callers.
func newRuntimeWithEvidence(adapter Adapter, broker CredentialBroker, verifier CapabilityVerifier, minimum, durable EvidenceLevel) (*Runtime, error) {
	if adapter == nil || broker == nil || verifier == nil {
		return nil, errors.New("Provider Adapter, CredentialBroker, and CapabilityVerifier are required")
	}
	if evidenceRank(minimum) == 0 || evidenceRank(durable) < evidenceRank(minimum) {
		return nil, errors.New("Provider Runtime evidence policy is invalid")
	}
	identity := adapter.Identity()
	if err := identity.Validate(); err != nil {
		return nil, fmt.Errorf("invalid Provider Adapter identity: %w", err)
	}
	return &Runtime{
		adapter: adapter, broker: broker, verifier: verifier, identity: identity,
		minimumEvidence: minimum, durableEvidence: durable,
	}, nil
}

func (r *Runtime) Capabilities(ctx context.Context) (Capabilities, error) {
	return r.verifiedCapabilities(ctx)
}

func (r *Runtime) Start(ctx context.Context, request StartRequest) (StartResult, error) {
	if err := request.Validate(); err != nil {
		return StartResult{}, ErrProviderInvalidRequest
	}
	boundedContext, cancel, err := withRequestDeadline(ctx, request.Deadline)
	if err != nil {
		return StartResult{}, err
	}
	defer cancel()
	ctx = boundedContext
	capabilities, err := r.verifiedCapabilities(ctx)
	if err != nil {
		return StartResult{}, err
	}
	if !capabilities.HasEvidenceAtLeast(CapabilityStartIdempotency, r.minimumEvidence) &&
		!capabilities.HasEvidenceAtLeast(CapabilityStartLookup, r.minimumEvidence) {
		return StartResult{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (StartResult, error) {
		result, err := r.adapter.Start(ctx, request, lease)
		if err != nil {
			return StartResult{}, err
		}
		leaked, err := containsCredential(lease, result.OperationID, result.SessionID, result.ReceiptID, result.EvidenceSHA256)
		if err != nil {
			return StartResult{}, err
		}
		if leaked {
			return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
		}
		return result, nil
	})
	if err != nil {
		return StartResult{}, safeAdapterError(err)
	}
	if result.OperationID != request.OperationID || !validIdentifier(result.SessionID) ||
		!validIdentifier(result.ReceiptID) || !validSHA256(result.EvidenceSHA256) {
		return StartResult{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) Send(ctx context.Context, request TurnRequest) (TurnResult, error) {
	if err := request.Validate(); err != nil {
		return TurnResult{}, ErrProviderInvalidRequest
	}
	boundedContext, cancel, err := withRequestDeadline(ctx, request.Deadline)
	if err != nil {
		return TurnResult{}, err
	}
	defer cancel()
	ctx = boundedContext
	capabilities, err := r.verifiedCapabilities(ctx)
	if err != nil {
		return TurnResult{}, err
	}
	if !capabilities.HasEvidenceAtLeast(CapabilityReadOnlyContextEnforcement, r.minimumEvidence) {
		return TurnResult{}, ErrUnsupportedCapability
	}
	if !capabilities.HasEvidenceAtLeast(CapabilitySendIdempotency, r.minimumEvidence) &&
		!capabilities.HasEvidenceAtLeast(CapabilitySendLookup, r.minimumEvidence) {
		return TurnResult{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (TurnResult, error) {
		result, err := r.adapter.Send(ctx, request, lease)
		if err != nil {
			return TurnResult{}, err
		}
		leaked, err := containsCredential(lease, result.OperationID, result.TurnID, result.SessionID,
			result.ReceiptID, result.Output, result.EvidenceSHA256)
		if err != nil {
			return TurnResult{}, err
		}
		if leaked {
			return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
		}
		return result, nil
	})
	if err != nil {
		return TurnResult{}, safeAdapterError(err)
	}
	if result.OperationID != request.OperationID || result.TurnID != request.TurnID ||
		result.SessionID != request.SessionID ||
		(result.Status != "COMPLETED" && result.Status != "FAILED" && result.Status != "UNKNOWN") ||
		result.TokensUsed > request.MaxTokens {
		return TurnResult{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	if result.Status == "UNKNOWN" {
		if result.TokensUsed != 0 || result.Output != "" {
			return TurnResult{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
		}
		return result, nil
	}
	if !validIdentifier(result.ReceiptID) || !validSHA256(result.EvidenceSHA256) {
		return TurnResult{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) Stop(ctx context.Context, request StopRequest) (StopResult, error) {
	if err := request.Validate(); err != nil {
		return StopResult{}, ErrProviderInvalidRequest
	}
	capabilities, err := r.verifiedCapabilities(ctx)
	if err != nil {
		return StopResult{}, err
	}
	if !capabilities.HasEvidenceAtLeast(CapabilityStopProof, r.minimumEvidence) {
		return StopResult{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (StopResult, error) {
		result, err := r.adapter.Stop(ctx, request, lease)
		if err != nil {
			return StopResult{}, err
		}
		leaked, err := containsCredential(lease, result.OperationID, result.SessionID, result.EvidenceSHA256)
		if err != nil {
			return StopResult{}, err
		}
		if leaked {
			return StopResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
		}
		return result, nil
	})
	if err != nil {
		return StopResult{}, safeAdapterError(err)
	}
	if result.OperationID != request.OperationID || result.SessionID != request.SessionID ||
		!result.Stopped || !result.Quiescent || !validSHA256(result.EvidenceSHA256) {
		return StopResult{}, errors.Join(ErrStopUnconfirmed, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) Resume(ctx context.Context, request ResumeRequest) (ResumeResult, error) {
	if err := request.Validate(); err != nil {
		return ResumeResult{}, ErrProviderInvalidRequest
	}
	boundedContext, cancel, err := withRequestDeadline(ctx, request.Deadline)
	if err != nil {
		return ResumeResult{}, err
	}
	defer cancel()
	ctx = boundedContext
	capabilities, err := r.verifiedCapabilities(ctx)
	if err != nil {
		return ResumeResult{}, err
	}
	if !capabilities.HasEvidenceAtLeast(CapabilitySessionReattach, r.durableEvidence) {
		return ResumeResult{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (ResumeResult, error) {
		result, err := r.adapter.Resume(ctx, request, lease)
		if err != nil {
			return ResumeResult{}, err
		}
		leaked, err := containsCredential(lease, result.OperationID, result.SessionID, result.EvidenceSHA256)
		if err != nil {
			return ResumeResult{}, err
		}
		if leaked {
			return ResumeResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
		}
		return result, nil
	})
	if err != nil {
		return ResumeResult{}, safeAdapterError(err)
	}
	if result.OperationID != request.OperationID || result.SessionID != request.SessionID ||
		!result.Attached || !validSHA256(result.EvidenceSHA256) {
		return ResumeResult{}, errors.Join(ErrResumeUnconfirmed, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) Lookup(ctx context.Context, request LookupRequest) (LookupResult, error) {
	if err := request.Validate(); err != nil {
		return LookupResult{}, ErrProviderInvalidRequest
	}
	capabilities, err := r.verifiedCapabilities(ctx)
	if err != nil {
		return LookupResult{}, err
	}
	capability := CapabilityStartLookup
	if request.TurnID != "" {
		capability = CapabilitySendLookup
	}
	if !capabilities.HasEvidenceAtLeast(capability, r.minimumEvidence) {
		return LookupResult{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (LookupResult, error) {
		result, err := r.adapter.Lookup(ctx, request, lease)
		if err != nil {
			return LookupResult{}, err
		}
		leaked, err := containsCredential(lease, result.OperationID, result.Status, result.ReceiptID,
			result.SessionID, result.TurnID, result.ResultSHA256, result.UsageID, result.EvidenceSHA256)
		if err != nil {
			return LookupResult{}, err
		}
		if leaked {
			return LookupResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
		}
		return result, nil
	})
	if err != nil {
		return LookupResult{}, safeAdapterError(err)
	}
	if err := result.Validate(request); err != nil {
		return LookupResult{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) Result(ctx context.Context, request ResultRequest) (Result, error) {
	if err := request.Validate(); err != nil {
		return Result{}, ErrProviderInvalidRequest
	}
	capabilities, err := r.verifiedCapabilities(ctx)
	if err != nil {
		return Result{}, err
	}
	if !capabilities.HasEvidenceAtLeast(CapabilityFinalOutput, r.minimumEvidence) {
		return Result{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (Result, error) {
		result, err := r.adapter.Result(ctx, request, lease)
		if err != nil {
			return Result{}, err
		}
		leaked, err := containsCredential(lease, result.OperationID, result.SessionID, result.TurnID,
			result.Content, result.ResultSHA256, result.EvidenceSHA256)
		if err != nil {
			return Result{}, err
		}
		if leaked {
			return Result{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
		}
		return result, nil
	})
	if err != nil {
		return Result{}, safeAdapterError(err)
	}
	if result.OperationID != request.OperationID || result.SessionID != request.SessionID ||
		result.TurnID != request.TurnID || !result.Final || sha256Hex(result.Content) != result.ResultSHA256 ||
		!validSHA256(result.EvidenceSHA256) {
		return Result{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) Usage(ctx context.Context, request UsageRequest) (Usage, error) {
	if err := request.Validate(); err != nil {
		return Usage{}, ErrProviderInvalidRequest
	}
	capabilities, err := r.verifiedCapabilities(ctx)
	if err != nil {
		return Usage{}, err
	}
	if !capabilities.HasEvidenceAtLeast(CapabilityUsageIdentity, r.minimumEvidence) ||
		!capabilities.HasEvidenceAtLeast(CapabilityUsageCompleteness, r.minimumEvidence) {
		return Usage{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (Usage, error) {
		result, err := r.adapter.Usage(ctx, request, lease)
		if err != nil {
			return Usage{}, err
		}
		leaked, err := containsCredential(lease, result.OperationID, result.SessionID, result.TurnID, result.UsageID, result.EvidenceSHA256)
		if err != nil {
			return Usage{}, err
		}
		if leaked {
			return Usage{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
		}
		return result, nil
	})
	if err != nil {
		return Usage{}, safeAdapterError(err)
	}
	if result.OperationID != request.OperationID || result.SessionID != request.SessionID ||
		result.TurnID != request.TurnID || !validIdentifier(result.UsageID) ||
		!validSHA256(result.EvidenceSHA256) {
		return Usage{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) ReplayEvents(ctx context.Context, request ReplayEventsRequest) (EventPage, error) {
	if err := request.Validate(); err != nil {
		return EventPage{}, ErrProviderInvalidRequest
	}
	capabilities, err := r.verifiedCapabilities(ctx)
	if err != nil {
		return EventPage{}, err
	}
	if !capabilities.HasEvidenceAtLeast(CapabilityEventReplay, r.durableEvidence) {
		return EventPage{}, ErrUnsupportedCapability
	}
	page, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (EventPage, error) {
		page, err := r.adapter.ReplayEvents(ctx, request, lease)
		if err != nil {
			return EventPage{}, err
		}
		values := []string{page.SessionID, page.FromCursor, page.NextCursor, page.EvidenceSHA256}
		for _, event := range page.Events {
			values = append(values, event.EventID, event.SessionID, event.OperationID, event.TurnID,
				string(event.Kind), event.PreviousCursor, event.Cursor, event.EvidenceSHA256)
		}
		leaked, err := containsCredential(lease, values...)
		if err != nil {
			return EventPage{}, err
		}
		if leaked {
			return EventPage{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
		}
		return page, nil
	})
	if err != nil {
		return EventPage{}, safeAdapterError(err)
	}
	if err := page.Validate(request); err != nil {
		return EventPage{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	return page, nil
}

func withRequestDeadline(ctx context.Context, deadline time.Time) (context.Context, context.CancelFunc, error) {
	if ctx == nil || deadline.IsZero() {
		return nil, nil, ErrProviderInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if !time.Now().Before(deadline) {
		return nil, nil, context.DeadlineExceeded
	}
	bounded, cancel := context.WithDeadline(ctx, deadline)
	if err := bounded.Err(); err != nil {
		cancel()
		return nil, nil, err
	}
	return bounded, cancel, nil
}

func (r *Runtime) verifiedCapabilities(ctx context.Context) (Capabilities, error) {
	if ctx == nil {
		return Capabilities{}, ErrProviderInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return Capabilities{}, err
	}
	if r.adapter.Identity() != r.identity {
		return Capabilities{}, ErrUnsupportedCapability
	}
	declaration, err := r.adapter.Capabilities(ctx)
	if err != nil {
		return Capabilities{}, ErrUnsupportedCapability
	}
	verified, err := declaration.Clone().Verified(ctx, r.identity, r.verifier)
	if err != nil {
		return Capabilities{}, ErrUnsupportedCapability
	}
	return verified, nil
}

func containsCredential(lease CredentialLease, values ...string) (bool, error) {
	found := false
	err := lease.WithSecret(func(secret []byte) error {
		if len(secret) == 0 {
			return nil
		}
		for _, value := range values {
			valueBytes := []byte(value)
			matched := bytes.Contains(valueBytes, secret)
			zero(valueBytes)
			if matched {
				found = true
				break
			}
		}
		return nil
	})
	if err != nil {
		return false, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	return found, nil
}

func withCredential[T any](ctx context.Context, broker CredentialBroker, identity Identity, call func(CredentialLease) (T, error)) (result T, err error) {
	if ctx == nil {
		return result, ErrProviderInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	lease, acquireErr := broker.Acquire(ctx, identity)
	if acquireErr != nil || lease == nil {
		if lease != nil {
			if closeErr := closeCredentialLease(lease); closeErr != nil {
				return result, errors.Join(ErrCredentialUnavailable, ErrCredentialLeaseCleanupFailed)
			}
		}
		return result, ErrCredentialUnavailable
	}
	defer func() {
		panicked := recover() != nil
		closeErr := closeCredentialLease(lease)
		if panicked || closeErr != nil {
			var zero T
			result = zero
			err = &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
		}
	}()
	return call(lease)
}

func closeCredentialLease(lease CredentialLease) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("Provider credential lease close failed")
		}
	}()
	return lease.Close()
}

func safeAdapterError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrCredentialUnavailable) {
		if errors.Is(err, ErrCredentialLeaseCleanupFailed) {
			return errors.Join(ErrCredentialUnavailable, ErrCredentialLeaseCleanupFailed)
		}
		return ErrCredentialUnavailable
	}
	var operationError *OperationError
	if errors.As(err, &operationError) && operationError != nil &&
		(operationError.Class == FailureDefinitive || operationError.Class == FailureAmbiguous) &&
		validOperationCode(operationError.Code) {
		return &OperationError{Class: operationError.Class, Code: operationError.Code}
	}
	return &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
}
