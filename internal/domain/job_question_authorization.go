package domain

import (
	"errors"
	"fmt"
	"time"
)

type JobQuestionAuthorizationRequest struct {
	JobID                  string                   `json:"job_id"`
	CommandID              string                   `json:"command_id"`
	QuestionMessageID      string                   `json:"question_message_id"`
	PlannerRoleExecutionID string                   `json:"planner_role_execution_id"`
	ProviderSessionID      string                   `json:"provider_session_id"`
	RepositoryID           string                   `json:"repository_id"`
	PlanningRepositoryID   string                   `json:"planning_repository_id"`
	ProviderID             string                   `json:"provider_id"`
	AccountID              string                   `json:"account_id"`
	ConfigurationSHA256    string                   `json:"configuration_sha256"`
	ReadOnlyBoundary       PlanningReadOnlyBoundary `json:"read_only_boundary"`
	Budget                 PlanningBudget           `json:"budget"`
	Deadline               time.Time                `json:"deadline"`
}

type JobQuestionAuthorization struct {
	ID                     string                   `json:"id"`
	CommandID              string                   `json:"command_id"`
	JobID                  string                   `json:"job_id"`
	JobApprovalID          string                   `json:"job_approval_id"`
	ContractRevision       uint64                   `json:"contract_revision"`
	QuestionMessageID      string                   `json:"question_message_id"`
	PlannerRoleExecutionID string                   `json:"planner_role_execution_id"`
	ProviderSessionID      string                   `json:"provider_session_id"`
	RepositoryID           string                   `json:"repository_id"`
	PlanningRepositoryID   string                   `json:"planning_repository_id"`
	ProviderID             string                   `json:"provider_id"`
	AccountID              string                   `json:"account_id"`
	ConfigurationSHA256    string                   `json:"configuration_sha256"`
	ReadOnlyBoundary       PlanningReadOnlyBoundary `json:"read_only_boundary"`
	Budget                 PlanningBudget           `json:"budget"`
	IssuedAt               time.Time                `json:"issued_at"`
	Deadline               time.Time                `json:"deadline"`
	CreatedAt              time.Time                `json:"created_at"`
	CreatedRevision        uint64                   `json:"created_revision"`
}

func JobQuestionAuthorizationID(commandID string) string {
	return planningOperationID("job_question_authorization", commandID)
}

