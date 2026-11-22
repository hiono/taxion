package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type PlannerProposalOutcome struct {
	ResultReceipt   *store.CommandReceipt
	Result           domain.ProviderResultObservation
	ResultReused     bool
	ProposalReceipt  store.CommandReceipt
	Revision         domain.JobContractRevision
}

type PlannerWorkflowController struct {
	provider *ProviderController
}

func NewPlannerWorkflowController(providerController *ProviderController) (*PlannerWorkflowController, error) {
	if providerController == nil || providerController.store == nil || providerController.planning == nil {
		return nil, errors.New("ProviderController with planning lifecycle is required")
	}
	return &PlannerWorkflowController{provider: providerController}, nil
}

// ProposeJobContract sequences terminal Turn / complete Usage verification,
// final Result persistence, and the provenance-bound JobContract proposal.
func (c *PlannerWorkflowController) ProposeJobContract(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, turnID, commandID string,
	principal auth.AuthenticatedPrincipal,
) (PlannerProposalOutcome, error) {
	var outcome PlannerProposalOutcome
	if ctx == nil || jobID == "" || roleExecutionID == "" || sessionID == "" || turnID == "" || commandID == "" {
		return outcome, errors.New("Planner proposal requires context and complete Job / execution / Session / Turn / Command identities")
	}
	if err := principal.Validate(); err != nil {
		return outcome, fmt.Errorf("validate Planner HUMAN Principal: %w", err)
	}

	job, execution, _, operationID, err := c.provider.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, turnID, principal,
	)
	if err != nil {
		return outcome, err
	}
	if execution.Role != domain.RolePlanner || execution.TaskID != "" {
		return outcome, errors.New("JobContract proposal requires a Taskless PLANNER RoleExecution")
	}

	var turn domain.PlanningTurnIntent
	foundTurn := false
	for _, candidate := range job.PlanningTurnIntents {
		if candidate.ID == turnID {
			turn, foundTurn = candidate, true
			break
		}
	}
	if !foundTurn || turn.RoleExecutionID != execution.ID || turn.OperationID != operationID ||
		turn.ProviderSessionID != sessionID {
		return outcome, errors.New("Planner proposal Turn does not match the accepted Provider operation")
	}
	if err := turn.Validate(job); err != nil {
		return outcome, fmt.Errorf("validate Planner Turn intent: %w", err)
	}

	var latestTurnObservation domain.PlanningTurnObservation
	foundTurnObservation := false
	for _, candidate := range job.PlanningTurnObservations {
		if candidate.TurnID == turn.ID && (!foundTurnObservation || candidate.CreatedRevision > latestTurnObservation.CreatedRevision) {
			latestTurnObservation, foundTurnObservation = candidate, true
		}
	}
	if !foundTurnObservation {
		return outcome, errors.New("Planner proposal requires a persisted terminal Turn observation")
	}
	if err := latestTurnObservation.Validate(job); err != nil {
		return outcome, fmt.Errorf("validate Planner Turn observation: %w", err)
	}
	if latestTurnObservation.Status != domain.PlanningTurnCompleted ||
		latestTurnObservation.RoleExecutionID != execution.ID ||
		latestTurnObservation.OperationID != operationID ||
		latestTurnObservation.ProviderSessionID != sessionID {
		return outcome, errors.New("Planner proposal requires the matching COMPLETED Turn observation")
	}

	var latestUsage domain.ProviderUsageObservation
	foundUsage := false
	for _, candidate := range job.ProviderUsageObservations {
		if candidate.OperationID == operationID && candidate.TurnID == turn.ID &&
			candidate.SessionID == sessionID && candidate.RoleExecutionID == execution.ID &&
			(!foundUsage || candidate.CreatedRevision > latestUsage.CreatedRevision) {
			latestUsage, foundUsage = candidate, true
		}
	}
	if !foundUsage {
		return outcome, errors.New("Planner proposal requires a persisted Usage observation")
	}
	if err := latestUsage.Validate(job); err != nil {
		return outcome, fmt.Errorf("validate Planner Usage observation: %w", err)
	}
	if !latestUsage.Complete {
		return outcome, errors.New("Planner proposal requires Complete=true Usage for the exact Turn")
	}

	for _, candidate := range job.ProviderResultObservations {
		if candidate.RoleExecutionID != execution.ID || candidate.OperationID != operationID ||
			candidate.TurnID != turn.ID || candidate.SessionID != sessionID {
			continue
		}
		if outcome.Result.ID != "" {
			return outcome, errors.New("multiple Provider Result observations exist for the Planner Turn")
		}
		if err := candidate.Validate(job); err != nil {
			return outcome, fmt.Errorf("validate persisted Planner Result: %w", err)
		}
		if !candidate.Final {
			return outcome, errors.New("Planner proposal requires a final Provider Result")
		}
		outcome.Result = candidate
	}
	if outcome.Result.ID == "" {
		receipt, result, retrieveErr := c.provider.RetrieveProviderResult(
			ctx, jobID, roleExecutionID, sessionID, turnID, principal,
		)
		if retrieveErr != nil {
			return outcome, fmt.Errorf("persist final Planner Provider Result: %w", retrieveErr)
		}
		outcome.ResultReceipt = &receipt
		outcome.Result = result
	} else {
		outcome.ResultReused = true
	}

	// Re-read after Result persistence so proposal provenance is derived from the
	// canonical aggregate state, not only from the Provider response.
	current, err := c.provider.store.GetJob(ctx, jobID)
	if err != nil {
		return outcome, fmt.Errorf("read Job after final Planner Result persistence: %w", err)
	}
	persisted := false
	for _, candidate := range current.ProviderResultObservations {
		if candidate.ID != outcome.Result.ID {
			continue
		}
		if err := candidate.Validate(current); err != nil {
			return outcome, fmt.Errorf("validate persisted final Planner Result: %w", err)
		}
		if candidate.RoleExecutionID != execution.ID || candidate.OperationID != operationID ||
			candidate.SessionID != sessionID || candidate.TurnID != turn.ID || !candidate.Final {
			return outcome, errors.New("persisted Planner Result identity differs from the completed Turn")
		}
		outcome.Result = candidate
		persisted = true
		break
	}
	if !persisted {
		return outcome, errors.New("final Planner Result was not found in the JobAggregate after persistence")
	}

	receipt, revision, err := c.provider.planning.ProposeJobContract(
		ctx, jobID, execution.ID, outcome.Result.ID, commandID, principal,
	)
	if err != nil {
		return outcome, fmt.Errorf("persist Planner JobContract proposal: %w", err)
	}
	outcome.ProposalReceipt = receipt
	outcome.Revision = revision
	return outcome, nil
}
