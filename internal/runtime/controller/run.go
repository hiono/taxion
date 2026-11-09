package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type WorkerFactory func(core.GraphDispatch) (runtime.Identity, error)

// RunReviewFlow holds a durable single-runner claim. An interrupted process's
// claim remains visible so another process cannot silently resume uncertain work.
func RunReviewFlow(ctx context.Context, db *store.Store, ledgerPath, id string, factory WorkerFactory, out io.Writer, configuration ...core.ApprovalConfiguration) (core.ReviewFlow, error) {
	return runReviewFlow(ctx, db, ledgerPath, id, factory, out, false, false, false, configuration...)
}

// ResumeReviewFlow rechecks the read-only gate inside the runner claim
// transaction, so a concurrent change cannot consume a stale passing report.
func ResumeReviewFlow(ctx context.Context, db *store.Store, ledgerPath, id string, factory WorkerFactory, out io.Writer, configuration ...core.ApprovalConfiguration) (core.ReviewFlow, error) {
	return runReviewFlow(ctx, db, ledgerPath, id, factory, out, false, true, false, configuration...)
}

// StepReviewFlow executes at most one role turn. It does not issue the next
// handoff after the turn completes; continuation remains an explicit decision.
func StepReviewFlow(ctx context.Context, db *store.Store, ledgerPath, id string, factory WorkerFactory, out io.Writer, configuration ...core.ApprovalConfiguration) (core.ReviewFlow, error) {
	return runReviewFlow(ctx, db, ledgerPath, id, factory, out, true, false, false, configuration...)
}

// ValidateReviewFlowInput performs the approved host stage without creating a
// model session, leaving the issued Reviewer available for worker diagnostics.
func ValidateReviewFlowInput(ctx context.Context, db *store.Store, ledgerPath, id string, configuration core.ApprovalConfiguration) (core.ReviewFlow, error) {
	return runReviewFlow(ctx, db, ledgerPath, id, nil, io.Discard, false, false, true, configuration)
}

