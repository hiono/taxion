package controller

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestDispatchSourcesPreserveOriginalsForIndependentRoles(t *testing.T) {
	raw := "Original request\r\nPreserve this condition absent from the objective."
	original, _ := json.Marshal(map[string]string{"kind": "human-request", "request": raw, "actor": "human"})
	design := core.ContextDocument{ID: "design", Kind: "design", Source: "human:design", Version: "2",
		Text: "Rejected global stops: the original design rationale, not a Coder inference."}
	open := core.ContextDocument{ID: "open", Kind: "questions", Source: "human:open", Version: "1",
		Text: "Original unresolved matter."}
	designDecision, err := core.ContextDocumentDecision(design, "source:design")
	if err != nil {
		t.Fatal(err)
	}
	openDecision, err := core.ContextDocumentDecision(open, "source:open")
	if err != nil {
		t.Fatal(err)
	}
	source := core.GraphDispatch{ID: "predecessor", TaskID: "previous", Recipient: "CODER",
		Status: core.DispatchSucceeded, InstructionHash: "previous-bound",
		Result: &core.DispatchResult{DispatchID: "predecessor", WorkReport: "Coder's account",
			Validations: []core.DispatchValidation{{Command: "test", Output: "actual Controller failure", ExitCode: 1}}}}
	ref, err := flowResultDecision(source)
	if err != nil {
		t.Fatal(err)
	}
	var shared []core.ContextDocument
	for _, role := range []string{"CODER", "REVIEWER"} {
		dispatch := core.GraphDispatch{ID: "receiver", Recipient: role, InstructionHash: "bound-" + role,
			Instruction: core.DispatchInstruction{Version: core.ContextAccessInstructionVersion, Objective: "Short objective",
				Decisions: []core.TaskDecision{
					{ID: "intake:root", Source: "human:original", Text: string(original)},
					{ID: "intake-confirmation:root", Source: "human:approval", Text: `{"reason":"Original explicit approval."}`},
					designDecision, openDecision, ref,
				}}}
		state := store.Workspace{HasGraph: true, Graph: core.TaskGraph{Dispatches: []core.GraphDispatch{
			source, dispatch, {ID: "unrelated", Result: &core.DispatchResult{WorkReport: "Unrelated private task."}},
		}}}
		documents, err := DispatchContextDocuments(state, dispatch)
		if err != nil {
			t.Fatal(err)
		}
		var current []core.ContextDocument
		kinds := map[string]bool{}
		for _, document := range documents {
			kinds[document.Kind] = true
			if document.Kind != "contract" {
				current = append(current, document)
			}
			if strings.Contains(document.Text, "Unrelated private task.") {
				t.Fatal("source reader included another Task")
			}
			if document.Kind == "request" && document.Text != raw {
				t.Fatal("supplied original wording was summarized")
			}
			if document.Kind == "evidence" && !strings.Contains(document.Text, "actual Controller failure") {
				t.Fatal("predecessor evidence contains only the Coder account")
			}
		}
		for _, kind := range []string{"request", "approval", "design", "questions", "evidence"} {
			if !kinds[kind] {
				t.Fatal("required supplied source category was lost", kind)
			}
		}
		if shared != nil && !reflect.DeepEqual(shared, current) {
			t.Fatal("Reviewer did not receive independently accessible originals")
		}
		shared = current
		for _, compact := range []bool{false, true} {
			projection, err := RenderWorkspaceDispatchContext(state, dispatch, compact)
			if err != nil || strings.Contains(projection, design.Text) || strings.Contains(projection, open.Text) {
				t.Fatal("prompt inlined large captured documents instead of their index", err)
			}
		}
		dispatch.Instruction.Version = 5
		if documents, err := DispatchContextDocuments(state, dispatch); err != nil || documents != nil {
			t.Fatal("old Dispatch acquired a changed runtime context", err)
		}
	}
}

