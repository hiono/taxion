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

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type WorkerFactory func(core.GraphDispatch) (runtime.Identity, error)

// RunReviewFlow holds a durable single-runner claim. An interrupted process's
// claim remains visible so another process cannot silently resume uncertain work.
func RunReviewFlow(ctx context.Context, db *store.Store, ledgerPath, id string, factory WorkerFactory, out io.Writer) (core.ReviewFlow, error) {
	return runReviewFlow(ctx, db, ledgerPath, id, factory, out, false)
}

// StepReviewFlow executes at most one role turn. It does not issue the next
// handoff after the turn completes; continuation remains an explicit decision.
func StepReviewFlow(ctx context.Context, db *store.Store, ledgerPath, id string, factory WorkerFactory, out io.Writer) (core.ReviewFlow, error) {
	return runReviewFlow(ctx, db, ledgerPath, id, factory, out, true)
}

func runReviewFlow(ctx context.Context, db *store.Store, ledgerPath, id string, factory WorkerFactory, out io.Writer, singleTurn bool) (core.ReviewFlow, error) {
	if db == nil || factory == nil || out == nil {
		return core.ReviewFlow{}, fmt.Errorf("taxion: flow runner requires a store, worker factory and event output")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return core.ReviewFlow{}, err
	}
	runnerID := hex.EncodeToString(nonce)
	var flow core.ReviewFlow
	err := db.UpdateWorkspace(func(state *store.Workspace) error {
		var ok bool
		flow, ok = state.Graph.ReviewFlow(id)
		if !ok || flow.Status != "RUNNING" {
			return fmt.Errorf("taxion: runner requires a running review flow")
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
	runCtx, cancel := context.WithDeadline(ctx, flow.Deadline)
	defer cancel()
	for {
		flow, err = advanceReviewFlow(db, ledgerPath, id, runnerID)
		if err != nil || flow.Status != "RUNNING" {
			break
		}
		if err = runCtx.Err(); err != nil {
			break
		}
		state, loadErr := db.LoadWorkspace()
		if loadErr != nil {
			err = loadErr
			break
		}
		dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
		releasedUnstarted := len(flow.HandoffChanges) != 0 && !flow.HandoffHeld() && !dispatch.ExecutionStarted
		if dispatch.Status != core.DispatchIssued && !(releasedUnstarted && dispatch.Status == core.DispatchAcknowledged) ||
			dispatch.RuntimeSessionID != "" && !releasedUnstarted {
			err = fmt.Errorf("taxion: continuous runner requires a fresh issued Dispatch; reconcile existing session %s", dispatch.RuntimeSessionID)
			break
		}
		if dispatch.Recipient == "REVIEWER" {
			dispatch, err = PrepareReview(db, dispatch.ID, flow.Contract.Worktree)
			if err != nil {
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
				_, checkErr := advanceReviewFlow(db, ledgerPath, id, runnerID)
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
			stored.Status, stored.Reason = "STOPPED", "runner failed: "+err.Error()
		}
		stored.RunnerID = ""
		storeFlow(state, stored)
		flow = stored
		return nil
	})
	return flow, errors.Join(err, saveErr)
}

// StreamEvents finalizes only a terminal turn. Monitoring applies while waiting
// and after usage events, including before a final result can be adopted.
func (c *Controller) StreamEvents(ctx context.Context, dispatch core.GraphDispatch, cursor uint64, out io.Writer, check func() error) (err error) {
	defer func() {
		if err != nil {
			err = errors.Join(err, c.stopIfExecuting(dispatch.ID))
		}
	}()
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
	streamID := time.Now().UTC().Format(time.RFC3339Nano)
	var finalOutput string
	checkLimits := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if check != nil {
			return check()
		}
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("taxion: event wait: %w", ctx.Err())
		case <-ticker.C:
			if err := checkLimits(); err != nil {
				return err
			}
		case event, ok := <-events:
			if !ok {
				return fmt.Errorf("taxion: event stream ended before a terminal turn event")
			}
			if err := encoder.Encode(event); err != nil {
				return fmt.Errorf("taxion: write runtime event: %w", err)
			}
			switch event.Type {
			case runtime.EventOutput, runtime.EventUsage, runtime.EventTurnFinished, runtime.EventFailed, runtime.EventCancelled:
				if err := c.recordEvent(dispatch.ID, streamID, event); err != nil {
					return err
				}
			}
			switch event.Type {
			case runtime.EventOutput:
				finalOutput = event.Payload
			case runtime.EventUsage:
				if err := checkLimits(); err != nil {
					return err
				}
			case runtime.EventTurnFinished:
				if err := checkLimits(); err != nil {
					return err
				}
				observed, finishErr := c.Finish(ctx, dispatch.ID, finalOutput)
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
