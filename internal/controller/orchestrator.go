package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

type OrchestratorStore interface {
	GetJob(context.Context, string) (domain.Job, error)
}

type OrchestratorController struct {
	store                 OrchestratorStore
	communication         *CommunicationController
	questionAuthorization *JobQuestionAuthorizationController
	questionTurn          *JobQuestionTurnController
	questionBudget        domain.PlanningBudget
}

func NewOrchestratorController(
	orchestratorStore OrchestratorStore,
	communication *CommunicationController,
	questionAuthorization *JobQuestionAuthorizationController,
	questionTurn *JobQuestionTurnController,
	questionBudget domain.PlanningBudget,
) (*OrchestratorController, error) {
	if orchestratorStore == nil || communication == nil || questionAuthorization == nil || questionTurn == nil {
		return nil, errors.New("OrchestratorStore, CommunicationController, JobQuestionAuthorizationController, and JobQuestionTurnController are required")
	}
	if questionBudget.MaxTurns == 0 || questionBudget.MaxTokens == 0 {
		return nil, errors.New("Orchestrator question budget must reserve at least one turn and token")
	}
	return &OrchestratorController{
		store: orchestratorStore, communication: communication,
		questionAuthorization: questionAuthorization, questionTurn: questionTurn,
		questionBudget: questionBudget,
	}, nil
}

// ProcessPlannerInbox routes persisted Coder / Reviewer questions to the same
// Taskless Planner execution and returns the answer to each original sender.
func (c *OrchestratorController) ProcessPlannerInbox(
	ctx context.Context,
	jobID, plannerRoleExecutionID string,
) ([]JobQuestionTurnOutcome, error) {
	if ctx == nil || jobID == "" || plannerRoleExecutionID == "" {
		return nil, errors.New("Planner inbox processing requires Job and RoleExecution identities")
	}
	inbox, err := c.communication.InboxForRole(ctx, jobID, plannerRoleExecutionID)
	if err != nil {
		return nil, err
	}
	outcomes := make([]JobQuestionTurnOutcome, 0, len(inbox))
	for _, question := range inbox {
		if question.Sender.Kind != domain.ActorCoder && question.Sender.Kind != domain.ActorReviewer {
			continue
		}
		job, err := c.store.GetJob(ctx, jobID)
		if err != nil {
			return outcomes, err
		}
		if err := job.Validate(); err != nil {
			return outcomes, fmt.Errorf("validate Job before routing Planner question: %w", err)
		}
		grant, found := questionAuthorizationForMessage(job, question.ID)
		if !found {
			deadline, err := questionAuthorizationDeadline(job, plannerRoleExecutionID)
			if err != nil {
				return outcomes, err
			}
			_, grant, err = c.questionAuthorization.Authorize(
				ctx, jobID, question.ID, orchestratorQuestionAuthorizationCommandID(question.ID),
				c.questionBudget, deadline,
			)
			if err != nil {
				return outcomes, fmt.Errorf("authorize Planner question %s: %w", question.ID, err)
			}
		}
		commandID := orchestratorQuestionTurnCommandID(grant.ID)
		reservedTokens := grant.Budget.MaxTokens
		if intent, exists := latestQuestionTurnIntentForAuthorization(job, grant.ID); exists {
			commandID = intent.CommandID
			reservedTokens = intent.ReservedTokens
		}
		outcome, err := c.questionTurn.Send(ctx, jobID, grant.ID, commandID, reservedTokens)
		outcomes = append(outcomes, outcome)
		if err != nil {
			return outcomes, fmt.Errorf("process Planner question %s: %w", question.ID, err)
		}
	}
	return outcomes, nil
}

func questionAuthorizationForMessage(
	job domain.Job,
	questionMessageID string,
) (domain.JobQuestionAuthorization, bool) {
	var latest domain.JobQuestionAuthorization
	found := false
	for _, grant := range job.JobQuestionAuthorizations {
		if grant.QuestionMessageID == questionMessageID &&
			(!found || grant.CreatedRevision > latest.CreatedRevision) {
			latest, found = grant, true
		}
	}
	return latest, found
}

func latestQuestionTurnIntentForAuthorization(
	job domain.Job,
	authorizationID string,
) (domain.JobQuestionTurnIntent, bool) {
	var latest domain.JobQuestionTurnIntent
	found := false
	for _, intent := range job.JobQuestionTurnIntents {
		if intent.JobQuestionAuthorizationID == authorizationID &&
			(!found || intent.CreatedRevision > latest.CreatedRevision) {
			latest, found = intent, true
		}
	}
	return latest, found
}

func questionAuthorizationDeadline(job domain.Job, plannerRoleExecutionID string) (time.Time, error) {
	if len(job.JobContractRevisions) == 0 {
		return time.Time{}, errors.New("Planner question requires an approved JobContract")
	}
	planner, found := findRoleExecution(job, plannerRoleExecutionID)
	if !found || planner.Role != domain.RolePlanner || planner.TaskID != "" {
		return time.Time{}, errors.New("Planner question recipient is not a Taskless Planner RoleExecution")
	}
	grantFound := false
	var planningGrant domain.PlanningAuthorization
	for _, candidate := range job.PlanningAuthorizations {
		if candidate.ID == planner.PlanningAuthorizationID {
			planningGrant, grantFound = candidate, true
			break
		}
	}
	if !grantFound {
		return time.Time{}, errors.New("Planner question has no PlanningAuthorization")
	}
	var start domain.ProviderStartIntent
	startFound := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == planner.ID {
			start, startFound = candidate, true
			break
		}
	}
	if !startFound {
		return time.Time{}, errors.New("Planner question has no ProviderStartIntent")
	}
	deadline := job.JobContractRevisions[len(job.JobContractRevisions)-1].Contract.Deadline
	if planningGrant.Deadline.Before(deadline) {
		deadline = planningGrant.Deadline
	}
	if start.Deadline.Before(deadline) {
		deadline = start.Deadline
	}
	return deadline.UTC().Round(0), nil
}

func orchestratorQuestionAuthorizationCommandID(questionMessageID string) string {
	return "cmd_" + sha256String("taxion:orchestrator:question-authorization:v1:" + questionMessageID)[:32]
}

func orchestratorQuestionTurnCommandID(authorizationID string) string {
	return "cmd_" + sha256String("taxion:orchestrator:question-turn:v1:" + authorizationID)[:32]
}
