package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

func NewTaskTurnDispatchObservation(
	job Job, claimID, principalID, commandID, status, evidenceSHA256 string, now time.Time,
) (TaskTurnDispatchObservation, error) {
	now = now.UTC().Round(0)
	if err := job.Validate(); err != nil {
		return TaskTurnDispatchObservation{}, fmt.Errorf("validate Job before Task Turn dispatch observation: %w", err)
	}
	if !validCommandID(commandID) || now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return TaskTurnDispatchObservation{}, errors.New("Task Turn dispatch observation command and timestamp are required")
	}
	claim, found := taskTurnDispatchClaimByID(job, claimID)
	if !found || claim.PrincipalID != principalID {
		return TaskTurnDispatchObservation{}, errors.New("Task Turn dispatch observation requires its claim owner")
	}
	if _, active := ActiveTaskTurnDispatchClaim(job, claim.OperationID); !active {
		return TaskTurnDispatchObservation{}, errors.New("Task Turn dispatch observation requires an active claim")
	}
	observation := TaskTurnDispatchObservation{
		ID: TaskTurnDispatchObservationID(commandID), CommandID: commandID,
		JobID: job.ID, ClaimID: claim.ID, TurnID: claim.TurnID, OperationID: claim.OperationID,
		PrincipalID: principalID, Status: status, EvidenceSHA256: evidenceSHA256,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := observation.Validate(job); err != nil {
		return TaskTurnDispatchObservation{}, err
	}
	return observation, nil
}

func (o TaskTurnDispatchObservation) Validate(job Job) error {
	if !validCommandID(o.CommandID) || o.ID != TaskTurnDispatchObservationID(o.CommandID) ||
		o.JobID != job.ID || !validPlanningIdentity(o.ClaimID) ||
		!validPlanningIdentity(o.TurnID) || !validPlanningIdentity(o.OperationID) ||
		!strings.HasPrefix(o.PrincipalID, "human_") || o.CreatedAt.IsZero() || o.CreatedRevision == 0 {
		return errors.New("TaskTurnDispatchObservation identity or metadata is invalid")
	}
	claim, found := taskTurnDispatchClaimByID(job, o.ClaimID)
	if !found || claim.TurnID != o.TurnID || claim.OperationID != o.OperationID ||
		claim.PrincipalID != o.PrincipalID || o.CreatedRevision <= claim.CreatedRevision ||
		o.CreatedAt.Before(claim.CreatedAt) {
		return errors.New("TaskTurnDispatchObservation does not match its claim")
	}
	for _, release := range job.TaskTurnDispatchReleases {
		if release.ClaimID == claim.ID && release.CreatedRevision < o.CreatedRevision {
			return errors.New("TaskTurnDispatchObservation was appended after its claim was released")
		}
	}
	start, started := taskTurnDispatchObservationForClaim(job, claim.ID, TaskTurnDispatchObservationStarted)
	switch o.Status {
	case TaskTurnDispatchObservationStarted:
		if (started && start.ID != o.ID) || o.EvidenceSHA256 != "" {
			return errors.New("Task Turn dispatch start must be the first claim observation and contain no failure evidence")
		}
		if resumeStatus, found := LatestPlanningResumeStatus(job, claim.RoleExecutionID); found &&
			resumeStatus != PlanningResumeAttached {
			return errors.New("Task Turn dispatch start requires the latest Resume to be ATTACHED")
		}
	case TaskTurnDispatchObservationPreDispatchFailure:
		if started || !validSHA256(o.EvidenceSHA256) {
			return errors.New("pre-dispatch failure requires evidence and must precede dispatch start")
		}
	case TaskTurnDispatchObservationPreAdapterRejection, TaskTurnDispatchObservationDefinitiveFailure:
		if !started || start.CreatedRevision >= o.CreatedRevision || !validSHA256(o.EvidenceSHA256) {
			return errors.New("post-start failure requires prior dispatch start and evidence")
		}
	case TaskTurnDispatchObservationLookupNotFound:
		if !started || start.CreatedRevision >= o.CreatedRevision || !validSHA256(o.EvidenceSHA256) {
			return errors.New("Lookup NOT_FOUND requires a prior dispatch start and evidence")
		}
	default:
		return errors.New("unsupported Task Turn dispatch observation status")
	}
	return nil
}

func validateTaskTurnDispatchObservations(job Job) error {
	seenIDs := make(map[string]struct{}, len(job.TaskTurnDispatchObservations))
	seenCommands := make(map[string]struct{}, len(job.TaskTurnDispatchObservations))
	byClaim := make(map[string][]TaskTurnDispatchObservation)
	var previousRevision uint64
	var previousAt time.Time
	for _, observation := range job.TaskTurnDispatchObservations {
		if err := observation.Validate(job); err != nil {
			return fmt.Errorf("%w: invalid TaskTurnDispatchObservation: %v", ErrInvalidJob, err)
		}
		if observation.CreatedAt.After(job.UpdatedAt) || observation.CreatedRevision > job.Revision ||
			observation.CreatedRevision <= previousRevision || observation.CreatedAt.Before(previousAt) {
			return fmt.Errorf("%w: TaskTurnDispatchObservation append order is invalid", ErrInvalidJob)
		}
		previousRevision, previousAt = observation.CreatedRevision, observation.CreatedAt
		if _, ok := seenIDs[observation.ID]; ok {
			return fmt.Errorf("%w: duplicate TaskTurnDispatchObservation ID", ErrInvalidJob)
		}
		if _, ok := seenCommands[observation.CommandID]; ok {
			return fmt.Errorf("%w: duplicate TaskTurnDispatchObservation command", ErrInvalidJob)
		}
		seenIDs[observation.ID] = struct{}{}
		seenCommands[observation.CommandID] = struct{}{}
		byClaim[observation.ClaimID] = append(byClaim[observation.ClaimID], observation)
	}
	for claimID, observations := range byClaim {
		started, preDispatchFailure, postStartFailure, lookupNotFound := 0, 0, 0, 0
		for _, observation := range observations {
			switch observation.Status {
			case TaskTurnDispatchObservationStarted:
				started++
			case TaskTurnDispatchObservationPreDispatchFailure:
				preDispatchFailure++
			case TaskTurnDispatchObservationPreAdapterRejection, TaskTurnDispatchObservationDefinitiveFailure:
				postStartFailure++
			case TaskTurnDispatchObservationLookupNotFound:
				lookupNotFound++
			}
		}
		if started > 1 || preDispatchFailure > 1 || postStartFailure > 1 || lookupNotFound > 1 ||
			(preDispatchFailure > 0 && (started > 0 || postStartFailure > 0 || lookupNotFound > 0)) ||
			(postStartFailure > 0 && lookupNotFound > 0) {
			return fmt.Errorf("%w: conflicting Task Turn dispatch observations for claim %s", ErrInvalidJob, claimID)
		}
	}
	return nil
}
