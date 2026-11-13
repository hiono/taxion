package controller

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/specsearch"
	"github.com/hiono/taxion/internal/store"
	"github.com/hiono/taxion/internal/tools"
)

func TestLocalSearchAttachesSourcesAndCompletesIndependentReviewFlow(t *testing.T) {
	policy := tools.Policy{EvidenceDirectory: filepath.Join(t.TempDir(), "tool-evidence")}
	approval, err := json.Marshal(tools.Approval{Kind: tools.ApprovalKind, TaskID: "implementation", Actor: "human",
		Roles: map[string]tools.Policy{"CODER": policy, "REVIEWER": policy}})
	if err != nil {
		t.Fatal(err)
	}
	db, ledger, request := reviewFlowFixture(t, core.TaskDecision{ID: "tools", Text: string(approval), Source: "human:synthetic"})
	defer db.Close()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.Transition(request.TaskID, core.Draft, "human", "approve local source acquisition", core.TransitionProof{})
	}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "synthetic.docx")
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	part, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(`<root><p><t>Application tested. Preserve independent review.</t></p></root>`)); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	assetHash, err := tools.Hash(path)
	if err != nil {
		t.Fatal(err)
	}
	scope := specsearch.Scope{EvidenceDirectory: filepath.Join(root, "private"), Documents: []specsearch.Document{{
		Asset: tools.Asset{Path: path, Hash: assetHash}, Title: "Synthetic application contract",
		Source: "synthetic:application-contract", Version: "v1", Access: "synthetic",
	}}}
	scopeHash, err := scope.Hash()
	if err != nil {
		t.Fatal(err)
	}
	scope.Approval = &specsearch.Approval{Actor: "human", Source: "human:local-search", Hash: scopeHash, Copilot: true}
	none, err := SearchSpecifications(t.Context(), db, request.TaskID, scope, "unmatched")
	if err != nil || len(none.Specifications) != 0 {
		t.Fatal("empty search fabricated input", none, err)
	}
	found, err := SearchSpecifications(t.Context(), db, request.TaskID, scope, "application tested")
	if err != nil || len(found.Specifications) != 1 {
		t.Fatal("conversation acquisition failed", found, err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.Transition(request.TaskID, core.Ready, "human", "authorize acquired specification", core.TransitionProof{})
	}); err != nil {
		t.Fatal(err)
	}
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	coder := executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := executeFlowDispatch(t, db, flow, "APPROVE", true)
	if !reflect.DeepEqual(coder.Instruction.Specifications, reviewer.Instruction.Specifications) ||
		!reflect.DeepEqual(coder.Instruction.Specifications, found.Specifications) ||
		coder.RuntimeSessionID == reviewer.RuntimeSessionID {
		t.Fatal("automatic acquisition lost provenance or role isolation")
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || flow.Status != "COMPLETED" {
		t.Fatal("acquired source failed normal independent acceptance", flow, err)
	}
}
