package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func publicationScanner(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scanner")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then echo 1.9.0; exit 0; fi\ncat >/dev/null\nprintf '[]'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

type publicationFixture struct {
	mu      sync.Mutex
	sends   int
	found   bool
	failure bool
	entered chan struct{}
	release chan struct{}
}

func (*publicationFixture) Check(context.Context, store.ExternalUpdate) error { return nil }
func (p *publicationFixture) Reconcile(context.Context, store.ExternalUpdate) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return "99", p.found, nil
}
func (p *publicationFixture) Send(_ context.Context, _ store.ExternalUpdate) (string, error) {
	p.mu.Lock()
	p.sends++
	p.found = true
	fail := p.failure
	p.mu.Unlock()
	if p.entered != nil {
		close(p.entered)
		<-p.release
	}
	if fail {
		return "", errors.New("simulated response lost after publication")
	}
	return "99", nil
}

func publicationProposal() store.ExternalUpdate {
	return store.ExternalUpdate{ID: "accepted-report", TaskID: "implementation", Provider: "github", Host: "github.com",
		Repository: "owner/repo", Kind: "ISSUE", Number: 53, Action: "COMMENT", Body: "Approved execution evidence.",
		Actor: "human", ApprovalSource: "human:exact-report", Account: "owner", CredentialRef: "gh:active"}
}

func TestApprovedPublicationClaimRestartAndUncertainDelivery(t *testing.T) {
	db, ledger, _ := reviewFlowFixture(t)
	scanner := publicationScanner(t)
	proposal := publicationProposal()
	hash, err := PublicationHash(proposal)
	if err != nil {
		t.Fatal(err)
	}
	request := PublicationRequest{Update: proposal, ExpectedHash: hash}
	invalid := request
	invalid.Update.Body = "different report"
	if _, err := ApprovePublication(t.Context(), db, invalid, scanner); err == nil {
		t.Fatal("modified approval accepted")
	}
	if _, err := ApprovePublication(t.Context(), db, request, "missing-scanner"); err == nil {
		t.Fatal("missing scan accepted")
	}
	for range 2 {
		if _, err := ApprovePublication(t.Context(), db, request, scanner); err != nil {
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
	publisher := &publicationFixture{failure: true}
	update, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher)
	if err == nil || update.Status != "UNKNOWN" {
		t.Fatalf("%+v %v", update, err)
	}
	if _, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher); err == nil {
		t.Fatal("uncertain delivery resent")
	}
	if publisher.sends != 1 {
		t.Fatal("duplicate remote write")
	}
	if _, err := ReconcilePublication(t.Context(), db, proposal.ID, "wrong", true, publisher); err == nil {
		t.Fatal("wrong sender recovered")
	}
	update, err = ReconcilePublication(t.Context(), db, proposal.ID, update.Claim, true, publisher)
	if err != nil || update.Status != "SENT" || update.RemoteID != "99" {
		t.Fatalf("%+v %v", update, err)
	}
	if _, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher); err != nil || publisher.sends != 1 {
		t.Fatal("delivered report resent", err)
	}
	state, err := db.LoadWorkspace()
	if err != nil || len(state.Records.Updates) != 1 {
		t.Fatal("replay duplicated local approval", err)
	}
}

func TestPublicationConcurrentConsumersAndCloseEvidence(t *testing.T) {
	db, ledger, flow := completedFixture(t)
	proposal := publicationProposal()
	scanner := publicationScanner(t)
	hash, _ := PublicationHash(proposal)
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, scanner); err != nil {
		t.Fatal(err)
	}
	other, err := store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	publisher := &publicationFixture{entered: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher); done <- err }()
	<-publisher.entered
	if _, err := DeliverPublication(t.Context(), other, proposal.ID, scanner, publisher); err == nil {
		t.Fatal("second consumer acquired claim")
	}
	close(publisher.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	closeProposal := proposal
	closeProposal.ID, closeProposal.Action, closeProposal.Body, closeProposal.FlowID = "close", "CLOSE", "", flow.ID
	hash, _ = PublicationHash(closeProposal)
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{closeProposal, hash}, scanner); err != nil {
		t.Fatal(err)
	}
	gitTest(t, flow.Contract.Worktree, "commit", "--allow-empty", "-m", "chore: external update")
	before, _ := db.LoadWorkspace()
	if _, err := DeliverPublication(t.Context(), db, closeProposal.ID, scanner, publisher); err == nil {
		t.Fatal("changed HEAD allowed Issue close")
	}
	after, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed close claim changed records")
	}
}

