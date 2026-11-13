package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/core"
)

// JobExecutionContext is the least-privilege projection sent to a Role.
// Runtime sessions, local worktree paths, approval records, credentials,
// budget internals and controller-only execution bindings are intentionally
// excluded.
type JobExecutionContext struct {
	Job        JobContextSummary      `json:"job"`
	Current    RoleExecutionSummary   `json:"currentExecution"`
	Tasks      []JobTaskContext       `json:"tasks"`
	Executions []RoleExecutionSummary `json:"roleExecutions"`
	Messages   []JobMessageContext    `json:"messages"`
}

type JobContextSummary struct {
	ID                   string                   `json:"id"`
	Request              string                   `json:"request"`
	Goal                 string                   `json:"goal"`
	Constraints          []string                 `json:"constraints,omitempty"`
	Status               string                   `json:"status"`
	ApprovalRevision     int                      `json:"approvalRevision,omitempty"`
	Approved             bool                     `json:"approved"`
	Acceptance           []string                 `json:"acceptance,omitempty"`
	RequiredEvidence     []string                 `json:"requiredEvidence,omitempty"`
	Repositories         []core.JobRepositoryScope `json:"repositories,omitempty"`
	Operations           []string                 `json:"operations,omitempty"`
	Budget               core.JobBudget           `json:"budget,omitempty"`
	Deadline             time.Time                `json:"deadline,omitempty"`
	SecurityRestrictions []string                 `json:"securityRestrictions,omitempty"`
}

type JobTaskContext struct {
	ID                 string                `json:"id"`
	Title              string                `json:"title"`
	Repository         string                `json:"repository"`
	Type               string                `json:"type"`
	Issue              string                `json:"issue,omitempty"`
	ChangeRef          string                `json:"changeRef,omitempty"`
	ParentID           string                `json:"parentId,omitempty"`
	DependsOn          []core.TaskDependency `json:"dependsOn,omitempty"`
	Owner              string                `json:"owner"`
	Reviewer           string                `json:"reviewer,omitempty"`
	Status             string                `json:"status"`
	Priority           string                `json:"priority,omitempty"`
	Objective          string                `json:"objective"`
	Scope              []string              `json:"scope"`
	OutOfScope         []string              `json:"outOfScope"`
	Acceptance         []string              `json:"acceptance"`
	RequiredEvidence   []string              `json:"requiredEvidence"`
	ValidationCommands []string              `json:"validationCommands,omitempty"`
	BaselineSHA        string                `json:"baselineSha,omitempty"`
	TargetSHA          string                `json:"targetSha,omitempty"`
	OutputMode         string                `json:"outputMode,omitempty"`
	TargetTreeSHA      string                `json:"targetTreeSha,omitempty"`
	TargetChangedFiles []string              `json:"targetChangedFiles,omitempty"`
	Risk               string                `json:"risk,omitempty"`
	CreatedFrom        string                `json:"createdFrom,omitempty"`
	NextAction         string                `json:"nextAction,omitempty"`
	ExitCondition      string                `json:"exitCondition,omitempty"`
	DeferredReason     string                `json:"deferredReason,omitempty"`
	ResumeCondition    string                `json:"resumeCondition,omitempty"`
}

type RoleExecutionSummary struct {
	ID                string    `json:"id"`
	Role              string    `json:"role"`
	TaskID            string    `json:"taskId,omitempty"`
	ParentExecutionID string    `json:"parentExecutionId,omitempty"`
	TriggerMessageID  string    `json:"triggerMessageId,omitempty"`
	Status            string    `json:"status"`
	CreatedAt         time.Time `json:"createdAt"`
	StartedAt         time.Time `json:"startedAt,omitempty"`
	UpdatedAt         time.Time `json:"updatedAt"`
	FinishedAt        time.Time `json:"finishedAt,omitempty"`
}

type JobMessageContext struct {
	ID       string    `json:"id"`
	FromRole string    `json:"fromRole"`
	ToRole   string    `json:"toRole"`
	Body     string    `json:"body"`
	ReplyTo  string    `json:"replyTo,omitempty"`
	At       time.Time `json:"at"`
}

