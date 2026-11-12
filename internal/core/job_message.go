package core

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// JobMessage is an append-only natural-language exchange attached to a Job.
// It records collaboration, not authorization or a new execution grant.
type JobMessage struct {
	ID               string    `json:"id"`
	TaskID           string    `json:"taskId"`
	FromRole         string    `json:"fromRole"`
	ToRole           string    `json:"toRole"`
	Body             string    `json:"body"`
	Source           string    `json:"source"`
	SourceDispatchID string    `json:"sourceDispatchId,omitempty"`
	ReplyTo          string    `json:"replyTo,omitempty"`
	At               time.Time `json:"at"`
}

var collaborationRoles = []string{"PLANNER", "CODER", "REVIEWER", "HUMAN"}

// JobConversation returns the Job's collaboration history in append order.
func (g TaskGraph) JobConversation(taskID string) []JobMessage {
	messages := make([]JobMessage, 0)
	for _, message := range g.JobMessages {
		if message.TaskID == taskID {
			messages = append(messages, message)
		}
	}
	return messages
}

// AppendJobMessage adds a natural-language exchange without imposing a
// question category, answer schema, or semantic-completeness requirement.
func (g *TaskGraph) AppendJobMessage(message JobMessage) error {
	if g == nil {
		return graphError("Job collaboration requires a Task Graph")
	}
	next := cloneGraph(*g)
	if message.At.IsZero() {
		message.At = time.Now().UTC()
	}
	next.JobMessages = append(next.JobMessages, message)
	if err := next.Validate(); err != nil {
		return err
	}
	*g = next
	return nil
}

func (g TaskGraph) validateJobMessages() error {
	taskIDs := make(map[string]bool, len(g.Tasks))
	for _, task := range g.Tasks {
		taskIDs[task.ID] = true
	}
	dispatches := make(map[string]GraphDispatch, len(g.Dispatches))
	for _, dispatch := range g.Dispatches {
		dispatches[dispatch.ID] = dispatch
	}
	seen := make(map[string]JobMessage, len(g.JobMessages))
	for _, message := range g.JobMessages {
		if strings.TrimSpace(message.ID) == "" || seen[message.ID].ID != "" ||
			!taskIDs[message.TaskID] || !slices.Contains(collaborationRoles, message.FromRole) ||
			!slices.Contains(collaborationRoles, message.ToRole) || message.FromRole == message.ToRole ||
			strings.TrimSpace(message.Body) == "" || strings.TrimSpace(message.Source) == "" ||
			message.At.IsZero() {
			return graphError("invalid Job collaboration message %q", message.ID)
		}
		if message.SourceDispatchID != "" {
			dispatch, ok := dispatches[message.SourceDispatchID]
			if !ok || dispatch.TaskID != message.TaskID || dispatch.Recipient != message.FromRole {
				return graphError("Job message source Dispatch does not match its Job and sender")
			}
		}
		if message.ReplyTo != "" {
			prior, ok := seen[message.ReplyTo]
			if !ok || prior.TaskID != message.TaskID ||
				prior.ToRole != message.FromRole || prior.FromRole != message.ToRole {
				return graphError("Job message reply does not answer an earlier exchange addressed to its sender")
			}
		}
		seen[message.ID] = message
	}
	return nil
}
