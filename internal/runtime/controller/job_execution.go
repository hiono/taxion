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
	"github.com/hiono/taxion/internal/tools"
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
	authorizer, ok := worker.(runtime.JobOperationAuthorizer)
	if !ok {
		return nil, fmt.Errorf("taxion: Job Role runtime must support controller-installed operation preflight")
	}
	controller := &JobRoleController{store: db, runtime: worker}
	authorizer.SetJobOperationAuthorizer(func(ctx context.Context, sessionID, operation string) error {
		return controller.AuthorizeSessionOperation(ctx, sessionID, operation, time.Now().UTC())
	})
	return controller, nil
}

func (c *JobRoleController) checkRoleToolPolicy(job core.Job) error {
	if job.Contract == nil {
		return fmt.Errorf("taxion: Job Role tool policy requires an approved Job contract")
	}
	identity, ok := c.runtime.(runtime.ToolPolicyIdentity)
	if !ok {
		return fmt.Errorf("taxion: Job Role runtime must expose its selected tool policy")
	}
	approved, ok := job.Contract.RoleToolPolicies[c.runtime.Role()]
	if !ok {
		return fmt.Errorf("taxion: Job contract has no explicit tool policy for Role %s", c.runtime.Role())
	}
	if err := approved.Validate(); err != nil {
		return err
	}
	var policy *tools.Policy
	if err := json.Unmarshal(approved.Snapshot, &policy); err != nil {
		return fmt.Errorf("taxion: approved Job Role tool policy is invalid")
	}
	if policy != nil {
		if err := policy.Check(); err != nil {
			return err
		}
	}
	if !tools.Equal(identity.ToolPolicy(), policy) {
		return fmt.Errorf("taxion: Runtime tool policy differs from the immutable Job Role approval")
	}
	return nil
}

func (c *JobRoleController) approvedRuntimeConfiguration(job core.Job) (runtime.ConfigurationIdentity, string, error) {
	if job.Contract == nil {
		return nil, "", fmt.Errorf("taxion: Job Role runtime requires an approved Job contract")
	}
	identity, ok := c.runtime.(runtime.ConfigurationIdentity)
	if !ok {
		return nil, "", fmt.Errorf("taxion: Job Role runtime must expose its selected configuration")
	}
	selected, err := core.SelectApprovalConfiguration(
		identity.ExecutionConfiguration(), job.Contract.Configuration.Sources,
	)
	if err != nil || selected.Digest != job.Contract.Configuration.Digest {
		return nil, "", fmt.Errorf("taxion: Job Role runtime configuration differs from approved Job configuration")
	}
	var approvedSelection struct {
		Runtime struct {
			Account string `json:"account"`
		} `json:"runtime"`
	}
	if err := json.Unmarshal(job.Contract.Configuration.Selection, &approvedSelection); err != nil ||
		strings.TrimSpace(approvedSelection.Runtime.Account) == "" {
		return nil, "", fmt.Errorf("taxion: approved Job configuration must identify the expected Runtime account")
	}
	return identity, approvedSelection.Runtime.Account, nil
}