func TestFailureAnalysisKeepsProposedLessonAndDecision(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	dispatch := executeFlowDispatch(t, db, flow, "FAILED", true)
	body, _ := json.Marshal(dispatch.Result)
	record := store.FailureRecord{ID: "failure-1", TaskID: dispatch.TaskID, DispatchID: dispatch.ID,
		ResultHash: receiptHash(string(body)), Cause: "missing tool", Correction: "restore approved tool",
		LessonCandidate: "check tool availability before dispatch", ValidationEvidence: []string{"dispatch:" + dispatch.ID + ":result"},
		Actor: "human", DecisionSource: "human:failure-disposition"}
	for range 2 {
		if _, err := RecordFailureAnalysis(db, record); err != nil {
			t.Fatal(err)
		}
	}
	record.Cause = "new decision"
	if _, err := RecordFailureAnalysis(db, record); err == nil {
		t.Fatal("decision overwritten")
	}
	record.ID = "invented-validation"
	record.ValidationEvidence = []string{"invented:test-success"}
	if _, err := RecordFailureAnalysis(db, record); err == nil {
		t.Fatal("unsaved validation evidence accepted")
	}
	state, _ := db.LoadWorkspace()
	if len(state.Records.Findings) != 1 || state.Graph.Tasks[0].Status != core.Blocked {
		t.Fatal("finding changed execution decision")
	}
}

func TestCreateIssueRecoveryLinksOnlyApprovedDraft(t *testing.T) {
	db, ledger, _ := reviewFlowFixture(t)
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		task, _ := state.Graph.GraphTask("implementation")
		task.Status, task.Issue = core.Draft, "PENDING:registration"
		task.DependsOn = []core.TaskDependency{{TaskID: "registration", State: core.Completed}}
		graph := core.TaskGraph{Version: 1}
		registration := task
		registration.ID, registration.Type, registration.Issue = "registration", "EVIDENCE_COLLECTION", "Issue #56"
		registration.DependsOn = nil
		if err := graph.Track(registration, "human", "track Issue registration evidence"); err != nil {
			return err
		}
		if err := graph.Track(task, "human", "prepare independent Issue registration"); err != nil {
			return err
		}
		state.Graph = graph
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: registration.ID, Title: registration.Title, Repo: registration.Repo})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	proposal := publicationProposal()
	proposal.Action, proposal.Number, proposal.Title = "CREATE", 0, "Independent implementation"
	hash, _ := PublicationHash(proposal)
	scanner := publicationScanner(t)
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, scanner); err != nil {
		t.Fatal(err)
	}
	duplicate := proposal
	duplicate.ID = "different-registration"
	duplicateHash, _ := PublicationHash(duplicate)
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{duplicate, duplicateHash}, scanner); err == nil {
		t.Fatal("duplicate Task registration accepted")
	}
	publisher := &publicationFixture{failure: true}
	update, err := DeliverPublication(t.Context(), db, proposal.ID, scanner, publisher)
	if err == nil || update.Status != "UNKNOWN" {
		t.Fatal(update, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	update, err = ReconcilePublication(t.Context(), db, proposal.ID, update.Claim, true, publisher)
	if err != nil || update.Status != "SENT" || publisher.sends != 1 {
		t.Fatal(update, err)
	}
	state, err := db.LoadWorkspace()
	task, _ := state.Graph.GraphTask(proposal.TaskID)
	if err != nil || task.Issue != "Issue #99" || task.Status != core.Draft {
		t.Fatal("Issue registration lost or authorized execution", task, err)
	}
	if _, err := ApprovePublication(t.Context(), db, PublicationRequest{proposal, hash}, scanner); err != nil {
		t.Fatal("registration replay rejected", err)
	}
}
