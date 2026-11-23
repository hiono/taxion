package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

type JobOutput struct {
	ID                          string    `json:"id"`
	CommandID                   string    `json:"command_id"`
	JobID                       string    `json:"job_id"`
	TaskID                      string    `json:"task_id"`
	RoleExecutionID             string    `json:"role_execution_id"`
	WorktreeAssignmentID        string    `json:"worktree_assignment_id"`
	RepositoryID                string    `json:"repository_id"`
	CommitSHA                   string    `json:"commit_sha"`
	TreeSHA                     string    `json:"tree_sha"`
	TurnID                      string    `json:"turn_id"`
	ProviderResultObservationID string    `json:"provider_result_observation_id"`
	ProviderStopIntentID        string    `json:"provider_stop_intent_id"`
	ProviderStopEvidenceSHA256  string    `json:"provider_stop_evidence_sha256"`
	CreatedAt                   time.Time `json:"created_at"`
	CreatedRevision             uint64    `json:"created_revision"`
}

func JobOutputCommandID(taskID, roleExecutionID, turnID, commitSHA, treeSHA string) string {
	material := strings.Join([]string{taskID, roleExecutionID, turnID, commitSHA, treeSHA}, "\x00")
	sum := sha256.Sum256([]byte("taxion:job-output:" + material))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func JobOutputID(commandID string) string {
	return planningOperationID("job_output", commandID)
}

func NewJobOutput(job Job, taskID, roleExecutionID, turnID, commitSHA, treeSHA string, now time.Time) (JobOutput, error) {
	if err := job.Validate(); err != nil {
		return JobOutput{}, err
	}
	now = now.UTC().Round(0)
	if now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return JobOutput{}, errors.New("JobOutput timestamp or Job revision is invalid")
	}
	commandID := JobOutputCommandID(taskID, roleExecutionID, turnID, commitSHA, treeSHA)
	output := JobOutput{
		ID: JobOutputID(commandID), CommandID: commandID, JobID: job.ID,
		TaskID: taskID, RoleExecutionID: roleExecutionID,
		CommitSHA: commitSHA, TreeSHA: treeSHA, TurnID: turnID,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := output.bindSources(job); err != nil {
		return JobOutput{}, err
	}
	if err := output.Validate(job); err != nil {
		return JobOutput{}, err
	}
	return output, nil
}

func (o JobOutput) Validate(job Job) error {
	if !validCommandID(o.CommandID) || o.ID != JobOutputID(o.CommandID) ||
		o.CommandID != JobOutputCommandID(o.TaskID, o.RoleExecutionID, o.TurnID, o.CommitSHA, o.TreeSHA) ||
		o.JobID != job.ID || !validPlanningIdentity(o.TaskID) ||
		!validPlanningIdentity(o.RoleExecutionID) || !validPlanningIdentity(o.TurnID) ||
		!validPlanningIdentity(o.WorktreeAssignmentID) || strings.TrimSpace(o.RepositoryID) == "" ||
		!validGitObjectID(o.CommitSHA) || !validGitObjectID(o.TreeSHA) ||
		!validPlanningIdentity(o.ProviderResultObservationID) ||
		!validPlanningIdentity(o.ProviderStopIntentID) || !validSHA256(o.ProviderStopEvidenceSHA256) ||
		o.CreatedAt.IsZero() || o.CreatedRevision == 0 {
		return errors.New("JobOutput identity or immutable Git snapshot metadata is invalid")
	}
	return o.bindSources(job)
}

func (o *JobOutput) bindSources(job Job) error {
	var task Task
	taskFound := false
	for _, candidate := range job.Tasks {
		if candidate.ID == o.TaskID {
			task, taskFound = candidate, true
			break
		}
	}
	var execution RoleExecution
	executionFound := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == o.RoleExecutionID {
			execution, executionFound = candidate, true
			break
		}
	}
	var assignment WorktreeAssignment
	assignmentFound := false
	for _, candidate := range job.WorktreeAssignments {
		if candidate.TaskID == o.TaskID && candidate.OwnerRoleExecutionID == o.RoleExecutionID {
			assignment, assignmentFound = candidate, true
			break
		}
	}
	var turn TaskTurnIntent
	turnFound := false
	for _, candidate := range job.TaskTurnIntents {
		if candidate.ID == o.TurnID {
			turn, turnFound = candidate, true
			break
		}
	}
	turnObservation, observed := latestTaskTurnObservation(job, o.TurnID)
	if !taskFound || !executionFound || !assignmentFound || !turnFound || len(task.TargetRepositories) != 1 ||
		execution.Role != RoleCoder || execution.TaskID != task.ID ||
		execution.WorktreeID != assignment.ID || assignment.ID != WorktreeID(job.ID, task.ID) ||
		assignment.RepositoryID != task.TargetRepositories[0] ||
		turn.RoleExecutionID != execution.ID || turn.TaskID != task.ID ||
		turn.ProviderSessionID == "" || !observed || turnObservation.Status != TaskTurnCompleted {
		return errors.New("JobOutput requires a completed Coder Turn and its exclusively assigned Task Worktree")
	}
	for _, candidate := range job.TaskTurnIntents {
		if candidate.RoleExecutionID == execution.ID && candidate.CreatedRevision > turn.CreatedRevision {
			return errors.New("JobOutput must reference the latest Coder Turn")
		}
	}
	var result ProviderResultObservation
	resultFound := false
	for _, candidate := range job.ProviderResultObservations {
		if (o.ProviderResultObservationID == "" && candidate.RoleExecutionID == execution.ID &&
			candidate.OperationID == turn.OperationID && candidate.TurnID == turn.ID &&
			candidate.SessionID == turn.ProviderSessionID) || candidate.ID == o.ProviderResultObservationID {
			result, resultFound = candidate, true
			break
		}
	}
	if !resultFound || !result.Final || result.RoleExecutionID != execution.ID ||
		result.OperationID != turn.OperationID || result.TurnID != turn.ID ||
		result.SessionID != turn.ProviderSessionID || result.EvidenceSHA256 != turnObservation.EvidenceSHA256 {
		return errors.New("JobOutput must bind to the final Provider Result of its Coder Turn")
	}
	var usage ProviderUsageObservation
	usageFound := false
	for _, candidate := range job.ProviderUsageObservations {
		if candidate.RoleExecutionID == execution.ID && candidate.OperationID == turn.OperationID &&
			candidate.TurnID == turn.ID && candidate.SessionID == turn.ProviderSessionID &&
			(!usageFound || candidate.CreatedRevision > usage.CreatedRevision) {
			usage, usageFound = candidate, true
		}
	}
	if !usageFound || !usage.Complete {
		return errors.New("JobOutput requires complete Provider Usage for its Coder Turn")
	}
	var stopIntent ProviderStopIntent
	stopIntentFound := false
	for _, candidate := range job.ProviderStopIntents {
		if candidate.RoleExecutionID == execution.ID && candidate.SessionID == turn.ProviderSessionID &&
			(o.ProviderStopIntentID == "" || candidate.ID == o.ProviderStopIntentID) {
			stopIntent, stopIntentFound = candidate, true
			break
		}
	}
	var stopObservation ProviderStopObservation
	stopObservationFound := false
	if stopIntentFound {
		for _, candidate := range job.ProviderStopObservations {
			if candidate.OperationID == stopIntent.OperationID &&
				(!stopObservationFound || candidate.CreatedRevision > stopObservation.CreatedRevision) {
				stopObservation, stopObservationFound = candidate, true
			}
		}
	}
	if !stopIntentFound || !stopObservationFound || !stopObservation.Stopped ||
		!stopObservation.Quiescent || !validSHA256(stopObservation.EvidenceSHA256) {
		return errors.New("JobOutput requires verified Provider Stopped and Quiescent evidence")
	}
	if o.WorktreeAssignmentID == "" {
		o.WorktreeAssignmentID = assignment.ID
	}
	if o.RepositoryID == "" {
		o.RepositoryID = assignment.RepositoryID
	}
	if o.ProviderResultObservationID == "" {
		o.ProviderResultObservationID = result.ID
	}
	if o.ProviderStopIntentID == "" {
		o.ProviderStopIntentID = stopIntent.ID
	}
	if o.ProviderStopEvidenceSHA256 == "" {
		o.ProviderStopEvidenceSHA256 = stopObservation.EvidenceSHA256
	}
	if o.WorktreeAssignmentID != assignment.ID || o.RepositoryID != assignment.RepositoryID ||
		o.ProviderResultObservationID != result.ID || o.ProviderStopIntentID != stopIntent.ID ||
		o.ProviderStopEvidenceSHA256 != stopObservation.EvidenceSHA256 {
		return errors.New("JobOutput provenance binding does not match its source facts")
	}
	minimumRevision := assignment.CreatedRevision
	minimumTime := assignment.CreatedAt
	for _, revision := range []uint64{turn.CreatedRevision, turnObservation.CreatedRevision, result.CreatedRevision, usage.CreatedRevision, stopIntent.CreatedRevision, stopObservation.CreatedRevision} {
		if revision > minimumRevision {
			minimumRevision = revision
		}
	}
	for _, timestamp := range []time.Time{turn.CreatedAt, turnObservation.CreatedAt, result.CreatedAt, usage.CreatedAt, stopIntent.CreatedAt, stopObservation.CreatedAt} {
		if timestamp.After(minimumTime) {
			minimumTime = timestamp
		}
	}
	if o.CreatedRevision != 0 && o.CreatedRevision <= minimumRevision {
		return errors.New("JobOutput must follow its Result, Usage, Stop, and Worktree source facts")
	}
	if !o.CreatedAt.IsZero() && o.CreatedAt.Before(minimumTime) {
		return errors.New("JobOutput timestamp precedes its source facts")
	}
	return nil
}

