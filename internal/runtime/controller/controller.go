package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

// Controller binds one role-scoped runtime to durable Task Graph Dispatches.
type Controller struct {
	store   *store.Store
	runtime runtime.Identity
	mu      sync.Mutex
}

func New(db *store.Store, worker runtime.Identity) (*Controller, error) {
	if db == nil || worker == nil {
		return nil, fmt.Errorf("taxion: controller requires a store and runtime")
	}
	if strings.TrimSpace(worker.Role()) == "" || strings.TrimSpace(worker.ConfigHash()) == "" {
		return nil, fmt.Errorf("taxion: runtime role and configuration hash must not be empty")
	}
	return &Controller{store: db, runtime: worker}, nil
}

func PrepareReview(db *store.Store, dispatchID, source string) (core.GraphDispatch, error) {
	if db == nil || strings.TrimSpace(source) == "" {
		return core.GraphDispatch{}, fmt.Errorf("taxion: review preparation requires a store and source worktree")
	}
	source, err := filepath.Abs(source)
	if err != nil {
		return core.GraphDispatch{}, err
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return core.GraphDispatch{}, fmt.Errorf("taxion: resolve review source: %w", err)
	}
	var prepared core.GraphDispatch
	created := false
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		dispatch, ok := state.Graph.GraphDispatch(dispatchID)
		if !state.HasGraph || !ok || dispatch.Status != core.DispatchIssued || dispatch.Recipient != "REVIEWER" {
			return fmt.Errorf("taxion: prepare review requires an issued Reviewer Dispatch")
		}
		prepared = dispatch
		if err := state.Graph.CheckHandoffStart(dispatchID); err != nil {
			return err
		}
		if dispatch.ReviewWorktreeSource != "" {
			if !sameWorktree(dispatch.ReviewWorktreeSource, source) {
				return fmt.Errorf("taxion: review source differs from the managed worktree record")
			}
			return nil
		}
		if err := gitproof.CreateReviewWorktree(source, dispatch); err != nil {
			return err
		}
		created = true
		prepared.ReviewWorktreeSource = source
		for index := range state.Graph.Dispatches {
			if state.Graph.Dispatches[index].ID == dispatchID {
				state.Graph.Dispatches[index].ReviewWorktreeSource = source
			}
		}
		return nil
	})
	if err != nil && created {
		changed := false
		result := &core.DispatchResult{
			DispatchID: prepared.ID, TaskID: prepared.TaskID, Recipient: prepared.Recipient,
			Repository: prepared.Repository, Status: core.DispatchSucceeded,
			StartedSHA: prepared.Instruction.BaselineSHA, OutputSHA: prepared.Instruction.BaselineSHA,
			ChangedFiles: []string{}, WorktreeChanged: &changed,
		}
		err = errors.Join(err, gitproof.ReleaseReviewWorktree(source, prepared, result))
	}
	return prepared, err
}

// Start creates and durably binds an SDK session, or resumes the existing
// binding. The session never crosses Dispatch, task, role, or config boundaries.
func (c *Controller) Start(ctx context.Context, dispatchID string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	dispatch, err := c.dispatch(dispatchID)
	if err != nil {
		return "", err
	}
	if dispatch.Recipient != c.runtime.Role() {
		return "", fmt.Errorf("taxion: Dispatch role %s does not match runtime role %s", dispatch.Recipient, c.runtime.Role())
	}
	if dispatch.Recipient == "REVIEWER" && dispatch.ReviewWorktreeSource == "" {
		return "", fmt.Errorf("taxion: Reviewer requires a controller-managed worktree")
	}
	if dispatch.Status == core.DispatchCancelRequested || dispatch.Status == core.DispatchSuspended {
		return "", fmt.Errorf("taxion: Dispatch status %s does not authorize runtime resume", dispatch.Status)
	}
	if !sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) {
		return "", fmt.Errorf("taxion: runtime worktree does not match Dispatch worktree")
	}
	if dispatch.RuntimeSessionID != "" && dispatch.RuntimeConfigHash != c.runtime.ConfigHash() {
		return "", fmt.Errorf("taxion: runtime config does not match Dispatch session binding")
	}
	if err := c.authorizeExecution(ctx, dispatchID, "START"); err != nil {
		return "", err
	}
	if dispatch.RuntimeSessionID != "" {
		if err := c.runtime.Resume(ctx, dispatch.RuntimeSessionID); err != nil {
			return "", err
		}
		if err := c.checkServiceAccess(ctx, dispatchID, dispatch.RuntimeSessionID); err != nil {
			return "", err
		}
		return dispatch.RuntimeSessionID, nil
	}

	sessionID, err := c.runtime.Start(ctx, dispatch.TaskID)
	if err != nil {
		return "", err
	}
	if err := c.checkServiceAccess(ctx, dispatchID, sessionID); err != nil {
		if stopErr := c.runtime.Stop(ctx, sessionID); stopErr != nil {
			return "", errors.Join(err, fmt.Errorf("taxion: stop unbound runtime session: %w", stopErr))
		}
		return "", err
	}
	err = c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Task Graph is not initialized")
		}
		current, ok := state.Graph.GraphDispatch(dispatchID)
		if !ok || !active(current.Status) ||
			current.TaskID != dispatch.TaskID || current.Recipient != dispatch.Recipient {
			return fmt.Errorf("taxion: Dispatch changed while starting runtime session")
		}
		if _, err := c.checkExecutionConfiguration(state.Graph, dispatchID); err != nil {
			return err
		}
		if err := state.Graph.BindRuntimeSession(dispatchID, sessionID, c.runtime.ConfigHash()); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if stopErr := c.runtime.Stop(ctx, sessionID); stopErr != nil {
			return "", errors.Join(err, fmt.Errorf("taxion: stop unbound runtime session: %w", stopErr))
		}
		return "", err
	}
	return sessionID, nil
}

