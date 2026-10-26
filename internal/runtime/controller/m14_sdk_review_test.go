package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/runtime/copilot"
	"github.com/hiono/taxion/internal/store"
	"github.com/hiono/taxion/internal/tools"
)

// This follow-up spends only the explicitly reapproved independent Reviewer.
// The interrupted flow and its successful acquisition/Coder remain unchanged.
func TestM14LiveSDKReauthorizedReview(t *testing.T) {
	approvalMode := os.Getenv("TAXION_M14_REVIEW_APPROVAL")
	preflight := approvalMode == "preflight-v2"
	if !preflight && approvalMode != "approve-fixed-review" {
		t.Skip("requires separate user approval of the remaining Reviewer")
	}
	root := os.Getenv("TAXION_M14_SDK_OUTPUT")
	if !filepath.IsAbs(root) {
		t.Fatal("requires the original private acceptance directory")
	}
	db, err := store.Open(filepath.Join(root, "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	reviewDB, err := store.Open(filepath.Join(root, "review-workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reviewDB.InitializeWorkspace(state); err != nil {
		t.Fatal(err)
	}
	defer reviewDB.Close()
	db = reviewDB
	coder, ok := state.Graph.GraphDispatch("live-source-flow-dispatch-0")
	if !ok || coder.Status != core.DispatchSucceeded || coder.Result == nil {
		t.Fatal("requires the saved successful real Coder result")
	}
	if err := validateResultReceipt(&state, coder); err != nil {
		t.Fatal(err)
	}
	if err := core.CheckSpecificationUse(coder.Instruction.Specifications, "copilot"); err != nil {
		t.Fatal(err)
	}
	coderResult := *coder.Result
	if err := gitproof.Verify(coder, &coderResult); err != nil {
		t.Fatal("saved Coder SHA or worktree differs from its evidence", err)
	}
	originalPolicy, err := tools.ResolveForTask(state.Graph, coder.Instruction.Decisions, coder.TaskID, "REVIEWER")
	if err != nil || originalPolicy == nil {
		t.Fatal("original Reviewer tool approval unavailable", err)
	}
	if err := originalPolicy.Check(); err != nil {
		t.Fatal(err)
	}
	if preflight {
		review, ok := state.Graph.GraphDispatch("live-source-flow-dispatch-1")
		if !ok || !reflect.DeepEqual(review.Instruction.Specifications, coder.Instruction.Specifications) {
			t.Fatal("saved role handoff changed its acquired snapshots")
		}
		full, err := RenderDispatchContext(db, review, false)
		if err != nil {
			t.Fatal(err)
		}
		compact, err := RenderDispatchContext(db, review, true)
		if err != nil || len(compact) >= len(full) {
			t.Fatal("review input did not remove nested prior instructions", err)
		}
		after, err := db.LoadWorkspace()
		if err != nil || !reflect.DeepEqual(state, after) {
			t.Fatal("free preflight changed saved acceptance", err)
		}
		t.Logf("Free preflight: exact Coder receipt/SHA, source permissions and role policy; context %d -> %d bytes; no SDK session", len(full), len(compact))
		return
	}
	before := state
	task := core.GraphTask{
		ID: "m14-debug-task", Title: "Review exact acquired synthetic status sources", Type: "REVIEW",
		Repo: coder.Repository, Issue: "Issue #60", Owner: "REVIEWER", Status: core.Draft,
		Objective: "Independently verify the successful existing Coder output at its exact SHA against the same acquired synthetic sources.",
		Scope:     []string{"internal"}, Acceptance: coder.Instruction.Acceptance,
		RequiredEvidence: coder.Instruction.RequiredEvidence, ValidationCommands: coder.Instruction.ValidationCommands,
		Worktree: filepath.Join(root, "debug-worktree"), Branch: coder.Instruction.Branch,
		BaselineSHA: coder.Result.OutputSHA, TargetSHA: coder.Result.OutputSHA,
		Specifications: coder.Instruction.Specifications, CreatedFrom: "dispatch:" + coder.ID,
		ExitCondition: "Independent sourced verdict recorded", AuthorizationID: "debug-auth",
		Decisions: []core.TaskDecision{
			{ID: "debug-auth", Source: "human:m14-review-retry-50571-total", Text: "Authorize one independent Reviewer at 30 SDK soft credits, 16000 additional observed tokens and 10 minutes; preserve prior acquisition, Coder and CANCELLED review; no automatic retry"},
			{ID: "result:" + coder.ID, Source: "dispatch:" + coder.ID, Text: "Use the saved successful real Coder result at its exact SHA"},
		},
	}
	foundProvenance := false
	for _, sourceTask := range state.Graph.Tasks {
		for _, decision := range sourceTask.Decisions {
			if decision.ID == "result:"+coder.ID {
				task.Decisions[1] = decision
				foundProvenance = true
			}
		}
	}
	if !foundProvenance {
		t.Fatal("successful Coder result provenance is unavailable")
	}
	approval, err := json.Marshal(tools.Approval{Kind: tools.ApprovalKind, TaskID: task.ID, Actor: "human",
		Roles: map[string]tools.Policy{"REVIEWER": *originalPolicy}})
	if err != nil {
		t.Fatal(err)
	}
	task.Decisions = append(task.Decisions, core.TaskDecision{ID: "tools:debug", Source: "human:m14-review-retry-50571-total", Text: string(approval)})
	var dispatch core.GraphDispatch
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: task.ID, Title: task.Title, Repo: task.Repo, CreatedAt: time.Now().UTC()})
		if err := state.Graph.Track(task, "human", "reapprove only the remaining independent review"); err != nil {
			return err
		}
		if err := state.Graph.Transition(task.ID, core.Ready, "human", "authorize unchanged snapshots and successful Coder SHA", core.TransitionProof{}); err != nil {
			return err
		}
		var err error
		dispatch, err = core.NewGraphDispatch(state.Graph, "m14-debug-dispatch", task.ID, task.Repo, core.RelationshipNone, "", "")
		if err != nil {
			return err
		}
		if err := state.Graph.IssueDispatch(dispatch, "ORCHESTRATOR", "user-reapproved independent review"); err != nil {
			return err
		}
		state.Ledger.Dispatches = append(state.Ledger.Dispatches, core.Dispatch{
			ID: dispatch.ID, TaskID: task.ID, Repo: task.Repo, Active: true, CreatedAt: time.Now().UTC(),
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatch, err = PrepareReview(db, dispatch.ID, coder.Instruction.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	changed := false
	example := core.DispatchResult{
		DispatchID: dispatch.ID, TaskID: task.ID, Recipient: "REVIEWER", Repository: task.Repo,
		Status: core.DispatchSucceeded, StartedSHA: coder.Result.OutputSHA, OutputSHA: coder.Result.OutputSHA,
		ChangedFiles: []string{}, Evidence: []string{"test-report"}, NextTransition: core.WaitingForReview,
		ReviewVerdict: "APPROVE", WorktreeChanged: &changed, Findings: []core.DispatchFinding{},
		Acceptance: []core.DispatchAcceptance{{Criterion: task.Acceptance[0], Status: "PASS", Evidence: []string{"test-report"}}},
	}
	template, err := json.Marshal(example)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := RenderDispatchContext(db, dispatch, true)
	if err != nil {
		t.Fatal(err)
	}
	responseSchema := m14ReviewResultSchema(dispatch)
	log, err := os.OpenFile(filepath.Join(root, "private-debug-review-events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	var worker *copilot.Adapter
	worker, err = copilot.New(copilot.Config{
		Role: "REVIEWER", WorkingDirectory: dispatch.Instruction.Worktree, MaxAICredits: 30,
		AvailableTools: []string{"view"}, ToolPolicy: originalPolicy, ResponseSchema: responseSchema,
		SystemPrompt: "Read internal/app.go with view. Independently evaluate the exact synthetic source snapshots and acceptance. Preserve all files. Return only plain JSON, never Markdown fences. Example for the expected unchanged baseline; replace verdict/findings if evidence disagrees:\n" + string(template) + "\nSaved instruction:\n" + contract,
		AuthorizeTools: func(ctx context.Context, sessionID string) error {
			control, err := New(db, worker)
			if err != nil {
				return err
			}
			return control.AuthorizeTools(ctx, dispatch.ID, sessionID)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := control.stopIfExecuting(dispatch.ID); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	_, err = control.Start(ctx, dispatch.ID)
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := control.EventCursor(dispatch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Send(ctx, dispatch.ID, "Execute the saved independent review and return its sourced structured verdict."); err != nil {
		t.Fatal(err)
	}
	checkBudget := func() error {
		current, err := db.LoadWorkspace()
		if err != nil {
			return err
		}
		currentDispatch, _ := current.Graph.GraphDispatch(dispatch.ID)
		var tokens int64
		for _, usage := range currentDispatch.RuntimeUsage {
			if usage.InputTokens == nil || usage.OutputTokens == nil {
				return fmt.Errorf("Reviewer usage is unconfirmed")
			}
			tokens += *usage.InputTokens + *usage.OutputTokens
		}
		if tokens >= 16000 {
			return fmt.Errorf("Reviewer observed token ceiling reached")
		}
		return nil
	}
	runErr := control.StreamEvents(ctx, dispatch, cursor, log, checkBudget)
	closeErr := worker.Close()
	state, loadErr := db.LoadWorkspace()
	if loadErr != nil {
		t.Fatal("load final acceptance evidence", loadErr)
	}
	saved, _ := state.Graph.GraphDispatch(dispatch.ID)
	data, marshalErr := json.MarshalIndent(state, "", "  ")
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if err := os.WriteFile(filepath.Join(root, "debug-workspace.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if runErr != nil || closeErr != nil || loadErr != nil || saved.Status != core.DispatchSucceeded || saved.Result == nil ||
		saved.Result.ReviewVerdict != "APPROVE" || saved.RuntimeSessionID == coder.RuntimeSessionID ||
		!reflect.DeepEqual(saved.Instruction.Specifications, coder.Instruction.Specifications) {
		t.Fatal("reauthorized independent review failed", saved.Status, runErr, closeErr, loadErr)
	}
	if err := validateResultReceipt(&state, saved); err != nil {
		t.Fatal(err)
	}
	if err := gitproof.ReverifyReleasedReview(saved, saved.Result); err != nil {
		t.Fatal(err)
	}
	for _, previous := range before.Graph.Dispatches {
		current, ok := state.Graph.GraphDispatch(previous.ID)
		if !ok || !reflect.DeepEqual(previous, current) {
			t.Fatal("follow-up changed earlier Dispatch evidence", previous.ID)
		}
	}
	if !reflect.DeepEqual(before.Graph.ReviewFlows, state.Graph.ReviewFlows) {
		t.Fatal("follow-up changed the original stopped flow")
	}
	if len(saved.RuntimeUsage) == 0 {
		t.Fatal("Reviewer completed without confirmed usage")
	}
	var tokens int64
	for _, usage := range saved.RuntimeUsage {
		if usage.InputTokens == nil || usage.OutputTokens == nil {
			t.Fatal("Reviewer usage is unconfirmed")
		}
		tokens += *usage.InputTokens + *usage.OutputTokens
	}
	t.Logf("APPROVE: exact Coder SHA, source snapshots, released worktree and separate real SDK Reviewer; %d observed tokens", tokens)
}

func m14ReviewResultSchema(dispatch core.GraphDispatch) map[string]any {
	schema := DispatchResultSchema(dispatch, true)
	properties := schema["properties"].(map[string]any)
	properties["evidence"] = map[string]any{"type": "array",
		"items": map[string]any{"type": "string", "enum": dispatch.Instruction.RequiredEvidence}}
	acceptance := properties["acceptance"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	acceptance["evidence"] = map[string]any{"type": "array",
		"items": map[string]any{"type": "string", "enum": dispatch.Instruction.RequiredEvidence}}
	findings := properties["findings"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	findings["evidence"] = map[string]any{"type": "array",
		"items": map[string]any{"type": "string", "enum": dispatch.Instruction.RequiredEvidence}}
	return schema
}

func TestM14ReviewEvidenceSchemaPreservesVerdicts(t *testing.T) {
	dispatch := core.GraphDispatch{Recipient: "REVIEWER", Instruction: core.DispatchInstruction{
		RequiredEvidence: []string{"test-report"}, Acceptance: []string{"Status returns 202"},
	}}
	schema := m14ReviewResultSchema(dispatch)
	properties := schema["properties"].(map[string]any)
	evidenceItems := properties["evidence"].(map[string]any)["items"].(map[string]any)["enum"]
	if !reflect.DeepEqual(evidenceItems, dispatch.Instruction.RequiredEvidence) ||
		!reflect.DeepEqual(properties["reviewVerdict"].(map[string]any)["enum"], []string{"APPROVE", "REQUEST_CHANGES", "BLOCKED"}) {
		t.Fatal("schema altered the verdict or omitted exact evidence")
	}
	for _, name := range []string{"acceptance", "findings"} {
		item := properties[name].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
		identifiers := item["evidence"].(map[string]any)["items"].(map[string]any)["enum"]
		if !reflect.DeepEqual(identifiers, dispatch.Instruction.RequiredEvidence) {
			t.Fatal("descriptive evidence strings remained permitted", name, identifiers)
		}
	}
	if _, err := json.Marshal(schema); err != nil {
		t.Fatal(err)
	}
}
