package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type PlanningLifecycleStore interface {
	GetJob(context.Context, string) (domain.Job, error)
	AddProviderStartObservation(context.Context, string, uint64, string, string, string, string, string, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderStartObservation, error)
	AddPlanningTurnIntent(context.Context, string, uint64, string, string, string, string, uint64, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningTurnIntent, error)
	AddPlanningTurnObservation(context.Context, string, uint64, string, string, string, uint64, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningTurnObservation, error)
	AddPlanningEffectDecision(context.Context, string, uint64, string, string, string, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningEffectDecision, error)
	AddPlanningResumeIntent(context.Context, string, uint64, string, string, string, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningResumeIntent, error)
	AddPlanningResumeObservation(context.Context, string, uint64, string, string, string, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningResumeObservation, error)
	AddPlanningBoundaryDecision(context.Context, string, uint64, string, string, string, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningBoundaryDecision, error)
}

type PlanningController struct {
	store PlanningLifecycleStore
}

func NewPlanningController(lifecycleStore PlanningLifecycleStore) (*PlanningController, error) {
	if lifecycleStore == nil {
		return nil, errors.New("PlanningLifecycleStore is required")
	}
	return &PlanningController{store: lifecycleStore}, nil
}

func (c *PlanningController) RecordStartObservation(
	ctx context.Context,
	jobID, operationID, commandID, status, providerSessionID, receiptID, evidenceSHA256 string,
	reconcilesUnknown bool,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.ProviderStartObservation, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderStartObservation{}, err
	}
	return c.store.AddProviderStartObservation(
		ctx, jobID, job.Revision, operationID, commandID, status,
		providerSessionID, receiptID, evidenceSHA256, reconcilesUnknown, principal,
	)
}

func (c *PlanningController) BeginTurn(ctx context.Context, jobID, roleExecutionID, providerSessionID, commandID, promptSHA256 string, reservedTokens uint64, principal auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningTurnIntent, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningTurnIntent{}, err
	}
	receipt, turn, err := c.store.AddPlanningTurnIntent(ctx, jobID, job.Revision, roleExecutionID, providerSessionID, commandID, promptSHA256, reservedTokens, principal)
	if err != nil {
		return receipt, turn, c.recordBoundaryDenial(ctx, jobID, roleExecutionID, commandID, err, principal)
	}
	return receipt, turn, nil
}

func (c *PlanningController) RecordTurnObservation(ctx context.Context, jobID, turnID, commandID, status string, tokensUsed uint64, evidenceSHA256 string, reconcilesUnknown bool, principal auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningTurnObservation, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningTurnObservation{}, err
	}
	return c.store.AddPlanningTurnObservation(ctx, jobID, job.Revision, turnID, commandID, status, tokensUsed, evidenceSHA256, reconcilesUnknown, principal)
}

func (c *PlanningController) AuthorizeEffect(ctx context.Context, jobID, roleExecutionID, commandID, effect, contextSHA256 string, principal auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningEffectDecision, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningEffectDecision{}, err
	}
	receipt, decision, err := c.store.AddPlanningEffectDecision(ctx, jobID, job.Revision, roleExecutionID, commandID, effect, contextSHA256, principal)
	if err != nil {
		return receipt, decision, c.recordBoundaryDenial(ctx, jobID, roleExecutionID, commandID, err, principal)
	}
	if decision.Decision != domain.PlanningEffectAllow {
		return receipt, decision, &PlanningEffectDeniedError{Decision: decision}
	}
	return receipt, decision, nil
}

func (c *PlanningController) Resume(ctx context.Context, jobID, roleExecutionID, providerSessionID, commandID, capabilityEvidenceSHA256 string, reattachmentSupported bool, principal auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningResumeIntent, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, err
	}
	receipt, resume, err := c.store.AddPlanningResumeIntent(ctx, jobID, job.Revision, roleExecutionID, providerSessionID, commandID, capabilityEvidenceSHA256, reattachmentSupported, principal)
	if err != nil {
		return receipt, resume, c.recordBoundaryDenial(ctx, jobID, roleExecutionID, commandID, err, principal)
	}
	return receipt, resume, nil
}

func (c *PlanningController) RecordResumeObservation(ctx context.Context, jobID, resumeID, commandID, status, evidenceSHA256 string, reconcilesUnknown bool, principal auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.PlanningResumeObservation, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningResumeObservation{}, err
	}
	return c.store.AddPlanningResumeObservation(ctx, jobID, job.Revision, resumeID, commandID, status, evidenceSHA256, reconcilesUnknown, principal)
}

func (c *PlanningController) recordBoundaryDenial(ctx context.Context, jobID, roleExecutionID, sourceCommandID string, cause error, principal auth.AuthenticatedPrincipal) error {
	var boundaryError *domain.PlanningBoundaryError
	if !errors.As(cause, &boundaryError) {
		return cause
	}
	for attempt := 0; attempt < 2; attempt++ {
		job, err := c.store.GetJob(ctx, jobID)
		if err != nil {
			return errors.Join(cause, fmt.Errorf("read Job to persist boundary denial: %w", err))
		}
		_, _, err = c.store.AddPlanningBoundaryDecision(
			ctx, jobID, job.Revision, roleExecutionID, sourceCommandID,
			boundaryError.Boundary, boundaryError.ReasonCode, principal,
		)
		if errors.Is(err, store.ErrIdempotencyConflict) && attempt == 0 {
			continue
		}
		if err != nil {
			return errors.Join(cause, fmt.Errorf("persist PlanningBoundaryDecision: %w", err))
		}
		return cause
	}
	return cause
}

type PlanningEffectDeniedError struct {
	Decision domain.PlanningEffectDecision
}

func (e *PlanningEffectDeniedError) Error() string {
	return "PlanningAuthorization denied effect: " + e.Decision.Reason
}
