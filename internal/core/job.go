package core

import (
	"slices"
	"strings"
	"time"
)

// Job is the durable unit of user intent and collaboration. Tasks are
// independently identified work items within a Job.
type Job struct {
	ID          string       `json:"id"`
	Request     string       `json:"request"`
	Goal        string       `json:"goal"`
	Constraints []string     `json:"constraints,omitempty"`
	Status      JobStatus    `json:"status"`
	CreatedAt   time.Time    `json:"createdAt"`
	UpdatedAt   time.Time    `json:"updatedAt"`
	Contract    *JobContract `json:"contract,omitempty"`
}

type JobEvent struct {
	JobID  string    `json:"jobId"`
	From   JobStatus `json:"from,omitempty"`
	To     JobStatus `json:"to"`
	Actor  string    `json:"actor"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
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

func jobTransitionAllowed(from, to JobStatus) bool {
	switch from {
	case JobDraft:
		return to == JobActive || to == JobCancelled
	case JobActive:
		return slices.Contains([]JobStatus{JobWaiting, JobCompleted, JobFailed, JobCancelled}, to)
	case JobWaiting:
		return slices.Contains([]JobStatus{JobActive, JobCompleted, JobFailed, JobCancelled}, to)
	case JobFailed, JobCompleted:
		return to == JobWaiting
	default:
		return false
	}
}

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
			job.Contract = cloneJobContract(job.Contract)
			return job, true
		}
	}
	return Job{}, false
}

// JobForTask resolves ownership from the Task's canonical JobID reference.
func (g TaskGraph) JobForTask(taskID string) (Job, bool) {
	task, ok := g.GraphTask(taskID)
	if !ok || task.JobID == "" {
		return Job{}, false
	}
	return g.Job(task.JobID)
}

// CreateJob creates an independent collaboration unit. Tasks acquire membership
// by storing this Job's ID on the Task itself.
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
	next.Jobs = append(next.Jobs, job)
	next.JobHistory = append(next.JobHistory, JobEvent{
		JobID: job.ID, To: job.Status, Actor: "controller",
		Reason: "Job created from intake contract", At: job.CreatedAt,
	})
	if err := next.validateJobModel(); err != nil {
		return err
	}
	*g = next
	return nil
}

// TransitionJob records a Job state transition independently of Task state.
func (g *TaskGraph) TransitionJob(id string, status JobStatus, actor, reason string, at time.Time) error {
	if g == nil || strings.TrimSpace(actor) == "" || strings.TrimSpace(reason) == "" {
		return graphError("Job transition requires graph, actor and reason")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	next := cloneGraph(*g)
	if err := transitionJob(&next, id, status, actor, reason, at); err != nil {
		return err
	}
	if err := next.validateJobModel(); err != nil {
		return err
	}
	*g = next
	return nil
}

func transitionJob(graph *TaskGraph, id string, status JobStatus, actor, reason string, at time.Time) error {
	for i := range graph.Jobs {
		job := &graph.Jobs[i]
		if job.ID != id {
			continue
		}
		if job.Status == status {
			return nil
		}
		hasHistory := false
		for _, event := range graph.JobHistory {
			if event.JobID == id {
				hasHistory = true
				break
			}
		}
		if !hasHistory {
			graph.JobHistory = append(graph.JobHistory, JobEvent{
				JobID: id, To: job.Status, Actor: "controller",
				Reason: "existing Job state adopted", At: job.CreatedAt,
			})
		}
		if !jobTransitionAllowed(job.Status, status) {
			return graphError("invalid Job transition %s -> %s", job.Status, status)
		}
		if at.Before(job.UpdatedAt) {
			return graphError("Job transition timestamp precedes the latest Job update")
		}
		previous := job.Status
		job.Status = status
		job.UpdatedAt = at
		graph.JobHistory = append(graph.JobHistory, JobEvent{
			JobID: id, From: previous, To: status, Actor: actor, Reason: reason, At: at,
		})
		return nil
	}
	return graphError("Job %s not found", id)
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

func (g *TaskGraph) BindRoleExecutionContextHash(id, contextHash string) error {
	if g == nil || !validContentSHA256(contextHash) {
		return graphError("RoleExecution context binding requires a valid context hash")
	}
	next := cloneGraph(*g)
	for i := range next.RoleExecutions {
		execution := &next.RoleExecutions[i]
		if execution.ID != id {
			continue
		}
		if execution.Status != RoleExecutionRunning || execution.RuntimeSessionID == "" {
			return graphError("RoleExecution context binding requires a running, bound session")
		}
		execution.ContextHash = contextHash
		if err := next.validateJobModel(); err != nil {
			return err
		}
		*g = next
		return nil
	}
	return graphError("RoleExecution %s not found", id)
}

func (g TaskGraph) validateJobModel() error {
	if err := g.validateJobApprovals(); err != nil {
		return err
	}
	if err := g.validateJobArtifacts(); err != nil {
		return err
	}
	tasks := make(map[string]GraphTask, len(g.Tasks))
	for _, task := range g.Tasks {
		if task.ID == "" || tasks[task.ID].ID != "" {
			return graphError("invalid or duplicate Task identity %q", task.ID)
		}
		tasks[task.ID] = task
	}
	jobs := make(map[string]Job, len(g.Jobs))
	for _, job := range g.Jobs {
		if strings.TrimSpace(job.ID) == "" || jobs[job.ID].ID != "" ||
			strings.TrimSpace(job.Request) == "" || strings.TrimSpace(job.Goal) == "" ||
			!validJobStatus(job.Status) || job.CreatedAt.IsZero() || job.UpdatedAt.Before(job.CreatedAt) ||
			!validList(job.Constraints) {
			return graphError("invalid or duplicate Job %q", job.ID)
		}
		jobs[job.ID] = job
	}
	for _, task := range g.Tasks {
		if task.JobID == "" {
			continue // Unassigned legacy Tasks remain readable; new intake always assigns a Job.
		}
		owner, ok := jobs[task.JobID]
		if !ok || task.ID == task.JobID {
			return graphError("Task %s references a missing or colliding Job", task.ID)
		}
		if owner.Contract != nil && !TaskWithinJobScope(task, *owner.Contract) {
			return graphError("Task %s exceeds its Job repository or file-access scope", task.ID)
		}
	}
	for _, job := range g.Jobs {
		if _, collides := tasks[job.ID]; collides {
			return graphError("Job and Task identities must be distinct")
		}
	}
	jobStates := make(map[string]JobStatus, len(g.Jobs))
	jobEventAt := make(map[string]time.Time, len(g.Jobs))
	for _, event := range g.JobHistory {
		job, ok := jobs[event.JobID]
		if !ok || strings.TrimSpace(event.Actor) == "" || strings.TrimSpace(event.Reason) == "" ||
			!validJobStatus(event.To) || event.At.IsZero() || event.At.Before(job.CreatedAt) ||
			(!jobEventAt[event.JobID].IsZero() && event.At.Before(jobEventAt[event.JobID])) {
			return graphError("invalid Job history event")
		}
		previous, exists := jobStates[event.JobID]
		if !exists {
			if event.From != "" {
				return graphError("Job history must begin with its initial state")
			}
		} else if event.From != previous || !jobTransitionAllowed(previous, event.To) {
			return graphError("invalid Job history transition")
		}
		jobStates[event.JobID] = event.To
		jobEventAt[event.JobID] = event.At
	}
	for _, job := range g.Jobs {
		if state, exists := jobStates[job.ID]; exists && state != job.Status {
			return graphError("Job %s does not match its state history", job.ID)
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
		if owner := jobs[execution.JobID]; owner.Contract != nil && !slices.Contains(owner.Contract.Roles, execution.Role) {
			return graphError("RoleExecution %s uses a Role outside its Job contract", execution.ID)
		}
		if execution.TaskID != "" {
			task, ok := tasks[execution.TaskID]
			if !ok || task.JobID != execution.JobID {
				return graphError("RoleExecution %s references a Task outside its Job", execution.ID)
			}
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
