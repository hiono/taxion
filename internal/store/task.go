package store

import (
	"context"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func (s *Store) AddTask(
	ctx context.Context,
	expectedRevision uint64,
	request domain.TaskRequest,
) (CommandReceipt, domain.Task, error) {
	return persistPlanningFact(
		ctx, s, request.JobID, expectedRevision, request.CommandID, "TASK", request,
		func(job domain.Job, now time.Time) (domain.Task, error) {
			return domain.NewTask(job, request, now)
		},
		func(job *domain.Job, task domain.Task) {
			job.Tasks = append(job.Tasks, task)
		},
		func(job domain.Job) (domain.Task, bool) {
			for _, task := range job.Tasks {
				if task.CommandID == request.CommandID {
					return task, true
				}
			}
			return domain.Task{}, false
		},
	)
}
