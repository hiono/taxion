package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type CommunicationStore interface {
	GetJob(context.Context, string) (domain.Job, error)
	AddJobMessage(context.Context, string, uint64, string, domain.JobMessageRequest) (store.CommandReceipt, domain.JobMessage, error)
}

type RoleMessageRequest struct {
	JobID             string
	RoleExecutionID   string
	ProviderSessionID string
	CommandID         string
	Recipient         domain.ActorRef
	ReplyToMessageID  string
	Body              string
}

type HumanMessageRequest struct {
	JobID            string
	CommandID        string
	Recipient        domain.ActorRef
	ReplyToMessageID string
	Body             string
}

type SystemMessageRequest struct {
	JobID            string
	CommandID        string
	Recipient        domain.ActorRef
	ReplyToMessageID string
	Body             string
}

type CommunicationOutcome struct {
	MessageReceipt store.CommandReceipt
	Message        domain.JobMessage
}

type RoleExecutionContext struct {
	JobID             string
	RoleExecution     domain.RoleExecution
	ProviderSessionID string
	Messages          []domain.JobMessage
}

type CommunicationController struct {
	store CommunicationStore
}

func NewCommunicationController(communicationStore CommunicationStore) (*CommunicationController, error) {
	if communicationStore == nil {
		return nil, errors.New("CommunicationStore is required")
	}
	return &CommunicationController{store: communicationStore}, nil
}

func (c *CommunicationController) SendFromRole(ctx context.Context, request RoleMessageRequest) (CommunicationOutcome, error) {
	var outcome CommunicationOutcome
	if ctx == nil || request.JobID == "" || request.RoleExecutionID == "" ||
		request.ProviderSessionID == "" || request.CommandID == "" {
		return outcome, errors.New("Role message requires Job, RoleExecution, Session, and Command identities")
	}
	job, err := c.store.GetJob(ctx, request.JobID)
	if err != nil {
		return outcome, err
	}
	if err := job.Validate(); err != nil {
		return outcome, fmt.Errorf("validate Job before Role message: %w", err)
	}
	execution, found := findRoleExecution(job, request.RoleExecutionID)
	if !found {
		return outcome, errors.New("message sender RoleExecution was not found in the Job")
	}
	if !hasAcceptedRoleSession(job, execution, request.ProviderSessionID) {
		return outcome, errors.New("message sender Session is not the accepted Session for the RoleExecution")
	}
	actorKind, err := actorKindForRole(execution.Role)
	if err != nil {
		return outcome, err
	}
	return c.persistMessage(ctx, request.JobID, request.CommandID, domain.JobMessageRequest{
		Sender:    domain.ActorRef{Kind: actorKind, ID: execution.ID},
		Recipient: request.Recipient, ReplyToMessageID: request.ReplyToMessageID,
		Body: request.Body,
	})
}

