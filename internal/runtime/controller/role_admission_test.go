package controller

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestBundleRoleAdmissionUsesExplicitCoverageWithoutPacketLifecycle(t *testing.T) {
	for _, covered := range []bool{false, true} {
		t.Run(map[bool]string{false: "uncovered", true: "covered"}[covered], func(t *testing.T) {
			selected, err := config.Resolve([]config.Layer{{Source: "user", Input: []byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: ai, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: configuration-approval}
`)}})
			if err != nil {
				t.Fatal(err)
			}
			approval := config.Approval{Kind: config.ApprovalKind, TaskID: "implementation",
				Reason: "bounded independent review", Allowed: []config.Config{selected.Config}}
			if covered {
				approval.AdditionalTaskIDs = []string{"flow-review-0", "flow-review-1"}
			}
			decision := core.TaskDecision{ID: selected.Config.Execution.ApprovalRef,
				Source: "human:configuration", Text: string(mustFlowJSON(t, approval))}
			db, ledger, request := reviewFlowFixture(t, decision, core.TaskDecision{
				ID: "intake-confirmation:implementation", Source: "human:proposal",
				Text: `{"taskId":"implementation","expectedHash":"` + packetProposal + `"}`,
			})
			request.Configuration, err = core.SelectApprovalConfiguration(mustFlowJSON(t, selected.Config), selected.Sources)
			if err != nil {
				t.Fatal(err)
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			task, _ := state.Graph.GraphTask(request.TaskID)
			if err := db.UpdateWorkspace(func(current *store.Workspace) error {
				bundle, err := core.ComposeReauthorizationApprovalBundleWithProposalHash("parent-approval",
					packetProposal, task, request.Configuration, "human", "human:proposal", "approved contract", time.Now().UTC())
				if err != nil {
					return err
				}
				return current.Graph.AppendApproval(bundle)
			}); err != nil {
				t.Fatal(err)
			}
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			executeFlowDispatch(t, db, flow, "", true, selected)
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			flow, advanceErr := AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
			after, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			if !covered {
				if advanceErr == nil || !reflect.DeepEqual(before.Graph.Tasks, after.Graph.Tasks) ||
					!reflect.DeepEqual(before.Graph.Approvals, after.Graph.Approvals) ||
					!reflect.DeepEqual(before.Graph.Dispatches, after.Graph.Dispatches) {
					t.Fatal("uncovered Reviewer created execution authority or changed saved work", advanceErr)
				}
				return
			}
			if advanceErr != nil {
				t.Fatal("explicitly approved Reviewer requires neither reapproval nor optional packets", advanceErr)
			}
			review, ok := after.Graph.GraphTask("flow-review-0")
			if !ok {
				t.Fatal("Reviewer was not issued")
			}
			review.Status = core.Ready
			bundle, matched := after.Graph.MatchingApproval(review.ID, review, request.Configuration)
			if !matched || bundle.Source != "approval:parent-approval" {
				t.Fatal("derived Reviewer lacks its exact entry bundle", bundle)
			}
			if !reflect.DeepEqual(before.Graph.Approvals, after.Graph.Approvals[:len(before.Graph.Approvals)]) {
				t.Fatal("parent approval changed")
			}
			checkRoleAssessment(t, after.Graph, flow, review, before.Graph.Approvals[0], request.Configuration)
			executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true, selected)
			flow, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
			if err != nil {
				t.Fatal("covered repair demanded another contract approval", err)
			}

			executeFlowDispatch(t, db, flow, "", true, selected)
			flow, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
			if err != nil {
				t.Fatal("covered re-review demanded another contract approval", err)
			}
			executeFlowDispatch(t, db, flow, "APPROVE", true, selected)
			flow, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
			if err != nil || flow.Status != "COMPLETED" {
				t.Fatal("same-target independent review did not complete", flow.Status, err)
			}
		})
	}
}

func checkRoleAssessment(t *testing.T, graph core.TaskGraph, flow core.ReviewFlow, review core.GraphTask,
	source core.ApprovalBundle, configuration core.ApprovalConfiguration) {
	t.Helper()
	before, err := core.CanonicalJSON(graph)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := assessFlowRole(graph, flow, review, source, core.EntryReviewerDispatch, configuration)
	if err != nil || accepted.Err() != nil || len(accepted.NextActions) == 0 {
		t.Fatal("pure role assessment rejected saved covered handoff", accepted, err)
	}
	repeated, err := assessFlowRole(graph, flow, review, source, core.EntryReviewerDispatch, configuration)
	if err != nil || !reflect.DeepEqual(accepted, repeated) {
		t.Fatal("same saved basis produced a different role decision", err)
	}
	for _, field := range []string{"scope", "role", "worktree", "repair-count", "source", "source-record", "configuration"} {
		t.Run(field, func(t *testing.T) {
			changed, changedFlow, changedSource := review, flow, source
			changedConfiguration := configuration
			switch field {
			case "scope":
				changed.Scope = []string{"other"}
			case "role":
				changed.Owner = "CODER"
			case "worktree":
				changed.Worktree = filepath.Join(flow.ReviewRoot, "other")
			case "repair-count":
				changedFlow.Repairs = changedFlow.MaxRepairs + 1
			case "source":
				changedSource.BundleHash = "invalid"
			case "source-record":
				changedSource.Actor = "forged"
			case "configuration":
				changedConfiguration.Selection = mustFlowJSON(t, config.Config{})
			}
			result, err := assessFlowRole(graph, changedFlow, changed, changedSource, core.EntryReviewerDispatch, changedConfiguration)
			if err != nil || result.Verdict != "blocked" || len(result.Conditions) == 0 ||
				result.BasisHash == accepted.BasisHash || result.Conditions[0].EvidenceSource == "" ||
				result.Conditions[0].Resolution == "" {
				t.Fatal("changed role basis was not explained and blocked", result, err)
			}
		})
	}
	after, err := core.CanonicalJSON(graph)
	if err != nil || string(before) != string(after) {
		t.Fatal("assessment changed saved authority", err)
	}
}
