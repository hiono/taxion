package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type JobQuestionAuthorizationStore interface {
	GetJob(context.Context, string) (domain.Job, error)
	AddJobQuestionAuthorization(
		context.Context,
		string,
		uint64,
		domain.JobQuestionAuthorizationRequest,
	) (store.CommandReceipt, domain.JobQuestionAuthorization, error)
}

type QuestionReadOnlyContext struct {
	RepositoryID string
	Boundary     domain.PlanningReadOnlyBoundary
}

type QuestionReadOnlyContextResolver interface {
	ResolveReadOnlyBoundary(
		context.Context,
		domain.Job,
		domain.JobMessage,
		domain.RoleExecution,
	) (QuestionReadOnlyContext, error)
}

type JobQuestionAuthorizationController struct {
	store    JobQuestionAuthorizationStore
	resolver QuestionReadOnlyContextResolver
}

func NewJobQuestionAuthorizationController(
	authorizationStore JobQuestionAuthorizationStore,
	resolver QuestionReadOnlyContextResolver,
) (*JobQuestionAuthorizationController, error) {
	if authorizationStore == nil || resolver == nil {
		return nil, errors.New("JobQuestionAuthorizationStore and trusted QuestionReadOnlyContextResolver are required")
	}
	return &JobQuestionAuthorizationController{
		store: authorizationStore, resolver: resolver,
	}, nil
}

// Authorize issues a bounded grant for one persisted QUESTION after resolving an immutable read-only context.
func (c *JobQuestionAuthorizationController) Authorize(
	ctx context.Context,
	jobID, questionMessageID, commandID string,
	budget domain.PlanningBudget,
	deadline time.Time,
) (store.CommandReceipt, domain.JobQuestionAuthorization, error) {
	if ctx == nil || jobID == "" || questionMessageID == "" || commandID == "" {
		return store.CommandReceipt{}, domain.JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization requires Job, Question, and Command identities")
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.JobQuestionAuthorization{}, err
	}
	if err := job.Validate(); err != nil {
		return store.CommandReceipt{}, domain.JobQuestionAuthorization{}, fmt.Errorf("validate Job before question authorization: %w", err)
	}
	var question domain.JobMessage
	questionFound := false
	for _, candidate := range job.JobMessages {
		if candidate.ID == questionMessageID {
			question, questionFound = candidate, true
			break
		}
	}
	if !questionFound || question.Kind != domain.MessageKindQuestion ||
		question.Recipient.Kind != domain.ActorPlanner {
		return store.CommandReceipt{}, domain.JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization requires a persisted QUESTION addressed to a PLANNER")
	}
	var planner domain.RoleExecution
	plannerFound := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == question.Recipient.ID {
			planner, plannerFound = candidate, true
			break
		}
	}
	if !plannerFound || planner.Role != domain.RolePlanner || planner.TaskID != "" {
		return store.CommandReceipt{}, domain.JobQuestionAuthorization{}, errors.New("QUESTION recipient is not a Taskless PLANNER RoleExecution")
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
		return store.CommandReceipt{}, domain.JobQuestionAuthorization{}, errors.New("Planner Provider Start intent was not found")
	}
	var accepted domain.ProviderStartObservation
	acceptedFound := false
	for _, candidate := range job.ProviderStartObservations {
		if candidate.OperationID == start.ID {
			accepted, acceptedFound = candidate, true
		}
	}
	if !acceptedFound || accepted.Status != domain.ProviderStartAccepted {
		return store.CommandReceipt{}, domain.JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization requires an accepted Planner Provider Session")
	}
	readOnlyContext, err := c.resolver.ResolveReadOnlyBoundary(ctx, job, question, planner)
	if err != nil {
		return store.CommandReceipt{}, domain.JobQuestionAuthorization{}, fmt.Errorf("resolve immutable read-only question context: %w", err)
	}
	planningGrantFound := false
	var planningGrant domain.PlanningAuthorization
	for _, candidate := range job.PlanningAuthorizations {
		if candidate.ID == planner.PlanningAuthorizationID {
			planningGrant, planningGrantFound = candidate, true
			break
		}
	}
	if !planningGrantFound {
		return store.CommandReceipt{}, domain.JobQuestionAuthorization{}, errors.New("Planner PlanningAuthorization was not found")
	}
	request := domain.JobQuestionAuthorizationRequest{
		JobID: job.ID, CommandID: commandID, QuestionMessageID: question.ID,
		PlannerRoleExecutionID: planner.ID, ProviderSessionID: accepted.ProviderSessionID,
		ProviderID:   start.ProviderID, AccountID: start.AccountID,
		ConfigurationSHA256: start.ConfigurationSHA256, RepositoryID: readOnlyContext.RepositoryID,
		PlanningRepositoryID: planningGrant.RepositoryID,
		ReadOnlyBoundary: readOnlyContext.Boundary,
		Budget: budget, Deadline: deadline.UTC().Round(0),
	}
	return c.store.AddJobQuestionAuthorization(ctx, job.ID, job.Revision, request)
}
