package controller

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func savedReviewAcceptanceFixture(t *testing.T) (*store.Store, string, ReviewFlowRequest, ReviewAcceptanceRequest) {
	t.Helper()
	db, ledger, request, coder := packetReviewSuccessorFixture(t, core.OutputModeWorktreeTreeSnapshot)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "APPROVE", true, packetConfigurationFixture(t))
	gitTest(t, coder.Instruction.Worktree, "switch", "--detach")
	gitTest(t, coder.Instruction.Worktree, "add", "-A")
	gitTest(t, flow.Contract.Worktree, "add", "-A")
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
	if !errors.Is(err, ErrFlowStopped) {
		t.Fatalf("fixture did not stop: %v", err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		flow, _ := state.Graph.ReviewFlow(flow.ID)
		last, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
		for _, receipt := range state.Records.Events {
			if receipt.DispatchID == last.ID && receipt.Type == "verified-result" {
				flow.Deadline = receipt.At.Add(time.Nanosecond)
			}
		}
		// Reproduce the saved v0.7.19 secondary error, not the corrected diagnostic.
		flow.Reason = lostHandoffStopReason
		flow.HandoffChanges[len(flow.HandoffChanges)-1].Reason = flow.Reason
		storeFlow(state, flow)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return db, ledger, request, ReviewAcceptanceRequest{
		FlowID: flow.ID, Actor: "user", Source: "user:acceptance",
		Reason: "Accept the saved independent approval without further execution", AllowExpired: true,
	}
}

func TestAcceptSavedReviewPreviewAndApply(t *testing.T) {
	db, ledger, request, acceptance := savedReviewAcceptanceFixture(t)
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	flow, _ := before.Graph.ReviewFlow(request.ID)
	reviewer, _ := before.Graph.GraphDispatch(flow.DispatchIDs[0])
	coder, _ := before.Graph.GraphDispatch(flow.SourceDispatchID)
	files := map[string][]byte{}
	for _, root := range []string{coder.Instruction.Worktree, flow.Contract.Worktree} {
		index := gitTest(t, root, "rev-parse", "--path-format=absolute", "--git-path", "index")
		data, err := os.ReadFile(index)
		if err != nil {
			t.Fatal(err)
		}
		files[index] = data
	}
	observed := acceptanceFiles(t, filepath.Dir(ledger), flow.Contract.Worktree)
	database := filepath.Join(filepath.Dir(ledger), "workspace.db")
	readonly, err := store.OpenReadOnly(database)
	if err != nil {
		t.Fatal(err)
	}
	defer readonly.Close()
	preview, err := AcceptSavedReview(t.Context(), readonly, ledger, acceptance, request.Configuration, true)
	if err != nil {
		t.Fatal(err)
	}
	if preview.StateChanged || !preview.DeadlineExpired || !preview.OriginalReasonUnavailable ||
		preview.ReviewerTaskFinalStatus != core.Blocked || preview.OutputTreeSHA != reviewer.Result.OutputTreeSHA {
		t.Fatalf("invalid preview: %+v", preview)
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("preview mutated state", err)
	}
	if !reflect.DeepEqual(observed, acceptanceFiles(t, filepath.Dir(ledger), flow.Contract.Worktree)) {
		t.Fatal("preview changed database, Memory, Git objects, refs, index or worktree bytes")
	}
	for path, original := range files {
		actual, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(original, actual) {
			t.Fatal("preview changed Git index", path, err)
		}
	}
	if _, err := AcceptSavedReview(t.Context(), db, ledger, acceptance, request.Configuration, false); err == nil {
		t.Fatal("accepted without exact preview")
	}
	acceptance.ExpectedHash = preview.Hash
	applied, err := AcceptSavedReview(t.Context(), db, ledger, acceptance, request.Configuration, false)
	if err != nil {
		t.Fatal(err)
	}
	after, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	completed, _ := after.Graph.ReviewFlow(flow.ID)
	task, _ := after.Graph.GraphTask(flow.TaskID)
	reviewTask, _ := after.Graph.GraphTask(reviewer.TaskID)
	if !applied.StateChanged || completed.Status != "COMPLETED" || task.Status != core.Completed ||
		reviewTask.Status != core.Blocked || !completed.HandoffHeld() ||
		!reflect.DeepEqual(flow.HandoffChanges, completed.HandoffChanges) ||
		!reflect.DeepEqual(before.Graph.Dispatches, after.Graph.Dispatches) ||
		!reflect.DeepEqual(before.Graph.Approvals, after.Graph.Approvals) ||
		!reflect.DeepEqual(before.Records.Events, after.Records.Events) ||
		!reflect.DeepEqual(before.Records.Budgets, after.Records.Budgets) ||
		len(after.Records.ReviewAcceptances) != 1 ||
		!reflect.DeepEqual(after.Records.ReviewAcceptances[0].PreviousFlow, flow) {
		t.Fatal("acceptance changed execution, approvals, stop evidence or review Task")
	}
	if err := revalidateFlowResult(after.Graph, completed, after.Records); err != nil {
		t.Fatal("accepted result cannot be revalidated", err)
	}
	if _, err := AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration); err != nil {
		t.Fatal("completed advance reran or rejected saved acceptance", err)
	}
	if _, err := AcceptSavedReview(t.Context(), db, ledger, acceptance, request.Configuration, false); err == nil {
		t.Fatal("replayed acceptance applied again")
	}
	for _, edit := range []func(*store.Workspace){
		func(s *store.Workspace) { s.Records.ReviewAcceptances = nil },
		func(s *store.Workspace) { s.Records.ReviewAcceptances[0].Reason = "rewritten" },
		func(s *store.Workspace) { s.Records.ReviewAcceptances[0].PreviousFlow.Reason = "rewritten stop" },
	} {
		if err := db.UpdateWorkspace(func(s *store.Workspace) error { edit(s); return nil }); err == nil {
			t.Fatal("acceptance audit was mutable")
		}
	}
	for path, original := range files {
		actual, err := os.ReadFile(path)
		if err != nil || !reflect.DeepEqual(original, actual) {
			t.Fatal("acceptance changed Git index", path, err)
		}
	}
}