func (c *Controller) Send(ctx context.Context, dispatchID, prompt string) error {
	return c.SendWithBudgetGuidance(ctx, dispatchID, prompt, "")
}

func (c *Controller) SendWithBudgetGuidance(ctx context.Context, dispatchID, prompt, mode string) error {
	dispatch, err := c.boundDispatch(dispatchID)
	if err != nil {
		return err
	}
	if dispatch.Status == core.DispatchCancelRequested || dispatch.Status == core.DispatchSuspended {
		return fmt.Errorf("taxion: Dispatch status %s does not authorize sending instructions", dispatch.Status)
	}
	if policy := dispatch.Instruction.BudgetGuidance; mode != "" &&
		(policy == nil || !slices.Contains(policy.AllowedModes, mode)) {
		return fmt.Errorf("taxion: budget guidance override requires approval")
	}
	if err := c.authorizeExecution(ctx, dispatchID, "SEND"); err != nil {
		return err
	}
	if err := c.runtime.Resume(ctx, dispatch.RuntimeSessionID); err != nil {
		return err
	}
	if err := c.checkServiceAccess(ctx, dispatchID, dispatch.RuntimeSessionID); err != nil {
		return err
	}
	if dispatch.Status == core.DispatchRunning {
		return c.markRunning(dispatchID)
	}
	observation, err := c.prepareBudgetGuidance(ctx, dispatch, mode)
	if err != nil {
		return errors.Join(err, c.recordBudgetGuidance(dispatch, observation, "denied", err))
	}
	if err := c.markRunning(dispatchID); err != nil {
		return err
	}
	if observation != nil {
		if err := c.AuthorizeTools(ctx, dispatchID, dispatch.RuntimeSessionID); err != nil {
			return err
		}
	}
	refreshQuotaState(observation, time.Now().UTC())
	supplement, err := budgetSupplement(observation)
	if err != nil {
		return err
	}
	if observation != nil && observation.Account != nil && observation.Account.State == "available" &&
		observation.Account.ExecutionDenied {
		return errors.Join(fmt.Errorf("taxion: account entitlement exhausted; paid overage is not authorized"),
			c.recordBudgetGuidance(dispatch, observation, "denied", nil))
	}
	if err := c.recordBudgetGuidance(dispatch, observation, "prepared", nil); err != nil {
		return err
	}
	sendErr := c.runtime.Send(ctx, dispatch.RuntimeSessionID, prompt+supplement)
	recordErr := c.recordBudgetGuidance(dispatch, observation, "send-returned", sendErr)
	if err := errors.Join(sendErr, recordErr); err != nil {
		if ctx.Err() != nil {
			stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			return errors.Join(err, c.Stop(stopCtx, dispatchID))
		}
		if failErr := c.Fail(dispatchID, err.Error()); failErr != nil {
			return errors.Join(err, fmt.Errorf("taxion: record runtime failure: %w", failErr))
		}
		return err
	}
	return nil
}

