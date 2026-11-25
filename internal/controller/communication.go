package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type CommunicationStore interface {
	GetJob(context.Context, string) (domain.Job, error)
	AddJobMessage(context.Context, string, uint64, string, domain.JobMessageRequest) (store.CommandReceipt, domain.JobMessage, error)
	AddDeliveryObservation(context.Context, string, uint64, string, string, string, domain.DeliveryStatus, string, bool) (store.CommandReceipt, domain.DeliveryObservation, error)
}

type RoleMessageRequest struct {
	JobID             string
	RoleExecutionID   string
	ProviderSessionID string
	CommandID         string
	Recipient         domain.ActorRef
	Kind              domain.MessageKind
	ThreadID          string
	ReplyToMessageID  string
	BatchID           string
	Body              string
}

type HumanMessageRequest struct {
	JobID            string
	CommandID        string
	Recipient        domain.ActorRef
	Kind             domain.MessageKind
	ThreadID         string
	ReplyToMessageID string
	BatchID          string
	Body             string
}

type CommunicationOutcome struct {
	MessageReceipt  store.CommandReceipt
	Message         domain.JobMessage
	DeliveryReceipt *store.CommandReceipt
	Delivery        *domain.DeliveryObservation
}

type CommunicationController struct {
	store CommunicationStore
	now   func() time.Time
}

func NewCommunicationController(communicationStore CommunicationStore) (*CommunicationController, error) {
	if communicationStore == nil {
		return nil, errors.New("CommunicationStore is required")
	}
	return &CommunicationController{
		store: communicationStore,
		now:   func() time.Time { return time.Now().UTC().Round(0) },
	}, nil
}

func (c *CommunicationController) SendFromRole(
	ctx context.Context,
	request RoleMessageRequest,
) (CommunicationOutcome, error) {
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
	messageRequest := domain.JobMessageRequest{
		Sender:    domain.ActorRef{Kind: actorKind, ID: execution.ID},
		Recipient: request.Recipient, Kind: request.Kind,
		ThreadID: request.ThreadID, ReplyToMessageID: request.ReplyToMessageID,
		BatchID: request.BatchID, Body: request.Body,
	}
	return c.persistMessage(ctx, request.JobID, request.CommandID, messageRequest)
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
	messageRequest := domain.JobMessageRequest{
		Sender:    domain.ActorRef{Kind: domain.ActorHuman, ID: principal.PrincipalID()},
		Recipient: request.Recipient, Kind: request.Kind,
		ThreadID: request.ThreadID, ReplyToMessageID: request.ReplyToMessageID,
		BatchID: request.BatchID, Body: request.Body,
	}
	return c.persistMessage(ctx, request.JobID, request.CommandID, messageRequest)
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
		if err != nil {
			if errors.Is(err, store.ErrIdempotencyConflict) && attempt == 0 {
				current, readErr := c.store.GetJob(ctx, jobID)
				if readErr != nil {
					return outcome, errors.Join(err, readErr)
				}
				if _, exists := findMessageByCommand(current, commandID); !exists {
					continue
				}
			}
			return outcome, err
		}
		outcome.MessageReceipt = receipt
		outcome.Message = message
		deliveryReceipt, delivery, err := c.ensureRequestedDelivery(ctx, jobID, message)
		if err != nil {
			return outcome, fmt.Errorf("persist initial Message delivery state: %w", err)
		}
		outcome.DeliveryReceipt = deliveryReceipt
		outcome.Delivery = delivery
		return outcome, nil
	}
	return outcome, store.ErrIdempotencyConflict
}

func (c *CommunicationController) ensureRequestedDelivery(
	ctx context.Context,
	jobID string,
	message domain.JobMessage,
) (*store.CommandReceipt, *domain.DeliveryObservation, error) {
	for attempt := 0; attempt < 2; attempt++ {
		job, err := c.store.GetJob(ctx, jobID)
		if err != nil {
			return nil, nil, err
		}
		if latest, found := latestDeliveryObservation(job, message.ID); found {
			return nil, &latest, nil
		}
		commandID := deliveryRequestCommandID(message.CommandID)
		receipt, observation, err := c.store.AddDeliveryObservation(
			ctx, jobID, job.Revision, message.ID, "", commandID, domain.DeliveryRequested, "", false,
		)
		if errors.Is(err, store.ErrIdempotencyConflict) && attempt == 0 {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		return &receipt, &observation, nil
	}
	return nil, nil, store.ErrIdempotencyConflict
}

// RecordDeliveryObservation appends an Orchestrator-owned delivery outcome for a persisted JobMessage.
// It records delivery evidence only; it does not dispatch or retry the message.
func (c *CommunicationController) RecordDeliveryObservation(
	ctx context.Context,
	jobID, messageID, requestObservationID, commandID string,
	status domain.DeliveryStatus,
	evidenceSHA256 string,
	reconcilesUnknown bool,
) (store.CommandReceipt, domain.DeliveryObservation, error) {
	return c.recordDeliveryObservation(ctx, jobID, messageID, requestObservationID, commandID, status, evidenceSHA256, reconcilesUnknown)
}

func (c *CommunicationController) recordDeliveryObservation(
	ctx context.Context,
	jobID, messageID, requestObservationID, commandID string,
	status domain.DeliveryStatus,
	evidenceSHA256 string,
	reconcilesUnknown bool,
) (store.CommandReceipt, domain.DeliveryObservation, error) {
	if ctx == nil || jobID == "" || messageID == "" || commandID == "" {
		return store.CommandReceipt{}, domain.DeliveryObservation{}, errors.New("DeliveryObservation requires Job, Message, and Command identities")
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.DeliveryObservation{}, err
	}
	return c.store.AddDeliveryObservation(
		ctx, jobID, job.Revision, messageID, requestObservationID, commandID, status, evidenceSHA256, reconcilesUnknown,
	)
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

func latestDeliveryObservation(job domain.Job, messageID string) (domain.DeliveryObservation, bool) {
	var latest domain.DeliveryObservation
	found := false
	for _, observation := range job.DeliveryObservations {
		if observation.MessageID == messageID &&
			(!found || observation.CreatedRevision > latest.CreatedRevision) {
			latest, found = observation, true
		}
	}
	return latest, found
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

func deliveryRequestCommandID(messageCommandID string) string {
	sum := sha256.Sum256([]byte("taxion:delivery-requested:" + messageCommandID))
	return "cmd_" + hex.EncodeToString(sum[:16])
}
