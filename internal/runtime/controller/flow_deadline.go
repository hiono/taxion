package controller

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type flowDeadlineContext struct {
	context.Context
	deadline atomic.Pointer[time.Time]
	parent   context.Context
}

func ReviewFlowContext(parent context.Context, db *store.Store, id string) (context.Context, context.CancelFunc, error) {
	return withFlowDeadline(parent, db, id, nil, false)
}

func (c *flowDeadlineContext) Deadline() (time.Time, bool) {
	deadline := *c.deadline.Load()
	if _, dynamic := c.parent.(*flowDeadlineContext); !dynamic {
		if outer, ok := c.parent.Deadline(); ok && outer.Before(deadline) {
			deadline = outer
		}
	}
	return deadline, true
}

func (c *flowDeadlineContext) Err() error {
	if c.Context.Err() != nil && context.Cause(c.Context) == context.DeadlineExceeded {
		return context.DeadlineExceeded
	}
	return c.Context.Err()
}

// withFlowDeadline re-reads formal extensions without replacing the worker's
// context/session. Parent cancellation and independent caller deadlines still win.
func withFlowDeadline(parent context.Context, db *store.Store, id string, dispatch *core.GraphDispatch, validation bool) (context.Context, context.CancelFunc, error) {
	deadline := func() (time.Time, error) {
		var at time.Time
		err := db.ObserveWorkspace(func(state store.Workspace) error {
			flow, ok := state.Graph.ReviewFlow(id)
			if !ok || flow.Status != "RUNNING" {
				return ErrFlowStopped
			}
			at = flow.ExecutionDeadline()
			if dispatch != nil {
				at = stageDeadline(flow, *dispatch, validation)
			}
			return nil
		})
		return at, err
	}
	first, err := deadline()
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancelCause(parent)
	bounded := &flowDeadlineContext{Context: ctx, parent: parent}
	bounded.deadline.Store(&first)
	// Recheck at the old boundary itself: an approved transaction may have
	// committed after the previous poll, but before the hard deadline.
	go func() {
		for {
			at, err := deadline()
			if err != nil {
				cancel(err)
				return
			}
			bounded.deadline.Store(&at)
			if !time.Now().Before(at) {
				cancel(context.DeadlineExceeded)
				return
			}
			timer := time.NewTimer(min(250*time.Millisecond, time.Until(at)))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return bounded, func() { cancel(context.Canceled) }, nil
}