func (c *JobRoleController) checkConfiguration(ctx context.Context, job core.Job) error {
	if err := c.checkRoleToolPolicy(job); err != nil {
		return err
	}
	identity, account, err := c.approvedRuntimeConfiguration(job)
	if err != nil {
		return err
	}
	if err := identity.CheckAuthentication(ctx, account); err != nil {
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
		currentExecution, ok := state.Graph.RoleExecution(executionID)
		if !ok {
			return fmt.Errorf("taxion: RoleExecution disappeared during Runtime start")
		}
		currentJob, ok := state.Graph.Job(currentExecution.JobID)
		if !ok || currentJob.Contract == nil {
			return fmt.Errorf("taxion: Job contract disappeared during Runtime start")
		}
		if _, _, err := c.approvedRuntimeConfiguration(currentJob); err != nil {
			return err
		}
		if err := c.checkRoleToolPolicy(currentJob); err != nil {
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

// Resume reattaches a waiting or suspended RoleExecution only while its
// current Job approval, budget, Task scope and Runtime identity remain valid.
func (c *JobRoleController) Resume(ctx context.Context, executionID string) error {
	if c == nil || c.store == nil || c.runtime == nil {
		return fmt.Errorf("taxion: Job Role controller is not initialized")
	}
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	if !state.HasGraph {
		return fmt.Errorf("taxion: Job Role resume requires a Task Graph")
	}
	execution, ok := state.Graph.RoleExecution(executionID)
	if !ok || execution.Role != c.runtime.Role() || execution.RuntimeSessionID == "" ||
		execution.RuntimeConfigHash != c.runtime.ConfigHash() {
		return fmt.Errorf("taxion: RoleExecution is not bound to this Runtime")
	}
	job, ok := state.Graph.Job(execution.JobID)
	if !ok || job.Contract == nil {
		return fmt.Errorf("taxion: RoleExecution has no Job contract")
	}
	if err := state.Graph.CheckRoleExecutionResume(executionID, time.Now().UTC()); err != nil {
		return err
	}
	if err := c.checkConfiguration(ctx, job); err != nil {
		return err
	}
	if err := c.runtime.Resume(ctx, execution.RuntimeSessionID); err != nil {
		return err
	}
	if identity, ok := c.runtime.(runtime.ConfigurationIdentity); ok {
		if err := identity.CheckServiceAccess(ctx, execution.RuntimeSessionID); err != nil {
			return errors.Join(err, c.runtime.Stop(ctx, execution.RuntimeSessionID))
		}
	}
	err = c.store.UpdateWorkspace(func(state *store.Workspace) error {
		current, ok := state.Graph.RoleExecution(executionID)
		if !ok || current.RuntimeSessionID != execution.RuntimeSessionID ||
			current.RuntimeConfigHash != c.runtime.ConfigHash() {
			return fmt.Errorf("taxion: RoleExecution Runtime binding changed during resume")
		}
		now := time.Now().UTC()
		if err := state.Graph.CheckRoleExecutionResume(executionID, now); err != nil {
			return err
		}
		currentJob, ok := state.Graph.Job(current.JobID)
		if !ok || currentJob.Contract == nil {
			return fmt.Errorf("taxion: Job contract disappeared during Runtime resume")
		}
		if _, _, err := c.approvedRuntimeConfiguration(currentJob); err != nil {
			return err
		}
		if err := c.checkRoleToolPolicy(currentJob); err != nil {
			return err
		}
		if err := state.Graph.TransitionRoleExecution(executionID, core.RoleExecutionRunning,
			"controller", "Job Role Runtime session resumed", now); err != nil {
			return err
		}
		_, contextHash, err := BuildJobExecutionContext(state.Graph, executionID)
		if err != nil {
			return err
		}
		return state.Graph.BindRoleExecutionContextHash(executionID, contextHash)
	})
	if err != nil {
		return errors.Join(err, c.runtime.Stop(ctx, execution.RuntimeSessionID))
	}
	return nil
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
	job, ok := state.Graph.Job(execution.JobID)
	if !ok || job.Contract == nil {
		return fmt.Errorf("taxion: RoleExecution has no Job contract")
	}
	if err := c.checkConfiguration(ctx, job); err != nil {
		return err
	}
	// Resume may take time. Re-read and re-authorize after it returns so the
	// prompt contains the latest Job state and cannot use a stale approval.
	if err := c.runtime.Resume(ctx, execution.RuntimeSessionID); err != nil {
		return err
	}
	var rendered string
	err = c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Job Role send requires a Task Graph")
		}
		now := time.Now().UTC()
		if err := state.Graph.CheckRoleExecutionTurn(executionID, now); err != nil {
			return err
		}
		current, ok := state.Graph.RoleExecution(executionID)
		if !ok || current.Status != core.RoleExecutionRunning ||
			current.Role != c.runtime.Role() || current.RuntimeSessionID != execution.RuntimeSessionID ||
			current.RuntimeConfigHash != c.runtime.ConfigHash() {
			return fmt.Errorf("taxion: RoleExecution Runtime binding changed before sending the turn")
		}
		currentJob, ok := state.Graph.Job(current.JobID)
		if !ok || currentJob.Contract == nil {
			return fmt.Errorf("taxion: Job contract disappeared before sending the turn")
		}
		if err := c.checkConfiguration(ctx, currentJob); err != nil {
			return err
		}
		context, contextHash, err := BuildJobExecutionContext(state.Graph, executionID)
		if err != nil {
			return err
		}
		rendered, err = RenderJobExecutionContext(context)
		if err != nil {
			return err
		}
		return state.Graph.BindRoleExecutionContextHash(executionID, contextHash)
	})
	if err != nil {
		// Resume may reactivate a provider session before the latest Job state is
		// revalidated. If that revalidation fails, stop the session so stale
		// authorization cannot leave an executable Runtime behind.
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		stopErr := c.runtime.Stop(stopCtx, execution.RuntimeSessionID)
		cancel()
		if stopErr != nil {
			return errors.Join(err, fmt.Errorf("taxion: stop Runtime after Job revalidation failure: %w", stopErr))
		}
		suspendErr := c.store.UpdateWorkspace(func(state *store.Workspace) error {
			if !state.HasGraph {
				return fmt.Errorf("taxion: Job Role send requires a Task Graph while suspending stale Runtime")
			}
			current, ok := state.Graph.RoleExecution(executionID)
			if !ok || current.RuntimeSessionID != execution.RuntimeSessionID ||
				current.Status != core.RoleExecutionRunning {
				return fmt.Errorf("taxion: RoleExecution changed while stopping a stale Runtime")
			}
			return state.Graph.TransitionRoleExecution(executionID, core.RoleExecutionSuspended,
				"controller", "Job authorization revalidation failed after Runtime resume", time.Now().UTC())
		})
		return errors.Join(err, suspendErr)
	}
	message := "Current Job context (authoritative snapshot):\n" + rendered +
		"\n\nCurrent instruction:\n" + prompt
	return c.runtime.Send(ctx, execution.RuntimeSessionID, message)
}

