package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
)

type ProviderExecutionStore interface {
	JobContractProposalStore
	CommunicationStore
	AddProviderStartObservation(context.Context, string, uint64, string, string, string, string, string, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderStartObservation, error)
	AddPlanningTurnIntent(context.Context, string, uint64, string, string, string, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningTurnIntent, error)
	AddPlanningTurnObservation(context.Context, string, uint64, string, string, string, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningTurnObservation, error)
	AddPlanningResumeIntent(context.Context, string, uint64, string, string, string, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningResumeIntent, error)
	AddPlanningResumeObservation(context.Context, string, uint64, string, string, string, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningResumeObservation, error)
	StartTasklessPlanner(context.Context, string, uint64, string, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.RoleExecution, domain.ProviderStartIntent, error)
	AddProviderEventObservation(context.Context, string, uint64, string, string, string, string, string, string, string, string, string, time.Time, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderEventObservation, error)
	AddProviderStopIntent(context.Context, string, uint64, string, string, string, domain.ProviderStopReason, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderStopIntent, error)
	AddProviderStopObservation(context.Context, string, uint64, string, string, bool, bool, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderStopObservation, error)
	AddProviderResultObservation(context.Context, string, uint64, string, string, string, string, string, string, bool, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderResultObservation, error)
	AddProviderUsageObservation(context.Context, string, uint64, string, string, string, string, string, uint64, bool, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderUsageObservation, error)
}

type PlanningProviderRuntime interface {
	Identity() provider.Identity
	Capabilities(context.Context) (provider.Capabilities, error)
	Start(context.Context, provider.StartRequest) (provider.StartResult, error)
	StartClaimed(context.Context, provider.StartRequest, func() error) (provider.StartResult, error)
	Send(context.Context, provider.TurnRequest) (provider.TurnResult, error)
	Resume(context.Context, provider.ResumeRequest) (provider.ResumeResult, error)
	Stop(context.Context, provider.StopRequest) (provider.StopResult, error)
	Lookup(context.Context, provider.LookupRequest) (provider.LookupResult, error)
	ReplayEvents(context.Context, provider.ReplayEventsRequest) (provider.EventPage, error)
	Result(context.Context, provider.ResultRequest) (provider.Result, error)
	Usage(context.Context, provider.UsageRequest) (provider.Usage, error)
}

type ProviderController struct {
	store   ProviderExecutionStore
	runtime PlanningProviderRuntime
	now     func() time.Time
}

func NewProviderController(lifecycleStore ProviderExecutionStore, runtime PlanningProviderRuntime) (*ProviderController, error) {
	if lifecycleStore == nil || runtime == nil {
		return nil, errors.New("ProviderExecutionStore and Provider Runtime are required")
	}
	return &ProviderController{
		store: lifecycleStore, runtime: runtime,
		now: func() time.Time { return time.Now().UTC().Round(0) },
	}, nil
}

// StartPlanning starts the DRAFT planning Provider operation.
func (c *ProviderController) StartPlanning(
	ctx context.Context,
	jobID, authorizationID, commandID string,
	contextDocuments []provider.ContextDocument,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.RoleExecution, domain.ProviderStartIntent, provider.StartResult, error) {
	if err := principal.Validate(); err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
	}
	grant, found := planningAuthorization(job, authorizationID)
	if !found {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, errors.New("PlanningAuthorization was not found")
	}
	now := c.now()
	if job.State != domain.JobDraft || grant.PrincipalID != principal.PrincipalID() {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, errors.New("Provider Start requires the authorized HUMAN Principal and a DRAFT Job")
	}
	if err := grant.Validate(job); err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
	}
	existingIntent := false
	for _, existing := range job.ProviderStartIntents {
		if existing.CommandID == commandID && existing.PlanningAuthorizationID == authorizationID {
			existingIntent = true
			break
		}
	}
	if !existingIntent {
		if err := grant.ValidateFor(job, principal.PrincipalID(), now); err != nil {
			return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
		}
	}
	if !providerIdentityMatches(c.runtime.Identity(), grant) {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, errors.New("Provider Runtime identity differs from PlanningAuthorization")
	}
	if err := validatePlanningContext(grant, contextDocuments); err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
	}

	request := provider.StartRequest{
		OperationID: domain.ProviderStartOperationID(commandID),
		JobID:       job.ID, RoleExecutionID: domain.RoleExecutionID(commandID),
		RequestSHA256: grant.RequestSHA256,
		Prompt:        job.Request, PromptSHA256: sha256String(job.Request),
		Context:  append([]provider.ContextDocument(nil), contextDocuments...),
		Deadline: grant.Deadline,
	}
	if err := request.Validate(); err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
	}
	if !existingIntent {
		if err := c.requirePlanningBoundary(ctx); err != nil {
			return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
		}
	}

	initialRevision := job.Revision
	receipt, execution, intent, err := c.store.StartTasklessPlanner(
		ctx, jobID, initialRevision, authorizationID, commandID, principal,
	)
	if err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	current, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	currentGrant, found := planningAuthorization(current, authorizationID)
	if !found {
		return receipt, execution, intent, provider.StartResult{}, errors.New("PlanningAuthorization disappeared after Start intent commit")
	}
	if !providerIdentityMatches(c.runtime.Identity(), currentGrant) {
		return receipt, execution, intent, provider.StartResult{}, errors.New("Provider Runtime identity changed after Start intent commit")
	}
	if err := intent.Validate(current, execution); err != nil {
		return receipt, execution, intent, provider.StartResult{}, fmt.Errorf("validate committed Provider Start intent: %w", err)
	}
	request.OperationID = intent.ID
	request.RoleExecutionID = execution.ID

	if observation, exists := latestStartObservation(current, intent.ID); exists {
		switch observation.Status {
		case domain.ProviderStartAccepted:
			return receipt, execution, intent, startResultFromObservation(observation), nil
		case domain.ProviderStartFailed:
			return receipt, execution, intent, provider.StartResult{}, errors.New("Provider Start was definitively rejected")
		case domain.ProviderStartUnknown:
			return c.reconcileStart(ctx, receipt, execution, intent, request, currentGrant, principal)
		}
	}

	if !intent.ClaimBeforeDispatch && receipt.Revision != initialRevision+1 {
		return c.reconcileStart(ctx, receipt, execution, intent, request, currentGrant, principal)
	}
	if err := currentGrant.ValidateFor(current, principal.PrincipalID(), c.now()); err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	if err := c.requirePlanningBoundary(ctx); err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	claimed := false
	var result provider.StartResult
	if intent.ClaimBeforeDispatch {
		result, err = c.runtime.StartClaimed(ctx, request, func() error {
			job, err := c.store.GetJob(ctx, jobID)
			if err != nil {
				return err
			}
			if _, found := latestStartObservation(job, intent.ID); found {
				return errors.New("Provider Start was already claimed")
			}
			commandID, err := newProviderDispatchCommandID()
			if err != nil {
				return err
			}
			_, _, err = c.store.AddProviderStartObservation(ctx, jobID, job.Revision, intent.ID,
				commandID, domain.ProviderStartUnknown, "", "", "", false, principal)
			claimed = err == nil
			return err
		})
	} else {
		result, err = c.runtime.Start(ctx, request)
	}
	if err != nil {
		if intent.ClaimBeforeDispatch && !claimed {
			return receipt, execution, intent, provider.StartResult{}, err
		}
		if providerOperationMayHaveStarted(err) {
			if persistErr := c.recordStartObservation(ctx, jobID, intent.ID, domain.ProviderStartUnknown, "", "", "", false, principal); persistErr != nil {
				return receipt, execution, intent, provider.StartResult{}, errors.Join(err, fmt.Errorf("persist UNKNOWN Provider Start observation: %w", persistErr))
			}
		} else if evidence, definitive := providerDefinitiveFailureEvidence(intent.ID, err); definitive && !intent.ClaimBeforeDispatch {
			if persistErr := c.recordStartObservation(ctx, jobID, intent.ID, domain.ProviderStartFailed, "", "", evidence, false, principal); persistErr != nil {
				return receipt, execution, intent, provider.StartResult{}, errors.Join(err, fmt.Errorf("persist FAILED Provider Start observation: %w", persistErr))
			}
		}
		return receipt, execution, intent, provider.StartResult{}, err
	}
	if err := c.recordStartObservation(ctx, jobID, intent.ID, domain.ProviderStartAccepted,
		result.SessionID, result.ReceiptID, result.EvidenceSHA256, false, principal); err != nil {
		return receipt, execution, intent, provider.StartResult{}, fmt.Errorf("persist Provider Start observation: %w", err)
	}
	return receipt, execution, intent, result, nil
}