func validateJobOutputs(job Job) error {
	seenIDs := make(map[string]struct{}, len(job.JobOutputs))
	seenCommands := make(map[string]struct{}, len(job.JobOutputs))
	seenTurns := make(map[string]struct{}, len(job.JobOutputs))
	var previousRevision uint64
	var previousAt time.Time
	for _, output := range job.JobOutputs {
		if err := output.Validate(job); err != nil {
			return errors.New("invalid JobOutput: " + err.Error())
		}
		if output.CreatedAt.After(job.UpdatedAt) || output.CreatedRevision > job.Revision ||
			output.CreatedRevision <= previousRevision || output.CreatedAt.Before(previousAt) {
			return errors.New("JobOutput append order is invalid")
		}
		if _, exists := seenIDs[output.ID]; exists {
			return errors.New("duplicate JobOutput identity")
		}
		if _, exists := seenCommands[output.CommandID]; exists {
			return errors.New("duplicate JobOutput command identity")
		}
		if _, exists := seenTurns[output.TurnID]; exists {
			return errors.New("a Coder Turn has more than one immutable JobOutput")
		}
		seenIDs[output.ID] = struct{}{}
		seenCommands[output.CommandID] = struct{}{}
		seenTurns[output.TurnID] = struct{}{}
		previousRevision, previousAt = output.CreatedRevision, output.CreatedAt
	}
	return nil
}

func validGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}