func (c *Controller) Resume(ctx context.Context, dispatchID string) error {
	dispatch, err := c.boundDispatch(dispatchID)
	if err != nil {
		return err
	}
	if dispatch.Status == core.DispatchCancelRequested || dispatch.Status == core.DispatchSuspended {
		return fmt.Errorf("taxion: Dispatch status %s does not authorize runtime resume", dispatch.Status)
	}
	if err := c.authorizeExecution(ctx, dispatchID, "RESUME"); err != nil {
		return err
	}
	if err := c.runtime.Resume(ctx, dispatch.RuntimeSessionID); err != nil {
		return err
	}
	return c.checkServiceAccess(ctx, dispatchID, dispatch.RuntimeSessionID)
}

func (c *Controller) Stop(ctx context.Context, dispatchID string) error {
	dispatch, err := c.boundDispatch(dispatchID)
	if err != nil {
		return err
	}
	if dispatch.Status == core.DispatchRunning {
		if err := c.transition(dispatchID, core.DispatchCancelRequested, "cancel requested", nil, false, false); err != nil {
			return err
		}
	}
	attach := c.runtime.Resume
	if worker, ok := c.runtime.(runtime.StopAttachment); ok {
		attach = worker.ResumeForStop
	}
	if err := attach(ctx, dispatch.RuntimeSessionID); err != nil {
		return fmt.Errorf("taxion: attach saved session for safe stop: %w", err)
	}
	if err := c.runtime.Stop(ctx, dispatch.RuntimeSessionID); err != nil {
		return err
	}
	result := &core.DispatchResult{
		DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
		Repository: dispatch.Repository, Status: core.DispatchCancelled,
		StartedSHA: dispatch.ExpectedStartSHA(),
		Evidence:   []string{"runtime-stop:" + dispatch.RuntimeSessionID}, NextTransition: core.Blocked,
	}
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	for _, flow := range state.Graph.ReviewFlows {
		if dispatch.Recipient != "CODER" || !slices.Contains(flow.DispatchIDs, dispatchID) {
			continue
		}
		partial, captureErr := gitproof.CapturePartial(dispatch)
		if captureErr != nil {
			result.Blockers = []string{"partial Git evidence unavailable: " + captureErr.Error()}
		} else {
			result.PartialGitEvidence = partial
			result.Evidence = append(result.Evidence, "partial-tree:"+partial.TreeSHA)
		}
		return errors.Join(captureErr, c.transition(dispatchID, core.DispatchCancelled, "runtime stopped", result, false, false))
	}
	return c.transition(dispatchID, core.DispatchCancelled, "runtime stopped", result, false, false)
}

func (c *Controller) Fail(dispatchID, reason string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("taxion: runtime failure reason must not be empty")
	}
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	dispatch, ok := state.Graph.GraphDispatch(dispatchID)
	if !state.HasGraph || !ok ||
		dispatch.RuntimeSessionID == "" || dispatch.Recipient != c.runtime.Role() ||
		dispatch.RuntimeConfigHash != c.runtime.ConfigHash() ||
		!sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) ||
		dispatch.Status != core.DispatchRunning && dispatch.Status != core.DispatchCancelRequested {
		return fmt.Errorf("taxion: runtime failure requires a running Dispatch")
	}
	result := &core.DispatchResult{
		DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
		Repository: dispatch.Repository, Status: core.DispatchFailed,
		StartedSHA: dispatch.ExpectedStartSHA(), Blockers: []string{reason},
		NextTransition: core.Blocked,
	}
	return c.transition(dispatchID, core.DispatchFailed, "runtime failed: "+reason, result, false, false)
}