type ProviderEventReplayResult struct {
	Observations []domain.ProviderEventObservation
	NextCursor   string
	HasMore      bool
}

// ReplayProviderEvents appends Provider events to the JobAggregate. Replaying from
// an earlier persisted cursor is allowed; deterministic event commands make it idempotent.
// StopPlanning persists the immutable Stop intent before asking the Provider to stop the Session.
func (c *ProviderController) StopPlanning(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, commandID string,
	reason domain.ProviderStopReason,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.ProviderStopIntent, domain.ProviderStopObservation, error) {
	if ctx == nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, provider.ErrProviderInvalidRequest
	}
	if err := principal.Validate(); err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, err
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, err
	}
	if job.State != domain.JobDraft {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Stop requires a DRAFT Job")
	}
	var execution domain.RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Stop RoleExecution was not found")
	}
	var grant domain.PlanningAuthorization
	foundGrant := false
	for _, candidate := range job.PlanningAuthorizations {
		if candidate.ID == execution.PlanningAuthorizationID {
			grant, foundGrant = candidate, true
			break
		}
	}
	if !foundGrant || grant.PrincipalID != principal.PrincipalID() {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Stop requires the authorized HUMAN Principal")
	}
	if err := grant.Validate(job); err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, err
	}
	if !providerIdentityMatches(c.runtime.Identity(), grant) {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Runtime identity differs from PlanningAuthorization")
	}
	var startIntent domain.ProviderStartIntent
	foundStart := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == execution.ID {
			startIntent, foundStart = candidate, true
			break
		}
	}
	if !foundStart {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Stop has no Start intent")
	}
	if err := startIntent.Validate(job, execution); err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, fmt.Errorf("validate Provider Start intent: %w", err)
	}
	startObservation, foundStartObservation := latestStartObservation(job, startIntent.ID)
	if !foundStartObservation || startObservation.Status != domain.ProviderStartAccepted ||
		startObservation.ProviderSessionID != sessionID {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Stop requires the accepted Start Session")
	}
	existingStopIntent := false
	for _, existing := range job.ProviderStopIntents {
		if existing.CommandID == commandID {
			existingStopIntent = true
			break
		}
	}
	intentReceipt, intent, err := c.store.AddProviderStopIntent(
		ctx, jobID, job.Revision, roleExecutionID, sessionID, commandID, reason, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, err
	}
	currentJob, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return intentReceipt, intent, domain.ProviderStopObservation{}, err
	}
	previous, hasPrevious := latestProviderStopObservation(currentJob, intent.OperationID)
	if hasPrevious && previous.Quiescent {
		return intentReceipt, intent, previous, nil
	}
	if existingStopIntent {
		if hasPrevious {
			return intentReceipt, intent, previous, provider.ErrStopUnconfirmed
		}
		observation, persistErr := c.persistProviderStopObservation(
			ctx, currentJob, intent, principal, false, false, "", false,
		)
		if persistErr != nil {
			return intentReceipt, intent, observation, errors.Join(provider.ErrStopUnconfirmed, persistErr)
		}
		return intentReceipt, intent, observation, provider.ErrStopUnconfirmed
	}
	stopResult, stopErr := c.runtime.Stop(ctx, provider.StopRequest{
		OperationID: intent.OperationID,
		SessionID:   sessionID,
	})
	if stopErr != nil {
		if providerOperationMayHaveStarted(stopErr) {
			if hasPrevious && (previous.Stopped || providerStopObservationUnknown(previous)) {
				return intentReceipt, intent, previous, stopErr
			}
			observation, persistErr := c.persistProviderStopObservation(
				ctx, currentJob, intent, principal, false, false, "", false,
			)
			if persistErr != nil {
				return intentReceipt, intent, observation, errors.Join(stopErr, persistErr)
			}
			return intentReceipt, intent, observation, stopErr
		}
		if evidence, definitive := providerStopDefinitiveFailureEvidence(intent.OperationID, stopErr); definitive {
			if hasPrevious && previous.Stopped {
				return intentReceipt, intent, previous, stopErr
			}
			reconcilesUnknown := hasPrevious && providerStopObservationUnknown(previous)
			observation, persistErr := c.persistProviderStopObservation(
				ctx, currentJob, intent, principal, false, false, evidence, reconcilesUnknown,
			)
			if persistErr != nil {
				return intentReceipt, intent, observation, errors.Join(stopErr, persistErr)
			}
			return intentReceipt, intent, observation, stopErr
		}
		return intentReceipt, intent, previous, stopErr
	}
	if stopResult.OperationID != intent.OperationID || stopResult.SessionID != sessionID ||
		!stopResult.Stopped || !stopResult.Quiescent || !validProviderEvidenceSHA256(stopResult.EvidenceSHA256) {
		stopErr = errors.Join(provider.ErrStopUnconfirmed, &provider.OperationError{
			Class: provider.FailureAmbiguous, Code: provider.OperationCodeUnknown,
		})
		if hasPrevious && (previous.Stopped || providerStopObservationUnknown(previous)) {
			return intentReceipt, intent, previous, stopErr
		}
		observation, persistErr := c.persistProviderStopObservation(
			ctx, currentJob, intent, principal, false, false, "", false,
		)
		if persistErr != nil {
			return intentReceipt, intent, observation, errors.Join(stopErr, persistErr)
		}
		return intentReceipt, intent, observation, stopErr
	}
	reconcilesUnknown := hasPrevious && providerStopObservationUnknown(previous)
	observation, err := c.persistProviderStopObservation(
		ctx, currentJob, intent, principal, true, true, stopResult.EvidenceSHA256, reconcilesUnknown,
	)
	return intentReceipt, intent, observation, err
}

