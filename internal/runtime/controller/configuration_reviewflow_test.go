package controller

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
)

func TestReviewFlowUsesUnchangedExplicitMultiTaskConfigurationApproval(t *testing.T) {
	for _, scope := range []string{"none", "first", "both"} {
		t.Run(scope, func(t *testing.T) {
			resolved, err := config.Resolve([]config.Layer{{Source: "user", Input: []byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: ai, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: configuration-approval}
`)}})
			if err != nil {
				t.Fatal(err)
			}
			approval := config.Approval{
				Kind: config.ApprovalKind, TaskID: "implementation", Reason: "Human authorizes listed reviews within flow ceilings",
				Allowed: []config.Config{resolved.Config},
			}
			if scope != "none" {
				approval.AdditionalTaskIDs = []string{"flow-review-0"}
			}
			if scope == "both" {
				approval.AdditionalTaskIDs = append(approval.AdditionalTaskIDs, "flow-review-1")
			}
			decision := core.TaskDecision{
				ID: resolved.Config.Execution.ApprovalRef, Source: "human:configuration-scope",
				Text: string(mustFlowJSON(t, approval)),
			}
			db, ledgerPath, request := reviewFlowFixture(t, decision)
			flow, err := StartReviewFlow(db, ledgerPath, request)
			if err != nil {
				t.Fatal(err)
			}
			beforePreflight, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			preflightErr := CheckReviewFlowConfiguration(db, flow.ID, resolved.Config)
			if (preflightErr == nil) != (scope == "both") {
				t.Fatalf("free preflight accepted missing review approval: scope=%s error=%v", scope, preflightErr)
			}
			afterPreflight, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(beforePreflight, afterPreflight) {
				t.Fatal("configuration preflight mutated workspace", err)
			}
			executeFlowDispatch(t, db, flow, "", true, resolved)
			flow, err = AdvanceReviewFlow(db, ledgerPath, flow.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertDecision := func() core.GraphDispatch {
				t.Helper()
				state, err := db.LoadWorkspace()
				if err != nil {
					t.Fatal(err)
				}
				dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
				found := false
				for _, saved := range dispatch.Instruction.Decisions {
					found = found || saved == decision
				}
				if !found {
					t.Fatal("generated Task changed or lost Human configuration approval")
				}
				return dispatch
			}
			firstReview := assertDecision()
			if firstReview.TaskID != request.ID+"-review-0" {
				t.Fatal("unexpected generated review ID")
			}
			if scope == "none" {
				before, err := db.LoadWorkspace()
				if err != nil {
					t.Fatal(err)
				}
				if err := BindConfiguration(db, firstReview.ID, resolved); err == nil {
					t.Fatal("Reviewer inherited parent authorization implicitly")
				}
				after, err := db.LoadWorkspace()
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatal("rejected Reviewer binding changed state", err)
				}
				return
			}
			executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true, resolved)
			flow, err = AdvanceReviewFlow(db, ledgerPath, flow.ID)
			if err != nil {
				t.Fatal(err)
			}
			executeFlowDispatch(t, db, flow, "", true, resolved)
			flow, err = AdvanceReviewFlow(db, ledgerPath, flow.ID)
			if err != nil {
				t.Fatal(err)
			}
			secondReview := assertDecision()
			if secondReview.TaskID != request.ID+"-review-1" {
				t.Fatal("unexpected generated re-review ID")
			}
			if scope == "first" {
				if err := BindConfiguration(db, secondReview.ID, resolved); err == nil {
					t.Fatal("one review authorization granted a re-review")
				}
				return
			}
			executeFlowDispatch(t, db, flow, "APPROVE", true, resolved)
			flow, err = AdvanceReviewFlow(db, ledgerPath, flow.ID)
			if err != nil || flow.Status != "COMPLETED" {
				t.Fatal("explicitly authorized review/repair flow failed", flow.Status, err)
			}
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			for _, dispatch := range state.Graph.Dispatches {
				if dispatch.Configuration == nil {
					t.Fatal("flow Dispatch lost its complete selected configuration")
				}
				var selected config.Config
				if err := json.Unmarshal(dispatch.Configuration.Selection, &selected); err != nil || selected != resolved.Config {
					t.Fatal("derived authorization changed the selected tuple", err)
				}
			}
		})
	}
}
