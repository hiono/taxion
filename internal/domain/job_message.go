package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

type ActorKind string

const (
	ActorHuman        ActorKind = "HUMAN"
	ActorController   ActorKind = "CONTROLLER"
	ActorOrchestrator ActorKind = "ORCHESTRATOR"
	ActorPlanner      ActorKind = RolePlanner
	ActorCoder        ActorKind = RoleCoder
	ActorReviewer     ActorKind = RoleReviewer
)

const (
	ControllerActorID   = "taxion.controller"
	OrchestratorActorID = "taxion.orchestrator"
)

type ActorRef struct {
	Kind ActorKind `json:"kind"`
	ID   string    `json:"id"`
}

type MessageKind string

const (
	MessageKindMessage      MessageKind = "MESSAGE"
	MessageKindQuestion     MessageKind = "QUESTION"
	MessageKindAnswer       MessageKind = "ANSWER"
	MessageKindRequest      MessageKind = "REQUEST"
	MessageKindNotification MessageKind = "NOTIFICATION"
	MessageKindHandoff      MessageKind = "HANDOFF"
)

type DeliveryStatus string

const (
	DeliveryRequested DeliveryStatus = "REQUESTED"
	DeliveryAccepted  DeliveryStatus = "ACCEPTED"
	DeliveryFailed    DeliveryStatus = "FAILED"
	DeliveryUnknown   DeliveryStatus = "UNKNOWN"
)

const maxJobMessageBodyBytes = 64 * 1024

type JobMessageRequest struct {
	Sender           ActorRef    `json:"sender"`
	Recipient        ActorRef    `json:"recipient"`
	Kind             MessageKind `json:"kind"`
	ThreadID         string      `json:"thread_id,omitempty"`
	ReplyToMessageID string      `json:"reply_to_message_id,omitempty"`
	BatchID          string      `json:"batch_id,omitempty"`
	Body             string      `json:"body"`
}

type JobMessage struct {
	ID               string      `json:"id"`
	CommandID        string      `json:"command_id"`
	JobID            string      `json:"job_id"`
	Sender           ActorRef    `json:"sender"`
	Recipient        ActorRef    `json:"recipient"`
	Kind             MessageKind `json:"kind"`
	ThreadID         string      `json:"thread_id"`
	ReplyToMessageID string      `json:"reply_to_message_id,omitempty"`
	BatchID          string      `json:"batch_id,omitempty"`
	Body             string      `json:"body"`
	CreatedAt        time.Time   `json:"created_at"`
	CreatedRevision  uint64      `json:"created_revision"`
}

