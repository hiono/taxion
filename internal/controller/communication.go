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
	CommandID         string
	Recipient         domain.ActorRef
	ReplyToMessageID string
	Body              string
}

type CommunicationOutcome struct {
	MessageReceipt store.CommandReceipt
	Message        domain.JobMessage
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
		Sender: domain.ActorRef{Kind: actorKind, ID: execution.ID},
		Recipient: request.Recipient, ReplyToMessageID: request.ReplyToMessageID,
		Body: request.Body,
	})
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
		Sender: domain.ActorRef{Kind: domain.ActorHuman, ID: principal.PrincipalID()},
		Recipient: request.Recipient, ReplyToMessageID: request.ReplyToMessageID,
		Body: request.Body,
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
		current, readErr := c.store.GetJob(ctx, jobID)
		if readErr != nil {
			return outcome, errors.Join(err, readErr)
		}
		if _, exists := findMessageByCommand(current, commandID); exists {
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

func findMessageByCommand(job domain.Job, commandID string) (domain.JobMessage, bool) {
	for _, message := range job.JobMessages {
		if message.CommandID == commandID {
			return message, true
		}
	}
	return domain.JobMessage{}, false
}

func hasAcceptedRoleSession(job domain.Job, execution domain.RoleExecution, sessionID string) bool {
	var latest *domain.ProviderStartObservation
	for i := range job.ProviderStartObservations {
		observation := &job.ProviderStartObservations[i]
		if observation.OperationID == execution.ProviderStartOperationID &&
			(latest == nil || observation.CreatedRevision > latest.CreatedRevision) {
			latest = observation
		}
	}
	return latest != nil && latest.Status == domain.ProviderStartAccepted &&
		latest.ProviderSessionID == sessionID
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