func TestContextSpecificationsExposeOnlyProviderApprovedExcerpt(t *testing.T) {
	db, _, _ := reviewFlowFixture(t)
	defer db.Close()
	specification := attachSpecification(t, db, "implementation")
	fullText := specification.Body + "\nUNAPPROVED FULL-DOCUMENT MATERIAL."
	if err := os.WriteFile(specification.LocalPath, []byte(fullText), 0600); err != nil {
		t.Fatal(err)
	}
	specification.DocumentHash = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(fullText)))
	hash, err := core.SpecificationHash(specification)
	if err != nil {
		t.Fatal(err)
	}
	specification.Ingestion.Hash, specification.Copilot.Hash = hash, hash
	dispatch := core.GraphDispatch{ID: "receiver", InstructionHash: "bound",
		Instruction: core.DispatchInstruction{Version: core.ContextAccessInstructionVersion,
			Specifications: []core.Specification{specification}}}
	state := store.Workspace{HasGraph: true, Graph: core.TaskGraph{Dispatches: []core.GraphDispatch{dispatch}}}
	documents, err := DispatchContextDocuments(state, dispatch)
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range documents {
		if strings.Contains(document.Text, "UNAPPROVED FULL-DOCUMENT MATERIAL.") {
			t.Fatal("excerpt permission exposed the full local source")
		}
		if document.Kind == "specification" && document.Text != specification.Body {
			t.Fatal("approved excerpt changed")
		}
	}
	selectionConfig := config.Config{}
	selectionConfig.Runtime.Provider = "opencode"
	selection, _ := json.Marshal(selectionConfig)
	// A Copilot-only excerpt must not be exported through the other provider.
	state.Graph.Dispatches[0].Configuration = &core.DispatchConfiguration{Selection: selection}
	if _, err := DispatchContextDocuments(state, dispatch); err == nil {
		t.Fatal("source export ignored provider-specific permission")
	}
	if err := os.WriteFile(specification.LocalPath, []byte("changed source version"), 0600); err != nil {
		t.Fatal(err)
	}
	state.Graph.Dispatches[0].Configuration = nil
	if _, err := DispatchContextDocuments(state, dispatch); err == nil {
		t.Fatal("source version drift was ignored")
	}
}

func TestContextOriginalSurvivesReviewRepairAndRereview(t *testing.T) {
	original := core.ContextDocument{ID: "original-design", Kind: "design", Source: "human:design",
		Version: "revision-2", Text: "Preserve the exact original reason in every role, not the Coder's summary."}
	decision, err := core.ContextDocumentDecision(original, "context-source:design")
	if err != nil {
		t.Fatal(err)
	}
	db, ledger, request := reviewFlowFixture(t, decision)
	defer db.Close()
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	var originalHash string
	for _, verdict := range []string{"", "REQUEST_CHANGES", "", "APPROVE"} {
		state, err := db.LoadWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		dispatch, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
		documents, err := DispatchContextDocuments(state, dispatch)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, document := range documents {
			if document.ID == original.ID {
				found = true
				if originalHash != "" && document.Hash != originalHash || document.Text != original.Text {
					t.Fatal("a later role lost or summarized the original design")
				}
				originalHash = document.Hash
			}
		}
		if !found {
			t.Fatal("issued role has no independently readable original", dispatch.Recipient)
		}
		executeFlowDispatch(t, db, flow, verdict, true)
		flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	if flow.Status != "COMPLETED" {
		t.Fatal("original-source access prevented authorized repair and independent review")
	}
}

func TestIntakeSourceCaptureChangesApprovalBasis(t *testing.T) {
	db, _ := intakeApprovalFixture(t, false)
	defer db.Close()
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := state.Graph.GraphTask("confirmed-intake")
	task.ID, task.Decisions = "source-intake", nil
	document := core.ContextDocument{ID: "design", Kind: "design", Source: "human:original-design",
		Version: "1", Text: "The original design decision."}
	proposal, err := ProposeIntake(db, IntakeRequest{Task: task, Request: "Original full request.",
		Actor: "human", Source: "human:request", ContextSources: []core.ContextDocument{document}})
	if err != nil {
		t.Fatal(err)
	}
	sources, err := core.ContextDocumentsFromDecisions(proposal.Decisions)
	if err != nil || len(sources) != 1 || sources[0].Text != document.Text {
		t.Fatal("intake did not capture supplied source", sources, err)
	}
	originalHash, _ := IntakeHash(proposal)
	document.Text = "Changed source that requires a different proposal basis."
	changed, err := core.ContextDocumentDecision(document, proposal.Decisions[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	proposal.Decisions[1] = changed
	changedHash, _ := IntakeHash(proposal)
	if originalHash == changedHash {
		t.Fatal("changed source did not affect the approval basis")
	}
}
