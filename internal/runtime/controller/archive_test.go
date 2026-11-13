package controller

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestArchiveStoppedFlowPreservesHistoryAndBlocksReuse(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	archive := ArchiveRequest{TaskIDs: []string{request.TaskID}, Actor: "human", Source: "human:discard", Reason: "obsolete trial"}
	if _, err := ArchiveTasks(db, archive, true); err == nil {
		t.Fatal("running flow was archivable")
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	review := executeFlowDispatch(t, db, flow, "CANCELLED", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if !errors.Is(err, ErrFlowStopped) {
		t.Fatal("expected stopped flow", err)
	}
	// Selecting an issued role must include its owning flow as well.
	archive.TaskIDs = []string{review.TaskID}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	preview, err := ArchiveTasks(db, archive, true)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(preview.TaskIDs, request.TaskID) || !slices.Contains(preview.TaskIDs, review.TaskID) ||
		len(preview.FlowIDs) != 1 || len(preview.DispatchIDs) != 2 {
		t.Fatal("archive omitted related flow records", preview)
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("preview mutated workspace")
	}
	for _, mutate := range []func(*store.Workspace){
		func(state *store.Workspace) { state.Graph.Dispatches[0].Status = core.DispatchSuspended },
		func(state *store.Workspace) { state.Graph.ReviewFlows[0].RunnerID = "unreconciled-runner" },
		func(state *store.Workspace) { state.Graph.ReviewFlows[0].Status = "RUNNING" },
		func(state *store.Workspace) { state.Ledger.Dispatches[0].Active = true },
		func(state *store.Workspace) {
			state.Records.Updates = []store.ExternalUpdate{{ID: "pending-write", TaskID: request.TaskID, Status: "UNKNOWN"}}
		},
	} {
		state, err := db.LoadWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		mutate(&state)
		if _, err := previewArchive(state, archive); err == nil {
			t.Fatal("archive accepted active or unreconciled work")
		}
	}
	archive.ExpectedHash = "stale"
	if _, err := ArchiveTasks(db, archive, false); err == nil {
		t.Fatal("stale preview accepted")
	}
	archive.ExpectedHash = preview.Hash
	changedReason := archive
	changedReason.Reason = "different approval"
	if _, err := ArchiveTasks(db, changedReason, false); err == nil {
		t.Fatal("changed approval reused the preview hash")
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.AddConcern(core.Concern{ID: "new-evidence", TaskID: request.TaskID,
			Source: "human:review", Text: "New evidence since preview"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := ArchiveTasks(db, archive, false); err == nil {
		t.Fatal("changed workspace reused the preview hash")
	}
	before, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	preview, err = ArchiveTasks(db, archive, true)
	if err != nil {
		t.Fatal(err)
	}
	archive.ExpectedHash = preview.Hash
	if _, err := ArchiveTasks(db, archive, false); err != nil {
		t.Fatal(err)
	}
	after, _ = db.LoadWorkspace()
	if len(after.Graph.Archives) != 1 {
		t.Fatal("archive receipt missing")
	}
	savedArchive := after.Graph.Archives
	after.Graph.Archives = nil
	if !reflect.DeepEqual(before, after) {
		t.Fatal("archive rewrote contracts, receipts or history")
	}
	after.Graph.Archives = savedArchive
	plan, err := Plan(db, ledger, request.Repository)
	if err != nil || len(plan.Tasks) != 0 || len(plan.ReviewFlows) != 0 {
		t.Fatal("archive leaked into default planning", plan, err)
	}
	history, err := PlanWithArchived(db, ledger, request.Repository, true)
	if err != nil || len(history.Tasks) != len(preview.TaskIDs) || len(history.ReviewFlows) != 1 {
		t.Fatal("archive history disappeared", history, err)
	}
	for _, task := range history.Tasks {
		if task.Action != "ARCHIVED" {
			t.Fatal("archived Task was executable planning input", task)
		}
	}
	context, err := ObservePlannerContext(db, ledger, request.Repository, request.TaskID)
	if err != nil || context.RecordedNextCheck != "ARCHIVED" {
		t.Fatal("explicit history lookup failed", err)
	}
	task, _ := after.Graph.GraphTask(request.TaskID)
	if err := after.Graph.Track(task, "human", "reuse"); err == nil {
		t.Fatal("archived Task could be edited")
	}
	if err := after.Graph.Transition(task.ID, core.Cancelled, "human", "reuse", core.TransitionProof{}); err == nil {
		t.Fatal("archived Task could transition")
	}
	if err := after.Graph.CheckHandoff(task.ID); err == nil {
		t.Fatal("archived Task could hand off")
	}
	if err := after.Graph.CheckHandoffStart(flow.DispatchIDs[0]); err == nil {
		t.Fatal("previously started archived Dispatch could resume")
	}
	if _, err := ArchiveTasks(db, archive, false); err == nil {
		t.Fatal("archive replay accepted")
	}
	for _, mutate := range []func(*store.Workspace){
		func(state *store.Workspace) { state.Graph.Archives = nil },
		func(state *store.Workspace) { state.Graph.Archives[0].Reason = "rewrite" },
		func(state *store.Workspace) { state.Graph.Tasks[0].Title = "rewrite" },
		func(state *store.Workspace) { state.Graph.Tasks[0].Decisions[0].Text = "rewrite" },
		func(state *store.Workspace) { state.Graph.ReviewFlows[0].Reason = "rewrite" },
		func(state *store.Workspace) { state.Graph.Dispatches[0].Result.Evidence[0] = "rewrite" },
		func(state *store.Workspace) { state.Graph.History[0].Reason = "rewrite" },
	} {
		if err := db.UpdateWorkspace(func(state *store.Workspace) error { mutate(state); return nil }); err == nil {
			t.Fatal("archived record could be rewritten")
		}
	}
	if _, err := os.Stat(filepath.Join(flow.Contract.Worktree, "internal/app.go")); err != nil {
		t.Fatal("archive removed Git work", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	durable, err := reopened.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(durable.Graph.Archives, savedArchive) {
		t.Fatal("archive did not survive restart", err)
	}
}