func runReviewFlow(ctx context.Context, db *store.Store, ledgerPath, id string, factory WorkerFactory, out io.Writer, singleTurn, resume, inputOnly bool, configuration ...core.ApprovalConfiguration) (result core.ReviewFlow, returnErr error) {
	if db == nil || factory == nil && !inputOnly || out == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: flow runner requires a store, worker factory and event output")
	}
	if _, inherited := ctx.Value(operationKey{}).(store.Operation); !inherited {
		lease, err := db.LockOperation()
		if err != nil {
			return core.ReviewFlow{}, err
		}
		defer func() { returnErr = errors.Join(returnErr, lease.Close()) }()
	}
	var selected core.ApprovalConfiguration
	if len(configuration) != 0 {
		selected = configuration[0]
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return core.ReviewFlow{}, err
	}
	runnerID := hex.EncodeToString(nonce)
	var flow core.ReviewFlow
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if resume {
			report, err := ResumeContractGateReport(ctx, *state, id, selected)
			if err != nil {
				return err
			}
			if report.Verdict != GateVerdictPass {
				return fmt.Errorf("taxion: resume gate %s: %s", report.BlockingConditions[0].Code, report.BlockingConditions[0].Message)
			}
		}
		var ok bool
		flow, ok = state.Graph.ReviewFlow(id)
		if !ok || flow.Status != "RUNNING" {
			return fmt.Errorf("taxion: runner requires a running review flow")
		}
		if inputOnly && (flow.Artifact == nil || len(flow.DispatchIDs) != 0) {
			return fmt.Errorf("taxion: validate-input requires an artifact Flow before its Reviewer Dispatch; it never replays validation")
		}
		if err := checkOperation(state, ctx, flow.TaskID, flow.ID); err != nil {
			return err
		}
		if flow.RunnerID != "" {
			return fmt.Errorf("taxion: flow is claimed by runner %s; reconcile its execution and claim before retrying", flow.RunnerID)
		}
		flow.RunnerID = runnerID
		storeFlow(state, flow)
		return nil
	})
	if err != nil {
		return flow, err
	}
	runCtx, cancel, deadlineErr := withFlowDeadline(ctx, db, flow.ID, nil, false)
	if deadlineErr != nil {
		// Still release the claim through the common save path.
		err = deadlineErr
		runCtx, cancel = ctx, func() {}
	}
	defer cancel()
	preparing := false
	for err == nil {
		preparing = false
		flow, err = advanceReviewFlow(db, ledgerPath, id, runnerID, selected)
		if err != nil || flow.Status != "RUNNING" {
			break
		}
		if err = runCtx.Err(); err != nil {
			break
		}
		if flow.Artifact != nil && len(flow.DispatchIDs) == 0 {
			if err = runArtifactValidation(runCtx, db, ledgerPath, id, runnerID, selected); err != nil || inputOnly {
				break
			}
			continue
		}
		state, loadErr := db.LoadWorkspace()
		if loadErr != nil {
			err = loadErr
			break
		}
		dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
		if err = checkUnstartedDispatch(flow, dispatch); err != nil {
			break
		}
		if err = checkLifecyclePacket(&state, dispatch.ID); err != nil {
			break
		}
		preparing = true
		if dispatch.Recipient == "REVIEWER" {
			dispatch, err = prepareReview(runCtx, db, dispatch.ID, flow.Contract.Worktree)
			if err != nil {
				break
			}
			if err = writeReviewBudget(runCtx, out, flow, dispatch); err != nil {
				break
			}
		}
		var worker runtime.Identity
		worker, err = factory(dispatch)
		if err != nil {
			break
		}
		closer, ok := worker.(io.Closer)
		if !ok {
			err = fmt.Errorf("taxion: flow runtime requires close support")
			break
		}
		control, setupErr := New(db, worker)
		if setupErr == nil {
			preparing = false
			_, setupErr = control.Start(runCtx, dispatch.ID)
		}
		var cursor uint64
		if setupErr == nil {
			cursor, setupErr = control.EventCursor(dispatch.ID)
		}
		if setupErr == nil {
			setupErr = control.Send(runCtx, dispatch.ID, "Execute the saved Dispatch contract and return its structured final result.")
		}
		if setupErr == nil {
			setupErr = control.StreamEvents(runCtx, dispatch, cursor, out, func() error {
				if unchanged, err := observeRunningFlow(db, ledgerPath, id, runnerID); err != nil || unchanged {
					return err
				}
				_, checkErr := advanceReviewFlow(db, ledgerPath, id, runnerID, selected)
				return checkErr
			})
		}
		if setupErr != nil && control != nil && !handoffHeldOnly(setupErr) {
			setupErr = errors.Join(setupErr, control.stopIfExecuting(dispatch.ID))
		}
		err = errors.Join(setupErr, closer.Close())
		if err != nil {
			break
		}
		if singleTurn {
			break
		}
	}
	saveErr := db.UpdateWorkspace(func(state *store.Workspace) error {
		stored, ok := state.Graph.ReviewFlow(id)
		if !ok || stored.RunnerID != runnerID {
			return fmt.Errorf("taxion: flow runner claim changed during execution")
		}
		if err != nil && stored.Status == "RUNNING" && !handoffHeldOnly(err) {
			now := time.Now().UTC()
			report := taskBudgetReport(state.Graph, stored, now)
			dispatch, _ := state.Graph.GraphDispatch(stored.LastDispatchID())
			stageStop := ""
			if preparing {
				stageStop = stageBudgetStop(state.Graph, stored, dispatch, now)
			}
			var budgetErr *budgetStopError
			budgetStopped := errors.As(err, &budgetErr) || activeBudgetStop(report) != "" || stageStop != ""
			if stageStop != "" {
				err = errors.Join(err, errors.New(stageStop))
			}
			if preparing && runCtx.Err() == nil && !budgetStopped {
				report.StageObservation = &StageObservation{
					DispatchID: dispatch.ID, Role: dispatch.Recipient, Phase: "preparation-blocked",
					At: time.Now().UTC(), Deadline: stageDeadline(stored, dispatch, false),
					Reason: core.Redact(err.Error()),
				}
				if auditErr := appendBudgetAudit(state, stored, "STAGE_OBSERVED", report); auditErr != nil {
					return auditErr
				}
				stored.RunnerID = ""
				storeFlow(state, stored)
				flow = stored
				return nil
			}
			reason := "runner failed: " + err.Error()
			if holdErr := holdReviewFlowForStop(&state.Graph, &stored, reason); holdErr != nil {
				return holdErr
			}
			stored.StopCode = "SAFETY_STOP"
			stored.Status, stored.Reason = "STOPPED", reason
			stored.StopCause = "other"
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded) || budgetStopped {
				stored.StopCode = "BUDGET_STOP"
				stored.StopCause = ""
				stored.StopCause = flowStopCause(stored)
			}
			if stored.StopCode == "BUDGET_STOP" {
				if auditErr := appendBudgetAudit(state, stored, "BUDGET_STOP", taskBudgetReport(state.Graph, stored, time.Now().UTC())); auditErr != nil {
					return auditErr
				}
			}
		}
		stored.RunnerID = ""
		storeFlow(state, stored)
		flow = stored
		return nil
	})
	if flow.StopCode == "BUDGET_STOP" {
		if state, loadErr := db.LoadWorkspace(); loadErr == nil {
			if report, reportErr := EvaluateTaskBudget(state.Graph, flow.ID, time.Now().UTC()); reportErr == nil {
				if payload, marshalErr := json.Marshal(report); marshalErr == nil {
					saveErr = errors.Join(saveErr, json.NewEncoder(out).Encode(runtime.Event{
						Type: "BUDGET_STOP", Payload: string(payload),
					}))
				} else {
					saveErr = errors.Join(saveErr, marshalErr)
				}
			} else {
				saveErr = errors.Join(saveErr, reportErr)
			}
		} else {
			saveErr = errors.Join(saveErr, loadErr)
		}
	}
	return flow, errors.Join(err, saveErr)
}

