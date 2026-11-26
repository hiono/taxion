package store

import (
	"context"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

const addJobMessageCommandType = "AddJobMessage"

func (s *Store) AddJobMessage(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	commandID string,
	request domain.JobMessageRequest,
) (CommandReceipt, domain.JobMessage, error) {
	return persistPlanningFact(
		ctx, s, jobID, expectedRevision, commandID, addJobMessageCommandType, request,
		func(job domain.Job, now time.Time) (domain.JobMessage, error) {
			return domain.NewJobMessage(job, commandID, request, now)
		},
		func(job *domain.Job, message domain.JobMessage) {
			job.JobMessages = append(job.JobMessages, message)
		},
		func(job domain.Job) (domain.JobMessage, bool) {
			for _, message := range job.JobMessages {
				if message.CommandID == commandID {
					return message, true
				}
			}
			return domain.JobMessage{}, false
		},
	)
}
