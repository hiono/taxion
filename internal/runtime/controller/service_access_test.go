package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestServiceDeniedStartStopsUnboundSessionAndNeverSends(t *testing.T) {
	db, id, worker, _ := executionConfigurationFixture(t)
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	worker.serviceErr = errors.New("service unavailable")
	if _, err := control.Start(context.Background(), id); !errors.Is(err, worker.serviceErr) {
		t.Fatal("denied service started execution", err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, _ := state.Graph.GraphDispatch(id)
	if dispatch.RuntimeSessionID != "" || worker.stops != 1 || worker.serviceChecks != 1 || len(worker.sends) != 0 {
		t.Fatal("denied service bound or sent to the session, or lost cleanup")
	}
}

func TestServiceDeniedResumeAndSendDoNotRunOrBlockSafeStop(t *testing.T) {
	db, id, worker, _ := executionConfigurationFixture(t)
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	worker.serviceErr = errors.New("service access revoked")
	for _, operation := range []func() error{
		func() error { _, err := control.Start(context.Background(), id); return err },
		func() error { return control.Resume(context.Background(), id) },
		func() error { return control.Send(context.Background(), id, "must not send") },
	} {
		if err := operation(); !errors.Is(err, worker.serviceErr) {
			t.Fatal("denied service continued", err)
		}
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, _ := state.Graph.GraphDispatch(id)
	if dispatch.Status == core.DispatchRunning || len(worker.sends) != 0 || worker.serviceChecks != 4 {
		t.Fatal("denied service marked execution running or sent a prompt")
	}
	checks := worker.serviceChecks
	if err := control.Stop(context.Background(), id); err != nil || worker.serviceChecks != checks {
		t.Fatal("safe stop required service availability", err)
	}
}

func TestServiceAccessRechecksApprovalAfterProviderObservation(t *testing.T) {
	for _, operation := range []string{"START", "RESUME", "SEND"} {
		t.Run(operation, func(t *testing.T) {
			db, id, worker, _ := executionConfigurationFixture(t)
			control, err := New(db, worker)
			if err != nil {
				t.Fatal(err)
			}
			if operation != "START" {
				if _, err := control.Start(context.Background(), id); err != nil {
					t.Fatal(err)
				}
			}
			worker.onService = func() {
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					return state.Graph.Transition("task", core.Blocked, "human", "revoke during service check", core.TransitionProof{})
				}); err != nil {
					t.Fatal(err)
				}
			}
			switch operation {
			case "START":
				_, err = control.Start(context.Background(), id)
			case "RESUME":
				err = control.Resume(context.Background(), id)
			case "SEND":
				err = control.Send(context.Background(), id, "must not send")
			}
			if err == nil || len(worker.sends) != 0 {
				t.Fatal("revoked approval passed service observation")
			}
			if operation == "START" && worker.stops != 1 {
				t.Fatal("revoked approval left unbound session unstopped")
			}
		})
	}
}