// Wait records that a RoleExecution is awaiting another Role or external input.
func (c *JobRoleController) Wait(executionID, reason string, at time.Time) error {
	if c == nil || c.store == nil || c.runtime == nil ||
		strings.TrimSpace(reason) == "" || at.IsZero() {
		return fmt.Errorf("taxion: Job Role wait requires an initialized controller, reason and timestamp")
	}
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Job Role wait requires a Task Graph")
		}
		execution, err := c.requireBoundExecution(state.Graph, executionID)
		if err != nil || execution.Status != core.RoleExecutionRunning {
			return fmt.Errorf("taxion: only this Runtime's running RoleExecution can wait")
		}
		return state.Graph.TransitionRoleExecution(executionID, core.RoleExecutionWaiting,
			"controller", reason, at)
	})
}

func (c *JobRoleController) requireBoundExecution(graph core.TaskGraph, executionID string) (core.RoleExecution, error) {
	if c == nil || c.store == nil || c.runtime == nil {
		return core.RoleExecution{}, fmt.Errorf("taxion: Job Role controller is not initialized")
	}
	execution, ok := graph.RoleExecution(executionID)
	if !ok || execution.Role != c.runtime.Role() || execution.RuntimeSessionID == "" ||
		execution.RuntimeConfigHash != c.runtime.ConfigHash() {
		return core.RoleExecution{}, fmt.Errorf("taxion: RoleExecution is not bound to this Runtime")
	}
	return execution, nil
}

// Events exposes only the event stream bound to this RoleExecution session.
func (c *JobRoleController) Events(ctx context.Context, executionID string) (<-chan runtime.Event, error) {
	if c == nil || c.store == nil || c.runtime == nil {
		return nil, fmt.Errorf("taxion: Job Role controller is not initialized")
	}
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return nil, err
	}
	if !state.HasGraph {
		return nil, fmt.Errorf("taxion: Job Role events require a Task Graph")
	}
	execution, err := c.requireBoundExecution(state.Graph, executionID)
	if err != nil || execution.Status != core.RoleExecutionRunning {
		return nil, fmt.Errorf("taxion: RoleExecution is not a running session bound to this Runtime")
	}
	source, err := c.runtime.Events(ctx, execution.RuntimeSessionID)
	if err != nil {
		return nil, err
	}
	events := make(chan runtime.Event)
	go func() {
		defer close(events)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-source:
				if !ok {
					return
				}
				if event.Type == runtime.EventUsage {
					var usage core.DispatchUsage
					if err := json.Unmarshal([]byte(event.Payload), &usage); err != nil {
						event = runtime.Event{Type: runtime.EventFailed, Payload: "invalid Job usage event: " + err.Error()}
					} else {
						usage.ObservedAt = time.Now().UTC()
						if err := c.RecordUsage(executionID, usage); err != nil {
							event = runtime.Event{Type: runtime.EventFailed, Payload: "Job usage rejected: " + err.Error()}
						}
					}
				}
				select {
				case events <- event:
				case <-ctx.Done():
					return
				}
				if event.Type == runtime.EventFailed {
					return
				}
			}
		}
	}()
	return events, nil
}

