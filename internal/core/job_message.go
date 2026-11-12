package core

import (
	"strings"
	"time"
)

// JobMessage is an append-only natural-language exchange attached to a Job.
// TaskID and SourceDispatchID remain optional legacy provenance fields.
type JobMessage struct {
	ID                string    `json:"id"`
	JobID             string    `json:"jobId,omitempty"`
	TaskID            string    `json:"taskId,omitempty"`
	FromRole          string    `json:"fromRole"`
	ToRole            string    `json:"toRole"`
	Body              string    `json:"body"`
	Source            string    `json:"source"`
	SourceDispatchID  string    `json:"sourceDispatchId,omitempty"`
	SourceExecutionID string    `json:"sourceExecutionId,omitempty"`
	TargetExecutionID string    `json:"targetExecutionId,omitempty"`
	ReplyTo           string    `json:"replyTo,omitempty"`
	At                time.Time `json:"at"`
}

// JobConversation returns a Job's collaboration history in append order.
// Legacy records without JobID are addressed by their former TaskID.
func (g TaskGraph) JobConversation(jobID string) []JobMessage {
	messages := make([]JobMessage, 0)
	for _, message := range g.JobMessages {
		if message.JobID == jobID || message.JobID == "" && message.TaskID == jobID {
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
	if err := next.validateJobModel(); err != nil {
		return err
	}
	if err := next.validateJobMessages(); err != nil {
		return err
	}
	*g = next
	return nil
}

func (g TaskGraph) validateJobMessages() error {
	jobs := make(map[string]Job, len(g.Jobs))
	for _, job := range g.Jobs {
		jobs[job.ID] = job
	}
	tasks := make(map[string]GraphTask, len(g.Tasks))
	for _, task := range g.Tasks {
		tasks[task.ID] = task
	}
	dispatches := make(map[string]GraphDispatch, len(g.Dispatches))
	for _, dispatch := range g.Dispatches {
		dispatches[dispatch.ID] = dispatch
	}
	executions := make(map[string]RoleExecution, len(g.RoleExecutions))
	for _, execution := range g.RoleExecutions {
		executions[execution.ID] = execution
	}
	seen := make(map[string]JobMessage, len(g.JobMessages))
	for _, message := range g.JobMessages {
		if strings.TrimSpace(message.ID) == "" || seen[message.ID].ID != "" ||
			strings.TrimSpace(message.FromRole) == "" || strings.TrimSpace(message.ToRole) == "" ||
			message.FromRole == message.ToRole || strings.TrimSpace(message.Body) == "" ||
			strings.TrimSpace(message.Source) == "" || message.At.IsZero() {
			return graphError("invalid Job collaboration message %q", message.ID)
		}
		if message.JobID != "" {
			if _, ok := jobs[message.JobID]; !ok {
				return graphError("Job message references an unknown Job")
			}
			if message.TaskID != "" {
				task, ok := tasks[message.TaskID]
				if !ok || task.JobID != message.JobID {
					return graphError("Job message references a Task outside its Job")
				}
			}
			if message.FromRole != "HUMAN" && message.SourceExecutionID == "" {
				return graphError("Role-originated Job messages require source execution provenance")
			}
		} else {
			if message.TaskID == "" {
				return graphError("legacy Job messages require a Task ID")
			}
			if _, ok := tasks[message.TaskID]; !ok {
				return graphError("legacy Job message references an unknown Task")
			}
			if (message.FromRole == "CODER" || message.FromRole == "REVIEWER") && message.SourceDispatchID == "" {
				return graphError("legacy Coder and Reviewer Job messages require a source Dispatch")
			}
		}
		if message.SourceDispatchID != "" {
			dispatch, ok := dispatches[message.SourceDispatchID]
			if !ok || dispatch.TaskID != message.TaskID || dispatch.Recipient != message.FromRole {
				return graphError("Job message source Dispatch does not match its Task and sender")
			}
		}
		if message.SourceExecutionID != "" {
			execution, ok := executions[message.SourceExecutionID]
			if !ok || execution.JobID != message.JobID || execution.Role != message.FromRole {
				return graphError("Job message source execution does not match its Job and sender")
			}
		}
		if message.TargetExecutionID != "" {
			execution, ok := executions[message.TargetExecutionID]
			if !ok || execution.JobID != message.JobID || execution.Role != message.ToRole {
				return graphError("Job message target execution does not match its Job and recipient")
			}
		}
		if message.ReplyTo != "" {
			prior, ok := seen[message.ReplyTo]
			if !ok || prior.FromRole != message.ToRole || prior.ToRole != message.FromRole ||
				(message.JobID != "" && prior.JobID != message.JobID) ||
				(message.JobID == "" && (prior.JobID != "" || prior.TaskID != message.TaskID)) {
				return graphError("Job message reply does not answer an earlier exchange in the same Job and role pair")
			}
		}
		seen[message.ID] = message
	}
	return nil
}
