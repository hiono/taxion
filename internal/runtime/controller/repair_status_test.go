package controller

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestObserveRepairStatusClassifiesStops(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	started, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	load := func() store.Workspace {
		t.Helper()
		state, err := db.LoadWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		return state
	}
	observe := func(state store.Workspace) RepairStatus {
		t.Helper()
		status, err := ObserveRepairStatus(state, started.ID, core.ApprovalConfiguration{})
		if err != nil {
			t.Fatal(err)
		}
		if status.StateChanged || status.Version != RepairStatusVersion || len(status.Reasons) != 1 || status.NextAction == "" {
			t.Fatalf("malformed status: %+v", status)
		}
		return status
	}
	if got := observe(load()).Status; got != RepairNotApplicable {
		t.Fatalf("issued Coder Dispatch = %s", got)
	}

	executeFlowDispatch(t, db, started, "", true)
	flow, err := AdvanceReviewFlow(db, ledger, started.ID)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
	before := load()
	authorized := observe(before)
	if authorized.Status != RepairAuthorized || authorized.Repairs != 0 || authorized.MaxRepairs != 1 {
		t.Fatalf("repair boundary = %+v", authorized)
	}
	if after := load(); !reflect.DeepEqual(mustFlowJSON(t, before.Graph), mustFlowJSON(t, after.Graph)) {
		t.Fatal("observation changed the saved graph")
	}

	cases := []struct {
		name   string
		change func(*store.Workspace, *core.ReviewFlow)
		want   string
	}{
		{"deadline", func(_ *store.Workspace, f *core.ReviewFlow) {
			f.StartedAt, f.Deadline = time.Now().UTC().Add(-time.Hour), time.Now().UTC().Add(-time.Second)
		}, RepairCeilingExhausted},
		{"budget-stop", func(_ *store.Workspace, f *core.ReviewFlow) {
			f.Status, f.StopCode, f.Reason = "STOPPED", "BUDGET_STOP", "stop"
		}, RepairCeilingExhausted},
		{"budget-limit", func(_ *store.Workspace, f *core.ReviewFlow) {
			f.Status, f.StopCode, f.Reason = "STOPPED", "BUDGET_LIMIT", "stop"
		}, RepairCeilingExhausted},
		{"recontract", func(_ *store.Workspace, f *core.ReviewFlow) {
			f.Status, f.StopCode, f.Reason = "STOPPED", "RECONTRACT", "stop"
		}, RepairReapprovalRequired},
		{"safety-stop", func(_ *store.Workspace, f *core.ReviewFlow) {
			f.Status, f.StopCode, f.Reason = "STOPPED", "SAFETY_STOP", "stop"
		}, RepairHumanDecision},
		{"bundle-required", func(s *store.Workspace, _ *core.ReviewFlow) {
			s.Graph.ApprovalSchemaVersion = core.CurrentApprovalSchemaVersion
		}, RepairReapprovalRequired},
		{"cancelled-task", func(s *store.Workspace, f *core.ReviewFlow) {
			if err := s.Graph.Transition(f.TaskID, core.Cancelled, "human", "cancel", core.TransitionProof{}); err != nil {
				t.Fatal(err)
			}
		}, RepairHumanDecision},
		{"task-left-review", func(s *store.Workspace, f *core.ReviewFlow) {
			if err := s.Graph.Transition(f.TaskID, core.RequestChanges, "human", "manual", core.TransitionProof{}); err != nil {
				t.Fatal(err)
			}
		}, RepairHumanDecision},
		{"held-handoff", func(s *store.Workspace, f *core.ReviewFlow) {
			held, err := s.Graph.ChangeHandoff(f.ID, core.HandoffChange{ID: "hold-1", Action: "HOLD", Actor: "human", Source: "test", Reason: "hold"})
			if err != nil {
				t.Fatal(err)
			}
			*f = held
		}, RepairHumanDecision},
		{"unknown-approval-schema", func(s *store.Workspace, _ *core.ReviewFlow) {
			s.Graph.ApprovalSchemaVersion = core.CurrentApprovalSchemaVersion + 1
		}, RepairInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := load()
			flow, _ := state.Graph.ReviewFlow(started.ID)
			tc.change(&state, &flow)
			storeFlow(&state, flow)
			if got := observe(state); got.Status != tc.want {
				t.Fatalf("status = %+v, want %s", got, tc.want)
			}
		})
	}

	if _, err := ObserveRepairStatus(load(), "missing", core.ApprovalConfiguration{}); err == nil {
		t.Fatal("missing flow accepted")
	}
}

func TestObserveRepairStatusCeilingBeforeAndAfterStop(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	zero := 0
	request.MaxRepairs = &zero
	started, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, started, "", true)
	flow, err := AdvanceReviewFlow(db, ledger, started.ID)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
	for _, stop := range []bool{false, true} {
		if stop {
			if _, err := AdvanceReviewFlow(db, ledger, started.ID); !errors.Is(err, ErrFlowStopped) {
				t.Fatalf("advance at ceiling = %v", err)
			}
		}
		state, err := db.LoadWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		status, err := ObserveRepairStatus(state, started.ID, core.ApprovalConfiguration{})
		if err != nil || status.Status != RepairCeilingExhausted {
			t.Fatalf("stopped=%v status = %+v, %v", stop, status, err)
		}
	}
}