// BuildJobExecutionContext assembles a deterministic, Job-scoped context.
// The returned hash identifies exactly the projected information, not
// controller-only credentials or Runtime session metadata.
func BuildJobExecutionContext(graph core.TaskGraph, executionID string) (JobExecutionContext, string, error) {
	if err := graph.Validate(); err != nil {
		return JobExecutionContext{}, "", err
	}
	execution, ok := graph.RoleExecution(executionID)
	if !ok {
		return JobExecutionContext{}, "", fmt.Errorf("taxion: RoleExecution %s does not exist", executionID)
	}
	job, ok := graph.Job(execution.JobID)
	if !ok {
		return JobExecutionContext{}, "", fmt.Errorf("taxion: RoleExecution %s has no Job", executionID)
	}
	jobSummary := JobContextSummary{
		ID: job.ID, Request: job.Request, Goal: job.Goal,
		Constraints: append([]string(nil), job.Constraints...), Status: string(job.Status),
	}
	if job.Contract != nil {
		jobSummary.ApprovalRevision = job.Contract.Revision
		jobSummary.Acceptance = append([]string(nil), job.Contract.Acceptance...)
		jobSummary.RequiredEvidence = append([]string(nil), job.Contract.RequiredEvidence...)
		jobSummary.Repositories = append([]core.JobRepositoryScope(nil), job.Contract.Repositories...)
		for i := range jobSummary.Repositories {
			jobSummary.Repositories[i].FileAccess.Paths = append([]string(nil), job.Contract.Repositories[i].FileAccess.Paths...)
		}
		jobSummary.Operations = append([]string(nil), job.Contract.Operations...)
		jobSummary.Budget = job.Contract.Budget
		jobSummary.Deadline = job.Contract.Deadline
		jobSummary.SecurityRestrictions = append([]string(nil), job.Contract.SecurityRestrictions...)
		_, jobSummary.Approved = graph.CurrentJobApproval(job.ID)
	}
	result := JobExecutionContext{
		Job: jobSummary,
		Current:    projectRoleExecution(execution),
		Tasks:      make([]JobTaskContext, 0),
		Executions: make([]RoleExecutionSummary, 0),
		Messages:   make([]JobMessageContext, 0),
	}
	for _, task := range graph.Tasks {
		if task.JobID != job.ID {
			continue
		}
		result.Tasks = append(result.Tasks, JobTaskContext{
			ID: task.ID, Title: task.Title, Repository: task.Repo, Type: task.Type,
			Issue: task.Issue, ChangeRef: task.ChangeRef, ParentID: task.ParentID,
			DependsOn: append([]core.TaskDependency(nil), task.DependsOn...),
			Owner:     task.Owner, Reviewer: task.Reviewer, Status: string(task.Status),
			Priority: task.Priority, Objective: task.Objective,
			Scope: append([]string(nil), task.Scope...), OutOfScope: append([]string(nil), task.OutOfScope...),
			Acceptance:         append([]string(nil), task.Acceptance...),
			RequiredEvidence:   append([]string(nil), task.RequiredEvidence...),
			ValidationCommands: append([]string(nil), task.ValidationCommands...),
			BaselineSHA:        task.BaselineSHA, TargetSHA: task.TargetSHA, OutputMode: task.OutputMode,
			TargetTreeSHA: task.TargetTreeSHA, TargetChangedFiles: append([]string(nil), task.TargetChangedFiles...),
			Risk: task.Risk, CreatedFrom: task.CreatedFrom, NextAction: task.NextAction,
			ExitCondition: task.ExitCondition, DeferredReason: task.DeferredReason,
			ResumeCondition: task.ResumeCondition,
		})
	}
	for _, item := range graph.RoleExecutionsForJob(job.ID) {
		result.Executions = append(result.Executions, projectRoleExecution(item))
	}
	for _, message := range graph.JobConversation(job.ID) {
		result.Messages = append(result.Messages, JobMessageContext{
			ID: message.ID, FromRole: message.FromRole, ToRole: message.ToRole,
			Body: message.Body, ReplyTo: message.ReplyTo, At: message.At,
		})
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return JobExecutionContext{}, "", fmt.Errorf("taxion: encode Job execution context: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return result, hex.EncodeToString(digest[:]), nil
}

func projectRoleExecution(execution core.RoleExecution) RoleExecutionSummary {
	return RoleExecutionSummary{
		ID: execution.ID, Role: execution.Role, TaskID: execution.TaskID,
		ParentExecutionID: execution.ParentExecutionID, TriggerMessageID: execution.TriggerMessageID,
		Status: string(execution.Status), CreatedAt: execution.CreatedAt, StartedAt: execution.StartedAt,
		UpdatedAt: execution.UpdatedAt, FinishedAt: execution.FinishedAt,
	}
}

// RenderJobExecutionContext returns the deterministic context as readable JSON.
func RenderJobExecutionContext(context JobExecutionContext) (string, error) {
	encoded, err := json.MarshalIndent(context, "", "  ")
	if err != nil {
		return "", fmt.Errorf("taxion: render Job execution context: %w", err)
	}
	return string(encoded), nil
}
