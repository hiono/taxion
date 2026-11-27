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
	QuestionTurns   []JobQuestionTurnOutcome
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

// SendRoleMessage persists a natural-language message and routes it to the recipient Role.
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
	case domain.ActorPlanner:
		outcome.QuestionTurns, err = c.ProcessPlannerInbox(ctx, request.JobID, request.Recipient.ID)
		contexts := make(map[domain.ActorRef]RoleExecutionContext)
		order := make([]domain.ActorRef, 0, len(outcome.QuestionTurns)+1)
		ordered := make(map[domain.ActorRef]bool)
		var restoreErrors []error
		for _, turn := range outcome.QuestionTurns {
			reply := turn.Reply.Message
			if reply.ID == "" {
				continue
			}
			recipient := reply.Recipient
			if _, restored := contexts[recipient]; restored {
				continue
			}
			if !ordered[recipient] {
				order = append(order, recipient)
				ordered[recipient] = true
			}
			resumed, contextErr := c.communication.ContextForMessage(ctx, request.JobID, reply.ID)
			if contextErr != nil {
				restoreErrors = append(restoreErrors, fmt.Errorf("restore original Role context after Planner reply: %w", contextErr))
				if ctx.Err() != nil {
					break
				}
				continue
			}
			contexts[recipient] = resumed
		}

		replyContextRestored := false
		for _, resumed := range contexts {
			for _, reply := range resumed.Messages {
				if reply.ReplyToMessageID == message.Message.ID &&
					reply.Sender == (domain.ActorRef{Kind: domain.ActorPlanner, ID: request.Recipient.ID}) &&
					reply.Recipient == message.Message.Sender {
					replyContextRestored = true
					break
				}
			}
			if replyContextRestored {
				break
			}
		}
		if !replyContextRestored && ctx.Err() == nil {
			resumed, found, contextErr := c.contextForPlannerReply(ctx, request.JobID, message.Message.ID)
			if contextErr != nil {
				restoreErrors = append(restoreErrors, contextErr)
			} else if found {
				actorKind, actorErr := actorKindForRole(resumed.RoleExecution.Role)
				if actorErr != nil {
					restoreErrors = append(restoreErrors, actorErr)
				} else {
					actor := domain.ActorRef{Kind: actorKind, ID: resumed.RoleExecution.ID}
					if !ordered[actor] {
						order = append(order, actor)
						ordered[actor] = true
					}
					// The fallback reads the latest Job; replace any older context for this Actor.
					contexts[actor] = resumed
				}
			}
		}
		for _, actor := range order {
			if resumed, found := contexts[actor]; found {
				outcome.ResumedContexts = append(outcome.ResumedContexts, resumed)
			}
		}
		if err != nil {
			restoreErrors = append(restoreErrors, err)
		}
		if ctx.Err() != nil {
			restoreErrors = append(restoreErrors, ctx.Err())
		}
		return outcome, errors.Join(restoreErrors...)
	case domain.ActorCoder, domain.ActorReviewer:
		resumed, contextErr := c.communication.ContextForMessage(ctx, request.JobID, message.Message.ID)
		if contextErr != nil {
			return outcome, contextErr
		}
		outcome.ResumedContexts = append(outcome.ResumedContexts, resumed)
	}
	return outcome, nil
}

func (c *OrchestratorController) contextForPlannerReply(
	ctx context.Context,
	jobID, questionMessageID string,
) (RoleExecutionContext, bool, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return RoleExecutionContext{}, false, err
	}
	for _, message := range job.JobMessages {
		if message.ReplyToMessageID != questionMessageID ||
			message.Sender.Kind != domain.ActorPlanner {
			continue
		}
		resumed, err := c.communication.ContextForMessage(ctx, jobID, message.ID)
		return resumed, err == nil, err
	}
	return RoleExecutionContext{}, false, nil
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
	var processingErrors []error
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
		if questionHasPlannerReply(job, question) && !questionHasTurnIntent(job, question.ID) {
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
		reservedTokens := grant.Budget.MaxTokens
		if intent, exists := latestQuestionTurnIntentForAuthorization(job, grant.ID); exists {
			commandID = intent.CommandID
			reservedTokens = intent.ReservedTokens
		}
		if intent, exists := latestQuestionTurnIntentForAuthorization(job, grant.ID); exists {
			if observation, observed := latestJobQuestionTurnObservation(job, intent.ID); observed &&
				observation.Status == domain.JobQuestionTurnFailed {
				continue
			}
		}
		outcome, err := c.questionTurn.Send(ctx, jobID, grant.ID, commandID, reservedTokens)
		outcomes = append(outcomes, outcome)
		if err != nil {
			if ctx.Err() != nil {
				return outcomes, ctx.Err()
			}
			processingErrors = append(processingErrors, fmt.Errorf("process Planner question %s: %w", question.ID, err))
			if outcome.Observation.Status == domain.JobQuestionTurnUnknown {
				break
			}
			continue
		}
	}
	return outcomes, errors.Join(processingErrors...)
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

func questionHasTurnIntent(job domain.Job, questionMessageID string) bool {
	for _, intent := range job.JobQuestionTurnIntents {
		if intent.QuestionMessageID == questionMessageID {
			return true
		}
	}
	return false
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