// NewJobQuestionAuthorization creates a Controller-issued, single-question read-only grant.
// The trusted context resolver must supply an immutable boundary before this constructor is called.
func NewJobQuestionAuthorization(job Job, request JobQuestionAuthorizationRequest, now time.Time) (JobQuestionAuthorization, error) {
	if err := job.Validate(); err != nil {
		return JobQuestionAuthorization{}, fmt.Errorf("validate Job before JobQuestionAuthorization: %w", err)
	}
	if job.State != JobActive || len(job.JobContractRevisions) == 0 {
		return JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization requires an ACTIVE Job with an approved JobContract")
	}
	if !validCommandID(request.CommandID) || request.JobID != job.ID ||
		!validPlanningIdentity(request.QuestionMessageID) ||
		!validPlanningIdentity(request.PlannerRoleExecutionID) ||
		!validPlanningIdentity(request.ProviderSessionID) ||
		!validPlanningIdentity(request.RepositoryID) ||
		!validRepositoryIdentity(request.PlanningRepositoryID) ||
		!validPlanningIdentity(request.ProviderID) || !validPlanningIdentity(request.AccountID) ||
		!validSHA256(request.ConfigurationSHA256) || !validPlanningIdentity(request.ReadOnlyBoundary.Reference) ||
		!validSHA256(request.ReadOnlyBoundary.SHA256) ||
		(request.ReadOnlyBoundary.Mode != PlanningReadOnlyBoundarySnapshot &&
			request.ReadOnlyBoundary.Mode != PlanningReadOnlyBoundaryDocuments) ||
		request.Budget.MaxTurns == 0 || request.Budget.MaxTokens == 0 ||
		request.Deadline.IsZero() || now.IsZero() {
		return JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization request identity or bounds are invalid")
	}

	latest := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	approval, approved := jobApprovalForRevision(job, latest.Revision)
	if !approved || approval.Validate(job, latest) != nil {
		return JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization requires the exact latest JobApproval")
	}
	if !repositoryInJobContract(latest.Contract, request.RepositoryID) ||
		request.ConfigurationSHA256 != latest.Contract.ConfigurationSHA256 ||
		request.Deadline.After(latest.Contract.Deadline) ||
		!request.Deadline.After(now) ||
		request.Budget.MaxTurns > latest.Contract.Budget.MaxTurns ||
		request.Budget.MaxTokens > latest.Contract.Budget.MaxTokens {
		return JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization exceeds the approved JobContract budget or deadline")
	}
	usedTurns, usedTokens, err := executionPermissionBudgetUsed(job, latest.Revision)
	if err != nil {
		return JobQuestionAuthorization{}, err
	}
	if usedTurns > uint64(latest.Contract.Budget.MaxTurns) ||
		uint64(request.Budget.MaxTurns) > uint64(latest.Contract.Budget.MaxTurns)-usedTurns ||
		usedTokens > latest.Contract.Budget.MaxTokens ||
		request.Budget.MaxTokens > latest.Contract.Budget.MaxTokens-usedTokens {
		return JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization reservation exceeds the remaining Job budget")
	}

	question, found := jobMessageByID(job, request.QuestionMessageID)
	if !found || question.Kind != MessageKindQuestion ||
		question.Recipient != (ActorRef{Kind: ActorPlanner, ID: request.PlannerRoleExecutionID}) ||
		(question.Sender.Kind != ActorCoder && question.Sender.Kind != ActorReviewer) {
		return JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization must bind a persisted Role QUESTION addressed to the specified Planner")
	}
	planner, found := roleExecutionByID(job, request.PlannerRoleExecutionID)
	if !found || planner.Role != RolePlanner || planner.TaskID != "" {
		return JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization requires the addressed Taskless PLANNER RoleExecution")
	}
	planningGrant, found := planningAuthorizationByID(job, planner.PlanningAuthorizationID)
	if !found || planningGrant.Validate(job) != nil {
		return JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization Planner PlanningAuthorization is invalid")
	}
	var start ProviderStartIntent
	startFound := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == planner.ID {
			start, startFound = candidate, true
			break
		}
	}
	if !startFound || start.ProviderID != request.ProviderID || start.AccountID != request.AccountID ||
		start.ConfigurationSHA256 != request.ConfigurationSHA256 ||
		planningGrant.RepositoryID != request.PlanningRepositoryID ||
		request.Deadline.After(start.Deadline) || !now.Before(start.Deadline) ||
		providerStopIntentExists(job, planner.ID, request.ProviderSessionID, job.Revision+1) {
		return JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization Provider or repository identity differs from the accepted Planner execution")
	}
	startObservation, observed := latestProviderStartObservation(job, start.ID)
	if !observed || startObservation.Status != ProviderStartAccepted ||
		startObservation.ProviderSessionID != request.ProviderSessionID {
		return JobQuestionAuthorization{}, errors.New("JobQuestionAuthorization requires the exact accepted Planner Provider Session")
	}
	for _, existing := range job.JobQuestionAuthorizations {
		if existing.QuestionMessageID == question.ID {
			return JobQuestionAuthorization{}, errors.New("QUESTION already has a JobQuestionAuthorization")
		}
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return JobQuestionAuthorization{}, errors.New("Job revision exhausted")
	}

	now = now.UTC().Round(0)
	authorization := JobQuestionAuthorization{
		ID: JobQuestionAuthorizationID(request.CommandID), CommandID: request.CommandID,
		JobID: job.ID, JobApprovalID: approval.ID, ContractRevision: latest.Revision,
		QuestionMessageID: question.ID, PlannerRoleExecutionID: planner.ID,
		ProviderSessionID: request.ProviderSessionID, RepositoryID: request.RepositoryID,
		PlanningRepositoryID: request.PlanningRepositoryID,
		ProviderID:           request.ProviderID, AccountID: request.AccountID,
		ConfigurationSHA256: request.ConfigurationSHA256, ReadOnlyBoundary: request.ReadOnlyBoundary,
		Budget: request.Budget, IssuedAt: now, Deadline: request.Deadline.UTC().Round(0),
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := authorization.Validate(job); err != nil {
		return JobQuestionAuthorization{}, err
	}
	return authorization, nil
}