type DeliveryObservation struct {
	ID                string         `json:"id"`
	CommandID         string         `json:"command_id"`
	JobID             string         `json:"job_id"`
	MessageID         string         `json:"message_id"`
	Recipient         ActorRef       `json:"recipient"`
	ObservedBy        ActorRef       `json:"observed_by"`
	Status            DeliveryStatus `json:"status"`
	Attempt           uint32         `json:"attempt"`
	EvidenceSHA256    string         `json:"evidence_sha256,omitempty"`
	ReconcilesUnknown bool           `json:"reconciles_unknown,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	CreatedRevision   uint64         `json:"created_revision"`
}

func JobMessageID(commandID string) string {
	return planningOperationID("job_message", commandID)
}

func DeliveryObservationID(commandID string) string {
	return planningOperationID("delivery_observation", commandID)
}

func NewJobMessage(job Job, commandID string, request JobMessageRequest, now time.Time) (JobMessage, error) {
	if err := job.Validate(); err != nil {
		return JobMessage{}, fmt.Errorf("validate Job before message creation: %w", err)
	}
	if !validCommandID(commandID) || now.IsZero() || now.Before(job.UpdatedAt) {
		return JobMessage{}, errors.New("JobMessage command identity or timestamp is invalid")
	}
	if err := request.Sender.Validate(job); err != nil {
		return JobMessage{}, fmt.Errorf("invalid JobMessage sender: %w", err)
	}
	if err := request.Recipient.Validate(job); err != nil {
		return JobMessage{}, fmt.Errorf("invalid JobMessage recipient: %w", err)
	}
	if request.Sender == request.Recipient {
		return JobMessage{}, errors.New("JobMessage sender and recipient must differ")
	}
	if !validMessageKind(request.Kind) || request.Kind == MessageKindHandoff {
		return JobMessage{}, errors.New("unsupported JobMessage kind; HANDOFF requires its dedicated command")
	}
	if !utf8.ValidString(request.Body) || strings.TrimSpace(request.Body) == "" || len(request.Body) > maxJobMessageBodyBytes {
		return JobMessage{}, errors.New("JobMessage body must be non-empty valid UTF-8 within the size limit")
	}
	if request.BatchID != "" && !validPlanningIdentity(request.BatchID) {
		return JobMessage{}, errors.New("JobMessage BatchID is invalid")
	}
	if request.ReplyToMessageID != "" {
		repliedTo, found := jobMessageByID(job, request.ReplyToMessageID)
		if !found {
			return JobMessage{}, errors.New("JobMessage reply target was not found in the same Job")
		}
		if repliedTo.Recipient != request.Sender || repliedTo.Sender != request.Recipient {
			return JobMessage{}, errors.New("JobMessage reply actors do not match the original message")
		}
		if request.ThreadID != "" && request.ThreadID != repliedTo.ThreadID {
			return JobMessage{}, errors.New("JobMessage reply belongs to a different thread")
		}
		request.ThreadID = repliedTo.ThreadID
	}
	if request.Kind == MessageKindAnswer {
		if request.ReplyToMessageID == "" {
			return JobMessage{}, errors.New("ANSWER must reference its QUESTION")
		}
		repliedTo, _ := jobMessageByID(job, request.ReplyToMessageID)
		if repliedTo.Kind != MessageKindQuestion {
			return JobMessage{}, errors.New("ANSWER must reference a QUESTION message")
		}
	}
	messageID := JobMessageID(commandID)
	if request.ThreadID == "" {
		request.ThreadID = messageID
	}
	message := JobMessage{
		ID: messageID, CommandID: commandID, JobID: job.ID,
		Sender: request.Sender, Recipient: request.Recipient, Kind: request.Kind,
		ThreadID: request.ThreadID, ReplyToMessageID: request.ReplyToMessageID,
		BatchID: request.BatchID, Body: request.Body,
		CreatedAt: now.UTC().Round(0), CreatedRevision: job.Revision + 1,
	}
	if err := message.Validate(job); err != nil {
		return JobMessage{}, err
	}
	return message, nil
}

func (m JobMessage) Validate(job Job) error {
	if !validCommandID(m.CommandID) || m.ID != JobMessageID(m.CommandID) ||
		m.JobID != job.ID || !validPlanningIdentity(m.ThreadID) ||
		m.CreatedAt.IsZero() || m.CreatedRevision == 0 || m.CreatedRevision > job.Revision+1 {
		return errors.New("JobMessage identity or metadata is invalid")
	}
	if err := m.Sender.Validate(job); err != nil {
		return fmt.Errorf("invalid sender: %w", err)
	}
	if err := m.Recipient.Validate(job); err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	if m.Sender == m.Recipient || !validMessageKind(m.Kind) || m.Kind == MessageKindHandoff {
		return errors.New("JobMessage actors or kind are invalid")
	}
	for _, actor := range []ActorRef{m.Sender, m.Recipient} {
		if actor.Kind != ActorPlanner && actor.Kind != ActorCoder && actor.Kind != ActorReviewer {
			continue
		}
		execution, _ := roleExecutionByID(job, actor.ID)
		if execution.CreatedRevision >= m.CreatedRevision {
			return errors.New("JobMessage references a RoleExecution created after the message")
		}
	}
	if !utf8.ValidString(m.Body) || strings.TrimSpace(m.Body) == "" || len(m.Body) > maxJobMessageBodyBytes {
		return errors.New("JobMessage body is invalid")
	}
	if m.BatchID != "" && !validPlanningIdentity(m.BatchID) {
		return errors.New("JobMessage BatchID is invalid")
	}
	if m.ReplyToMessageID == "" {
		if m.ThreadID != m.ID {
			return errors.New("root JobMessage ThreadID must equal its MessageID")
		}
		if m.Kind == MessageKindAnswer {
			return errors.New("ANSWER must reference its QUESTION")
		}
		return nil
	}
	root, rootFound := jobMessageByID(job, m.ThreadID)
	if !rootFound || root.ID != root.ThreadID || root.ReplyToMessageID != "" || root.CreatedRevision >= m.CreatedRevision {
		return errors.New("JobMessage thread root is invalid")
	}
	repliedTo, found := jobMessageByID(job, m.ReplyToMessageID)
	if !found || repliedTo.CreatedRevision >= m.CreatedRevision ||
		repliedTo.ThreadID != m.ThreadID ||
		repliedTo.Recipient != m.Sender || repliedTo.Sender != m.Recipient {
		return errors.New("JobMessage reply correlation is invalid")
	}
	if m.Kind == MessageKindAnswer && repliedTo.Kind != MessageKindQuestion {
		return errors.New("ANSWER must reference a QUESTION message")
	}
	return nil
}

func (a ActorRef) Validate(job Job) error {
	if !validPlanningIdentity(a.ID) {
		return errors.New("Actor identity is invalid")
	}
	switch a.Kind {
	case ActorHuman:
		if job.IntakeProvenance == nil || a.ID != job.IntakeProvenance.PrincipalID {
			return errors.New("HUMAN Actor does not match the authenticated Job Principal")
		}
	case ActorController:
		if a.ID != ControllerActorID {
			return errors.New("CONTROLLER Actor identity is not Taxion-managed")
		}
	case ActorOrchestrator:
		if a.ID != OrchestratorActorID {
			return errors.New("ORCHESTRATOR Actor identity is not Taxion-managed")
		}
	case ActorPlanner, ActorCoder, ActorReviewer:
		execution, found := roleExecutionByID(job, a.ID)
		if !found || execution.Role != string(a.Kind) {
			return errors.New("Role Actor must reference a matching RoleExecution in the same Job")
		}
	default:
		return errors.New("Actor kind is unsupported")
	}
	return nil
}

func NewDeliveryObservation(
	job Job,
	messageID, commandID string,
	attempt uint32,
	status DeliveryStatus,
	evidenceSHA256 string,
	reconcilesUnknown bool,
	now time.Time,
) (DeliveryObservation, error) {
	if err := job.Validate(); err != nil {
		return DeliveryObservation{}, fmt.Errorf("validate Job before DeliveryObservation: %w", err)
	}
	if !validCommandID(commandID) || now.IsZero() || now.Before(job.UpdatedAt) {
		return DeliveryObservation{}, errors.New("DeliveryObservation command identity or timestamp is invalid")
	}
	message, found := jobMessageByID(job, messageID)
	if !found {
		return DeliveryObservation{}, errors.New("DeliveryObservation message was not found")
	}
	if evidenceSHA256 != "" && !validSHA256(evidenceSHA256) {
		return DeliveryObservation{}, errors.New("DeliveryObservation evidence digest is invalid")
	}
	if !validDeliveryStatus(status) {
		return DeliveryObservation{}, errors.New("DeliveryObservation status is invalid")
	}
	history := deliveryHistoryForMessage(job, messageID)
	if attempt == 0 {
		return DeliveryObservation{}, errors.New("DeliveryObservation attempt is required")
	}
	if len(history) == 0 {
		if status != DeliveryRequested || attempt != 1 || evidenceSHA256 != "" || reconcilesUnknown {
			return DeliveryObservation{}, errors.New("first DeliveryObservation must be REQUESTED")
		}
	} else {
		previous := history[len(history)-1]
		switch previous.Status {
		case DeliveryRequested:
			if status == DeliveryRequested || attempt != previous.Attempt || reconcilesUnknown {
				return DeliveryObservation{}, errors.New("REQUESTED delivery can only transition to a terminal observation")
			}
		case DeliveryUnknown:
			if (status != DeliveryAccepted && status != DeliveryFailed) || attempt != previous.Attempt || !reconcilesUnknown {
				return DeliveryObservation{}, errors.New("UNKNOWN delivery requires an explicit terminal reconciliation")
			}
		case DeliveryFailed:
			if previous.Attempt == ^uint32(0) || status != DeliveryRequested || attempt != previous.Attempt+1 || reconcilesUnknown || evidenceSHA256 != "" {
				return DeliveryObservation{}, errors.New("FAILED delivery can only start the next REQUESTED attempt")
			}
		case DeliveryAccepted:
			return DeliveryObservation{}, errors.New("ACCEPTED delivery is terminal")
		}
		if previous.Status != DeliveryUnknown && reconcilesUnknown {
			return DeliveryObservation{}, errors.New("delivery reconciliation requires a prior UNKNOWN observation")
		}
	}
	observation := DeliveryObservation{
		ID: DeliveryObservationID(commandID), CommandID: commandID, JobID: job.ID,
		MessageID: message.ID, Recipient: message.Recipient,
		ObservedBy: ActorRef{Kind: ActorOrchestrator, ID: OrchestratorActorID},
		Status:     status, Attempt: attempt, EvidenceSHA256: evidenceSHA256,
		ReconcilesUnknown: reconcilesUnknown,
		CreatedAt:         now.UTC().Round(0), CreatedRevision: job.Revision + 1,
	}
	if err := observation.Validate(job); err != nil {
		return DeliveryObservation{}, err
	}
	return observation, nil
}

func (d DeliveryObservation) Validate(job Job) error {
	if !validCommandID(d.CommandID) || d.ID != DeliveryObservationID(d.CommandID) ||
		d.JobID != job.ID || !validPlanningIdentity(d.MessageID) ||
		d.Attempt == 0 || d.CreatedAt.IsZero() || d.CreatedRevision == 0 ||
		d.CreatedRevision > job.Revision+1 || !validDeliveryStatus(d.Status) {
		return errors.New("DeliveryObservation identity or metadata is invalid")
	}
	message, found := jobMessageByID(job, d.MessageID)
	if !found || message.Recipient != d.Recipient {
		return errors.New("DeliveryObservation does not match its JobMessage recipient")
	}
	if message.CreatedRevision >= d.CreatedRevision {
		return errors.New("DeliveryObservation predates its JobMessage")
	}
	if d.ObservedBy != (ActorRef{Kind: ActorOrchestrator, ID: OrchestratorActorID}) {
		return errors.New("DeliveryObservation must be recorded by the Taxion Orchestrator")
	}
	if d.EvidenceSHA256 != "" && !validSHA256(d.EvidenceSHA256) {
		return errors.New("DeliveryObservation evidence digest is invalid")
	}
	if d.Status == DeliveryRequested && (d.EvidenceSHA256 != "" || d.ReconcilesUnknown) {
		return errors.New("REQUESTED delivery cannot contain result evidence")
	}
	if d.ReconcilesUnknown && d.Status != DeliveryAccepted && d.Status != DeliveryFailed {
		return errors.New("only terminal delivery observations can reconcile UNKNOWN")
	}
	return nil
}

func validateJobMessageFacts(job Job) error {
	seenIDs := make(map[string]struct{}, len(job.JobMessages))
	seenCommands := make(map[string]struct{}, len(job.JobMessages))
	var previousRevision uint64
	var previousCreatedAt time.Time
	for _, message := range job.JobMessages {
		if err := message.Validate(job); err != nil {
			return fmt.Errorf("invalid JobMessage: %w", err)
		}
		if message.CreatedRevision > job.Revision || message.CreatedAt.After(job.UpdatedAt) ||
			(message.CreatedRevision <= previousRevision && previousRevision != 0) ||
			(message.CreatedAt.Before(previousCreatedAt) && !previousCreatedAt.IsZero()) {
			return errors.New("JobMessage append order is invalid")
		}
		if _, exists := seenIDs[message.ID]; exists {
			return errors.New("duplicate JobMessage ID")
		}
		if _, exists := seenCommands[message.CommandID]; exists {
			return errors.New("duplicate JobMessage CommandID")
		}
		seenIDs[message.ID] = struct{}{}
		seenCommands[message.CommandID] = struct{}{}
		previousRevision, previousCreatedAt = message.CreatedRevision, message.CreatedAt
	}
	seenDeliveryIDs := make(map[string]struct{}, len(job.DeliveryObservations))
	seenDeliveryCommands := make(map[string]struct{}, len(job.DeliveryObservations))
	deliveryByMessage := make(map[string][]DeliveryObservation)
	var previousDeliveryRevision uint64
	var previousDeliveryCreatedAt time.Time
	for _, observation := range job.DeliveryObservations {
		if err := observation.Validate(job); err != nil {
			return fmt.Errorf("invalid DeliveryObservation: %w", err)
		}
		if observation.CreatedRevision > job.Revision || observation.CreatedAt.After(job.UpdatedAt) ||
			(observation.CreatedRevision <= previousDeliveryRevision && previousDeliveryRevision != 0) ||
			(observation.CreatedAt.Before(previousDeliveryCreatedAt) && !previousDeliveryCreatedAt.IsZero()) {
			return errors.New("DeliveryObservation append order is invalid")
		}
		if _, exists := seenDeliveryIDs[observation.ID]; exists {
			return errors.New("duplicate DeliveryObservation ID")
		}
		if _, exists := seenDeliveryCommands[observation.CommandID]; exists {
			return errors.New("duplicate DeliveryObservation CommandID")
		}
		seenDeliveryIDs[observation.ID] = struct{}{}
		seenDeliveryCommands[observation.CommandID] = struct{}{}
		deliveryByMessage[observation.MessageID] = append(deliveryByMessage[observation.MessageID], observation)
		previousDeliveryRevision, previousDeliveryCreatedAt = observation.CreatedRevision, observation.CreatedAt
	}
	for messageID, history := range deliveryByMessage {
		for index, observation := range history {
			if index == 0 {
				if observation.Status != DeliveryRequested || observation.Attempt != 1 || observation.ReconcilesUnknown {
					return fmt.Errorf("first delivery observation for %s must be REQUESTED", messageID)
				}
				continue
			}
			previous := history[index-1]
			switch previous.Status {
			case DeliveryRequested:
				if observation.Status == DeliveryRequested || observation.Attempt != previous.Attempt || observation.ReconcilesUnknown {
					return fmt.Errorf("invalid delivery transition after REQUESTED for %s", messageID)
				}
			case DeliveryUnknown:
				if (observation.Status != DeliveryAccepted && observation.Status != DeliveryFailed) ||
					observation.Attempt != previous.Attempt || !observation.ReconcilesUnknown {
					return fmt.Errorf("UNKNOWN delivery for %s requires terminal reconciliation", messageID)
				}
			case DeliveryFailed:
				if observation.Status != DeliveryRequested || observation.Attempt != previous.Attempt+1 || observation.ReconcilesUnknown {
					return fmt.Errorf("FAILED delivery for %s requires a new REQUESTED attempt", messageID)
				}
			case DeliveryAccepted:
				return fmt.Errorf("ACCEPTED delivery for %s cannot transition", messageID)
			}
			if observation.CreatedRevision <= previous.CreatedRevision || observation.CreatedAt.Before(previous.CreatedAt) {
				return fmt.Errorf("delivery observation order is invalid for %s", messageID)
			}
		}
	}
	return nil
}

func validMessageKind(kind MessageKind) bool {
	switch kind {
	case MessageKindMessage, MessageKindQuestion, MessageKindAnswer, MessageKindRequest, MessageKindNotification, MessageKindHandoff:
		return true
	default:
		return false
	}
}

func validDeliveryStatus(status DeliveryStatus) bool {
	switch status {
	case DeliveryRequested, DeliveryAccepted, DeliveryFailed, DeliveryUnknown:
		return true
	default:
		return false
	}
}

func jobMessageByID(job Job, messageID string) (JobMessage, bool) {
	for _, message := range job.JobMessages {
		if message.ID == messageID {
			return message, true
		}
	}
	return JobMessage{}, false
}

func roleExecutionByID(job Job, roleExecutionID string) (RoleExecution, bool) {
	for _, execution := range job.RoleExecutions {
		if execution.ID == roleExecutionID {
			return execution, true
		}
	}
	return RoleExecution{}, false
}

func deliveryHistoryForMessage(job Job, messageID string) []DeliveryObservation {
	history := make([]DeliveryObservation, 0)
	for _, observation := range job.DeliveryObservations {
		if observation.MessageID == messageID {
			history = append(history, observation)
		}
	}
	return history
}