// Finish accepts the structured final response of a bound session. Intermediate
// output never reaches this path, and Git evidence is generated by the controller.
func (c *Controller) Finish(ctx context.Context, dispatchID, payload string) (*core.DispatchResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dispatch, err := c.boundDispatch(dispatchID)
	if err != nil {
		return nil, err
	}
	if dispatch.Status != core.DispatchRunning {
		return nil, fmt.Errorf("taxion: final response requires a running Dispatch")
	}
	var result core.DispatchResult
	if err := core.DecodeGraphInput(strings.NewReader(payload), &result); err != nil {
		return nil, fmt.Errorf("taxion: decode final Dispatch result: %w", err)
	}
	if bound, ok := c.runtime.(interface{ UsesBoundResultIdentity() bool }); ok && bound.UsesBoundResultIdentity() {
		if err := bindResultIdentity(dispatch, &result); err != nil {
			return nil, err
		}
	}
	if result.GitEvidence != nil {
		return nil, fmt.Errorf("taxion: Git evidence is generated by the controller")
	}
	if len(result.ExecutionEvidence) != 0 {
		return nil, fmt.Errorf("taxion: execution observations are generated by the controller")
	}
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return nil, err
	}
	for _, receipt := range state.Records.Events {
		if receipt.DispatchID != dispatchID || receipt.Type != runtime.EventExecution {
			continue
		}
		if receipt.SessionID != dispatch.RuntimeSessionID || receipt.Hash != receiptHash(receipt.Payload) {
			return nil, fmt.Errorf("taxion: execution observation differs from its saved session receipt")
		}
		var observation core.ExecutionObservation
		if err := core.DecodeGraphInput(strings.NewReader(receipt.Payload), &observation); err != nil {
			return nil, fmt.Errorf("taxion: invalid saved execution observation: %w", err)
		}
		result.ExecutionEvidence = append(result.ExecutionEvidence, observation)
	}
	if bound, ok := c.runtime.(interface{ UsesBoundResultIdentity() bool }); ok && bound.UsesBoundResultIdentity() &&
		result.Status == core.DispatchSucceeded &&
		slices.Contains(dispatch.Instruction.RequiredEvidence, "work-report") && strings.TrimSpace(result.WorkReport) == "" {
		return nil, fmt.Errorf("taxion: bound result requires workReport content, not only an evidence identifier or external path")
	}

	if result.OutputTreeSHA != "" {
		return nil, fmt.Errorf("taxion: output tree SHA is generated by the controller")
	}
	if result.Status == core.DispatchSucceeded {
		if dispatch.Instruction.Version < core.DispatchInstructionVersion {
			return nil, fmt.Errorf("taxion: issue a Dispatch with the current execution contract")
		}
		if dispatch.ExecutionStartSHA == "" {
			return nil, fmt.Errorf("taxion: successful result requires an observed execution start SHA")
		}
		if dispatch.Instruction.OutputMode == core.OutputModeWorktreeTreeSnapshot &&
			dispatch.Recipient == "REVIEWER" {
			result.OutputTreeSHA = dispatch.Instruction.TargetTreeSHA
			for index := range result.Validations {
				result.Validations[index].Worktree = dispatch.Instruction.Worktree
				result.Validations[index].SHA = result.OutputTreeSHA
			}
		}
		if err := gitproof.ExecuteValidations(ctx, dispatch, &result); err != nil {
			verifyErr := fmt.Errorf("taxion: verify final Dispatch result: %w", err)
			if len(result.Validations) != 0 && !result.Validations[0].StartedAt.IsZero() {
				result.Status = core.DispatchFailed
				result.GitEvidence = nil
				result.Blockers = []string{verifyErr.Error()}
				result.NextTransition = core.Blocked
				result.ReviewVerdict = ""
				result.Acceptance = nil
				result.Findings = nil
				result.WorktreeChanged = nil
				saveErr := c.transition(dispatchID, core.DispatchFailed, "controller validation failed", &result, false, false)
				return &result, errors.Join(verifyErr, saveErr)
			}
			return nil, verifyErr
		}
		if !slices.Contains(result.Evidence, "controller-validation") {
			result.Evidence = append(result.Evidence, "controller-validation")
		}
		if dispatch.Recipient == "REVIEWER" {
			if dispatch.ReviewWorktreeSource == "" {
				return nil, fmt.Errorf("taxion: Reviewer result requires a controller-managed worktree")
			}
			if err := dispatch.ValidateResult(result.Status, &result); err != nil {
				return nil, err
			}
			closer, ok := c.runtime.(io.Closer)
			if !ok {
				return nil, fmt.Errorf("taxion: Reviewer runtime must support close before worktree release")
			}
			if err := closer.Close(); err != nil {
				return nil, fmt.Errorf("taxion: close Reviewer runtime: %w", err)
			}
			if err := gitproof.ReleaseReviewWorktree(dispatch.ReviewWorktreeSource, dispatch, &result); err != nil {
				return nil, err
			}
		}
	}
	if !slices.Contains([]core.DispatchStatus{core.DispatchSucceeded, core.DispatchFailed, core.DispatchSuspended}, result.Status) {
		return nil, fmt.Errorf("taxion: final response has invalid status %s", result.Status)
	}
	if err := dispatch.ValidateResult(result.Status, &result); err != nil {
		return nil, err
	}
	if err := c.transition(dispatchID, result.Status, "final response verified", &result, false, false); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Controller) Events(ctx context.Context, dispatchID string) (<-chan runtime.Event, error) {
	dispatch, err := c.boundDispatch(dispatchID)
	if err != nil {
		return nil, err
	}
	return c.runtime.Events(ctx, dispatch.RuntimeSessionID)
}