// A released hold can leave a bound session before the first send. Reattach
// that session; never treat an execution-started Dispatch as a fresh turn.
func checkUnstartedDispatch(flow core.ReviewFlow, dispatch core.GraphDispatch) error {
	if flow.HandoffHeld() {
		return core.ErrHandoffHeld
	}
	released := len(flow.HandoffChanges) != 0
	if dispatch.ExecutionStarted || dispatch.Result != nil || dispatch.Acknowledged && !released ||
		dispatch.Status != core.DispatchIssued && !(released && dispatch.Status == core.DispatchAcknowledged) ||
		dispatch.RuntimeSessionID != "" && !released {
		return fmt.Errorf("taxion: execution is not an unstarted Dispatch; inspect its saved session and result before continuing")
	}
	return nil
}

func writeReviewBudget(ctx context.Context, out io.Writer, flow core.ReviewFlow, dispatch core.GraphDispatch) error {
	now := time.Now().UTC()
	deadline, source := flow.ExecutionDeadline(), "flow"
	if caller, ok := ctx.Deadline(); ok && caller.Before(deadline) {
		deadline, source = caller, "caller"
	}
	payload, err := json.Marshal(struct {
		Version               int       `json:"version"`
		FlowID                string    `json:"flowId"`
		DispatchID            string    `json:"dispatchId"`
		ObservedAt            time.Time `json:"observedAt"`
		FlowDeadline          time.Time `json:"flowDeadline"`
		EffectiveDeadline     time.Time `json:"effectiveDeadline"`
		DeadlineSource        string    `json:"deadlineSource"`
		RemainingMilliseconds int64     `json:"remainingMilliseconds"`
	}{1, flow.ID, dispatch.ID, now, flow.Deadline, deadline, source, max(deadline.Sub(now).Milliseconds(), 0)})
	if err != nil {
		return err
	}
	if err := json.NewEncoder(out).Encode(runtime.Event{Type: "review_budget", Payload: string(payload)}); err != nil {
		return fmt.Errorf("taxion: write review budget before runtime startup: %w", err)
	}
	return nil
}