func (c *ProviderController) persistProviderStopObservation(
	ctx context.Context,
	job domain.Job,
	intent domain.ProviderStopIntent,
	principal auth.AuthenticatedPrincipal,
	stopped, quiescent bool,
	evidenceSHA256 string,
	reconcilesUnknown bool,
) (domain.ProviderStopObservation, error) {
	commandID := domain.ProviderStopObservationCommandID(
		intent.OperationID, stopped, quiescent, evidenceSHA256, reconcilesUnknown,
	)
	_, observation, err := c.store.AddProviderStopObservation(
		ctx, job.ID, job.Revision, intent.ID, commandID,
		stopped, quiescent, evidenceSHA256, reconcilesUnknown, principal,
	)
	return observation, err
}

func (c *ProviderController) ReplayProviderEvents(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, afterCursor string,
	principal auth.AuthenticatedPrincipal,
) (ProviderEventReplayResult, error) {
	result := ProviderEventReplayResult{NextCursor: afterCursor}
	if ctx == nil {
		return result, provider.ErrProviderInvalidRequest
	}
	if err := principal.Validate(); err != nil {
		return result, err
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return result, err
	}
	if job.State != domain.JobDraft {
		return result, errors.New("Provider event replay requires a DRAFT Job")
	}
	var execution domain.RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return result, errors.New("Provider event replay RoleExecution was not found")
	}
	grant, found := planningAuthorization(job, execution.PlanningAuthorizationID)
	if !found || grant.PrincipalID != principal.PrincipalID() {
		return result, errors.New("Provider event replay requires the authorized HUMAN Principal")
	}
	if err := grant.Validate(job); err != nil {
		return result, err
	}
	if !providerIdentityMatches(c.runtime.Identity(), grant) {
		return result, errors.New("Provider Runtime identity differs from PlanningAuthorization")
	}
	var startIntent domain.ProviderStartIntent
	foundStart := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == execution.ID {
			startIntent, foundStart = candidate, true
			break
		}
	}
	if !foundStart {
		return result, errors.New("Provider event replay has no Start intent")
	}
	if err := startIntent.Validate(job, execution); err != nil {
		return result, fmt.Errorf("validate Provider Start intent: %w", err)
	}
	startObservation, found := latestStartObservation(job, startIntent.ID)
	if !found || startObservation.Status != domain.ProviderStartAccepted ||
		startObservation.ProviderSessionID != sessionID {
		return result, errors.New("Provider event replay requires the accepted Start Session")
	}
	request := provider.ReplayEventsRequest{SessionID: sessionID, AfterCursor: afterCursor}
	if err := request.Validate(); err != nil {
		return result, provider.ErrProviderInvalidRequest
	}
	cursorKnown := afterCursor == ""
	for _, observation := range job.ProviderEventObservations {
		if observation.SessionID == sessionID && observation.Cursor == afterCursor {
			cursorKnown = true
			break
		}
	}
	if !cursorKnown {
		return result, errors.New("Provider event replay cursor is not part of the persisted Session history")
	}
	page, err := c.runtime.ReplayEvents(ctx, request)
	if err != nil {
		return result, err
	}
	if err := page.Validate(request); err != nil {
		return result, fmt.Errorf("validate Provider event page: %w", err)
	}
	currentJob := job
	for _, event := range page.Events {
		_, observation, err := c.store.AddProviderEventObservation(
			ctx, jobID, currentJob.Revision, roleExecutionID, event.OperationID, event.TurnID,
			sessionID, event.EventID, string(event.Kind), event.PreviousCursor, event.Cursor,
			event.EvidenceSHA256, event.ObservedAt, principal,
		)
		if err != nil {
			return result, fmt.Errorf("persist Provider event observation: %w", err)
		}
		result.Observations = append(result.Observations, observation)
		result.NextCursor = observation.Cursor
		currentJob, err = c.store.GetJob(ctx, jobID)
		if err != nil {
			return result, err
		}
	}
	result.NextCursor = page.NextCursor
	result.HasMore = page.HasMore
	return result, nil
}

func (c *ProviderController) reconcileStart(
	ctx context.Context,
	receipt store.CommandReceipt,
	execution domain.RoleExecution,
	intent domain.ProviderStartIntent,
	request provider.StartRequest,
	grant domain.PlanningAuthorization,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.RoleExecution, domain.ProviderStartIntent, provider.StartResult, error) {
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	canLookup := capabilities.HasEvidenceAtLeast(provider.CapabilityStartLookup, provider.EvidenceLive)
	canRetry := capabilities.HasEvidenceAtLeast(provider.CapabilityStartIdempotency, provider.EvidenceLive)
	if canLookup {
		lookup, lookupErr := c.runtime.Lookup(ctx, provider.LookupRequest{OperationID: intent.ID})
		if lookupErr != nil {
			if persistErr := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartUnknown, "", "", "", false, principal); persistErr != nil {
				return receipt, execution, intent, provider.StartResult{}, errors.Join(lookupErr, fmt.Errorf("persist UNKNOWN Provider Start observation: %w", persistErr))
			}
			return receipt, execution, intent, provider.StartResult{}, lookupErr
		}
		if lookup.Found {
			if lookup.Status == provider.LookupStatusFailed {
				if err := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartFailed, "", lookup.ReceiptID, lookup.EvidenceSHA256, false, principal); err != nil {
					return receipt, execution, intent, provider.StartResult{}, err
				}
				return receipt, execution, intent, provider.StartResult{}, errors.New("Provider Start lookup confirmed failure")
			}
			switch lookup.Status {
			case provider.LookupStatusUnknown, "PENDING", "DETACHED", "STOPPED":
				if err := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartUnknown,
					lookup.SessionID, lookup.ReceiptID, lookup.EvidenceSHA256, false, principal); err != nil {
					return receipt, execution, intent, provider.StartResult{}, err
				}
				return receipt, execution, intent, provider.StartResult{}, errors.New("Provider Start lookup does not confirm an attached Session")
			case "ACCEPTED", "RUNNING", "ATTACHED", "COMPLETED":
				if err := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartAccepted,
					lookup.SessionID, lookup.ReceiptID, lookup.EvidenceSHA256, false, principal); err != nil {
					return receipt, execution, intent, provider.StartResult{}, err
				}
			default:
				if err := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartUnknown,
					lookup.SessionID, lookup.ReceiptID, lookup.EvidenceSHA256, false, principal); err != nil {
					return receipt, execution, intent, provider.StartResult{}, err
				}
				return receipt, execution, intent, provider.StartResult{}, errors.New("Provider Start lookup returned an unrecognized state")
			}
			return receipt, execution, intent, provider.StartResult{
				OperationID: intent.ID, SessionID: lookup.SessionID,
				ReceiptID: lookup.ReceiptID, EvidenceSHA256: lookup.EvidenceSHA256,
			}, nil
		}
	}
	if !canRetry {
		if err := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartUnknown, "", "", "", false, principal); err != nil {
			return receipt, execution, intent, provider.StartResult{}, fmt.Errorf("persist unresolved Provider Start: %w", err)
		}
		return receipt, execution, intent, provider.StartResult{}, provider.ErrUnsupportedCapability
	}

	current, err := c.store.GetJob(ctx, intent.JobID)
	if err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	currentGrant, found := planningAuthorization(current, grant.ID)
	if !found {
		return receipt, execution, intent, provider.StartResult{}, errors.New("PlanningAuthorization was not found during Start recovery")
	}
	if err := currentGrant.ValidateFor(current, principal.PrincipalID(), c.now()); err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	if err := c.requirePlanningBoundary(ctx); err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	result, err := c.runtime.Start(ctx, request)
	if err != nil {
		if providerOperationMayHaveStarted(err) {
			if persistErr := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartUnknown, "", "", "", false, principal); persistErr != nil {
				return receipt, execution, intent, provider.StartResult{}, errors.Join(err, fmt.Errorf("persist UNKNOWN Provider Start observation: %w", persistErr))
			}
		}
		return receipt, execution, intent, provider.StartResult{}, err
	}
	if err := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartAccepted,
		result.SessionID, result.ReceiptID, result.EvidenceSHA256, false, principal); err != nil {
		return receipt, execution, intent, provider.StartResult{}, fmt.Errorf("persist recovered Provider Start observation: %w", err)
	}
	return receipt, execution, intent, result, nil
}

