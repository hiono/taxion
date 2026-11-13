package controller

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

type unavailableResume struct{ *fakeRuntime }

func (unavailableResume) Resume(context.Context, string) error {
	return errors.New("saved runtime session is unavailable")
}

func TestRestartRoleSessionAndExecutionAuthorization(t *testing.T) {
	for _, role := range []string{"CODER", "REVIEWER"} {
		for _, phase := range []string{"issued", "bound", "running", "missing-session"} {
			t.Run(role+"/"+phase, func(t *testing.T) {
				db, ledger, request := reviewFlowFixture(t)
				flow, err := StartReviewFlow(db, ledger, request)
				if err != nil {
					t.Fatal(err)
				}
				if role == "REVIEWER" {
					executeFlowDispatch(t, db, flow, "", true)
					flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := PrepareReview(db, flow.DispatchIDs[1], flow.Contract.Worktree); err != nil {
						t.Fatal(err)
					}
				}
				id := flow.DispatchIDs[len(flow.DispatchIDs)-1]
				state, _ := db.LoadWorkspace()
				dispatch, _ := state.Graph.GraphDispatch(id)
				worker := &fakeRuntime{role: role, hash: "restart-fixture", worktree: dispatch.Instruction.Worktree}
				control, err := New(db, worker)
				if err != nil {
					t.Fatal(err)
				}
				if phase != "issued" {
					if _, err := control.Start(t.Context(), id); err != nil {
						t.Fatal(err)
					}
				}
				if phase == "running" {
					if err := control.Send(t.Context(), id, "original instruction"); err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				control, err = New(db, worker)
				if err != nil {
					t.Fatal(err)
				}
				if phase == "missing-session" {
					control, err = New(db, unavailableResume{worker})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := control.Start(t.Context(), id); err == nil {
						t.Fatal("missing session accepted")
					}
					if worker.starts != 1 || len(worker.sends) != 0 {
						t.Fatal("missing session replaced or instructions resent")
					}
					return
				}
				if _, err := control.Start(t.Context(), id); err != nil {
					t.Fatal(err)
				}
				if worker.starts != 1 {
					t.Fatal("restart duplicated runtime session")
				}
				if phase == "running" {
					if err := control.Send(t.Context(), id, "repeat"); err == nil || len(worker.sends) != 1 {
						t.Fatal("interrupted execution resent")
					}
				} else if err := control.Send(t.Context(), id, "first authorized instruction"); err != nil || len(worker.sends) != 1 {
					t.Fatal("authorized unstarted execution", err)
				}
				state, _ = db.LoadWorkspace()
				saved, _ := state.Graph.GraphDispatch(id)
				if saved.Status != core.DispatchRunning || saved.RuntimeSessionID != "sdk-session" {
					t.Fatal("restart binding", saved)
				}
			})
		}
	}
}
