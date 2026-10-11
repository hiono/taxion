package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/store"
)

const investigationAnswer = `{"confirmed":[{"text":"The file was read in this investigation, not in the previous role.","sources":["/investigation/files/0/content","/investigation/sha"]}],"assumptions":[],"unconfirmed":["This does not prove tests ran."]}`

func TestInvestigationUsesApprovedReadOnlySourcesWithSeparateObservation(t *testing.T) {
	db, databasePath, worker, resolved := questionFixture(t)
	worker.role = "ORCHESTRATOR"
	q := &questionRuntime{configuredRuntime: worker, events: questionEvents(investigationAnswer)}
	if err := os.WriteFile(filepath.Join(worker.Worktree(), "README.md"), []byte("uncommitted private content"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(databasePath), "ledger.json")
	result, err := AskInvestigation(context.Background(), db, path, "task", "What does the file say?", resolved.Config, 1000, q,
		InvestigationRequest{Paths: []string{"README.md"}, MaxBytes: 100})
	if err != nil {
		t.Fatal(err)
	}
	files := result.Context.Investigation
	if files == nil || files.Files[0].Content != "baseline" || files.Observation.ContentHash == result.Context.Observation.ContentHash ||
		files.Observation.StartedAt.IsZero() || result.Answer == nil || result.Changed ||
		q.starts != 1 || q.stops != 1 || len(q.sends) != 1 || strings.Contains(q.sends[0], "uncommitted private content") {
		t.Fatal("investigation lost provenance, leaked mutable bytes or bypassed isolation", result)
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("investigation changed main history or approval", err)
	}
	if err := verifyInvestigation(context.Background(), result.Context); err != nil {
		t.Fatal(err)
	}
	files.Files[0].Content = "forged"
	if err := verifyInvestigation(context.Background(), result.Context); err == nil {
		t.Fatal("copied source hash accepted forged file content")
	}
}

func TestInvestigationRefusesSourcesBeforeAnyModelCall(t *testing.T) {
	for _, mode := range []string{"out-of-scope", "missing", "ceiling", "repository-denied", "authentication-denied", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			db, databasePath, worker, resolved := questionFixture(t)
			worker.role = "ORCHESTRATOR"
			q := &questionRuntime{configuredRuntime: worker, events: questionEvents(investigationAnswer)}
			request := InvestigationRequest{Paths: []string{"README.md"}, MaxBytes: 100}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "out-of-scope":
				request.Paths = []string{".git/config"}
			case "missing":
				request.Paths = []string{"missing"}
			case "ceiling":
				request.MaxBytes = 1
			case "repository-denied":
				q.repositoryErr = errors.New("repository denied")
			case "authentication-denied":
				q.authErr = errors.New("authentication denied")
			case "cancelled":
				cancel()
			}
			result, err := AskInvestigation(ctx, db, filepath.Join(filepath.Dir(databasePath), "ledger.json"), "task",
				"Read this file", resolved.Config, 1000, q, request)
			if err == nil || q.starts != 0 || len(q.sends) != 0 || result.Answer != nil {
				t.Fatal("invalid investigation consumed model work", mode, err)
			}
		})
	}
}

func questionDraftFixture(t *testing.T) (*store.Store, string, QuestionDraftRequest) {
	t.Helper()
	db, ledger, planner := plannerDraftFixture(t)
	observed, err := ObserveQuestion(db, ledger, "owner/repo", planner.Proposal.Context.Task.Task.ID)
	if err != nil {
		t.Fatal(err)
	}
	observed.Investigation, err = investigateFiles(context.Background(), observed, InvestigationRequest{Paths: []string{"internal/app.go"}, MaxBytes: 1000})
	if err != nil {
		t.Fatal(err)
	}
	planner.Proposal.Context = observed
	planner.Proposal.Answer = &QuestionAnswer{
		Confirmed: []QuestionClaim{
			{Text: "Selected finding.", Sources: []string{"/investigation/files/0/content", "/investigation/sha"}},
			{Text: "Unselected chat should not be copied.", Sources: []string{"/task/task/title"}},
		},
		Assumptions: []string{}, Unconfirmed: []string{"The original role did not necessarily consider this."},
	}
	return db, ledger, QuestionDraftRequest{PlannerDraftRequest: planner,
		NextRole: "REVIEWER", Selected: []int{0}, HumanInstruction: "Check this finding against the unchanged acceptance."}
}

func TestQuestionDraftSelectsFindingsWithoutApprovalAndRejectsStaleOrForgedEvidence(t *testing.T) {
	db, ledger, request := questionDraftFixture(t)
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	flow, err := RecordQuestionDraft(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	draft := flow.InstructionDrafts[0]
	if draft.Approval != nil || draft.Publication != nil || !flow.HandoffHeld() ||
		len(draft.Evidence) != 2 || strings.Contains(draft.Text, "Unselected chat should not be copied") ||
		!strings.Contains(draft.Text, request.HumanInstruction) {
		t.Fatal("question adoption copied chat, lost evidence or inferred approval")
	}
	if _, err := RecordQuestionDraft(db, ledger, request); err != nil {
		t.Fatal("exact input replay failed", err)
	}
	state, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before.Graph.Dispatches, state.Graph.Dispatches) {
		t.Fatal("question application rewrote original instruction or result")
	}
	if _, err := ApproveInstructionDraft(db, approvalRequest(t, db, "approve-question")); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("release-question", "hold", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "APPROVE", true)
	if _, err := AdvanceReviewFlow(db, ledger, flow.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordQuestionDraft(db, ledger, request); err != nil {
		t.Fatal("old replay changed completed work", err)
	}
	for _, mode := range []string{"index", "duplicate", "instruction", "role", "changed", "context", "sha", "file", "observation", "source"} {
		t.Run(mode, func(t *testing.T) {
			db, ledger, request := questionDraftFixture(t)
			switch mode {
			case "index":
				request.Selected = []int{2}
			case "duplicate":
				request.Selected = []int{0, 0}
			case "instruction":
				request.HumanInstruction = " "
			case "role":
				request.NextRole = "CODER"
			case "changed":
				request.Proposal.Changed = true
			case "context":
				request.Proposal.Context.Task.Task.Title = "forged"
			case "sha":
				request.Proposal.Context.Investigation.SHA = strings.Repeat("b", 40)
			case "file":
				request.Proposal.Context.Investigation.Files[0].Content = "forged"
			case "observation":
				request.Proposal.Context.Investigation.Observation.ContentHash = "forged"
			case "source":
				request.Proposal.Answer.Confirmed[0].Sources = []string{"/investigation/missing"}
			}
			before, _ := db.LoadWorkspace()
			if _, err := RecordQuestionDraft(db, ledger, request); err == nil {
				t.Fatal("invalid findings were applied")
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected findings changed the graph", err)
			}
		})
	}
	var saved struct {
		Investigation *FileInvestigation `json:"investigation"`
	}
	if err := json.Unmarshal([]byte(draft.Text), &saved); err != nil || saved.Investigation == nil ||
		!reflect.DeepEqual(saved.Investigation, request.Proposal.Context.Investigation) {
		t.Fatal("draft lost durable investigation evidence", err)
	}
}
