package store

import (
	"context"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

const (
	addJobQuestionTurnIntentCommandType      = "JOB_QUESTION_TURN_INTENT"
	addJobQuestionTurnObservationCommandType = "JOB_QUESTION_TURN_OBSERVATION"
)

func (s *Store) AddJobQuestionTurnIntent(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	request domain.JobQuestionTurnIntentRequest,
) (CommandReceipt, domain.JobQuestionTurnIntent, error) {
	return persistPlanningFact(
		ctx, s, jobID, expectedRevision, request.CommandID, addJobQuestionTurnIntentCommandType, request,
		func(job domain.Job, now time.Time) (domain.JobQuestionTurnIntent, error) {
			return domain.NewJobQuestionTurnIntent(job, request, now)
		},
		func(job *domain.Job, intent domain.JobQuestionTurnIntent) {
			job.JobQuestionTurnIntents = append(job.JobQuestionTurnIntents, intent)
		},
		func(job domain.Job) (domain.JobQuestionTurnIntent, bool) {
			for _, intent := range job.JobQuestionTurnIntents {
				if intent.CommandID == request.CommandID {
					return intent, true
				}
			}
			return domain.JobQuestionTurnIntent{}, false
		},
	)
}

func (s *Store) AddJobQuestionTurnObservation(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	request domain.JobQuestionTurnObservationRequest,
) (CommandReceipt, domain.JobQuestionTurnObservation, error) {
	return persistPlanningFact(
		ctx, s, jobID, expectedRevision, request.CommandID, addJobQuestionTurnObservationCommandType, request,
		func(job domain.Job, now time.Time) (domain.JobQuestionTurnObservation, error) {
			return domain.NewJobQuestionTurnObservation(job, request, now)
		},
		func(job *domain.Job, observation domain.JobQuestionTurnObservation) {
			job.JobQuestionTurnObservations = append(job.JobQuestionTurnObservations, observation)
		},
		func(job domain.Job) (domain.JobQuestionTurnObservation, bool) {
			for _, observation := range job.JobQuestionTurnObservations {
				if observation.CommandID == request.CommandID {
					return observation, true
				}
			}
			return domain.JobQuestionTurnObservation{}, false
		},
	)
}