func (c *JobRoleController) authorizeOperation(graph core.TaskGraph, executionID, sessionID, operation string, at time.Time) error {
	execution, err := c.requireBoundExecution(graph, executionID)
	if err != nil || execution.Status != core.RoleExecutionRunning ||
		sessionID != "" && execution.RuntimeSessionID != sessionID {
		return fmt.Errorf("taxion: operation requires a running execution bound to this Runtime session")
	}
	job, ok := graph.Job(execution.JobID)
	if !ok || job.Contract == nil {
		return fmt.Errorf("taxion: Runtime operation requires an approved Job contract")
	}
	if _, _, err := c.approvedRuntimeConfiguration(job); err != nil {
		return err
	}
	if err := c.checkRoleToolPolicy(job); err != nil {
		return err
	}
	return graph.CheckRoleExecutionOperation(executionID, operation, at)
}

// AuthorizeOperation is called by the Runtime tool adapter immediately before
// each privileged operation; a Job message or Task approval is not sufficient.
func (c *JobRoleController) AuthorizeOperation(executionID, operation string, at time.Time) error {
	if c == nil || c.store == nil || c.runtime == nil {
		return fmt.Errorf("taxion: Job Role controller is not initialized")
	}
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	if !state.HasGraph {
		return fmt.Errorf("taxion: operation authorization requires a Task Graph")
	}
	return c.authorizeOperation(state.Graph, executionID, "", operation, at)
}

// AuthorizeSessionOperation is the provider callback. It resolves the unique
// active RoleExecution by the exact provider session and checks that binding
// again before allowing the requested operation.
func (c *JobRoleController) AuthorizeSessionOperation(ctx context.Context, sessionID, operation string, at time.Time) error {
	if c == nil || c.store == nil || c.runtime == nil || strings.TrimSpace(sessionID) == "" {
		return fmt.Errorf("taxion: Job session authorization requires an identified Runtime session")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	if !state.HasGraph {
		return fmt.Errorf("taxion: operation authorization requires a Task Graph")
	}
	var matched string
	for _, execution := range state.Graph.RoleExecutions {
		if execution.RuntimeSessionID != sessionID || execution.Role != c.runtime.Role() {
			continue
		}
		if matched != "" {
			return fmt.Errorf("taxion: Runtime session is ambiguously bound to multiple RoleExecutions")
		}
		matched = execution.ID
	}
	if matched == "" {
		return fmt.Errorf("taxion: Runtime session is not bound to a Job RoleExecution")
	}
	return c.authorizeOperation(state.Graph, matched, sessionID, operation, at)
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
	if c == nil || c.store == nil || c.runtime == nil ||
		(status != core.RoleExecutionSucceeded && status != core.RoleExecutionFailed &&
			status != core.RoleExecutionCancelled) {
		return fmt.Errorf("taxion: Job Role finish requires an initialized controller and terminal outcome")
	}
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Job Role finish requires a Task Graph")
		}
		execution, err := c.requireBoundExecution(state.Graph, executionID)
		if err != nil || (execution.Status != core.RoleExecutionRunning &&
			execution.Status != core.RoleExecutionCancelRequested) {
			return fmt.Errorf("taxion: Job Role finish requires this Runtime's active execution")
		}
		return state.Graph.TransitionRoleExecution(executionID, status, actor, reason, at)
	})
}

// Stop requests cancellation before stopping the provider session. A failed
// stop remains CANCEL_REQUESTED and therefore blocks Job completion.
func (c *JobRoleController) Stop(ctx context.Context, executionID, reason string) error {
	if c == nil || c.store == nil || c.runtime == nil {
		return fmt.Errorf("taxion: Job Role controller is not initialized")
	}
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	if !state.HasGraph {
		return fmt.Errorf("taxion: Job Role stop requires a Task Graph")
	}
	execution, err := c.requireBoundExecution(state.Graph, executionID)
	if err != nil || execution.Status != core.RoleExecutionRunning {
		return fmt.Errorf("taxion: Job Role stop requires this Runtime's running execution")
	}
	now := time.Now().UTC()
	if err := c.store.UpdateWorkspace(func(state *store.Workspace) error {
		current, err := c.requireBoundExecution(state.Graph, executionID)
		if err != nil || current.Status != core.RoleExecutionRunning ||
			current.RuntimeSessionID != execution.RuntimeSessionID {
			return fmt.Errorf("taxion: Job Role Runtime binding changed before stop")
		}
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