func (a JobQuestionAuthorization) Validate(job Job) error {
	if !validCommandID(a.CommandID) || a.ID != JobQuestionAuthorizationID(a.CommandID) ||
		a.JobID != job.ID || !validPlanningIdentity(a.JobApprovalID) || a.ContractRevision == 0 ||
		!validPlanningIdentity(a.QuestionMessageID) || !validPlanningIdentity(a.PlannerRoleExecutionID) ||
		!validPlanningIdentity(a.ProviderSessionID) || !validPlanningIdentity(a.RepositoryID) ||
		!validRepositoryIdentity(a.PlanningRepositoryID) || !validPlanningIdentity(a.ProviderID) || !validPlanningIdentity(a.AccountID) ||
		!validSHA256(a.ConfigurationSHA256) ||
		!validPlanningIdentity(a.ReadOnlyBoundary.Reference) || !validSHA256(a.ReadOnlyBoundary.SHA256) ||
		(a.ReadOnlyBoundary.Mode != PlanningReadOnlyBoundarySnapshot &&
			a.ReadOnlyBoundary.Mode != PlanningReadOnlyBoundaryDocuments) ||
		a.Budget.MaxTurns == 0 || a.Budget.MaxTokens == 0 ||
		a.IssuedAt.IsZero() || a.Deadline.IsZero() || !a.Deadline.After(a.IssuedAt) ||
		!a.CreatedAt.Equal(a.IssuedAt) || a.CreatedRevision == 0 || a.CreatedRevision > job.Revision+1 {
		return errors.New("JobQuestionAuthorization identity or metadata is invalid")
	}
	var revision *JobContractRevision
	for i := range job.JobContractRevisions {
		if job.JobContractRevisions[i].Revision == a.ContractRevision {
			revision = &job.JobContractRevisions[i]
			break
		}
	}
	if revision == nil || !repositoryInJobContract(revision.Contract, a.RepositoryID) ||
		a.ConfigurationSHA256 != revision.Contract.ConfigurationSHA256 ||
		a.Deadline.After(revision.Contract.Deadline) ||
		a.Budget.MaxTurns > revision.Contract.Budget.MaxTurns ||
		a.Budget.MaxTokens > revision.Contract.Budget.MaxTokens {
		return errors.New("JobQuestionAuthorization does not fit its approved JobContract revision")
	}
	approval, found := jobApprovalForRevision(job, a.ContractRevision)
	if !found || approval.ID != a.JobApprovalID || approval.Validate(job, *revision) != nil ||
		a.CreatedRevision <= approval.CreatedRevision || a.IssuedAt.Before(approval.ApprovedAt) {
		return errors.New("JobQuestionAuthorization does not bind its exact JobApproval")
	}
	question, found := jobMessageByID(job, a.QuestionMessageID)
	if !found || question.Kind != MessageKindQuestion ||
		question.Recipient != (ActorRef{Kind: ActorPlanner, ID: a.PlannerRoleExecutionID}) ||
		(question.Sender.Kind != ActorCoder && question.Sender.Kind != ActorReviewer) ||
		question.CreatedRevision >= a.CreatedRevision || question.CreatedAt.After(a.IssuedAt) {
		return errors.New("JobQuestionAuthorization does not bind its original Role QUESTION")
	}
	planner, found := roleExecutionByID(job, a.PlannerRoleExecutionID)
	if !found || planner.Role != RolePlanner || planner.TaskID != "" ||
		planner.CreatedRevision >= a.CreatedRevision {
		return errors.New("JobQuestionAuthorization Planner RoleExecution is invalid")
	}
	planningGrant, found := planningAuthorizationByID(job, planner.PlanningAuthorizationID)
	if !found || planningGrant.Validate(job) != nil || planningGrant.RepositoryID != a.PlanningRepositoryID {
		return errors.New("JobQuestionAuthorization does not bind the Planner repository")
	}
	var start ProviderStartIntent
	startFound := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == planner.ID {
			start, startFound = candidate, true
			break
		}
	}
	if !startFound || start.ProviderID != a.ProviderID || start.AccountID != a.AccountID ||
		start.ConfigurationSHA256 != a.ConfigurationSHA256 || a.IssuedAt.After(start.Deadline) ||
		a.Deadline.After(start.Deadline) ||
		providerStopIntentExists(job, planner.ID, a.ProviderSessionID, a.CreatedRevision) {
		return errors.New("JobQuestionAuthorization Provider identity does not match the Planner Start")
	}
	startObservation, observed := latestProviderStartObservation(job, start.ID)
	if !observed || startObservation.Status != ProviderStartAccepted ||
		startObservation.ProviderSessionID != a.ProviderSessionID ||
		startObservation.CreatedRevision >= a.CreatedRevision {
		return errors.New("JobQuestionAuthorization does not bind the accepted Planner Session")
	}
	return nil
}

