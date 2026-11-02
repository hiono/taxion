package controller

import (
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
