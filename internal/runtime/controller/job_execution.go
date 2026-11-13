package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

// JobRoleController runs one RoleExecution against its approved Job contract.
// It does not reuse Task Dispatches or Task approval bundles.
type JobRoleController struct {
	store   *store.Store
	runtime runtime.Identity
}

func NewJobRoleController(db *store.Store, worker runtime.Identity) (*JobRoleController, error) {
	if db == nil || worker == nil || strings.TrimSpace(worker.Role()) == "" ||
		strings.TrimSpace(worker.ConfigHash()) == "" {
		return nil, fmt.Errorf("taxion: Job Role controller requires a store and identified runtime")
	}
	return &JobRoleController{store: db, runtime: worker}, nil
}

func (c *JobRoleController) checkConfiguration(ctx context.Context, job core.Job) error {
	identity, ok := c.runtime.(runtime.ConfigurationIdentity)
	if !ok {
		return fmt.Errorf("taxion: Job Role runtime must expose its selected configuration")
	}
	selected, err := core.SelectApprovalConfiguration(
		identity.ExecutionConfiguration(), job.Contract.Configuration.Sources,
	)
	if err != nil || selected.Digest != job.Contract.Configuration.Digest {
		return fmt.Errorf("taxion: Job Role runtime configuration differs from approved Job configuration")
	}
	var approvedSelection struct {
		Runtime struct {
			Account string `json:"account"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(job.Contract.Configuration.Selection, &approvedSelection); err != nil ||
		strings.TrimSpace(approvedSelection.Runtime.Account) == "" {
		return fmt.Errorf("taxion: approved Job configuration must identify the expected Runtime account")
	}
	if err := identity.CheckAuthentication(ctx, approvedSelection.Runtime.Account); err != nil {
		return err
	}
	if err := identity.CheckRepositoryAccess(ctx); err != nil {
		return err
	}
	return nil
}

// Start creates and binds a Runtime session only after checking the current
// Job approval, role allowlist, Task scope, deadline, and execution ceilings.
func (c *JobRoleController) Start(ctx context.Context, executionID string) (string, error) {
	if c == nil || c.store == nil || c.runtime == nil {
		return "", fmt.Errorf("taxion: Job Role controller is not initialized")
	}
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return "", err
	}
	if !state.HasGraph {
		return "", fmt.Errorf("taxion: Job Role start requires a Task Graph")
	}
	execution, ok := state.Graph.RoleExecution(executionID)
	if !ok || execution.Role != c.runtime.Role() {
		return "", fmt.Errorf("taxion: RoleExecution does not match the selected Runtime role")
	}
	job, ok := state.Graph.Job(execution.JobID)
	if !ok || job.Contract == nil {
		return "", fmt.Errorf("taxion: RoleExecution has no approved Job contract")
	}
	if execution.RuntimeSessionID != "" {
		return "", fmt.Errorf("taxion: RoleExecution already has a Runtime session; use Resume")
	}
	if err := state.Graph.CheckRoleExecutionStart(executionID, time.Now().UTC()); err != nil {
		return "", err
	}
	if err := c.checkConfiguration(ctx, job); err != nil {
		return "", err
	}
	runtimeTaskID := execution.TaskID
	if runtimeTaskID == "" {
		runtimeTaskID = job.ID
	}
	sessionID, err := c.runtime.Start(ctx, runtimeTaskID)
	if err != nil {
		return "", err
	}
	if identity, ok := c.runtime.(runtime.ConfigurationIdentity); ok {
		if err := identity.CheckServiceAccess(ctx, sessionID); err != nil {
			return "", errors.Join(err, c.runtime.Stop(ctx, sessionID))
		}
	}
	err = c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Job Role Task Graph disappeared during Runtime start")
		}
		now := time.Now().UTC()
		if err := state.Graph.CheckRoleExecutionStart(executionID, now); err != nil {
			return err
		}
		if err := state.Graph.TransitionRoleExecution(executionID, core.RoleExecutionRunning,
			"controller", "Job Role Runtime session started", now); err != nil {
			return err
		}
		if err := state.Graph.BindRoleExecutionSession(executionID, sessionID, c.runtime.ConfigHash()); err != nil {
			return err
		}
		_, contextHash, err := BuildJobExecutionContext(state.Graph, executionID)
		if err != nil {
			return err
		}
		return state.Graph.BindRoleExecutionContextHash(executionID, contextHash)
	})
	if err != nil {
		return "", errors.Join(err, c.runtime.Stop(ctx, sessionID))
	}
	return sessionID, nil
}

// Send refreshes the Job context before every Role turn. The Runtime receives
// the current Job scope, messages, Tasks, outputs and evidence as one snapshot.
func (c *JobRoleController) Send(ctx context.Context, executionID, prompt string) error {
	if c == nil || c.store == nil || c.runtime == nil || strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("taxion: Job Role send requires an initialized controller and prompt")
	}
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	if !state.HasGraph {
		return fmt.Errorf("taxion: Job Role send requires a Task Graph")
	}
	if err := state.Graph.CheckRoleExecutionTurn(executionID, time.Now().UTC()); err != nil {
		return err
	}
	execution, ok := state.Graph.RoleExecution(executionID)
	if !ok || execution.Status != core.RoleExecutionRunning ||
		execution.Role != c.runtime.Role() || execution.RuntimeSessionID == "" ||
		execution.RuntimeConfigHash != c.runtime.ConfigHash() {
		return fmt.Errorf("taxion: RoleExecution is not bound to this Runtime")
	}
	context, contextHash, err := BuildJobExecutionContext(state.Graph, executionID)
	if err != nil {
		return err
	}
	rendered, err := RenderJobExecutionContext(context)
	if err != nil {
		return err
	}
	if err := c.store.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.BindRoleExecutionContextHash(executionID, contextHash)
	}); err != nil {
		return err
	}
	if err := c.runtime.Resume(ctx, execution.RuntimeSessionID); err != nil {
		return err
	}
	message := "Current Job context (authoritative snapshot):\n" + rendered +
		"\n\nCurrent instruction:\n" + prompt
	return c.runtime.Send(ctx, execution.RuntimeSessionID, message)
}

// Events exposes only the event stream bound to this RoleExecution session.
func (c *JobRoleController) Events(ctx context.Context, executionID string) (<-chan runtime.Event, error) {
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return nil, err
	}
	execution, ok := state.Graph.RoleExecution(executionID)
	if !ok || execution.Status != core.RoleExecutionRunning ||
		execution.Role != c.runtime.Role() || execution.RuntimeSessionID == "" ||
		execution.RuntimeConfigHash != c.runtime.ConfigHash() {
		return nil, fmt.Errorf("taxion: RoleExecution is not bound to this Runtime")
	}
	return c.runtime.Events(ctx, execution.RuntimeSessionID)
}

// AuthorizeOperation is called by the Runtime tool adapter immediately before
// each privileged operation; a Job message or Task approval is not sufficient.
func (c *JobRoleController) AuthorizeOperation(executionID, operation string, at time.Time) error {
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	if !state.HasGraph {
		return fmt.Errorf("taxion: operation authorization requires a Task Graph")
	}
	return state.Graph.CheckRoleExecutionOperation(executionID, operation, at)
}

// RecordUsage appends an immutable provider accounting observation to the Job ledger.
// Replayed identical provider observations are idempotent.
func (c *JobRoleController) RecordUsage(executionID string, usage core.DispatchUsage) error {
	if c == nil || c.store == nil || c.runtime == nil || !nonnegativeUsage(usage) ||
		usage.TotalNanoAIU == nil || usage.Accounting == nil ||
		strings.TrimSpace(usage.Accounting.UsageID) == "" {
		return fmt.Errorf("taxion: Job usage requires a matching Runtime and complete accounting data")
	}
	if usage.ObservedAt.IsZero() {
		usage.ObservedAt = time.Now().UTC()
	}
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Job usage requires a Task Graph")
		}
		execution, ok := state.Graph.RoleExecution(executionID)
		if !ok || execution.RuntimeSessionID == "" ||
			execution.RuntimeSessionID != usage.Accounting.SourceSessionID ||
			execution.Role != c.runtime.Role() ||
			execution.RuntimeConfigHash != c.runtime.ConfigHash() {
			return fmt.Errorf("taxion: Job usage does not match this RoleExecution Runtime binding")
		}
		identityHash, err := core.CanonicalSHA256(struct {
			SourceSessionID string
			Sequence        int64
			UsageID         string
		}{
			SourceSessionID: usage.Accounting.SourceSessionID,
			Sequence:        usage.Accounting.Sequence,
			UsageID:         usage.Accounting.UsageID,
		})
		if err != nil {
			return err
		}
		record, err := core.NewJobUsage(core.JobUsage{
			ID: "usage-" + identityHash, JobID: execution.JobID,
			RoleExecutionID: execution.ID, Usage: usage,
		})
		if err != nil {
			return err
		}
		for _, existing := range state.Graph.JobUsageRecords(execution.JobID) {
			if existing.Usage.Accounting == nil || *existing.Usage.Accounting != *usage.Accounting {
				continue
			}
			if existing.RecordHash == record.RecordHash {
				return nil
			}
			return fmt.Errorf("taxion: provider usage identity was replayed with different content")
		}
		return state.Graph.AppendJobUsage(record)
	})
}

// PostMessage appends a natural-language request or reply addressed to one
// concrete RoleExecution in the same Job. It does not start or authorize it.
func (c *JobRoleController) PostMessage(executionID, targetExecutionID, messageID, body, replyTo string, at time.Time) error {
	if c == nil || c.store == nil || c.runtime == nil || strings.TrimSpace(messageID) == "" ||
		strings.TrimSpace(body) == "" || at.IsZero() {
		return fmt.Errorf("taxion: Job message requires controller, identity, body and timestamp")
	}
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Job message requires a Task Graph")
		}
		source, ok := state.Graph.RoleExecution(executionID)
		if !ok || source.Status != core.RoleExecutionRunning ||
			source.Role != c.runtime.Role() || source.RuntimeSessionID == "" ||
			source.RuntimeConfigHash != c.runtime.ConfigHash() {
			return fmt.Errorf("taxion: Job message source is not bound to this running Runtime")
		}
		if err := state.Graph.CheckRoleExecutionTurn(executionID, at); err != nil {
			return err
		}
		target, ok := state.Graph.RoleExecution(targetExecutionID)
		if !ok || target.JobID != source.JobID || target.Role == source.Role {
			return fmt.Errorf("taxion: Job message target must be another RoleExecution in the same Job")
		}
		switch target.Status {
		case core.RoleExecutionCreated, core.RoleExecutionRunning, core.RoleExecutionWaiting, core.RoleExecutionSuspended:
		default:
			return fmt.Errorf("taxion: Job message target is not able to receive work")
		}
		message := core.JobMessage{
			ID: messageID, JobID: source.JobID, FromRole: source.Role, ToRole: target.Role,
			Body: body, Source: "role-execution:" + executionID,
			SourceExecutionID: executionID, TargetExecutionID: targetExecutionID,
			ReplyTo: replyTo, At: at,
		}
		return state.Graph.AppendJobMessage(message)
	})
}

// Finish persists a terminal outcome after the Runtime turn has been observed.
func (c *JobRoleController) Finish(executionID string, status core.RoleExecutionStatus, actor, reason string, at time.Time) error {
	if status != core.RoleExecutionSucceeded && status != core.RoleExecutionFailed &&
		status != core.RoleExecutionCancelled {
		return fmt.Errorf("taxion: Job Role finish requires a terminal outcome")
	}
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Job Role finish requires a Task Graph")
		}
		return state.Graph.TransitionRoleExecution(executionID, status, actor, reason, at)
	})
}

// Stop requests cancellation before stopping the provider session. A failed
// stop remains CANCEL_REQUESTED and therefore blocks Job completion.
func (c *JobRoleController) Stop(ctx context.Context, executionID, reason string) error {
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	execution, ok := state.Graph.RoleExecution(executionID)
	if !ok || execution.Status != core.RoleExecutionRunning || execution.RuntimeSessionID == "" {
		return fmt.Errorf("taxion: Job Role stop requires a running bound execution")
	}
	now := time.Now().UTC()
	if err := c.store.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.TransitionRoleExecution(executionID, core.RoleExecutionCancelRequested,
			"controller", reason, now)
	}); err != nil {
		return err
	}
	if err := c.runtime.Stop(ctx, execution.RuntimeSessionID); err != nil {
		return err
	}
	return c.Finish(executionID, core.RoleExecutionCancelled, "controller", reason, time.Now().UTC())
}