func TestAcceptSavedReviewBeforeDeadline(t *testing.T) {
	db, ledger, request, acceptance := savedReviewAcceptanceFixture(t)
	acceptance.AllowExpired = false
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		flow, _ := state.Graph.ReviewFlow(request.ID)
		flow.Deadline = time.Now().UTC().Add(time.Hour)
		storeFlow(state, flow)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	flow, _ := state.Graph.ReviewFlow(request.ID)
	if preview, err := previewSavedReview(t.Context(), state, ledger, acceptance, request.Configuration, flow.Deadline.Add(-time.Nanosecond)); err != nil || preview.DeadlineExpired {
		t.Fatal("unexpired boundary rejected", err)
	}
	if _, err := previewSavedReview(t.Context(), state, ledger, acceptance, request.Configuration, flow.Deadline); err == nil {
		t.Fatal("deadline boundary accepted without explicit expired-result approval")
	}
	preview, err := AcceptSavedReview(t.Context(), db, ledger, acceptance, request.Configuration, true)
	if err != nil || preview.DeadlineExpired {
		t.Fatal("unexpired result rejected", err)
	}
	acceptance.ExpectedHash = preview.Hash
	if _, err := AcceptSavedReview(t.Context(), db, ledger, acceptance, request.Configuration, false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.LoadWorkspace(); err != nil {
		t.Fatal("unexpired acceptance audit cannot be read", err)
	}
}

func TestAcceptSavedReviewConcurrentApply(t *testing.T) {
	db, ledger, request, acceptance := savedReviewAcceptanceFixture(t)
	preview, err := AcceptSavedReview(t.Context(), db, ledger, acceptance, request.Configuration, true)
	if err != nil {
		t.Fatal(err)
	}
	acceptance.ExpectedHash = preview.Hash
	other, err := store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	results := make(chan error, 2)
	start := make(chan struct{})
	for _, connection := range []*store.Store{db, other} {
		go func() {
			<-start
			_, err := AcceptSavedReview(t.Context(), connection, ledger, acceptance, request.Configuration, false)
			results <- err
		}()
	}
	close(start)
	successes := 0
	for range 2 {
		if <-results == nil {
			successes++
		}
	}
	state, err := db.LoadWorkspace()
	if err != nil || successes != 1 || len(state.Records.ReviewAcceptances) != 1 {
		t.Fatal("concurrent acceptance was not exactly once", successes, err)
	}
}

func acceptanceFiles(t *testing.T, roots ...string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	for _, root := range roots {
		if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.Type().IsRegular() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err == nil {
				files[path] = data
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	return files
}

func TestAcceptSavedReviewFailsClosed(t *testing.T) {
	for _, failure := range []string{"expiry", "stale-hash", "actor", "reason", "runner", "other-stop",
		"configuration", "snapshot-ref", "current-branch", "current-index",
		"current-worktree", "source-worktree", "review-path", "late-result"} {
		t.Run(failure, func(t *testing.T) {
			db, ledger, request, acceptance := savedReviewAcceptanceFixture(t)
			preview, err := AcceptSavedReview(t.Context(), db, ledger, acceptance, request.Configuration, true)
			if err != nil {
				t.Fatal(err)
			}
			acceptance.ExpectedHash = preview.Hash
			state, _ := db.LoadWorkspace()
			flow, _ := state.Graph.ReviewFlow(request.ID)
			last, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
			coder, _ := state.Graph.GraphDispatch(flow.SourceDispatchID)
			switch failure {
			case "expiry":
				acceptance.AllowExpired = false
			case "stale-hash":
				acceptance.ExpectedHash = "sha256:" + strings.Repeat("a", 64)
			case "actor":
				acceptance.Actor = ""
			case "reason":
				acceptance.Reason = "different approval"
			case "configuration":
				request.Configuration = core.ApprovalConfiguration{}
			case "snapshot-ref":
				gitTest(t, flow.Contract.Worktree, "update-ref", "-d", coder.Result.GitEvidence.SnapshotRef)
			case "current-branch":
				gitTest(t, flow.Contract.Worktree, "switch", "--detach")
			case "current-index":
				gitTest(t, flow.Contract.Worktree, "read-tree", flow.Contract.BaselineSHA)
			case "current-worktree", "source-worktree":
				root := flow.Contract.Worktree
				if failure == "source-worktree" {
					root = coder.Instruction.Worktree
				}
				if err := os.WriteFile(filepath.Join(root, "internal/app.go"), []byte("drift"), 0600); err != nil {
					t.Fatal(err)
				}
			case "review-path":
				if err := os.MkdirAll(last.Instruction.Worktree, 0700); err != nil {
					t.Fatal(err)
				}
			default:
				if err := db.UpdateWorkspace(func(s *store.Workspace) error {
					changed, _ := s.Graph.ReviewFlow(flow.ID)
					switch failure {
					case "runner":
						changed.RunnerID = "still-owned"
					case "other-stop":
						changed.Reason = "a different safety stop"
					case "late-result":
						changed.Deadline = last.Result.GitEvidence.VerifiedAt
					}
					storeFlow(s, changed)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}

			if _, err := AcceptSavedReview(t.Context(), db, ledger, acceptance, request.Configuration, false); err == nil {
				t.Fatal("unsafe acceptance succeeded")
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected acceptance changed saved state", err)
			}
		})
	}
}

func TestAcceptSavedReviewRejectsBrokenEvidence(t *testing.T) {
	db, ledger, request, acceptance := savedReviewAcceptanceFixture(t)
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	original, err := core.CanonicalJSON(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, failure := range []string{"packet", "receipt", "receipt-time", "feedback", "approval", "instruction", "contract", "verdict"} {
		t.Run(failure, func(t *testing.T) {
			var changed store.Workspace
			if err := json.Unmarshal(original, &changed); err != nil {
				t.Fatal(err)
			}
			flow, _ := changed.Graph.ReviewFlow(request.ID)
			reviewID := flow.DispatchIDs[0]
			switch failure {
			case "packet":
				changed.Records.Packets = nil
			case "receipt", "receipt-time":
				for i := range changed.Records.Events {
					receipt := &changed.Records.Events[i]
					if receipt.DispatchID == reviewID && receipt.Type == "verified-result" {
						if failure == "receipt" {
							receipt.Payload = "{}"
							receipt.Hash = receiptHash(receipt.Payload)
						} else {
							receipt.At = flow.Deadline
						}
					}
				}
			case "feedback":
				review, _ := changed.Graph.GraphDispatch(reviewID)
				target, err := core.ObserveFeedbackTarget(review)
				if err != nil {
					t.Fatal(err)
				}
				hold := flow.HandoffChanges[len(flow.HandoffChanges)-1]
				flow.Feedback = append(flow.Feedback, core.FlowFeedback{
					ID: "pending", Target: target, Actor: "user", Source: "user:concern", Reason: "new concern",
					Text: "Do not accept before resolving this concern", HoldChangeID: hold.ID, At: hold.At.Add(time.Nanosecond),
				})
				storeFlow(&changed, flow)
				if err := changed.Graph.Validate(); err != nil {
					t.Fatal("pending feedback fixture is invalid", err)
				}
			case "approval":
				changed.Graph.Approvals = nil
			case "instruction", "verdict":
				for i := range changed.Graph.Dispatches {
					if changed.Graph.Dispatches[i].ID != reviewID {
						continue
					}
					if failure == "instruction" {
						changed.Graph.Dispatches[i].InstructionHash = "changed"
					} else {
						changed.Graph.Dispatches[i].Result.ReviewVerdict = "BLOCKED"
					}
				}
			case "contract":
				for i := range changed.Graph.Tasks {
					if changed.Graph.Tasks[i].ID == flow.TaskID {
						changed.Graph.Tasks[i].Acceptance = []string{"different acceptance"}
					}
				}
			}
			before, err := core.CanonicalJSON(changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := previewSavedReview(t.Context(), changed, ledger, acceptance, request.Configuration, time.Now().UTC()); err == nil {
				t.Fatal("invalid saved evidence accepted")
			}
			after, err := core.CanonicalJSON(changed)
			if err != nil || string(before) != string(after) {
				t.Fatal("failed preview changed saved evidence", err)
			}
		})
	}
}