func (a JobQuestionAuthorization) ValidateFor(
	job Job,
	questionMessageID, plannerRoleExecutionID, providerSessionID string,
	now time.Time,
) error {
	if err := a.Validate(job); err != nil {
		return err
	}
	if job.State != JobActive || len(job.JobContractRevisions) == 0 {
		return errors.New("JobQuestionAuthorization is not valid for an inactive Job")
	}
	latest := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	if latest.Revision != a.ContractRevision ||
		questionMessageID != a.QuestionMessageID ||
		plannerRoleExecutionID != a.PlannerRoleExecutionID ||
		providerSessionID != a.ProviderSessionID ||
		now.IsZero() || now.Before(a.IssuedAt) || !now.Before(a.Deadline) ||
		!now.Before(latest.Contract.Deadline) ||
		providerStopIntentExists(job, a.PlannerRoleExecutionID, a.ProviderSessionID, job.Revision+1) {
		return errors.New("JobQuestionAuthorization is stale, expired, or bound to another question or Session")
	}
	return nil
}

func validateJobQuestionAuthorizationFacts(job Job) error {
	seenIDs := make(map[string]struct{}, len(job.JobQuestionAuthorizations))
	seenCommands := make(map[string]struct{}, len(job.JobQuestionAuthorizations))
	seenQuestions := make(map[string]struct{}, len(job.JobQuestionAuthorizations))
	var previousRevision uint64
	var previousAt time.Time
	for _, authorization := range job.JobQuestionAuthorizations {
		if err := authorization.Validate(job); err != nil {
			return fmt.Errorf("invalid JobQuestionAuthorization: %w", err)
		}
		if authorization.CreatedAt.After(job.UpdatedAt) ||
			authorization.CreatedRevision > job.Revision ||
			authorization.CreatedRevision <= previousRevision ||
			(!previousAt.IsZero() && authorization.CreatedAt.Before(previousAt)) {
			return errors.New("JobQuestionAuthorization append order is invalid")
		}
		if _, exists := seenIDs[authorization.ID]; exists {
			return errors.New("duplicate JobQuestionAuthorization ID")
		}
		if _, exists := seenCommands[authorization.CommandID]; exists {
			return errors.New("duplicate JobQuestionAuthorization CommandID")
		}
		if _, exists := seenQuestions[authorization.QuestionMessageID]; exists {
			return errors.New("QUESTION has multiple JobQuestionAuthorizations")
		}
		seenIDs[authorization.ID] = struct{}{}
		seenCommands[authorization.CommandID] = struct{}{}
		seenQuestions[authorization.QuestionMessageID] = struct{}{}
		previousRevision, previousAt = authorization.CreatedRevision, authorization.CreatedAt
	}
	return nil
}

func planningAuthorizationByID(job Job, authorizationID string) (PlanningAuthorization, bool) {
	for _, authorization := range job.PlanningAuthorizations {
		if authorization.ID == authorizationID {
			return authorization, true
		}
	}
	return PlanningAuthorization{}, false
}

func repositoryInJobContract(contract JobContract, repositoryID string) bool {
	for _, candidate := range contract.TargetRepositories {
		if candidate == repositoryID {
			return true
		}
	}
	return false
}
