package store

import (
	"context"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func (s *Store) AddJobOutput(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	taskID, roleExecutionID, turnID, commitSHA, treeSHA string,
) (CommandReceipt, domain.JobOutput, error) {
	commandID := domain.JobOutputCommandID(taskID, roleExecutionID, turnID, commitSHA, treeSHA)
	request := struct {
		JobID, TaskID, RoleExecutionID, TurnID, CommitSHA, TreeSHA string
	}{jobID, taskID, roleExecutionID, turnID, commitSHA, treeSHA}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, "JOB_OUTPUT", request,
		func(job domain.Job, now time.Time) (domain.JobOutput, error) {
			return domain.NewJobOutput(job, taskID, roleExecutionID, turnID, commitSHA, treeSHA, now)
		},
		func(job *domain.Job, output domain.JobOutput) {
			job.JobOutputs = append(job.JobOutputs, output)
		},
		func(job domain.Job) (domain.JobOutput, bool) {
			for _, output := range job.JobOutputs {
				if output.CommandID == commandID {
					return output, true
				}
			}
			return domain.JobOutput{}, false
		},
	)
}