func (c *Controller) EventCursor(dispatchID string) (uint64, error) {
	dispatch, err := c.boundDispatch(dispatchID)
	if err != nil {
		return 0, err
	}
	source, ok := c.runtime.(runtime.EventCursor)
	if !ok {
		return 0, fmt.Errorf("taxion: runtime does not support event cursors")
	}
	return source.EventCursor(dispatch.RuntimeSessionID)
}

func (c *Controller) EventsAfter(ctx context.Context, dispatchID string, cursor uint64) (<-chan runtime.Event, error) {
	dispatch, err := c.boundDispatch(dispatchID)
	if err != nil {
		return nil, err
	}
	source, ok := c.runtime.(runtime.EventCursor)
	if !ok {
		return nil, fmt.Errorf("taxion: runtime does not support event cursors")
	}
	return source.EventsAfter(ctx, dispatch.RuntimeSessionID, cursor)
}

func (c *Controller) RecordUsage(dispatchID string, usage core.DispatchUsage) error {
	if !nonnegativeUsage(usage) {
		return fmt.Errorf("taxion: invalid runtime usage record")
	}
	if usage.ObservedAt.IsZero() {
		usage.ObservedAt = time.Now().UTC()
	}
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Task Graph is not initialized")
		}
		dispatch, ok := state.Graph.GraphDispatch(dispatchID)
		if !ok || dispatch.Status != core.DispatchRunning || dispatch.RuntimeSessionID == "" ||
			dispatch.Recipient != c.runtime.Role() || dispatch.RuntimeConfigHash != c.runtime.ConfigHash() ||
			!sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) {
			return fmt.Errorf("taxion: runtime usage does not match an active bound Dispatch")
		}
		for i := range state.Graph.Dispatches {
			if state.Graph.Dispatches[i].ID == dispatchID {
				return appendRuntimeUsage(&state.Graph.Dispatches[i], usage)
			}
		}
		return fmt.Errorf("taxion: Dispatch %q not found", dispatchID)
	})
}

func (c *Controller) boundDispatch(dispatchID string) (core.GraphDispatch, error) {
	dispatch, err := c.dispatch(dispatchID)
	if err != nil {
		return core.GraphDispatch{}, err
	}
	if dispatch.Recipient != c.runtime.Role() {
		return core.GraphDispatch{}, fmt.Errorf("taxion: Dispatch role %s does not match runtime role %s", dispatch.Recipient, c.runtime.Role())
	}
	if !sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) {
		return core.GraphDispatch{}, fmt.Errorf("taxion: runtime worktree does not match Dispatch worktree")
	}
	if dispatch.RuntimeSessionID == "" {
		return core.GraphDispatch{}, fmt.Errorf("taxion: Dispatch has no runtime session binding")
	}
	if dispatch.RuntimeConfigHash != c.runtime.ConfigHash() {
		return core.GraphDispatch{}, fmt.Errorf("taxion: runtime config does not match Dispatch session binding")
	}
	return dispatch, nil
}

func (c *Controller) dispatch(id string) (core.GraphDispatch, error) {
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return core.GraphDispatch{}, err
	}
	if !state.HasGraph {
		return core.GraphDispatch{}, fmt.Errorf("taxion: Task Graph is not initialized")
	}
	dispatch, ok := state.Graph.GraphDispatch(id)
	if !ok || !active(dispatch.Status) {
		return core.GraphDispatch{}, fmt.Errorf("taxion: active Dispatch %q not found", id)
	}
	return dispatch, nil
}

