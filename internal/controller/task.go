package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type TaskStore interface {
	GetJob(context.Context, string) (domain.Job, error)
	AddTask(context.Context, uint64, domain.TaskRequest) (store.CommandReceipt, domain.Task, error)
}

type TaskController struct {
	store TaskStore
	now   func() time.Time
}

func NewTaskController(taskStore TaskStore) (*TaskController, error) {
	if taskStore == nil {
		return nil, errors.New("TaskStore is required")
	}
	return &TaskController{
		store: taskStore,
		now:   func() time.Time { return time.Now().UTC().Round(0) },
	}, nil
}

// Create persists an immutable Task definition without starting a RoleExecution or Provider operation.
func (c *TaskController) Create(
	ctx context.Context,
	request domain.TaskRequest,
) (store.CommandReceipt, domain.Task, error) {
	if ctx == nil || request.JobID == "" {
		return store.CommandReceipt{}, domain.Task{}, errors.New("Task creation requires context and Job identity")
	}
	job, err := c.store.GetJob(ctx, request.JobID)
	if err != nil {
		return store.CommandReceipt{}, domain.Task{}, err
	}
	if _, err := domain.NewTask(job, request, c.now()); err != nil {
		return store.CommandReceipt{}, domain.Task{}, fmt.Errorf("Task request rejected: %w", err)
	}
	return c.store.AddTask(ctx, job.Revision, request)
}