func writeCreditStatus(ctx context.Context, encoder *json.Encoder, dispatch core.GraphDispatch, event runtime.Event, worker runtime.Identity) error {
	var usage core.DispatchUsage
	if err := json.Unmarshal([]byte(event.Payload), &usage); err != nil {
		return fmt.Errorf("taxion: decode per-call usage for credit status: %w", err)
	}
	accountQuota, accountUsedPercentage := readAccountQuotaForCreditStatus(ctx, dispatch, worker)
	status := "unavailable"
	if usage.AICreditsStatus != nil && *usage.AICreditsStatus != "" {
		status = *usage.AICreditsStatus
	}
	reason := "no validated same-unit account allowance is available"
	if usage.TotalNanoAIU == nil {
		reason = "per-call nano-AIU is unavailable; " + reason
	} else {
		reason = "per-call usage is reported in nano-AIU; " + reason
	}
	var sessionCeiling *float64
	if provider, ok := worker.(interface{ SessionCreditCeiling() float64 }); ok {
		if value := provider.SessionCreditCeiling(); value > 0 {
			sessionCeiling = &value
		}
	}
	payload, err := json.Marshal(struct {
		DispatchID             string                 `json:"dispatchId"`
		Role                   string                 `json:"role"`
		TurnNanoAIU            *float64               `json:"turnNanoAIU,omitempty"`
		AICreditsStatus        string                 `json:"aiCreditsStatus"`
		SessionCreditCeiling   *float64               `json:"sessionCreditCeiling,omitempty"`
		RemainingCreditBalance string                 `json:"remainingCreditBalance"`
		UsedPercentage         string                 `json:"usedPercentage"`
		RemainingPercentage    string                 `json:"remainingPercentage"`
		AccountQuota           *runtime.QuotaSnapshot `json:"accountQuota"`
		AccountUsedPercentage  *float64               `json:"accountUsedPercentage,omitempty"`
		Reason                 string                 `json:"reason"`
	}{
		DispatchID: dispatch.ID, Role: dispatch.Recipient, TurnNanoAIU: usage.TotalNanoAIU,
		AICreditsStatus: status, SessionCreditCeiling: sessionCeiling,
		RemainingCreditBalance: "unavailable", UsedPercentage: "unavailable", RemainingPercentage: "unavailable",
		AccountQuota: accountQuota, AccountUsedPercentage: accountUsedPercentage, Reason: reason,
	})
	if err != nil {
		return fmt.Errorf("taxion: encode per-call credit status: %w", err)
	}
	if err := encoder.Encode(runtime.Event{Type: "credit_status", Payload: string(payload)}); err != nil {
		return fmt.Errorf("taxion: write per-call credit status: %w", err)
	}
	return nil
}

func readAccountQuotaForCreditStatus(ctx context.Context, dispatch core.GraphDispatch,
	worker runtime.Identity) (*runtime.QuotaSnapshot, *float64) {
	quota := &runtime.QuotaSnapshot{
		Resource: "premium_interactions", Scope: "selected account", Unit: "requests",
		Source: "approval-gate", State: "unavailable", ObservedAt: time.Now().UTC(),
		Reason: "per-turn account quota read is not approved",
	}
	policy := dispatch.Instruction.BudgetGuidance
	if policy == nil || policy.Mode != "account" || !policy.AccountReadApproved {
		return quota, nil
	}
	if dispatch.Configuration == nil {
		quota.Reason = "selected runtime configuration is unavailable"
		return quota, nil
	}
	var selected config.Config
	if err := json.Unmarshal(dispatch.Configuration.Selection, &selected); err != nil {
		quota.Reason = "selected runtime configuration is invalid"
		return quota, nil
	}
	observation, err := readBudgetQuota(ctx, worker, selected, &budgetObservation{})
	if observation == nil || observation.Account == nil {
		quota.Reason = "account quota observation unavailable"
		if err != nil {
			quota.Reason = "account quota read failed; provider details withheld"
		}
		return quota, nil
	}
	quota = observation.Account
	if quota.State != "available" || quota.RemainingPercentage == nil {
		return quota, nil
	}
	usedPercentage := 100 - *quota.RemainingPercentage
	return quota, &usedPercentage
}