func (c *ProviderController) recordStartObservation(
	ctx context.Context,
	jobID, operationID, status, sessionID, receiptID, evidenceSHA256 string,
	reconcilesUnknown bool,
	principal auth.AuthenticatedPrincipal,
) error {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if previous, found := latestStartObservation(job, operationID); found {
		if previous.Status == status && status == domain.ProviderStartUnknown &&
			previous.ProviderSessionID == sessionID && previous.ReceiptID == receiptID &&
			previous.EvidenceSHA256 == evidenceSHA256 {
			return nil
		}
		if previous.Status == domain.ProviderStartUnknown && status != domain.ProviderStartUnknown {
			reconcilesUnknown = true
		} else if previous.Status != domain.ProviderStartUnknown {
			return errors.New("Provider Start observation is already terminal")
		}
	}
	commandID := domain.ProviderStartObservationCommandID(
		operationID, status, sessionID, receiptID, evidenceSHA256, reconcilesUnknown,
	)
	_, _, err = c.store.AddProviderStartObservation(
		ctx, jobID, job.Revision, operationID, commandID, status,
		sessionID, receiptID, evidenceSHA256, reconcilesUnknown, principal,
	)
	return err
}

func planningAuthorization(job domain.Job, id string) (domain.PlanningAuthorization, bool) {
	for _, grant := range job.PlanningAuthorizations {
		if grant.ID == id {
			return grant, true
		}
	}
	return domain.PlanningAuthorization{}, false
}

func latestStartObservation(job domain.Job, operationID string) (domain.ProviderStartObservation, bool) {
	var latest domain.ProviderStartObservation
	found := false
	for _, observation := range job.ProviderStartObservations {
		if observation.OperationID == operationID {
			latest, found = observation, true
		}
	}
	return latest, found
}

func providerIdentityMatches(identity provider.Identity, grant domain.PlanningAuthorization) bool {
	return identity.ProviderID == grant.ProviderID &&
		identity.AccountID == grant.AccountID &&
		identity.ConfigurationSHA256 == grant.ConfigurationSHA256
}

func (c *ProviderController) requirePlanningBoundary(ctx context.Context) error {
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return err
	}
	if capabilities.Identity != c.runtime.Identity() ||
		!capabilities.HasEvidenceAtLeast(provider.CapabilityReadOnlyContextEnforcement, provider.EvidenceLive) {
		return provider.ErrUnsupportedCapability
	}
	return nil
}

func validatePlanningContext(grant domain.PlanningAuthorization, documents []provider.ContextDocument) error {
	if len(documents) == 0 {
		return errors.New("DRAFT planning requires an immutable read-only context")
	}
	switch grant.ReadOnlyBoundary.Mode {
	case domain.PlanningReadOnlyBoundarySnapshot:
		if len(documents) != 1 || documents[0].ID != grant.ReadOnlyBoundary.Reference ||
			documents[0].SHA256 != grant.ReadOnlyBoundary.SHA256 {
			return errors.New("Provider context snapshot differs from PlanningAuthorization read-only boundary")
		}
		return nil
	case domain.PlanningReadOnlyBoundaryDocuments:
		ordered := append([]provider.ContextDocument(nil), documents...)
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
		var material string
		for i, document := range ordered {
			if i > 0 && ordered[i-1].ID == document.ID {
				return errors.New("Provider context contains duplicate document identity")
			}
			material += document.ID + "\n" + document.SHA256 + "\n"
		}
		if sha256String(material) != grant.ReadOnlyBoundary.SHA256 {
			return errors.New("Provider document set differs from PlanningAuthorization read-only boundary")
		}
		return nil
	default:
		return errors.New("unsupported PlanningAuthorization read-only boundary mode")
	}
}

func validProviderEvidenceSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func latestProviderStopObservation(job domain.Job, operationID string) (domain.ProviderStopObservation, bool) {
	var latest domain.ProviderStopObservation
	found := false
	for _, observation := range job.ProviderStopObservations {
		if observation.OperationID != operationID {
			continue
		}
		if !found || observation.CreatedRevision > latest.CreatedRevision {
			latest, found = observation, true
		}
	}
	return latest, found
}

func providerStopObservationUnknown(observation domain.ProviderStopObservation) bool {
	return !observation.Stopped && !observation.Quiescent && observation.EvidenceSHA256 == ""
}

func providerStopDefinitiveFailureEvidence(operationID string, err error) (string, bool) {
	var operationError *provider.OperationError
	if !errors.As(err, &operationError) || operationError == nil ||
		operationError.Class != provider.FailureDefinitive {
		return "", false
	}
	return sha256String("taxion:provider-stop-definitive-failure:" + operationID + ":" +
		string(operationError.Class) + ":" + operationError.Code), true
}

func providerOperationMayHaveStarted(err error) bool {
	var operationError *provider.OperationError
	return errors.As(err, &operationError) && operationError != nil &&
		operationError.Class == provider.FailureAmbiguous
}

func providerDefinitiveFailureEvidence(operationID string, err error) (string, bool) {
	var operationError *provider.OperationError
	if !errors.As(err, &operationError) || operationError == nil ||
		operationError.Class != provider.FailureDefinitive {
		return "", false
	}
	return sha256String("taxion:provider-start-definitive-failure:" + operationID + ":" + string(operationError.Class) + ":" + operationError.Code), true
}

func startResultFromObservation(observation domain.ProviderStartObservation) provider.StartResult {
	return provider.StartResult{
		OperationID: observation.OperationID, SessionID: observation.ProviderSessionID,
		ReceiptID: observation.ReceiptID, EvidenceSHA256: observation.EvidenceSHA256,
	}
}

func (c *ProviderController) RetrieveProviderResult(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, turnID string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.ProviderResultObservation, error) {
	_, _, _, operationID, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, turnID, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, err
	}
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, err
	}
	if capabilities.Identity != c.runtime.Identity() ||
		!capabilities.HasEvidenceAtLeast(provider.CapabilityFinalOutput, provider.EvidenceLive) {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, provider.ErrUnsupportedCapability
	}
	request := provider.ResultRequest{OperationID: operationID, SessionID: sessionID, TurnID: turnID}
	if err := request.Validate(); err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, provider.ErrProviderInvalidRequest
	}
	result, err := c.runtime.Result(ctx, request)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, err
	}
	if result.OperationID != operationID || result.SessionID != sessionID || result.TurnID != turnID {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, provider.ErrProviderInvalidResult
	}
	current, _, _, currentOperationID, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, turnID, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, err
	}
	if currentOperationID != operationID {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, provider.ErrProviderInvalidResult
	}
	return c.store.AddProviderResultObservation(
		ctx, current.ID, current.Revision, roleExecutionID, operationID, turnID, sessionID,
		result.Content, result.ResultSHA256, result.Final, result.EvidenceSHA256, principal,
	)
}