// InboxForRole returns messages addressed to a RoleExecution without recording delivery state.
func (c *CommunicationController) InboxForRole(
	ctx context.Context,
	jobID, roleExecutionID string,
) ([]domain.JobMessage, error) {
	if ctx == nil || jobID == "" || roleExecutionID == "" {
		return nil, errors.New("Role inbox requires Job and RoleExecution identities")
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if err := job.Validate(); err != nil {
		return nil, fmt.Errorf("validate Job before reading Role inbox: %w", err)
	}
	execution, found := findRoleExecution(job, roleExecutionID)
	if !found {
		return nil, errors.New("Role inbox execution was not found in the Job")
	}
	actorKind, err := actorKindForRole(execution.Role)
	if err != nil {
		return nil, err
	}
	recipient := domain.ActorRef{Kind: actorKind, ID: execution.ID}
	var inbox []domain.JobMessage
	for _, message := range job.JobMessages {
		if message.Recipient == recipient {
			inbox = append(inbox, message)
		}
	}
	return inbox, nil
}

func (c *CommunicationController) ContextForMessage(
	ctx context.Context,
	jobID, messageID string,
) (RoleExecutionContext, error) {
	var result RoleExecutionContext
	if ctx == nil || jobID == "" || messageID == "" {
		return result, errors.New("Role context lookup requires Job and Message identities")
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return result, err
	}
	if err := job.Validate(); err != nil {
		return result, fmt.Errorf("validate Job before restoring Role context: %w", err)
	}
	var message domain.JobMessage
	messageFound := false
	for _, candidate := range job.JobMessages {
		if candidate.ID == messageID {
			message, messageFound = candidate, true
			break
		}
	}
	if !messageFound || (message.Recipient.Kind != domain.ActorPlanner &&
		message.Recipient.Kind != domain.ActorCoder && message.Recipient.Kind != domain.ActorReviewer) {
		return result, errors.New("message recipient is not a persisted RoleExecution")
	}
	execution, found := findRoleExecution(job, message.Recipient.ID)
	if !found {
		return result, errors.New("message recipient RoleExecution was not found")
	}
	actorKind, err := actorKindForRole(execution.Role)
	if err != nil || actorKind != message.Recipient.Kind {
		return result, errors.New("message recipient does not match the RoleExecution identity")
	}
	sessionID, found := acceptedRoleSession(job, execution)
	if !found {
		return result, errors.New("RoleExecution has no currently accepted Provider Session")
	}
	actor := domain.ActorRef{Kind: actorKind, ID: execution.ID}
	if message.Sender != actor && message.Recipient != actor {
		return result, errors.New("message is not part of the recipient RoleExecution conversation")
	}
	conversation := make([]domain.JobMessage, 0, len(job.JobMessages))
	for _, candidate := range job.JobMessages {
		if candidate.Sender == actor || candidate.Recipient == actor {
			conversation = append(conversation, candidate)
		}
	}
	return RoleExecutionContext{
		JobID: job.ID, RoleExecution: execution, ProviderSessionID: sessionID, Messages: conversation,
	}, nil
}

func (c *CommunicationController) SendFromHuman(
	ctx context.Context,
	request HumanMessageRequest,
	principal auth.AuthenticatedPrincipal,
) (CommunicationOutcome, error) {
	var outcome CommunicationOutcome
	if ctx == nil || request.JobID == "" || request.CommandID == "" {
		return outcome, errors.New("HUMAN message requires Job and Command identities")
	}
	if err := principal.Validate(); err != nil {
		return outcome, fmt.Errorf("validate HUMAN message Principal: %w", err)
	}
	job, err := c.store.GetJob(ctx, request.JobID)
	if err != nil {
		return outcome, err
	}
	if job.IntakeProvenance == nil || job.IntakeProvenance.PrincipalID != principal.PrincipalID() {
		return outcome, errors.New("HUMAN message Principal does not own the Job")
	}
	return c.persistMessage(ctx, request.JobID, request.CommandID, domain.JobMessageRequest{
		Sender:    domain.ActorRef{Kind: domain.ActorHuman, ID: principal.PrincipalID()},
		Recipient: request.Recipient, ReplyToMessageID: request.ReplyToMessageID,
		Body: request.Body,
	})
}

func (c *CommunicationController) SendFromController(ctx context.Context, request SystemMessageRequest) (CommunicationOutcome, error) {
	return c.sendFromSystem(ctx, request, domain.ActorController, domain.ControllerActorID)
}

func (c *CommunicationController) SendFromOrchestrator(ctx context.Context, request SystemMessageRequest) (CommunicationOutcome, error) {
	return c.sendFromSystem(ctx, request, domain.ActorOrchestrator, domain.OrchestratorActorID)
}

func (c *CommunicationController) sendFromSystem(
	ctx context.Context,
	request SystemMessageRequest,
	actorKind domain.ActorKind,
	actorID string,
) (CommunicationOutcome, error) {
	var outcome CommunicationOutcome
	if ctx == nil || request.JobID == "" || request.CommandID == "" {
		return outcome, errors.New("system message requires Job and Command identities")
	}
	return c.persistMessage(ctx, request.JobID, request.CommandID, domain.JobMessageRequest{
		Sender:           domain.ActorRef{Kind: actorKind, ID: actorID},
		Recipient:        request.Recipient,
		ReplyToMessageID: request.ReplyToMessageID,
		Body:             request.Body,
	})
}

func (c *CommunicationController) persistMessage(
	ctx context.Context,
	jobID, commandID string,
	request domain.JobMessageRequest,
) (CommunicationOutcome, error) {
	var outcome CommunicationOutcome
	for attempt := 0; attempt < 2; attempt++ {
		job, err := c.store.GetJob(ctx, jobID)
		if err != nil {
			return outcome, err
		}
		receipt, message, err := c.store.AddJobMessage(ctx, jobID, job.Revision, commandID, request)
		if err == nil {
			return CommunicationOutcome{MessageReceipt: receipt, Message: message}, nil
		}
		if !errors.Is(err, store.ErrIdempotencyConflict) || attempt != 0 {
			return outcome, err
		}
	}
	return outcome, store.ErrIdempotencyConflict
}

func findRoleExecution(job domain.Job, roleExecutionID string) (domain.RoleExecution, bool) {
	for _, execution := range job.RoleExecutions {
		if execution.ID == roleExecutionID {
			return execution, true
		}
	}
	return domain.RoleExecution{}, false
}

func acceptedRoleSession(job domain.Job, execution domain.RoleExecution) (string, bool) {
	var latest *domain.ProviderStartObservation
	for i := range job.ProviderStartObservations {
		observation := &job.ProviderStartObservations[i]
		if observation.OperationID == execution.ProviderStartOperationID &&
			(latest == nil || observation.CreatedRevision > latest.CreatedRevision) {
			latest = observation
		}
	}
	if latest == nil || latest.Status != domain.ProviderStartAccepted || latest.ProviderSessionID == "" {
		return "", false
	}
	return latest.ProviderSessionID, true
}

func hasAcceptedRoleSession(job domain.Job, execution domain.RoleExecution, sessionID string) bool {
	acceptedSessionID, found := acceptedRoleSession(job, execution)
	return found && acceptedSessionID == sessionID
}

func actorKindForRole(role string) (domain.ActorKind, error) {
	switch role {
	case domain.RolePlanner:
		return domain.ActorPlanner, nil
	case domain.RoleCoder:
		return domain.ActorCoder, nil
	case domain.RoleReviewer:
		return domain.ActorReviewer, nil
	default:
		return "", errors.New("RoleExecution has an unsupported communication role")
	}
}
