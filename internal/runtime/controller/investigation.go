package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
)

func investigateFiles(ctx context.Context, saved QuestionContext, request InvestigationRequest) (*FileInvestigation, error) {
	task := saved.Task.Task
	sha := task.BaselineSHA
	for _, flow := range saved.Flows {
		if flow.TaskID == task.ID {
			// An issued flow's immutable scope is never widened by a later
			// Task edit or by a side question.
			if !reflect.DeepEqual(flow.Contract.Scope, task.Scope) || !reflect.DeepEqual(flow.Contract.OutOfScope, task.OutOfScope) ||
				flow.Contract.Worktree != task.Worktree || flow.Contract.Repo != task.Repo {
				return nil, fmt.Errorf("taxion: investigation requires unchanged issued scope and repository")
			}
		}
	}
	for _, dispatch := range saved.Task.Dispatches {
		if dispatch.Result != nil && dispatch.Status == core.DispatchSucceeded {
			sha = dispatch.Result.OutputSHA
		}
	}
	started := time.Now().UTC()
	files, err := gitproof.ReadCommittedFiles(ctx, task.Worktree, task.Repo, sha, task.Scope, task.OutOfScope, request.Paths, request.MaxBytes)
	if err != nil {
		return nil, err
	}
	observed := &FileInvestigation{SHA: sha, Files: files}
	content, err := json.Marshal(observed)
	if err != nil {
		return nil, err
	}
	observed.Observation = PlanObservation{StartedAt: started, CompletedAt: time.Now().UTC(), ContentHash: fmt.Sprintf("sha256:%x", sha256.Sum256(content))}
	return observed, nil
}

func verifyInvestigation(ctx context.Context, saved QuestionContext) error {
	if saved.Investigation == nil {
		return nil
	}
	paths := make([]string, 0, len(saved.Investigation.Files))
	for _, file := range saved.Investigation.Files {
		paths = append(paths, file.Path)
	}
	current, err := investigateFiles(ctx, saved, InvestigationRequest{Paths: paths, MaxBytes: 64 * 1024})
	if err != nil {
		return err
	}
	if current.SHA != saved.Investigation.SHA || !reflect.DeepEqual(current.Files, saved.Investigation.Files) ||
		current.Observation.ContentHash != saved.Investigation.Observation.ContentHash ||
		saved.Investigation.Observation.StartedAt.IsZero() || saved.Investigation.Observation.CompletedAt.Before(saved.Investigation.Observation.StartedAt) {
		return fmt.Errorf("taxion: investigation source content or SHA changed")
	}
	return nil
}
