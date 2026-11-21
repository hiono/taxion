package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
)

type ProviderExecutionStore interface {
	PlanningLifecycleStore
	StartTasklessPlanner(context.Context, string, uint64, string, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.RoleExecution, domain.ProviderStartIntent, error)
}

type PlanningProviderRuntime interface {
	Identity() provider.Identity
	Capabilities(context.Context) (provider.Capabilities, error)
	Start(context.Context, provider.StartRequest) (provider.StartResult, error)
	Lookup(context.Context, provider.LookupRequest) (provider.LookupResult, error)
}

type ProviderController struct {
	store    ProviderExecutionStore
	runtime  PlanningProviderRuntime
	planning *PlanningController
	now      func() time.Time
}

func NewProviderController(lifecycleStore ProviderExecutionStore, runtime PlanningProviderRuntime) (*ProviderController, error) {
	if lifecycleStore == nil || runtime == nil {
		return nil, errors.New("ProviderExecutionStore and Provider Runtime are required")
	}
	planning, err := NewPlanningController(lifecycleStore)
	if err != nil {
		return nil, err
	}
	return &ProviderController{
		store: lifecycleStore, runtime: runtime, planning: planning,
		now: func() time.Time { return time.Now().UTC().Round(0) },
	}, nil
}

// StartPlanning starts the DRAFT planning Provider operation. JobApproval-backed
// execution is intentionally unavailable until the JobApproval domain contract exists.
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
		MaxTurns: grant.Budget.MaxTurns, MaxTokens: grant.Budget.MaxTokens,
		Deadline: grant.Deadline,
	}
	if err := request.Validate(); err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
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

	if receipt.Revision != initialRevision+1 {
		return c.reconcileStart(ctx, receipt, execution, intent, request, currentGrant, principal)
	}
	if err := currentGrant.ValidateFor(current, principal.PrincipalID(), c.now()); err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	result, err := c.runtime.Start(ctx, request)
	if err != nil {
		if providerOperationMayHaveStarted(err) {
			if persistErr := c.recordStartObservation(ctx, jobID, intent.ID, domain.ProviderStartUnknown, "", "", "", false, principal); persistErr != nil {
				return receipt, execution, intent, provider.StartResult{}, errors.Join(err, fmt.Errorf("persist UNKNOWN Provider Start observation: %w", persistErr))
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
	} else if !canRetry {
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
	result, err := c.runtime.Start(ctx, request)
	if err != nil {
		if persistErr := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartUnknown, "", "", "", false, principal); persistErr != nil {
			return receipt, execution, intent, provider.StartResult{}, errors.Join(err, fmt.Errorf("persist UNKNOWN Provider Start observation: %w", persistErr))
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

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func providerOperationMayHaveStarted(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, provider.ErrUnsupportedCapability) ||
		errors.Is(err, provider.ErrCredentialUnavailable) ||
		errors.Is(err, provider.ErrProviderInvalidRequest) {
		return false
	}
	var operationError *provider.OperationError
	return errors.As(err, &operationError)
}

func startResultFromObservation(observation domain.ProviderStartObservation) provider.StartResult {
	return provider.StartResult{
		OperationID: observation.OperationID, SessionID: observation.ProviderSessionID,
		ReceiptID: observation.ReceiptID, EvidenceSHA256: observation.EvidenceSHA256,
	}
}