// StreamEvents finalizes only a terminal turn. Monitoring applies while waiting
// and after usage events, including before a final result can be adopted.
func (c *Controller) StreamEvents(ctx context.Context, dispatch core.GraphDispatch, cursor uint64, out io.Writer, check func() error) (err error) {
	defer c.cancelTurn(dispatch.ID)
	defer func() {
		if err == nil {
			return
		}
		var budgetErr *budgetStopError
		if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &budgetErr) {
			state, loadErr := c.store.LoadWorkspace()
			err = errors.Join(err, loadErr)
			if loadErr == nil {
				if flow, found := flowForDispatch(state.Graph, dispatch.ID); found && flow.Status == "RUNNING" {
					err = errors.Join(err, stopFlowForBudget(c.store, flow.ID, "stage execution stopped: "+err.Error()))
				}
			}
		}
		err = errors.Join(err, c.stopIfExecuting(dispatch.ID))
	}()
	finishCtx := ctx
	ctx, stageCancel, err := c.boundStageContext(ctx, dispatch, false)
	if err != nil {
		return err
	}
	defer stageCancel()
	if out == nil {
		return fmt.Errorf("taxion: runtime event output is required")
	}
	events, err := c.EventsAfter(ctx, dispatch.ID, cursor)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	encoder := json.NewEncoder(out)
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	dispatch, err = c.boundDispatch(dispatch.ID)
	if err != nil {
		return err
	}
	var initialQuota *budgetObservation
	var lastQuotaCheck time.Time
	for _, receipt := range state.Records.Events {
		if receipt.DispatchID != dispatch.ID || receipt.SessionID != dispatch.RuntimeSessionID || receipt.Type != "budget-guidance" {
			continue
		}
		var body struct {
			Observation *budgetObservation `json:"observation"`
			Phase       string             `json:"phase"`
		}
		if err := json.Unmarshal([]byte(receipt.Payload), &body); err != nil {
			return fmt.Errorf("taxion: invalid saved budget observation: %w", err)
		}
		if body.Phase == "prepared" || body.Phase == "send-returned" {
			if initialQuota == nil {
				initialQuota = body.Observation
			}
		}
		lastQuotaCheck = receipt.At
	}
	if initialQuota == nil && dispatch.Instruction.BudgetGuidance != nil && dispatch.Instruction.BudgetGuidance.Mode == "account" {
		return c.stopForAccountQuota(dispatch, "account quota monitoring requires the saved send observation")
	}
	for index := len(state.Records.Events) - 1; index >= 0; index-- {
		receipt := state.Records.Events[index]
		if receipt.DispatchID == dispatch.ID && receipt.Type == "budget-guidance" {
			if err := encoder.Encode(runtime.Event{Type: "budget-guidance", Payload: receipt.Payload}); err != nil {
				return fmt.Errorf("taxion: write budget guidance: %w", err)
			}
			break
		}
	}
	streamID := time.Now().UTC().Format(time.RFC3339Nano)
	monitor, _ := ctx.Value(progressMonitorKey{}).(bool)
	window := progressWindow{since: time.Now().UTC()}
	checkpoint := func(final bool) error {
		if !monitor {
			return nil
		}
		deadline, _ := ctx.Deadline()
		event := window.checkpoint(time.Now().UTC(), dispatch, deadline, final)
		if event == nil {
			return nil
		}
		if err := c.recordEvent(dispatch.ID, streamID, *event); err != nil {
			return err
		}
		return encoder.Encode(event)
	}
	var finalOutput string
	warned := ""
	warnResources := func() error {
		var event *runtime.Event
		var key string
		err := c.store.ObserveWorkspace(func(state store.Workspace) error {
			var err error
			event, key, err = resourceWarning(state, dispatch, time.Now().UTC())
			return err
		})
		if err != nil || event == nil || key == warned {
			return err
		}
		if err := c.recordEvent(dispatch.ID, streamID, *event); err != nil {
			return err
		}
		if err := encoder.Encode(event); err != nil {
			return err
		}
		warned = key
		return nil
	}
	checkLimits := func(final bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if check != nil {
			if err := check(); err != nil {
				return err
			}
		}
		if initialQuota != nil && initialQuota.Mode == "account" &&
			(final || time.Since(lastQuotaCheck) >= time.Minute ||
				initialQuota.Account != nil && initialQuota.Account.ResetAt != nil && !time.Now().Before(*initialQuota.Account.ResetAt)) {
			if err := c.monitorAccountQuota(ctx, dispatch, initialQuota); err != nil {
				return err
			}
			lastQuotaCheck = time.Now().UTC()
		}
		if !final {
			return warnResources()
		}
		return nil
	}
	if err := warnResources(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("taxion: event wait: %w", ctx.Err())
		case <-ticker.C:
			if err := checkLimits(false); err != nil {
				return err
			}
			if err := checkpoint(false); err != nil {
				return err
			}
		case event, ok := <-events:
			if !ok {
				return fmt.Errorf("taxion: event stream ended before a terminal turn event")
			}
			if event.AgentID != "" && (event.Type == runtime.EventOutput || event.Type == runtime.EventTurnFinished ||
				event.Type == runtime.EventFailed || event.Type == runtime.EventCancelled) {
				if err := encoder.Encode(event); err != nil {
					return fmt.Errorf("taxion: write child runtime event: %w", err)
				}
				continue
			}
			if monitor {
				window.observe(event)
			}
			switch event.Type {
			case runtime.EventTurnFinished, runtime.EventFailed, runtime.EventCancelled:
				if err := checkpoint(true); err != nil {
					return err
				}
			}
			switch event.Type {
			case runtime.EventOutput, runtime.EventUsage, runtime.EventUsageCheckpoint, runtime.EventTurnFinished, runtime.EventFailed, runtime.EventCancelled, runtime.EventExecution,
				runtime.EventContextUsage, "acp_capabilities", "acp_usage_unavailable", "acp_tool_update", "acp_message_start":
				if err := c.recordEvent(dispatch.ID, streamID, event); err != nil {
					return err
				}
			}
			if err := encoder.Encode(event); err != nil {
				return fmt.Errorf("taxion: write runtime event: %w", err)
			}
			switch event.Type {
			case runtime.EventOutput:
				finalOutput = event.Payload
			case runtime.EventUsage:
				if err := writeCreditStatus(ctx, encoder, dispatch, event, c.runtime); err != nil {
					return err
				}
				if err := checkLimits(false); err != nil {
					return err
				}
			case runtime.EventUsageCheckpoint:
				if err := checkLimits(false); err != nil {
					return err
				}
			case runtime.EventTurnFinished:
				if err := checkLimits(true); err != nil {
					return err
				}
				observed, finishErr := c.Finish(finishCtx, dispatch.ID, finalOutput)
				if finishErr != nil && observed == nil {
					return errors.Join(finishErr, c.Fail(dispatch.ID, finishErr.Error()))
				}
				return finishErr
			case runtime.EventFailed:
				return errors.Join(fmt.Errorf("taxion: execution failed: %s", event.Payload), c.Fail(dispatch.ID, event.Payload))
			case runtime.EventCancelled:
				return fmt.Errorf("taxion: execution cancelled: %s", event.Payload)
			}
		}
	}
}

func (c *Controller) stopIfExecuting(id string) error {
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	dispatch, ok := state.Graph.GraphDispatch(id)
	if !ok {
		return fmt.Errorf("taxion: Dispatch %s disappeared during safe stop", id)
	}
	if !active(dispatch.Status) || dispatch.RuntimeSessionID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := c.Stop(ctx, id); err != nil {
		return fmt.Errorf("taxion: safe stop Dispatch %s: %w", id, err)
	}
	return nil
}