func (c *ProviderController) RetrieveProviderUsage(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, turnID string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.ProviderUsageObservation, error) {
	_, _, _, operationID, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, turnID, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, err
	}
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, err
	}
	if capabilities.Identity != c.runtime.Identity() ||
		!capabilities.HasEvidenceAtLeast(provider.CapabilityUsageIdentity, provider.EvidenceLive) ||
		!capabilities.HasEvidenceAtLeast(provider.CapabilityUsageCompleteness, provider.EvidenceLive) {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, provider.ErrUnsupportedCapability
	}
	request := provider.UsageRequest{OperationID: operationID, SessionID: sessionID, TurnID: turnID}
	if err := request.Validate(); err != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, provider.ErrProviderInvalidRequest
	}
	result, err := c.runtime.Usage(ctx, request)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, err
	}
	if result.OperationID != operationID || result.SessionID != sessionID || result.TurnID != turnID {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, provider.ErrProviderInvalidResult
	}
	current, _, _, currentOperationID, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, turnID, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, err
	}
	if currentOperationID != operationID {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, provider.ErrProviderInvalidResult
	}
	return c.store.AddProviderUsageObservation(
		ctx, current.ID, current.Revision, roleExecutionID, operationID, turnID, sessionID,
		result.UsageID, result.Tokens, result.Complete, result.EvidenceSHA256, principal,
	)
}

func (c *ProviderController) providerObservationTarget(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, turnID string,
	principal auth.AuthenticatedPrincipal,
) (domain.Job, domain.RoleExecution, domain.PlanningAuthorization, string, error) {
	if ctx == nil {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", provider.ErrProviderInvalidRequest
	}
	if err := principal.Validate(); err != nil {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", err
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", err
	}
	if job.State != domain.JobDraft {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Result / Usage requires a DRAFT Job")
	}
	var execution domain.RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider observation RoleExecution was not found")
	}
	grant, foundGrant := planningAuthorization(job, execution.PlanningAuthorizationID)
	if !foundGrant || grant.PrincipalID != principal.PrincipalID() {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Result / Usage requires the authorized HUMAN Principal")
	}
	if err := grant.Validate(job); err != nil {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", err
	}
	if !providerIdentityMatches(c.runtime.Identity(), grant) {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Runtime identity differs from PlanningAuthorization")
	}
	var startIntent domain.ProviderStartIntent
	foundStart := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == execution.ID {
			startIntent, foundStart = candidate, true
			break
		}
	}
	if !foundStart {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Result / Usage has no Start intent")
	}
	if err := startIntent.Validate(job, execution); err != nil {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", fmt.Errorf("validate Provider Start intent: %w", err)
	}
	startObservation, foundObservation := latestStartObservation(job, startIntent.ID)
	if !foundObservation || startObservation.Status != domain.ProviderStartAccepted ||
		startObservation.ProviderSessionID != sessionID {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Result / Usage requires the accepted Start Session")
	}
	if turnID == "" {
		return job, execution, grant, startIntent.ID, nil
	}
	for _, turn := range job.PlanningTurnIntents {
		if turn.ID != turnID {
			continue
		}
		if err := turn.Validate(job); err != nil {
			return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", err
		}
		if turn.RoleExecutionID != execution.ID || turn.ProviderSessionID != sessionID {
			return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Result / Usage Turn identity mismatch")
		}
		return job, execution, grant, turn.OperationID, nil
	}
	return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Result / Usage Turn intent was not found")
}

