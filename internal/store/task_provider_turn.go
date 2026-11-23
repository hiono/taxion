package store

import (
	"context"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func (s *Store) AddTaskTurnIntent(
	ctx context.Context, jobID string, expectedRevision uint64,
	roleExecutionID, permissionID, sessionID, commandID, promptSHA256 string, reservedTokens uint64,
) (CommandReceipt, domain.TaskTurnIntent, error) {
	request := struct {
		JobID           string
		RoleExecutionID string
		PermissionID    string
		SessionID       string
		CommandID       string
		PromptSHA256    string
		ReservedTokens  uint64
	}{jobID, roleExecutionID, permissionID, sessionID, commandID, promptSHA256, reservedTokens}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, "TASK_PROVIDER_TURN_INTENT", request,
		func(job domain.Job, now time.Time) (domain.TaskTurnIntent, error) {
			return domain.NewTaskTurnIntent(job, roleExecutionID, permissionID, sessionID, commandID, promptSHA256, reservedTokens, now)
		},
		func(job *domain.Job, fact domain.TaskTurnIntent) {
			job.TaskTurnIntents = append(job.TaskTurnIntents, fact)
		},
		func(job domain.Job) (domain.TaskTurnIntent, bool) {
			for _, fact := range job.TaskTurnIntents {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.TaskTurnIntent{}, false
		},
	)
}

func (s *Store) AddTaskTurnObservation(
	ctx context.Context, jobID string, expectedRevision uint64,
	turnID, commandID, status string, tokensUsed uint64, evidenceSHA256 string, reconcilesUnknown bool,
) (CommandReceipt, domain.TaskTurnObservation, error) {
	request := struct {
		JobID             string
		TurnID            string
		CommandID         string
		Status            string
		EvidenceSHA256    string
		TokensUsed        uint64
		ReconcilesUnknown bool
	}{jobID, turnID, commandID, status, evidenceSHA256, tokensUsed, reconcilesUnknown}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, "TASK_PROVIDER_TURN_OBSERVATION", request,
		func(job domain.Job, now time.Time) (domain.TaskTurnObservation, error) {
			return domain.NewTaskTurnObservation(job, turnID, commandID, status, tokensUsed, evidenceSHA256, reconcilesUnknown, now)
		},
		func(job *domain.Job, fact domain.TaskTurnObservation) {
			job.TaskTurnObservations = append(job.TaskTurnObservations, fact)
		},
		func(job domain.Job) (domain.TaskTurnObservation, bool) {
			for _, fact := range job.TaskTurnObservations {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.TaskTurnObservation{}, false
		},
	)
}
