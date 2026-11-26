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
	ControllerActorID      = "taxion.controller"
	OrchestratorActorID    = "taxion.orchestrator"
	maxJobMessageBodyBytes = 64 * 1024
)

type ActorRef struct {
	Kind ActorKind `json:"kind"`
	ID   string    `json:"id"`
}

type JobMessageRequest struct {
	Sender           ActorRef `json:"sender"`
	Recipient        ActorRef `json:"recipient"`
	ReplyToMessageID string   `json:"reply_to_message_id,omitempty"`
	Body             string   `json:"body"`
}

type JobMessage struct {
	ID               string    `json:"id"`
	CommandID        string    `json:"command_id"`
	JobID            string    `json:"job_id"`
	Sender           ActorRef  `json:"sender"`
	Recipient        ActorRef  `json:"recipient"`
	ReplyToMessageID string    `json:"reply_to_message_id,omitempty"`
	Body             string    `json:"body"`
	CreatedAt        time.Time `json:"created_at"`
	CreatedRevision  uint64    `json:"created_revision"`
}

func JobMessageID(commandID string) string {
	return planningOperationID("job_message", commandID)
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
	if !utf8.ValidString(request.Body) || strings.TrimSpace(request.Body) == "" || len(request.Body) > maxJobMessageBodyBytes {
		return JobMessage{}, errors.New("JobMessage body must be non-empty valid UTF-8 within the size limit")
	}
	if request.ReplyToMessageID != "" {
		repliedTo, found := jobMessageByID(job, request.ReplyToMessageID)
		if !found || repliedTo.CreatedRevision >= job.Revision+1 ||
			repliedTo.Recipient != request.Sender || repliedTo.Sender != request.Recipient {
			return JobMessage{}, errors.New("JobMessage reply must target an earlier message between the same actors")
		}
	}

	message:= JobMessage{
		ID:               JobMessageID(commandID), CommandID: commandID, JobID: job.ID,
		Sender:           request.Sender, Recipient: request.Recipient,
		ReplyToMessageID: request.ReplyToMessageID, Body: request.Body,
		CreatedAt:        now.UTC().Round(0), CreatedRevision: job.Revision + 1,
	}
	if err := message.Validate(job); err != nil {
		return JobMessage{}, err
	}
	return message, nil
}

func (m JobMessage) Validate(job Job) error {
	if !validCommandID(m.CommandID) || m.ID != JobMessageID(m.CommandID) ||
		m.JobID != job.ID || m.CreatedAt.IsZero() ||
		m.CreatedRevision == 0 || m.CreatedRevision > job.Revision+1 {
		return errors.New("JobMessage identity or metadata is invalid")
	}
	if err := m.Sender.Validate(job); err != nil {
		return fmt.Errorf("invalid sender: %w", err)
	}
	if err := m.Recipient.Validate(job); err != nil {
		return fmt.Errorf("invalid recipient: %w", err)
	}
	if m.Sender == m.Recipient {
		return errors.New("JobMessage sender and recipient must differ")
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
	if m.ReplyToMessageID != "" {
		repliedTo, found := jobMessageByID(job, m.ReplyToMessageID)
		if !found || repliedTo.CreatedRevision >= m.CreatedRevision ||
			repliedTo.Recipient != m.Sender || repliedTo.Sender != m.Recipient {
			return errors.New("JobMessage reply correlation is invalid")
		}
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

func validateJobMessageFacts(job Job) error {
	seenIDs:= make(map[string]struct{}, len(job.JobMessages))
	seenCommands:= make(map[string]struct{}, len(job.JobMessages))
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
	return nil
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
