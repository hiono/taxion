package store

import (
	"context"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func (s *Store) AddJobQuestionAuthorization(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	request domain.JobQuestionAuthorizationRequest,
) (CommandReceipt, domain.JobQuestionAuthorization, error) {
	return persistPlanningFact(
		ctx, s, jobID, expectedRevision, request.CommandID, "JOB_QUESTION_AUTHORIZATION", request,
		func(job domain.Job, now time.Time) (domain.JobQuestionAuthorization, error) {
			return domain.NewJobQuestionAuthorization(job, request, now)
		},
		func(job *domain.Job, authorization domain.JobQuestionAuthorization) {
			job.JobQuestionAuthorizations = append(job.JobQuestionAuthorizations, authorization)
		},
		func(job domain.Job) (domain.JobQuestionAuthorization, bool) {
			for _, authorization := range job.JobQuestionAuthorizations {
				if authorization.CommandID == request.CommandID {
					return authorization, true
				}
			}
			return domain.JobQuestionAuthorization{}, false
		},
	)
}