func (c *Controller) markRunning(dispatchID string) error {
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		dispatch, ok := state.Graph.GraphDispatch(dispatchID)
		if !ok {
			return fmt.Errorf("taxion: handoff execution Dispatch not found")
		}
		if err := state.Graph.CheckHandoff(dispatch.TaskID); err != nil {
			return err
		}
		if _, err := c.checkExecutionConfiguration(state.Graph, dispatchID); err != nil {
			return err
		}
		if !state.HasGraph || !ok || !active(dispatch.Status) || dispatch.RuntimeSessionID == "" ||
			dispatch.Recipient != c.runtime.Role() ||
			dispatch.RuntimeConfigHash != c.runtime.ConfigHash() ||
			!sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) {
			return fmt.Errorf("taxion: runtime start does not match an active Dispatch")
		}
		if dispatch.Instruction.Version >= 4 && dispatch.ExecutionStartSHA == "" &&
			(dispatch.Status == core.DispatchIssued || dispatch.Status == core.DispatchAcknowledged) {
			start, err := gitproof.ObserveStart(dispatch)
			if err != nil {
				return err
			}
			for index := range state.Graph.Dispatches {
				if state.Graph.Dispatches[index].ID == dispatchID {
					state.Graph.Dispatches[index].ExecutionStartSHA = start
				}
			}
		}
		switch dispatch.Status {
		case core.DispatchIssued:
			if err := state.Graph.TransitionDispatch(dispatchID, core.DispatchAcknowledged,
				"runtime", "session accepted instruction", true, false, nil); err != nil {
				return err
			}
			fallthrough
		case core.DispatchAcknowledged:
			return state.Graph.TransitionDispatch(dispatchID, core.DispatchRunning,
				"runtime", "session started execution", true, true, nil)
		case core.DispatchRunning:
			return fmt.Errorf("taxion: execution was already authorized; observe the saved session or confirm its stop before issuing new work")
		default:
			return fmt.Errorf("taxion: Dispatch status %s does not authorize execution", dispatch.Status)
		}
	})
}

func (c *Controller) transition(dispatchID string, status core.DispatchStatus, reason string,
	result *core.DispatchResult, acknowledged, started bool) error {
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("taxion: Task Graph is not initialized")
		}
		if result != nil {
			previous, ok := state.Graph.GraphDispatch(dispatchID)
			if !ok {
				return fmt.Errorf("taxion: result Dispatch not found")
			}
			if previous.Result != nil {
				if err := validateResultReceipt(state, previous); err != nil {
					return err
				}
			}
		}
		if err := state.Graph.TransitionDispatch(dispatchID, status, "runtime", reason, acknowledged, started, result); err != nil {
			return err
		}
		if result != nil {
			saved, _ := state.Graph.GraphDispatch(dispatchID)
			body, err := json.Marshal(struct {
				Result *core.DispatchResult
				Usage  []core.DispatchUsage
			}{saved.Result, saved.RuntimeUsage})
			if err != nil {
				return err
			}
			var sequence uint64
			for _, receipt := range state.Records.Events {
				if receipt.DispatchID == dispatchID && receipt.Type == "verified-result" {
					sequence++
				}
			}
			state.Records.Events = append(state.Records.Events, store.RuntimeReceipt{
				DispatchID: dispatchID, SessionID: saved.RuntimeSessionID, Type: "verified-result",
				Sequence: sequence,
				Payload:  string(body), Hash: receiptHash(string(body)), At: time.Now().UTC(),
			})
			if err := validateResultReceipt(state, saved); err != nil {
				return err
			}
		}
		active := status == core.DispatchIssued || status == core.DispatchAcknowledged ||
			status == core.DispatchRunning || status == core.DispatchCancelRequested ||
			status == core.DispatchSuspended
		for i := range state.Ledger.Dispatches {
			if state.Ledger.Dispatches[i].ID == dispatchID {
				state.Ledger.Dispatches[i].Active = active
				return nil
			}
		}
		return fmt.Errorf("taxion: Dispatch %q has no ledger projection", dispatchID)
	})
}

func active(status core.DispatchStatus) bool {
	switch status {
	case core.DispatchIssued, core.DispatchAcknowledged, core.DispatchRunning,
		core.DispatchCancelRequested, core.DispatchSuspended:
		return true
	default:
		return false
	}
}

func sameWorktree(expected, actual string) bool {
	expectedAbs, expectedErr := filepath.Abs(expected)
	actualAbs, actualErr := filepath.Abs(actual)
	return expectedErr == nil && actualErr == nil && filepath.Clean(expectedAbs) == filepath.Clean(actualAbs)
}

func nonnegativeUsage(usage core.DispatchUsage) bool {
	if usage.Accounting != nil && (strings.TrimSpace(usage.Accounting.SourceSessionID) == "" ||
		usage.Accounting.Sequence <= 0 || strings.TrimSpace(usage.Accounting.UsageID) == "") {
		return false
	}
	for _, value := range []*int64{
		usage.InputTokens, usage.OutputTokens, usage.CacheReadTokens, usage.CacheWriteTokens,
	} {
		if value != nil && *value < 0 {
			return false
		}
	}
	return strings.TrimSpace(usage.Model) != ""
}