func (c *ProviderController) SendPlanning(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, commandID, prompt string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.PlanningTurnIntent, domain.PlanningTurnObservation, provider.TurnResult, error) {
	if ctx == nil || prompt == "" {
		return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, provider.ErrProviderInvalidRequest
	}
	job, execution, grant, _, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, "", principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	existingIntent := false
	for _, existing := range job.PlanningTurnIntents {
		if existing.CommandID == commandID && existing.RoleExecutionID == execution.ID {
			existingIntent = true
			break
		}
	}
	if !existingIntent {
		if err := grant.ValidateFor(job, principal.PrincipalID(), c.now()); err != nil {
			return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
		}
	}
	promptSHA256 := sha256String(prompt)
	request := provider.TurnRequest{
		OperationID: domain.PlanningTurnOperationID(commandID), TurnID: domain.PlanningTurnIntentID(commandID),
		SessionID: sessionID, PromptSHA256: promptSHA256, Prompt: prompt,
		Deadline: grant.Deadline,
	}
	if err := request.Validate(); err != nil {
		return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, provider.ErrProviderInvalidRequest
	}
	if !existingIntent {
		if err := c.requirePlanningBoundary(ctx); err != nil {
			return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
		}
	}
	receipt, turn, err := c.store.AddPlanningTurnIntent(
		ctx, jobID, job.Revision, execution.ID, sessionID, commandID, promptSHA256, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	current, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	for _, candidate := range current.PlanningTurnIntents {
		if candidate.CommandID == commandID {
			turn = candidate
			break
		}
	}
	if err := turn.Validate(current); err != nil {
		return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	request.OperationID, request.TurnID = turn.OperationID, turn.ID
	if previous, found := latestProviderTurnObservation(current, turn.ID); found && previous.Status != domain.PlanningTurnUnknown {
		return receipt, turn, previous, turnResultFromObservation(current, previous), nil
	}
	return c.reconcileTurn(ctx, receipt, current, turn, request, principal)
}

func (c *ProviderController) reconcileTurn(
	ctx context.Context,
	receipt store.CommandReceipt,
	job domain.Job,
	turn domain.PlanningTurnIntent,
	request provider.TurnRequest,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.PlanningTurnIntent, domain.PlanningTurnObservation, provider.TurnResult, error) {
	previous, previousFound := latestProviderTurnObservation(job, turn.ID)
	if previousFound && previous.Status != domain.PlanningTurnUnknown {
		return receipt, turn, previous, turnResultFromObservation(job, previous), nil
	}
	dispatchOwner := false
	if !previousFound {
		if err := c.requirePlanningBoundary(ctx); err != nil {
			return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
		}
		var claimed bool
		var claimErr error
		previous, claimed, claimErr = c.claimPlanningTurnDispatch(ctx, job.ID, turn.ID, principal)
		if claimErr != nil {
			return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, claimErr
		}
		if !claimed {
			if previous.Status != domain.PlanningTurnUnknown {
				current, err := c.store.GetJob(ctx, job.ID)
				if err != nil {
					return receipt, turn, previous, provider.TurnResult{}, err
				}
				return receipt, turn, previous, turnResultFromObservation(current, previous), nil
			}
			previousFound = true
		} else {
			previousFound = true
			dispatchOwner = true
		}
	}
	if previousFound && !dispatchOwner {
		capabilities, err := c.runtime.Capabilities(ctx)
		if err != nil {
			return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
		}
		if capabilities.Identity != c.runtime.Identity() {
			return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, provider.ErrUnsupportedCapability
		}
		canLookup := capabilities.HasEvidenceAtLeast(provider.CapabilitySendLookup, provider.EvidenceLive)
		canRetry := capabilities.HasEvidenceAtLeast(provider.CapabilitySendIdempotency, provider.EvidenceLive)
		if canLookup {
			lookup, lookupErr := c.runtime.Lookup(ctx, provider.LookupRequest{
				OperationID: turn.OperationID, SessionID: turn.ProviderSessionID, TurnID: turn.ID,
			})
			if lookupErr != nil {
				observation, persistErr := c.recordTurnObservation(
					ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, "", principal,
				)
				if persistErr != nil {
					return receipt, turn, observation, provider.TurnResult{}, errors.Join(lookupErr, persistErr)
				}
				return receipt, turn, observation, provider.TurnResult{}, lookupErr
			}
			if lookup.Found {
				if lookup.Status == domain.PlanningTurnCompleted || lookup.Status == domain.PlanningTurnFailed {
					current, err := c.store.GetJob(ctx, job.ID)
					if err != nil {
						return receipt, turn, previous, provider.TurnResult{}, err
					}
					content, found := plannerTurnMessage(current, turn)
					if lookup.Status == domain.PlanningTurnFailed && found {
						return receipt, turn, previous, provider.TurnResult{}, errors.New("Provider lookup failure conflicts with saved Planner response")
					}
					if lookup.Status == domain.PlanningTurnCompleted {
						if !found {
							haveResult := false
							for _, final := range current.ProviderResultObservations {
								if final.OperationID == turn.OperationID && final.TurnID == turn.ID &&
									final.RoleExecutionID == turn.RoleExecutionID && final.SessionID == turn.ProviderSessionID && final.Final {
									content, haveResult = final.Content, true
									break
								}
							}
							if !haveResult {
								_, final, err := c.RetrieveProviderResult(ctx, job.ID, turn.RoleExecutionID, turn.ProviderSessionID, turn.ID, principal)
								if err != nil {
									return receipt, turn, previous, provider.TurnResult{}, fmt.Errorf("recover Planner response: %w", err)
								}
								content = final.Content
							}
						}
						if lookup.ResultSHA256 != "" {
							digest := sha256.Sum256([]byte(content))
							if hex.EncodeToString(digest[:]) != lookup.ResultSHA256 {
								return receipt, turn, previous, provider.TurnResult{}, errors.New("Provider lookup result conflicts with Planner response")
							}
						}
						if !found {
							if err := c.validatePlannerTurnCompletion(ctx, turn, lookup.EvidenceSHA256, principal); err != nil {
								return receipt, turn, previous, provider.TurnResult{}, err
							}
							if err := c.savePlannerTurnMessage(ctx, turn, content); err != nil {
								return receipt, turn, previous, provider.TurnResult{}, err
							}
						}
					}
					observation, err := c.recordTurnObservation(
						ctx, job.ID, turn.ID, lookup.Status, lookup.EvidenceSHA256, principal,
					)
					if err != nil {
						unknown, persistErr := c.recordTurnObservation(
							ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, "", principal,
						)
						if persistErr != nil {
							return receipt, turn, unknown, provider.TurnResult{}, errors.Join(err, persistErr)
						}
						return receipt, turn, unknown, provider.TurnResult{}, err
					}
					current, err = c.store.GetJob(ctx, job.ID)
					if err != nil {
						return receipt, turn, observation, provider.TurnResult{}, err
					}
					return receipt, turn, observation, provider.TurnResult{
						OperationID: turn.OperationID, TurnID: turn.ID, SessionID: turn.ProviderSessionID,
						ReceiptID: lookup.ReceiptID, Status: lookup.Status,
						EvidenceSHA256: lookup.EvidenceSHA256, Output: turnResultFromObservation(current, observation).Output,
					}, nil
				}
				observation, persistErr := c.recordTurnObservation(
					ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, "", principal,
				)
				if persistErr != nil {
					return receipt, turn, observation, provider.TurnResult{}, persistErr
				}
				return receipt, turn, observation, provider.TurnResult{}, errors.New("Provider Turn lookup has not confirmed a terminal result")
			}
		}
		if !canRetry {
			observation, persistErr := c.recordTurnObservation(
				ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, "", principal,
			)
			if persistErr != nil {
				return receipt, turn, observation, provider.TurnResult{}, persistErr
			}
			return receipt, turn, observation, provider.TurnResult{}, provider.ErrUnsupportedCapability
		}
	}
	current, _, grant, _, err := c.providerObservationTarget(
		ctx, job.ID, turn.RoleExecutionID, turn.ProviderSessionID, turn.ID, principal,
	)
	if err != nil {
		return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	if err := grant.ValidateFor(current, principal.PrincipalID(), c.now()); err != nil {
		return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	if !dispatchOwner {
		if err := c.requirePlanningBoundary(ctx); err != nil {
			return receipt, turn, previous, provider.TurnResult{}, err
		}
	}
	result, err := c.runtime.Send(ctx, request)
	if err != nil {
		if providerOperationMayHaveStarted(err) {
			observation, persistErr := c.recordTurnObservation(
				ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, "", principal,
			)
			if persistErr != nil {
				return receipt, turn, observation, provider.TurnResult{}, errors.Join(err, persistErr)
			}
			return receipt, turn, observation, provider.TurnResult{}, err
		}
		return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	if result.OperationID != turn.OperationID || result.TurnID != turn.ID || result.SessionID != turn.ProviderSessionID {
		observation, persistErr := c.recordTurnObservation(
			ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, "", principal,
		)
		if persistErr != nil {
			return receipt, turn, observation, provider.TurnResult{}, errors.Join(provider.ErrProviderInvalidResult, persistErr)
		}
		return receipt, turn, observation, provider.TurnResult{}, provider.ErrProviderInvalidResult
	}
	if result.Status == domain.PlanningTurnUnknown {
		observation, persistErr := c.recordTurnObservation(
			ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, "", principal,
		)
		if persistErr != nil {
			return receipt, turn, observation, provider.TurnResult{}, persistErr
		}
		return receipt, turn, observation, result, errors.New("Provider Turn remains UNKNOWN")
	}
	if result.Status != domain.PlanningTurnCompleted && result.Status != domain.PlanningTurnFailed {
		observation, persistErr := c.recordTurnObservation(
			ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, "", principal,
		)
		if persistErr != nil {
			return receipt, turn, observation, provider.TurnResult{}, persistErr
		}
		return receipt, turn, observation, provider.TurnResult{}, provider.ErrProviderInvalidResult
	}
	if !dispatchOwner {
		current, err := c.store.GetJob(ctx, job.ID)
		if err != nil {
			return receipt, turn, previous, provider.TurnResult{}, err
		}
		if content, found := plannerTurnMessage(current, turn); found &&
			(result.Status != domain.PlanningTurnCompleted || content != result.Output) {
			return receipt, turn, previous, provider.TurnResult{}, errors.New("Provider retry conflicts with saved Planner response")
		}
	}
	if result.Status == domain.PlanningTurnCompleted && strings.TrimSpace(result.Output) != "" {
		if err := c.validatePlannerTurnCompletion(ctx, turn, result.EvidenceSHA256, principal); err != nil {
			return receipt, turn, previous, result, err
		}
		if err := c.savePlannerTurnMessage(ctx, turn, result.Output); err != nil {
			return receipt, turn, previous, result, err
		}
	}
	observation, err := c.recordTurnObservation(
		ctx, job.ID, turn.ID, result.Status, result.EvidenceSHA256, principal,
	)
	if err != nil {
		observation, persistErr := c.recordTurnObservation(
			ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, "", principal,
		)
		if persistErr != nil {
			return receipt, turn, observation, provider.TurnResult{}, errors.Join(err, persistErr)
		}
		return receipt, turn, observation, provider.TurnResult{}, err
	}
	return receipt, turn, observation, result, nil
}

func (c *ProviderController) recordTurnObservation(
	ctx context.Context,
	jobID, turnID, status string,
	evidenceSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (domain.PlanningTurnObservation, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return domain.PlanningTurnObservation{}, err
	}
	previous, found := latestProviderTurnObservation(job, turnID)
	if found {
		if previous.Status == domain.PlanningTurnUnknown {
			if status == domain.PlanningTurnUnknown {
				return previous, nil
			}
		} else if previous.Status == status && previous.EvidenceSHA256 == evidenceSHA256 {
			return previous, nil
		} else {
			return domain.PlanningTurnObservation{}, errors.New("Provider Turn already has a terminal observation")
		}
	}
	reconcilesUnknown := found && previous.Status == domain.PlanningTurnUnknown && status != domain.PlanningTurnUnknown
	commandID := providerTurnObservationCommandID(turnID, status, evidenceSHA256, reconcilesUnknown)
	_, observation, err := c.store.AddPlanningTurnObservation(
		ctx, job.ID, job.Revision, turnID, commandID, status, evidenceSHA256, reconcilesUnknown, principal,
	)
	return observation, err
}

func (c *ProviderController) claimPlanningTurnDispatch(
	ctx context.Context,
	jobID, turnID string,
	principal auth.AuthenticatedPrincipal,
) (domain.PlanningTurnObservation, bool, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return domain.PlanningTurnObservation{}, false, err
	}
	if previous, found := latestProviderTurnObservation(job, turnID); found {
		return previous, false, nil
	}
	commandID, err := newProviderDispatchCommandID()
	if err != nil {
		return domain.PlanningTurnObservation{}, false, err
	}
	_, observation, err := c.store.AddPlanningTurnObservation(
		ctx, job.ID, job.Revision, turnID, commandID, domain.PlanningTurnUnknown, "", false, principal,
	)
	if err != nil {
		return domain.PlanningTurnObservation{}, false, err
	}
	return observation, true, nil
}

func newProviderDispatchCommandID() (string, error) {
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", fmt.Errorf("generate Provider dispatch claim identity: %w", err)
	}
	return "cmd_" + hex.EncodeToString(random[:]), nil
}

func latestProviderTurnObservation(job domain.Job, turnID string) (domain.PlanningTurnObservation, bool) {
	var latest domain.PlanningTurnObservation
	found := false
	for _, observation := range job.PlanningTurnObservations {
		if observation.TurnID == turnID && (!found || observation.CreatedRevision > latest.CreatedRevision) {
			latest, found = observation, true
		}
	}
	return latest, found
}

func providerTurnObservationCommandID(turnID, status, evidenceSHA256 string, reconcilesUnknown bool) string {
	material := fmt.Sprintf("taxion:planning-turn-observation:%s:%s:%s:%t", turnID, status, evidenceSHA256, reconcilesUnknown)
	sum := sha256.Sum256([]byte(material))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func providerTurnMessageCommandID(turnID string) string {
	sum := sha256.Sum256([]byte("taxion:planning-turn-message:" + turnID))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func (c *ProviderController) validatePlannerTurnCompletion(ctx context.Context, turn domain.PlanningTurnIntent, evidence string, principal auth.AuthenticatedPrincipal) error {
	job, err := c.store.GetJob(ctx, turn.JobID)
	if err != nil {
		return err
	}
	latest, found := latestProviderTurnObservation(job, turn.ID)
	if found && latest.Status != domain.PlanningTurnUnknown {
		if latest.Status == domain.PlanningTurnCompleted && latest.EvidenceSHA256 == evidence {
			return nil
		}
		return errors.New("Provider Turn already has a different terminal observation")
	}
	commandID := providerTurnObservationCommandID(turn.ID, domain.PlanningTurnCompleted, evidence, found)
	_, err = domain.NewPlanningTurnObservation(job, turn.ID, principal.PrincipalID(), commandID,
		domain.PlanningTurnCompleted, evidence, found, c.now())
	return err
}

func (c *ProviderController) savePlannerTurnMessage(ctx context.Context, turn domain.PlanningTurnIntent, body string) error {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	communication := CommunicationController{store: c.store}
	_, err := communication.SendFromRole(ctx, RoleMessageRequest{
		JobID: turn.JobID, RoleExecutionID: turn.RoleExecutionID, ProviderSessionID: turn.ProviderSessionID,
		CommandID: providerTurnMessageCommandID(turn.ID),
		Recipient: domain.ActorRef{Kind: domain.ActorOrchestrator, ID: domain.OrchestratorActorID},
		Body:      body,
	})
	if err != nil {
		return fmt.Errorf("save Planner response: %w", err)
	}
	return nil
}

func plannerTurnMessage(job domain.Job, turn domain.PlanningTurnIntent) (string, bool) {
	for _, message := range job.JobMessages {
		if message.CommandID == providerTurnMessageCommandID(turn.ID) &&
			message.Sender == (domain.ActorRef{Kind: domain.ActorPlanner, ID: turn.RoleExecutionID}) &&
			message.Recipient == (domain.ActorRef{Kind: domain.ActorOrchestrator, ID: domain.OrchestratorActorID}) {
			return message.Body, true
		}
	}
	return "", false
}

func turnResultFromObservation(job domain.Job, observation domain.PlanningTurnObservation) provider.TurnResult {
	result := provider.TurnResult{
		OperationID: observation.OperationID, TurnID: observation.TurnID, SessionID: observation.ProviderSessionID,
		Status: observation.Status, EvidenceSHA256: observation.EvidenceSHA256,
	}
	if observation.Status == domain.PlanningTurnCompleted {
		for _, turn := range job.PlanningTurnIntents {
			if turn.ID == observation.TurnID {
				result.Output, _ = plannerTurnMessage(job, turn)
				break
			}
		}
	}
	return result
}

func (c *ProviderController) ResumePlanning(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, commandID string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.PlanningResumeIntent, domain.PlanningResumeObservation, provider.ResumeResult, error) {
	job, execution, grant, _, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, "", principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	var existing *domain.PlanningResumeIntent
	for i := range job.PlanningResumeIntents {
		candidate := &job.PlanningResumeIntents[i]
		if candidate.CommandID == commandID && candidate.RoleExecutionID == execution.ID {
			existing = candidate
			break
		}
	}
	if existing != nil {
		if previous, found := latestProviderResumeObservation(job, existing.ID); found && previous.Status != domain.PlanningResumeUnknown {
			receipt, intent, err := c.store.AddPlanningResumeIntent(
				ctx, job.ID, job.Revision, execution.ID, sessionID, commandID,
				existing.CapabilityEvidenceSHA256, true, principal,
			)
			if err != nil {
				return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
			}
			return receipt, intent, previous, resumeResultFromObservation(previous), nil
		}
	}
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	if capabilities.Identity != c.runtime.Identity() || !capabilities.SupportsCrossProcessReattachment() {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, provider.ErrUnsupportedCapability
	}
	evidenceSHA256 := ""
	if existing != nil {
		evidenceSHA256 = existing.CapabilityEvidenceSHA256
		if !hasCapabilityEvidenceDigest(capabilities, provider.CapabilitySessionReattach, provider.EvidenceDurable, evidenceSHA256) {
			return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, provider.ErrUnsupportedCapability
		}
	} else {
		evidenceSHA256, err = capabilityEvidenceDigest(
			capabilities, provider.CapabilitySessionReattach, provider.EvidenceDurable,
		)
		if err != nil {
			return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
		}
		if err := grant.ValidateFor(job, principal.PrincipalID(), c.now()); err != nil {
			return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
		}
	}
	receipt, intent, err := c.store.AddPlanningResumeIntent(
		ctx, job.ID, job.Revision, execution.ID, sessionID, commandID, evidenceSHA256, true, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	current, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	for _, candidate := range current.PlanningResumeIntents {
		if candidate.CommandID == commandID {
			intent = candidate
			break
		}
	}
	if err := intent.Validate(current); err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	if previous, found := latestProviderResumeObservation(current, intent.ID); found && previous.Status != domain.PlanningResumeUnknown {
		return receipt, intent, previous, resumeResultFromObservation(previous), nil
	}
	request := provider.ResumeRequest{
		OperationID: intent.OperationID, SessionID: intent.ProviderSessionID, Deadline: grant.Deadline,
	}
	if err := request.Validate(); err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, provider.ErrProviderInvalidRequest
	}
	latest, _, currentGrant, _, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, "", principal,
	)
	if err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	if err := currentGrant.ValidateFor(latest, principal.PrincipalID(), c.now()); err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	result, err := c.runtime.Resume(ctx, request)
	if err != nil {
		if providerOperationMayHaveStarted(err) || errors.Is(err, provider.ErrResumeUnconfirmed) {
			observation, persistErr := c.recordResumeObservation(
				ctx, jobID, intent.ID, domain.PlanningResumeUnknown, "", principal,
			)
			if persistErr != nil {
				return receipt, intent, observation, provider.ResumeResult{}, errors.Join(err, persistErr)
			}
			return receipt, intent, observation, provider.ResumeResult{}, err
		}
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	if result.OperationID != intent.OperationID || result.SessionID != intent.ProviderSessionID ||
		!result.Attached || !validProviderSHA256(result.EvidenceSHA256) {
		observation, persistErr := c.recordResumeObservation(
			ctx, jobID, intent.ID, domain.PlanningResumeUnknown, "", principal,
		)
		if persistErr != nil {
			return receipt, intent, observation, provider.ResumeResult{}, errors.Join(provider.ErrResumeUnconfirmed, persistErr)
		}
		return receipt, intent, observation, provider.ResumeResult{}, provider.ErrResumeUnconfirmed
	}
	observation, err := c.recordResumeObservation(
		ctx, jobID, intent.ID, domain.PlanningResumeAttached, result.EvidenceSHA256, principal,
	)
	if err != nil {
		unknown, persistErr := c.recordResumeObservation(
			ctx, jobID, intent.ID, domain.PlanningResumeUnknown, "", principal,
		)
		if persistErr != nil {
			return receipt, intent, unknown, provider.ResumeResult{}, errors.Join(err, persistErr)
		}
		return receipt, intent, unknown, provider.ResumeResult{}, err
	}
	return receipt, intent, observation, result, nil
}

func (c *ProviderController) recordResumeObservation(
	ctx context.Context,
	jobID, resumeID, status, evidenceSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (domain.PlanningResumeObservation, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return domain.PlanningResumeObservation{}, err
	}
	previous, found := latestProviderResumeObservation(job, resumeID)
	if found {
		if previous.Status == domain.PlanningResumeUnknown {
			if status == domain.PlanningResumeUnknown {
				return previous, nil
			}
		} else if previous.Status == status && previous.EvidenceSHA256 == evidenceSHA256 {
			return previous, nil
		} else {
			return domain.PlanningResumeObservation{}, errors.New("Provider Resume already has a terminal observation")
		}
	}
	reconcilesUnknown := found && previous.Status == domain.PlanningResumeUnknown && status != domain.PlanningResumeUnknown
	commandID := providerResumeObservationCommandID(resumeID, status, evidenceSHA256, reconcilesUnknown)
	_, observation, err := c.store.AddPlanningResumeObservation(
		ctx, job.ID, job.Revision, resumeID, commandID, status, evidenceSHA256, reconcilesUnknown, principal,
	)
	return observation, err
}

func latestProviderResumeObservation(job domain.Job, resumeID string) (domain.PlanningResumeObservation, bool) {
	var latest domain.PlanningResumeObservation
	found := false
	for _, observation := range job.PlanningResumeObservations {
		if observation.ResumeID == resumeID && (!found || observation.CreatedRevision > latest.CreatedRevision) {
			latest, found = observation, true
		}
	}
	return latest, found
}

func providerResumeObservationCommandID(resumeID, status, evidenceSHA256 string, reconcilesUnknown bool) string {
	material := fmt.Sprintf("taxion:planning-resume-observation:%s:%s:%s:%t", resumeID, status, evidenceSHA256, reconcilesUnknown)
	sum := sha256.Sum256([]byte(material))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func resumeResultFromObservation(observation domain.PlanningResumeObservation) provider.ResumeResult {
	return provider.ResumeResult{
		OperationID: observation.OperationID, SessionID: observation.ProviderSessionID,
		Attached: observation.Status == domain.PlanningResumeAttached, EvidenceSHA256: observation.EvidenceSHA256,
	}
}

func capabilityEvidenceDigest(capabilities provider.Capabilities, capability provider.Capability, minimum provider.EvidenceLevel) (string, error) {
	if !capabilities.HasEvidenceAtLeast(capability, minimum) {
		return "", provider.ErrUnsupportedCapability
	}
	var digest string
	var verifiedAt time.Time
	for _, evidence := range capabilities.Evidence {
		if evidence.Capability != capability || evidence.Identity != capabilities.Identity ||
			evidence.EvidenceLevel != minimum || evidence.VerifiedAt.IsZero() || !validProviderSHA256(evidence.EvidenceSHA256) {
			continue
		}
		if digest == "" || evidence.VerifiedAt.After(verifiedAt) {
			digest, verifiedAt = evidence.EvidenceSHA256, evidence.VerifiedAt
		}
	}
	if digest == "" {
		return "", provider.ErrUnsupportedCapability
	}
	return digest, nil
}

func hasCapabilityEvidenceDigest(capabilities provider.Capabilities, capability provider.Capability, minimum provider.EvidenceLevel, digest string) bool {
	if !capabilities.HasEvidenceAtLeast(capability, minimum) || !validProviderSHA256(digest) {
		return false
	}
	for _, evidence := range capabilities.Evidence {
		if evidence.Capability == capability && evidence.Identity == capabilities.Identity &&
			evidence.EvidenceLevel == minimum && evidence.EvidenceSHA256 == digest && !evidence.VerifiedAt.IsZero() {
			return true
		}
	}
	return false
}

func validProviderSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
