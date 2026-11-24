package store

import (
	"context"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
)

const addTaskTurnDispatchObservationCommandType = "AddTaskTurnDispatchObservation"

func (s *Store) AddTaskTurnDispatchObservation(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	claimID, commandID, status, evidenceSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.TaskTurnDispatchObservation, bool, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.TaskTurnDispatchObservation{}, false, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, ClaimID, CommandID, Status, EvidenceSHA256, PrincipalID string
	}{jobID, claimID, commandID, status, evidenceSHA256, principal.PrincipalID()}
	return persistPlanningFactWithStatus(
		ctx, s, jobID, expectedRevision, commandID, addTaskTurnDispatchObservationCommandType, request,
		func(job domain.Job, now time.Time) (domain.TaskTurnDispatchObservation, error) {
			return domain.NewTaskTurnDispatchObservation(job, claimID, principal.PrincipalID(), commandID, status, evidenceSHA256, now)
		},
		func(job *domain.Job, fact domain.TaskTurnDispatchObservation) {
			job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, fact)
		},
		func(job domain.Job) (domain.TaskTurnDispatchObservation, bool) {
			for _, fact := range job.TaskTurnDispatchObservations {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.TaskTurnDispatchObservation{}, false
		},
	)
}
