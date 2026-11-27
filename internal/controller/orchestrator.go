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

type OrchestratorMessageOutcome struct {
	Message         CommunicationOutcome
	ResumedContexts []RoleExecutionContext
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

// SendRoleMessage persists and routes a natural-language message.
// Provider execution is requested separately through ProcessPlannerQuestions.
func (c *OrchestratorController) SendRoleMessage(
	ctx context.Context,
	request RoleMessageRequest,
) (OrchestratorMessageOutcome, error) {
	var outcome OrchestratorMessageOutcome
	message, err := c.communication.SendFromRole(ctx, request)
	if err != nil {
		return outcome, err
	}
	outcome.Message = message

	switch request.Recipient.Kind {
	case domain.ActorCoder, domain.ActorReviewer:
		resumed, contextErr := c.communication.ContextForMessage(ctx, request.JobID, message.Message.ID)
		if contextErr != nil {
			return outcome, contextErr
		}
		outcome.ResumedContexts = append(outcome.ResumedContexts, resumed)
	}
	return outcome, nil
}

// ProcessPlannerQuestions executes only the explicitly selected Planner questions.
// Ordinary JobMessage delivery never starts a Provider operation implicitly.
func (c *OrchestratorController) ProcessPlannerQuestions(
	ctx context.Context,
	jobID, plannerRoleExecutionID string,
	questionMessageIDs ...string,
) ([]JobQuestionTurnOutcome, error) {
	if ctx == nil || jobID == "" || plannerRoleExecutionID == "" || len(questionMessageIDs) == 0 {
		return nil, errors.New("Planner question processing requires Job, Planner, and Question identities")
	}
	outcomes := make([]JobQuestionTurnOutcome, 0, len(questionMessageIDs))
	var processingErrors []error
	sessionBlocked := false
	for _, questionMessageID := range questionMessageIDs {
		job, err := c.store.GetJob(ctx, jobID)
		if err != nil {
			return outcomes, err
		}
		if err := job.Validate(); err != nil {
			return outcomes, fmt.Errorf("validate Job before routing Planner question: %w", err)
		}
		question, found := findJobMessage(job, questionMessageID)
		if !found {
			return outcomes, fmt.Errorf("Planner question %s was not found", questionMessageID)
		}
		if question.Recipient != (domain.ActorRef{Kind: domain.ActorPlanner, ID: plannerRoleExecutionID}) ||
			(question.Sender.Kind != domain.ActorCoder && question.Sender.Kind != domain.ActorReviewer) {
			return outcomes, fmt.Errorf("JobMessage %s is not a Coder / Reviewer question addressed to this Planner", questionMessageID)
		}

		intent, hasIntent := latestQuestionTurnIntentForMessage(job, question.ID)
		if questionHasPlannerReply(job, question) && !hasIntent {
			reply, _ := existingPlannerReply(job, question)
			outcomes = append(outcomes, JobQuestionTurnOutcome{Reply: CommunicationOutcome{Message: reply}})
			continue
		}
		if hasIntent {
			if observation, observed := latestJobQuestionTurnObservation(job, intent.ID); observed &&
				observation.Status == domain.JobQuestionTurnFailed {
				continue
			}
			if sessionBlocked {
				if observation, observed := latestJobQuestionTurnObservation(job, intent.ID); !observed ||
					observation.Status != domain.JobQuestionTurnCompleted {
					continue
				}
			}
			outcome, err := c.questionTurn.Send(
				ctx, jobID, intent.JobQuestionAuthorizationID, intent.CommandID, intent.ReservedTokens,
			)
			outcomes = append(outcomes, outcome)
			if err != nil {
				if ctx.Err() != nil {
					return outcomes, ctx.Err()
				}
				processingErrors = append(processingErrors, fmt.Errorf("process Planner question %s: %w", question.ID, err))
				if outcome.Observation.Status == domain.JobQuestionTurnUnknown {
					sessionBlocked = true
				}
			}
			continue
		}
		if sessionBlocked {
			continue
		}
		if questionHasPlannerReply(job, question) {
			reply, _ := existingPlannerReply(job, question)
			outcomes = append(outcomes, JobQuestionTurnOutcome{Reply: CommunicationOutcome{Message: reply}})
			continue
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
				if ctx.Err() != nil {
					return outcomes, ctx.Err()
				}
				processingErrors = append(processingErrors, fmt.Errorf("authorize Planner question %s: %w", question.ID, err))
				continue
			}
		}
		commandID := orchestratorQuestionTurnCommandID(grant.ID)
		outcome, err := c.questionTurn.Send(ctx, jobID, grant.ID, commandID, grant.Budget.MaxTokens)
		outcomes = append(outcomes, outcome)
		if err != nil {
			if ctx.Err() != nil {
				return outcomes, ctx.Err()
			}
			processingErrors = append(processingErrors, fmt.Errorf("process Planner question %s: %w", question.ID, err))
			if outcome.Observation.Status == domain.JobQuestionTurnUnknown {
				sessionBlocked = true
			}
		}
	}
	return outcomes, errors.Join(processingErrors...)
}

func latestQuestionTurnIntentForMessage(
	job domain.Job,
	questionMessageID string,
) (domain.JobQuestionTurnIntent, bool) {
	var latest domain.JobQuestionTurnIntent
	found := false
	for _, intent := range job.JobQuestionTurnIntents {
		if intent.QuestionMessageID == questionMessageID &&
			(!found || intent.CreatedRevision > latest.CreatedRevision) {
			latest, found = intent, true
		}
	}
	return latest, found
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

func questionHasPlannerReply(job domain.Job, question domain.JobMessage) bool {
	_, found := existingPlannerReply(job, question)
	return found
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
