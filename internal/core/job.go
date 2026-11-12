package core

import (
	"slices"
	"strings"
	"time"
)

// Job is the durable unit of user intent and collaboration. Tasks are
// independently identified work items within a Job.
type Job struct {
	ID          string    `json:"id"`
	Request     string    `json:"request"`
	Goal        string    `json:"goal"`
	RootTaskID  string    `json:"rootTaskId"`
	TaskIDs     []string  `json:"taskIds"`
	Constraints []string  `json:"constraints,omitempty"`
	Status      JobStatus `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type JobStatus string

const (
	JobDraft     JobStatus = "DRAFT"
	JobActive    JobStatus = "ACTIVE"
	JobWaiting   JobStatus = "WAITING"
	JobCompleted JobStatus = "COMPLETED"
	JobFailed    JobStatus = "FAILED"
	JobCancelled JobStatus = "CANCELLED"
)

func validJobStatus(status JobStatus) bool {
	return slices.Contains([]JobStatus{JobDraft, JobActive, JobWaiting, JobCompleted, JobFailed, JobCancelled}, status)
}

type RoleExecutionStatus string

const (
	RoleExecutionCreated         RoleExecutionStatus = "CREATED"
	RoleExecutionRunning         RoleExecutionStatus = "RUNNING"
	RoleExecutionWaiting         RoleExecutionStatus = "WAITING"
	RoleExecutionCancelRequested RoleExecutionStatus = "CANCEL_REQUESTED"
	RoleExecutionSuspended       RoleExecutionStatus = "SUSPENDED"
	RoleExecutionSucceeded       RoleExecutionStatus = "SUCCEEDED"
	RoleExecutionFailed          RoleExecutionStatus = "FAILED"
	RoleExecutionCancelled       RoleExecutionStatus = "CANCELLED"
)

// RoleExecution is one replaceable execution by one Role. Its lifetime is
// independent of the Job and of other RoleExecutions in that Job.
type RoleExecutionEvent struct {
	ExecutionID string              `json:"executionId"`
	From        RoleExecutionStatus `json:"from,omitempty"`
	To          RoleExecutionStatus `json:"to"`
	Actor       string              `json:"actor"`
	Reason      string              `json:"reason"`
	At          time.Time           `json:"at"`
}

type RoleExecution struct {
	ID                string              `json:"id"`
	JobID             string              `json:"jobId"`
	TaskID            string              `json:"taskId,omitempty"`
	Role              string              `json:"role"`
	ParentExecutionID string              `json:"parentExecutionId,omitempty"`
	TriggerMessageID  string              `json:"triggerMessageId,omitempty"`
	Status            RoleExecutionStatus `json:"status"`
	RuntimeSessionID  string              `json:"runtimeSessionId,omitempty"`
	RuntimeConfigHash string              `json:"runtimeConfigHash,omitempty"`
	ContextHash       string              `json:"contextHash,omitempty"`
	ResultHash        string              `json:"resultHash,omitempty"`
	StopReason        string              `json:"stopReason,omitempty"`
	CreatedAt         time.Time           `json:"createdAt"`
	StartedAt         time.Time           `json:"startedAt,omitempty"`
	UpdatedAt         time.Time           `json:"updatedAt"`
	FinishedAt        time.Time           `json:"finishedAt,omitempty"`
}

func validRoleExecutionStatus(status RoleExecutionStatus) bool {
	return slices.Contains([]RoleExecutionStatus{
		RoleExecutionCreated, RoleExecutionRunning, RoleExecutionWaiting,
		RoleExecutionCancelRequested, RoleExecutionSuspended,
		RoleExecutionSucceeded, RoleExecutionFailed, RoleExecutionCancelled,
	}, status)
}

func roleExecutionTerminal(status RoleExecutionStatus) bool {
	return slices.Contains([]RoleExecutionStatus{
		RoleExecutionSucceeded, RoleExecutionFailed, RoleExecutionCancelled,
	}, status)
}

func roleExecutionTransitionAllowed(from, to RoleExecutionStatus) bool {
	switch from {
	case RoleExecutionCreated:
		return to == RoleExecutionRunning || to == RoleExecutionCancelled
	case RoleExecutionRunning:
		return slices.Contains([]RoleExecutionStatus{
			RoleExecutionWaiting, RoleExecutionCancelRequested, RoleExecutionSuspended,
			RoleExecutionSucceeded, RoleExecutionFailed,
		}, to)
	case RoleExecutionWaiting:
		return to == RoleExecutionRunning || to == RoleExecutionCancelled ||
			to == RoleExecutionFailed || to == RoleExecutionSuspended
	case RoleExecutionCancelRequested:
		return to == RoleExecutionCancelled || to == RoleExecutionFailed ||
			to == RoleExecutionSuspended
	case RoleExecutionSuspended:
		return to == RoleExecutionRunning || to == RoleExecutionCancelled
	default:
		return false
	}
}

func (g TaskGraph) Job(id string) (Job, bool) {
	for _, job := range g.Jobs {
		if job.ID == id {
			job.Constraints = slices.Clone(job.Constraints)
			return job, true
		}
	}
	return Job{}, false
}

// JobForTask resolves the Job that owns a Task without changing the Task contract.
func (g TaskGraph) JobForTask(taskID string) (Job, bool) {
	for _, job := range g.Jobs {
		if slices.Contains(job.TaskIDs, taskID) {
			job.Constraints = slices.Clone(job.Constraints)
			job.TaskIDs = slices.Clone(job.TaskIDs)
			return job, true
		}
	}
	return Job{}, false
}

// CreateJob binds an existing root Task to a new Job. Job membership is stored
// on the Job so existing Task approval hashes and history remain unchanged.
func (g *TaskGraph) CreateJob(job Job) error {
	if g == nil {
		return graphError("Job creation requires a Task Graph")
	}
	next := cloneGraph(*g)
	if job.CreatedAt.IsZero() {
		job.CreatedAt = time.Now().UTC()
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = job.CreatedAt
	}
	if len(job.TaskIDs) == 0 && job.RootTaskID != "" {
		job.TaskIDs = []string{job.RootTaskID}
	}
	next.Jobs = append(next.Jobs, job)
	if err := next.validateJobModel(); err != nil {
		return err
	}
	*g = next
	return nil
}

// AttachTaskToJob adds a separately identified work item to an existing Job.
func (g *TaskGraph) AttachTaskToJob(jobID, taskID string) error {
	if g == nil || strings.TrimSpace(jobID) == "" || strings.TrimSpace(taskID) == "" {
		return graphError("Task attachment requires a Job and Task ID")
	}
	next := cloneGraph(*g)
	if _, ok := next.GraphTask(taskID); !ok {
		return graphError("Task %s does not exist", taskID)
	}
	for i := range next.Jobs {
		if next.Jobs[i].ID != jobID {
			if slices.Contains(next.Jobs[i].TaskIDs, taskID) {
				return graphError("Task %s already belongs to another Job", taskID)
			}
			continue
		}
		if !slices.Contains(next.Jobs[i].TaskIDs, taskID) {
			next.Jobs[i].TaskIDs = append(next.Jobs[i].TaskIDs, taskID)
			next.Jobs[i].UpdatedAt = time.Now().UTC()
		}
		if err := next.validateJobModel(); err != nil {
			return err
		}
		*g = next
		return nil
	}
	return graphError("Job %s not found", jobID)
}

func (g TaskGraph) RoleExecution(id string) (RoleExecution, bool) {
	for _, execution := range g.RoleExecutions {
		if execution.ID == id {
			return execution, true
		}
	}
	return RoleExecution{}, false
}

func (g TaskGraph) RoleExecutionsForJob(jobID string) []RoleExecution {
	result := make([]RoleExecution, 0)
	for _, execution := range g.RoleExecutions {
		if execution.JobID == jobID {
			result = append(result, execution)
		}
	}
	return result
}

// AppendRoleExecution creates a durable execution record, not an execution
// grant. The Controller must independently authorize every Runtime operation.
func (g *TaskGraph) AppendRoleExecution(execution RoleExecution) error {
	if g == nil {
		return graphError("RoleExecution requires a Task Graph")
	}
	next := cloneGraph(*g)
	if execution.Status != RoleExecutionCreated || !execution.StartedAt.IsZero() || !execution.FinishedAt.IsZero() {
		return graphError("new RoleExecution must begin in CREATED state")
	}
	if execution.CreatedAt.IsZero() {
		execution.CreatedAt = time.Now().UTC()
	}
	if execution.UpdatedAt.IsZero() {
		execution.UpdatedAt = execution.CreatedAt
	}
	next.RoleExecutions = append(next.RoleExecutions, execution)
	next.RoleExecutionHistory = append(next.RoleExecutionHistory, RoleExecutionEvent{
		ExecutionID: execution.ID, To: RoleExecutionCreated, Actor: "controller",
		Reason: "execution created", At: execution.CreatedAt,
	})
	if err := next.validateJobModel(); err != nil {
		return err
	}
	*g = next
	return nil
}

// TransitionRoleExecution records a lifecycle transition. It does not
// authorize a Runtime operation or bypass Controller checks.
func (g *TaskGraph) TransitionRoleExecution(id string, status RoleExecutionStatus, actor, reason string, at time.Time) error {
	if g == nil || strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return graphError("RoleExecution transition requires graph, actor and reason")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	next := cloneGraph(*g)
	for i := range next.RoleExecutions {
		execution := &next.RoleExecutions[i]
		if execution.ID != id {
			continue
		}
		if !roleExecutionTransitionAllowed(execution.Status, status) {
			return graphError("invalid RoleExecution transition %s -> %s", execution.Status, status)
		}
		previous := execution.Status
		execution.Status = status
		execution.UpdatedAt = at
		if status == RoleExecutionRunning && execution.StartedAt.IsZero() {
			execution.StartedAt = at
		}
		if roleExecutionTerminal(status) {
			execution.FinishedAt = at
		}
		if status == RoleExecutionSuspended || status == RoleExecutionFailed || status == RoleExecutionCancelled || status == RoleExecutionCancelRequested {
			execution.StopReason = reason
		}
		next.RoleExecutionHistory = append(next.RoleExecutionHistory, RoleExecutionEvent{
			ExecutionID: id, From: previous, To: status, Actor: actor, Reason: reason, At: at,
		})
		if err := next.validateJobModel(); err != nil {
			return err
		}
		*g = next
		return nil
	}
	return graphError("RoleExecution %s not found", id)
}

// BindRoleExecutionSession permanently binds one Runtime session to one
// RoleExecution and one configuration identity.
func (g *TaskGraph) BindRoleExecutionSession(id, sessionID, configHash string) error {
	if g == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(configHash) == "" {
		return graphError("Runtime binding requires a RoleExecution, session and configuration hash")
	}
	next := cloneGraph(*g)
	for i := range next.RoleExecutions {
		execution := &next.RoleExecutions[i]
		if execution.ID != id {
			continue
		}
		if execution.Status != RoleExecutionRunning {
			return graphError("Runtime session binding requires a running RoleExecution")
		}
		if execution.RuntimeSessionID != "" &&
			(execution.RuntimeSessionID != sessionID || execution.RuntimeConfigHash != configHash) {
			return graphError("RoleExecution is already bound to another Runtime session")
		}
		for _, other := range next.RoleExecutions {
			if other.ID != id && other.RuntimeSessionID == sessionID {
				return graphError("Runtime session cannot be shared by RoleExecutions")
			}
		}
		execution.RuntimeSessionID = sessionID
		execution.RuntimeConfigHash = configHash
		if err := next.validateJobModel(); err != nil {
			return err
		}
		*g = next
		return nil
	}
	return graphError("RoleExecution %s not found", id)
}

func (g TaskGraph) validateJobModel() error {
	tasks := make(map[string]GraphTask, len(g.Tasks))
	for _, task := range g.Tasks {
		tasks[task.ID] = task
	}
	jobs := make(map[string]Job, len(g.Jobs))
	taskOwners := make(map[string]string)
	for _, job := range g.Jobs {
		if strings.TrimSpace(job.ID) == "" || jobs[job.ID].ID != "" ||
			strings.TrimSpace(job.Request) == "" || strings.TrimSpace(job.Goal) == "" ||
			strings.TrimSpace(job.RootTaskID) == "" || !validJobStatus(job.Status) ||
			job.CreatedAt.IsZero() || job.UpdatedAt.Before(job.CreatedAt) ||
			!validList(job.Constraints) || !validList(job.TaskIDs) ||
			!slices.Contains(job.TaskIDs, job.RootTaskID) {
			return graphError("invalid or duplicate Job %q", job.ID)
		}
		root, ok := tasks[job.RootTaskID]
		if !ok || root.ParentID != "" || root.ID == job.ID {
			return graphError("Job %s requires a distinct registered root Task", job.ID)
		}
		for _, taskID := range job.TaskIDs {
			task, ok := tasks[taskID]
			if !ok || taskID == job.ID || taskOwners[taskID] != "" {
				return graphError("Job %s contains a missing, colliding or multiply-owned Task", job.ID)
			}
			taskOwners[taskID] = job.ID
			_ = task
		}
		jobs[job.ID] = job
	}
	for _, job := range g.Jobs {
		for _, taskID := range job.TaskIDs {
			if taskID == job.ID {
				return graphError("Job and Task identities must be distinct")
			}
		}
	}
	for _, task := range g.Tasks {
		if _, collides := jobs[task.ID]; collides {
			return graphError("Job and Task identities must be distinct")
		}
	}
	if len(g.Jobs) == 0 && len(g.RoleExecutions) != 0 {
		return graphError("RoleExecutions require first-class Jobs")
	}
	executions := make(map[string]RoleExecution, len(g.RoleExecutions))
	sessions := make(map[string]string, len(g.RoleExecutions))
	for _, execution := range g.RoleExecutions {
		_, jobOK := jobs[execution.JobID]
		if strings.TrimSpace(execution.ID) == "" || executions[execution.ID].ID != "" ||
			!jobOK || execution.ID == execution.JobID || jobs[execution.ID].ID != "" || tasks[execution.ID].ID != "" ||
			strings.TrimSpace(execution.Role) == "" || !validRoleExecutionStatus(execution.Status) ||
			execution.CreatedAt.IsZero() || execution.UpdatedAt.Before(execution.CreatedAt) ||
			(execution.RuntimeSessionID == "") != (execution.RuntimeConfigHash == "") ||
			(execution.Status == RoleExecutionCreated && !execution.StartedAt.IsZero()) ||
			(execution.Status != RoleExecutionCreated && execution.Status != RoleExecutionCancelled && execution.StartedAt.IsZero()) ||
			(roleExecutionTerminal(execution.Status) != !execution.FinishedAt.IsZero()) {
			return graphError("invalid RoleExecution %q", execution.ID)
		}
		if execution.TaskID != "" && taskOwners[execution.TaskID] != execution.JobID {
			return graphError("RoleExecution %s references a Task outside its Job", execution.ID)
		}
		if execution.ParentExecutionID != "" && execution.ParentExecutionID == execution.ID {
			return graphError("RoleExecution cannot be its own parent")
		}
		if execution.RuntimeSessionID != "" {
			if previous := sessions[execution.RuntimeSessionID]; previous != "" {
				return graphError("Runtime session is shared by RoleExecutions %s and %s", previous, execution.ID)
			}
			sessions[execution.RuntimeSessionID] = execution.ID
		}
		executions[execution.ID] = execution
	}
	for _, execution := range g.RoleExecutions {
		if execution.ParentExecutionID != "" {
			parent, ok := executions[execution.ParentExecutionID]
			if !ok || parent.JobID != execution.JobID {
				return graphError("RoleExecution %s has a missing or cross-Job parent", execution.ID)
			}
		}
	}
	visiting, visited := map[string]bool{}, map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return graphError("RoleExecution parent cycle at %s", id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		if parent := executions[id].ParentExecutionID; parent != "" {
			if err := visit(parent); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for id := range executions {
		if err := visit(id); err != nil {
			return err
		}
	}
	states := make(map[string]RoleExecutionStatus, len(g.RoleExecutions))
	lastEventAt := make(map[string]time.Time, len(g.RoleExecutions))
	for _, event := range g.RoleExecutionHistory {
		execution, ok := executions[event.ExecutionID]
		if !ok || strings.TrimSpace(event.Actor) == "" || strings.TrimSpace(event.Reason) == "" ||
			event.At.IsZero() || event.At.Before(execution.CreatedAt) ||
			(!lastEventAt[event.ExecutionID].IsZero() && event.At.Before(lastEventAt[event.ExecutionID])) {
			return graphError("invalid RoleExecution history event")
		}
		previous, exists := states[event.ExecutionID]
		if !exists {
			if event.From != "" || event.To != RoleExecutionCreated {
				return graphError("RoleExecution history must begin at CREATED")
			}
		} else if event.From != previous || !roleExecutionTransitionAllowed(previous, event.To) {
			return graphError("invalid RoleExecution history transition")
		}
		states[event.ExecutionID] = event.To
		lastEventAt[event.ExecutionID] = event.At
	}
	for _, execution := range g.RoleExecutions {
		if states[execution.ID] != execution.Status {
			return graphError("RoleExecution %s does not match its transition history", execution.ID)
		}
		if execution.TriggerMessageID != "" {
			found := false
			for _, message := range g.JobMessages {
				if message.ID == execution.TriggerMessageID && message.JobID == execution.JobID {
					found = true
					break
				}
			}
			if !found {
				return graphError("RoleExecution %s has a missing or cross-Job trigger message", execution.ID)
			}
		}
	}
	return nil
}
